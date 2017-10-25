package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	arp "github.com/mdlayher/arp"
)

const (
	arpReplyOp = 2
)

var (
	ethernetBroadcast = net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
)

type IPManager struct {
	*IPConfiguration

	states       <-chan bool
	currentState bool
	stateLock    sync.Mutex
	recheck      *sync.Cond
	arpClient    *arp.Client
}

func NewIPManager(config *IPConfiguration, states <-chan bool) (*IPManager, error) {
	m := &IPManager{
		IPConfiguration: config,
		states:          states,
		currentState:    false,
	}

	m.recheck = sync.NewCond(&m.stateLock)
	arpClient, err := arp.Dial(&m.iface)
	if err != nil {
		log.Printf("Problems with producing the arp client: %s", err)
		return nil, err
	}
	m.arpClient = arpClient

	return m, err
}

func (m *IPManager) applyLoop(ctx context.Context) {
	for {
		actualState := m.QueryAddress()
		m.stateLock.Lock()
		desiredState := m.currentState
		log.Printf("IP address %s state is %t, desired %t", m.GetCIDR(), actualState, desiredState)
		if actualState != desiredState {
			m.stateLock.Unlock()
			if desiredState {
				m.ConfigureAddress()
				// For now it is save to say that also working even if a
				// gratuitous arp message could not be send but logging an
				// errror should be enough.
				m.ARPSendGratuitous()
			} else {
				m.DeconfigureAddress()
			}
		} else {
			// Wait for notification
			m.recheck.Wait()
			// Want to query actual state anyway, so unlock
			m.stateLock.Unlock()

			// Check if we should exit
			select {
			case <-ctx.Done():
				m.DeconfigureAddress()
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
			m.arpClient.Close()
			return
		}
	}
}

func (m *IPManager) ARPSendGratuitous() error {
	gratuitousPackage, err := arp.NewPacket(
		arpReplyOp,
		m.iface.HardwareAddr,
		m.vip,
		ethernetBroadcast,
		net.IPv4bcast,
	)
	if err != nil {
		log.Printf("Gratuitous arp package is malformed: %s", err)
		return err
	}

	err = m.arpClient.WriteTo(gratuitousPackage, ethernetBroadcast)
	if err != nil {
		log.Printf("Cannot send gratuitous arp message: %s", err)
		return err
	}

	return nil
}

func (m *IPManager) QueryAddress() bool {
	c := exec.Command("ip", "addr", "show", m.iface.Name)

	lookup := fmt.Sprintf("inet %s", m.GetCIDR())
	result := false

	stdout, err := c.StdoutPipe()
	if err != nil {
		panic(err)
	}

	err = c.Start()
	if err != nil {
		panic(err)
	}

	scn := bufio.NewScanner(stdout)

	for scn.Scan() {
		line := scn.Text()
		if strings.Contains(line, lookup) {
			result = true
		}
	}

	c.Wait()

	return result
}

func (m *IPManager) ConfigureAddress() bool {
	log.Printf("Configuring address %s on %s", m.GetCIDR(), m.iface.Name)
	return m.runAddressConfiguration("add")
}

func (m *IPManager) DeconfigureAddress() bool {
	log.Printf("Removing address %s on %s", m.GetCIDR(), m.iface.Name)
	return m.runAddressConfiguration("delete")
}

func (m *IPManager) runAddressConfiguration(action string) bool {
	c := exec.Command("ip", "addr", action,
		m.GetCIDR(),
		"dev", m.iface.Name)
	err := c.Run()

	switch exit := err.(type) {
	case *exec.ExitError:
		if status, ok := exit.Sys().(syscall.WaitStatus); ok {
			if status.ExitStatus() == 2 {
				// Already exists
				return true
			} else {
				log.Printf("Got error %s", status)
			}
		}

		return false
	}
	if err != nil {
		log.Printf("Error running ip address %s %s on %s: %s",
			action, m.vip, m.iface.Name, err)
		return false
	}
	return true
}
