package ipmanager

import (
	"context"
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
	cleanupArp()
}

var log *zap.SugaredLogger = zap.L().Sugar()

// IPManager implements the main functionality of the VIP manager
type IPManager struct {
	configurer ipConfigurer

	states        <-chan bool
	shouldSetIPUp atomic.Bool
	recheckChan   chan struct{}
}

func getMask(vip netip.Addr, mask int) net.IPMask {
	if vip.Is4() { //IPv4
		if mask > 0 && mask < 33 {
			return net.CIDRMask(mask, 32)
		}
		var ip net.IP = vip.AsSlice()
		return ip.DefaultMask()
	}
	return net.CIDRMask(mask, 128) //IPv6
}

func getNetIface(iface string) *net.Interface {
	netIface, err := net.InterfaceByName(iface)
	if err != nil {
		log.Fatalf("Obtaining the interface raised an error: %s", err)
	}
	return netIface
}

// NewIPManager returns a new instance of IPManager
func NewIPManager(conf *vipconfig.Config, states <-chan bool) (m *IPManager, err error) {
	vip, err := netip.ParseAddr(conf.IP)
	if err != nil {
		return nil, err
	}
	vipMask := getMask(vip, conf.Mask)
	netIface := getNetIface(conf.Iface)
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
			m.configurer.cleanupArp()
			return
		}
	}
}
