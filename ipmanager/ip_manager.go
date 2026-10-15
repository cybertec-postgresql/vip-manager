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

// IPManager implements the main functionality of the VIP manager
type IPManager struct {
	configurer ipConfigurer
	logger     *zap.SugaredLogger

	states        <-chan bool
	shouldSetIPUp atomic.Bool
	recheckChan   chan struct{}
}

// log returns the logger of the manager, see IPConfiguration.log
func (m *IPManager) log() *zap.SugaredLogger {
	if m.logger == nil {
		return zap.NewNop().Sugar()
	}
	return m.logger
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
	sugar := conf.Logger.Sugar()
	ipConf.Logger = sugar
	m = &IPManager{
		states: states,
		logger: sugar,
	}
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
	var lastIsUp, lastShouldBeUp, reported bool
	for {
		isIPUp := m.configurer.queryAddress()
		shouldSetIPUp := m.shouldSetIPUp.Load()
		// the address is rechecked every few seconds and hardly ever changes,
		// so report it once and then only when something about it changed.
		// Repeating it forever used to be a good part of the vip-manager log.
		message := "IP address %s is %s, must be %s"
		if !reported || isIPUp != lastIsUp || shouldSetIPUp != lastShouldBeUp {
			m.log().Infof(message, m.configurer.getCIDR(), strUpDown[isIPUp], strUpDown[shouldSetIPUp])
			reported, lastIsUp, lastShouldBeUp = true, isIPUp, shouldSetIPUp
		} else {
			m.log().Debugf(message, m.configurer.getCIDR(), strUpDown[isIPUp], strUpDown[shouldSetIPUp])
		}
		if isIPUp != shouldSetIPUp {
			var isOk bool
			if shouldSetIPUp {
				isOk = m.configurer.configureAddress()
			} else {
				isOk = m.configurer.deconfigureAddress()
			}
			if !isOk {
				m.log().Error("Failed to configure virtual ip for this machine")
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
