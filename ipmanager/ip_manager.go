package ipmanager

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
)

type ipConfigurer interface {
	queryAddress() bool
	configureAddress() bool
	deconfigureAddress() bool
	getCIDR() string
	cleanupArp()
}

// IPManager implements the main functionality of the VIP manager
type IPManager struct {
	configurer ipConfigurer

	states       <-chan bool
	currentState bool
	stateLock    sync.Mutex
	recheck      *sync.Cond
}

// NewIPManager returns a new instance of IPManager
func NewIPManager(config *vipconfig.Config, ipConfig *IPConfiguration, states <-chan bool) (m *IPManager, err error) {
	m = &IPManager{
		states:       states,
		currentState: false,
	}
	m.recheck = sync.NewCond(&m.stateLock)
	switch config.HostingType {
	case "hetzner":
		m.configurer, err = newHetznerConfigurer(config, ipConfig)
		if err != nil {
			return nil, err
		}
	case "hetzner-cloud":
		m.configurer, err = newHetznerCloudConfigurer(config, ipConfig)
		if err != nil {
			return nil, err
		}
	case "basic":
		fallthrough
	default:
		m.configurer, err = newBasicConfigurer(ipConfig)
	}
	if err != nil {
		m = nil
	}
	return
}

func (m *IPManager) applyLoop(ctx context.Context) {
	timeout := 0
	for {
		// Check if we should exit
		select {
		case <-ctx.Done():
			m.configurer.deconfigureAddress()
			return
		case <-time.After(time.Duration(timeout) * time.Second):
			actualState := m.configurer.queryAddress()
			m.stateLock.Lock()
			desiredState := m.currentState
			log.Printf("IP address %s state is %t, desired %t", m.configurer.getCIDR(), actualState, desiredState)
			if actualState != desiredState {
				m.stateLock.Unlock()
				var configureState bool
				if desiredState {
					configureState = m.configurer.configureAddress()
				} else {
					configureState = m.configurer.deconfigureAddress()
				}
				if !configureState {
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

// SyncStates implements states synchronization
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
