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

// ipConfigurer adds, removes and queries the virtual IP. A cancelled ctx aborts
// the command or API call that is running.
type ipConfigurer interface {
	queryAddress(ctx context.Context) bool
	configureAddress(ctx context.Context) bool
	deconfigureAddress(ctx context.Context) bool
	getCIDR() string
}

// defaultShutdownGrace bounds two steps of the shutdown: the wait for applyLoop
// to return after its context is cancelled, and the removal of the address
// that follows. A cancelled context kills the `ip` or curl command applyLoop
// runs, so the first wait is normally short. It only reaches the limit when a
// configurer ignores the cancellation. The TimeoutStopSec of
// vip-manager.service must stay above twice this value, or systemd sends
// SIGKILL before the address is removed.
const defaultShutdownGrace = 10 * time.Second

// IPManager implements the main functionality of the VIP manager
type IPManager struct {
	configurer ipConfigurer
	log        *zap.SugaredLogger

	states        <-chan bool
	shouldSetIPUp atomic.Bool
	recheckChan   chan struct{}
	shutdownGrace time.Duration
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
	log := conf.Logger.Sugar()
	ipConf := &IPConfiguration{
		VIP:        vip,
		Netmask:    vipMask,
		Iface:      *netIface,
		RetryNum:   conf.RetryNum,
		RetryAfter: conf.RetryAfter,
		Log:        log,
	}
	m = &IPManager{
		log:           log,
		states:        states,
		shutdownGrace: defaultShutdownGrace,
		// buffered, see triggerRecheck
		recheckChan: make(chan struct{}, 1),
	}
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
		// select picks a ready case at random, so a pending recheck or an
		// expired timer can win over ctx.Done(). Without this check the loop
		// could configure the address again after the shutdown has started.
		if ctx.Err() != nil {
			return
		}
		isIPUp := m.configurer.queryAddress(ctx)
		shouldSetIPUp := m.shouldSetIPUp.Load()
		// The address is rechecked every ten seconds and hardly ever changes,
		// so it is reported at info level on the first pass and when the state
		// changed, and at debug level otherwise.
		report := m.log.Debugf
		if !reported || isIPUp != lastIsUp || shouldSetIPUp != lastShouldBeUp {
			report = m.log.Infof
			reported, lastIsUp, lastShouldBeUp = true, isIPUp, shouldSetIPUp
		}
		report("IP address %s is %s, must be %s",
			m.configurer.getCIDR(),
			strUpDown[isIPUp],
			strUpDown[shouldSetIPUp])
		if isIPUp != shouldSetIPUp {
			var isOk bool
			if shouldSetIPUp {
				isOk = m.configurer.configureAddress(ctx)
			} else {
				isOk = m.configurer.deconfigureAddress(ctx)
			}
			if !isOk {
				m.log.Error("Failed to configure virtual ip for this machine")
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

// removeAddressOnShutdown waits for applyLoop to return and then removes the
// address. ctx is already cancelled, which kills the command applyLoop runs,
// so the configure cannot land after the removal. The wait is bounded in case a
// configurer ignores the cancellation.
func (m *IPManager) removeAddressOnShutdown(ctx context.Context, applyDone <-chan struct{}) {
	select {
	case <-applyDone:
	case <-time.After(m.shutdownGrace):
		m.log.Warn("applyLoop did not return after the cancellation, removing the address anyway")
	}
	// ctx is cancelled and would kill the removal, so run it on a fresh deadline
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.shutdownGrace)
	defer cancel()
	// The removal is always attempted, also when the last query said the
	// address is absent, because a stale answer must not leave it assigned.
	// `ip addr delete` fails on a node that does not hold the address, so a
	// failure is only an error while the address is still there.
	if !m.configurer.deconfigureAddress(cleanupCtx) && m.configurer.queryAddress(cleanupCtx) {
		m.log.Errorf("Failed to remove address %s on shutdown", m.configurer.getCIDR())
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
		case newState := <-states:
			if m.shouldSetIPUp.Load() != newState {
				m.shouldSetIPUp.Store(newState)
				m.triggerRecheck()
			}
		case <-ctx.Done():
			m.removeAddressOnShutdown(ctx, applyDone)
			return
		}
	}
}
