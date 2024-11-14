package ipmanager

import (
	"context"
	"net"
	"net/netip"
	"sync"
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

	states       <-chan bool
	currentState bool
	stateLock    sync.Mutex
	recheck      *sync.Cond
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
	m.recheck = sync.NewCond(&m.stateLock)
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
			log.Infof("IP address %s state is %s, must be %s",
				m.configurer.getCIDR(),
				strUpDown[actualState],
				strUpDown[desiredState])
			if actualState != desiredState {
				m.stateLock.Unlock()
				var configureState bool
				if desiredState {
					configureState = m.configurer.configureAddress()
				} else {
					configureState = m.configurer.deconfigureAddress()
				}
				if !configureState {
					log.Error("Error while acquiring virtual ip for this machine")
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
