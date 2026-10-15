package ipmanager

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"go.uber.org/zap"
)

type ipConfigurer interface {
	queryAddress() bool
	configureAddress() bool
	deconfigureAddress() bool
	getCIDR() string
}

var log *zap.SugaredLogger

// shutdownGrace bounds the wait for an address (de)configuration that is still
// running when the shutdown starts. The wait has to be bounded: the Hetzner
// configurer shells out to curl, and if the cleanup took longer than the
// TimeoutStopSec of the service unit, systemd would send SIGKILL - in exactly
// the situation where the address must be released. Keep the TimeoutStopSec of
// vip-manager.service above this value.
// (a variable so that tests can shorten it)
var shutdownGrace = 10 * time.Second

// IPManager implements the main functionality of the VIP manager
type IPManager struct {
	configurer ipConfigurer

	states        <-chan bool
	shouldSetIPUp atomic.Bool
	recheckChan   chan struct{}
}

func getMask(vip netip.Addr, mask int) net.IPMask {
	if vip.Is4() || vip.Is4In6() { //IPv4
		if mask > 0 && mask < 33 {
			return net.CIDRMask(mask, 32)
		}
		var ip net.IP = vip.Unmap().AsSlice()
		return ip.DefaultMask()
	}
	return net.CIDRMask(mask, 128) //IPv6
}

func getNetIface(iface string) (*net.Interface, error) {
	netIface, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, fmt.Errorf("failed to get interface %s: %w", iface, err)
	}
	if netIface.Flags&net.FlagUp == 0 {
		return nil, fmt.Errorf("interface %s is not up", iface)
	}
	return netIface, nil
}

// NewIPManager returns a new instance of IPManager
func NewIPManager(conf *vipconfig.Config, states <-chan bool) (m *IPManager, err error) {
	vip, err := netip.ParseAddr(conf.IP)
	if err != nil {
		return nil, fmt.Errorf("failed to parse VIP address: %w", err)
	}
	// Normalise ::ffff:a.b.c.d to a.b.c.d so that the rest of the code can rely
	// on Is4/Is6 to pick the ARP or the Neighbor Advertisement path.
	vip = vip.Unmap()
	vipMask := getMask(vip, conf.Mask)
	netIface, err := getNetIface(conf.Iface)
	if err != nil {
		return nil, err
	}
	ipConf := &IPConfiguration{
		VIP:        vip,
		Netmask:    vipMask,
		Iface:      *netIface,
		RetryNum:   conf.RetryNum,
		RetryAfter: conf.RetryAfter,
	}
	m = &IPManager{
		states: states,
	}
	log = conf.Logger.Sugar()
	// buffered, see triggerRecheck
	m.recheckChan = make(chan struct{}, 1)
	switch conf.HostingType {
	case "hetzner":
		m.configurer, err = newHetznerConfigurer(ipConf, conf.Verbose)
	case "basic":
		fallthrough
	default:
		m.configurer, err = newBasicConfigurer(ipConf)
	}
	if err != nil {
		m = nil
	}
	return
}

func (m *IPManager) applyLoop(ctx context.Context) {
	strUpDown := map[bool]string{true: "up", false: "down"}
	for {
		isIPUp := m.configurer.queryAddress()
		shouldSetIPUp := m.shouldSetIPUp.Load()
		log.Infof("IP address %s is %s, must be %s",
			m.configurer.getCIDR(),
			strUpDown[isIPUp],
			strUpDown[shouldSetIPUp])
		if isIPUp != shouldSetIPUp {
			var isOk bool
			if shouldSetIPUp {
				isOk = m.configurer.configureAddress()
			} else {
				isOk = m.configurer.deconfigureAddress()
			}
			if !isOk {
				log.Error("Failed to configure virtual ip for this machine")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-m.recheckChan: // signal to recheck
		case <-time.After(time.Duration(10) * time.Second): // recheck every 10 seconds
		}
	}
}

// triggerRecheck asks applyLoop to reevaluate the state. The send never blocks:
// the channel is buffered and a signal that is already pending is as good as a
// new one, because applyLoop reads the current state itself. A blocking send
// here used to hang forever once applyLoop had returned on a cancelled
// context, which kept the shutdown below from ever removing the address.
func (m *IPManager) triggerRecheck() {
	select {
	case m.recheckChan <- struct{}{}:
	default:
	}
}

// SyncStates implements states synchronization
func (m *IPManager) SyncStates(ctx context.Context, states <-chan bool) {
	applyDone := make(chan struct{})
	go func() {
		defer close(applyDone)
		m.applyLoop(ctx)
	}()
	for {
		select {
		case newState, ok := <-states:
			if !ok {
				// a closed channel is always ready, don't spin on it
				states = nil
				continue
			}
			if m.shouldSetIPUp.Load() != newState {
				m.shouldSetIPUp.Store(newState)
				m.triggerRecheck()
			}
		case <-ctx.Done():
			// wait for a running (de)configuration to finish, so that the
			// cleanup below does not race applyLoop over the same address
			select {
			case <-applyDone:
			case <-time.After(shutdownGrace):
				log.Warn("Timed out waiting for the address configuration to finish, removing the address anyway")
			}
			m.configurer.deconfigureAddress()
			return
		}
	}
}
