package main

import (
	"context"
	"log"
	"net"
	"sync"
	"time"
)

var (
	ethernetBroadcast = net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
)

type IPConfigurer interface {
	QueryAddress() bool
	ConfigureAddress() bool
	DeconfigureAddress() bool
	GetCIDR() string
	cleanupArp()
}

type IPManager struct {
	configurer IPConfigurer

	states       <-chan bool
	currentState bool
	stateLock    sync.Mutex
	recheck      *sync.Cond
}

func NewIPManager(hostingType string, config *IPConfiguration, states <-chan bool) (*IPManager, error) {
	m := &IPManager{
		states:       states,
		currentState: false,
	}

	m.recheck = sync.NewCond(&m.stateLock)

	switch hostingType {
	case "hetzner":
		c, err := NewHetznerConfigurer(config)
		if err != nil {
			return nil, err
		}
		m.configurer = c
	case "hetzner_floating_ip":
		c, err := NewHetznerFloatingIPConfigurer(config)
		if err != nil {
			return nil, err
		}
		m.configurer = c
	case "basic":
		fallthrough
	default:
		c, err := NewBasicConfigurer(config)
		if err != nil {
			return nil, err
		}
		m.configurer = c
	}

	return m, nil
}

func (m *IPManager) applyLoop(ctx context.Context) {
	timeout := 0
	for {
		// Check if we should exit
		select {
		case <-ctx.Done():
			m.configurer.DeconfigureAddress()
			return
		case <-time.After(time.Duration(timeout) * time.Second):
			actualState := m.configurer.QueryAddress()
			m.stateLock.Lock()
			desiredState := m.currentState
			log.Printf("IP address %s state is %t, desired %t", m.configurer.GetCIDR(), actualState, desiredState)
			if actualState != desiredState {
				m.stateLock.Unlock()
				var configureState bool = false
				if desiredState {
					configureState = m.configurer.ConfigureAddress()
				} else {
					configureState = m.configurer.DeconfigureAddress()
				}
				if configureState != true {
					log.Printf("Error while acquiring virtual ip for this machine")
					//Sleep a little bit to avoid busy waiting due to the for loop.
					timeout = 10
				} else {
					timeout = 0
				}
			} else {
				// Wait for notification
				m.recheck.Wait()
				// Want to query actual state anyway, so unlock
				m.stateLock.Unlock()
			}
		}
	}
}

func (m *IPManager) SyncStates(ctx context.Context, states <-chan bool) {
	ticker := time.NewTicker(10 * time.Second)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		m.applyLoop(ctx)
		wg.Done()
	}()

	for {
		select {
		case newState := <-states:
			m.stateLock.Lock()
			if m.currentState != newState {
				m.currentState = newState
				m.recheck.Broadcast()
			}
			m.stateLock.Unlock()
		case <-ticker.C:
			m.recheck.Broadcast()
		case <-ctx.Done():
			m.recheck.Broadcast()
			wg.Wait()
			m.configurer.cleanupArp()
			return
		}
	}
}
