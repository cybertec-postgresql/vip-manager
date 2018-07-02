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
		states:          states,
		currentState:    false,
	}

	switch hostingType {
	case "basic":
		c, err := NewBasicConfigurer(config)
		if err != nil {
			return nil, err
		}
		m.configurer = c
	case "hetzner":
		c, err := NewHetznerConfigurer(config)
		if err != nil {
			return nil, err
		}
		m.configurer = c
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
	for {
		actualState := m.configurer.QueryAddress()
		m.stateLock.Lock()
		desiredState := m.currentState
		log.Printf("IP address %s state is %t, desired %t", m.configurer.GetCIDR(), actualState, desiredState)
		if actualState != desiredState {
			m.stateLock.Unlock()
			if desiredState {
				m.configurer.ConfigureAddress()
			} else {
				m.configurer.DeconfigureAddress()
			}
		} else {
			// Wait for notification
			m.recheck.Wait()
			// Want to query actual state anyway, so unlock
			m.stateLock.Unlock()

			// Check if we should exit
			select {
			case <-ctx.Done():
				m.configurer.DeconfigureAddress()
				return
			default:
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
