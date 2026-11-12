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

// IPManager implements the main functionality of the VIP manager
type IPManager struct {
	configurer ipConfigurer

	states        <-chan bool
	shouldSetIPUp atomic.Bool
	recheckChan   chan struct{}
}

// getMask returns the netmask for the given address. A mask of zero or less
// selects the default mask of the address class, which is what --netmask=-1
// documents. An out of range mask is rejected instead of being passed on to
// net.CIDRMask, which answers nil for it - a nil mask used to make getCIDR
// panic later on, when the manager was already running.
func getMask(vip netip.Addr, mask int) (net.IPMask, error) {
	if vip.Is4() || vip.Is4In6() { //IPv4
		if mask > 32 {
			return nil, fmt.Errorf("netmask /%d is out of range for the IPv4 address %s", mask, vip)
		}
		if mask > 0 {
			return net.CIDRMask(mask, 32), nil
		}
		var ip net.IP = vip.Unmap().AsSlice()
		return ip.DefaultMask(), nil
	}
	//IPv6, there is no class based default mask to fall back to
	if mask <= 0 || mask > 128 {
		return nil, fmt.Errorf("netmask /%d is out of range for the IPv6 address %s, specify one between 1 and 128", mask, vip)
	}
	return net.CIDRMask(mask, 128), nil
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
	vipMask, err := getMask(vip, conf.Mask)
	if err != nil {
		return nil, err
	}
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
	m.recheckChan = make(chan struct{})
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

// SyncStates implements states synchronization
func (m *IPManager) SyncStates(ctx context.Context, states <-chan bool) {
	go m.applyLoop(ctx)
	for {
		select {
		case newState := <-states:
			if m.shouldSetIPUp.Load() != newState {
				m.shouldSetIPUp.Store(newState)
				m.recheckChan <- struct{}{}
			}
		case <-ctx.Done():
			m.configurer.deconfigureAddress()
			return
		}
	}
}
