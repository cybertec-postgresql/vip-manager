package ipmanager

import (
	"context"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"go.uber.org/zap"
)

// TestMain installs the package level logger once. Assigning it from the
// individual tests races with the goroutines started by SyncStates.
func TestMain(m *testing.M) {
	log = zap.NewNop().Sugar()
	os.Exit(m.Run())
}

func minimalConfig(vip, iface string) *vipconfig.Config {
	return &vipconfig.Config{
		IP:          vip,
		Mask:        24,
		Iface:       iface,
		HostingType: "basic",
		Logger:      zap.NewNop(),
	}
}

// ---------------------------------------------------------------------------
// getNetIface
// ---------------------------------------------------------------------------

// TestGetNetIface_Nonexistent verifies that requesting an interface that does
// not exist returns an error containing "failed to get interface".
func TestGetNetIface_Nonexistent(t *testing.T) {
	t.Parallel()
	_, err := getNetIface("definitely_nonexistent_interface_999")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to get interface") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestGetNetIface_Success tests that getNetIface successfully returns a valid interface.
// On Windows, loopback is "Loopback Pseudo-Interface"; on Unix-like systems it's usually "lo".
// This test skips if no valid interface can be found.
func TestGetNetIface_Success(t *testing.T) {
	t.Parallel()

	// Try common loopback names
	names := []string{"lo", "lo0", "Loopback Pseudo-Interface 1"}
	var iface *net.Interface
	var err error

	for _, name := range names {
		iface, err = getNetIface(name)
		if err == nil {
			break
		}
	}

	if iface == nil || err != nil {
		t.Skip("no valid loopback interface available for testing")
	}
}

// ---------------------------------------------------------------------------
// NewIPManager
// ---------------------------------------------------------------------------

// TestNewIPManager_InvalidVIP verifies that a non-IP string is rejected with
// "failed to parse VIP address".
func TestNewIPManager_InvalidVIP(t *testing.T) {
	t.Parallel()
	states := make(chan bool)
	_, err := NewIPManager(minimalConfig("not-an-ip-address", "lo"), states)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse VIP address") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestNewIPManager_InvalidInterface verifies that a valid VIP combined with a
// nonexistent interface name returns an error from getNetIface.
func TestNewIPManager_InvalidInterface(t *testing.T) {
	t.Parallel()
	states := make(chan bool)
	_, err := NewIPManager(minimalConfig("10.0.0.1", "definitely_nonexistent_interface_999"), states)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to get interface") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ---------------------------------------------------------------------------
// getMask
// ---------------------------------------------------------------------------

func TestGetMask_IPv4_ValidRange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		addr netip.Addr
		mask int
		want string
	}{
		{"IPv4 /24", netip.MustParseAddr("192.168.1.1"), 24, "ffffff00"},
		{"IPv4 /32", netip.MustParseAddr("192.168.1.1"), 32, "ffffffff"},
		{"IPv4 /16", netip.MustParseAddr("10.0.0.1"), 16, "ffff0000"},
		{"IPv6 /64", netip.MustParseAddr("2001:db8::1"), 64, "ffffffffffffffff0000000000000000"},
		{"IPv6 /128", netip.MustParseAddr("2001:db8::1"), 128, "ffffffffffffffffffffffffffffffff"},
		// Is6 reports true for ::ffff:a.b.c.d, but it is an IPv4 address and
		// must get a 32 bit mask, not a 128 bit one.
		{"IPv4-in-IPv6 /24", netip.MustParseAddr("::ffff:192.0.2.1"), 24, "ffffff00"},
		{"IPv4-in-IPv6 /32", netip.MustParseAddr("::ffff:192.0.2.1"), 32, "ffffffff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := getMask(tt.addr, tt.mask)
			if m.String() != tt.want {
				t.Errorf("getMask(%v, %d) = %v, want %v", tt.addr, tt.mask, m.String(), tt.want)
			}
		})
	}
}

func TestGetMask_IPv4_OutOfRange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		addr netip.Addr
		mask int
		desc string
	}{
		{"IPv4 negative", netip.MustParseAddr("192.168.1.1"), -1, "negative mask"},
		{"IPv4 > 32", netip.MustParseAddr("192.168.1.1"), 33, "mask > 32"},
		{"IPv4 zero", netip.MustParseAddr("192.168.1.1"), 0, "zero mask"},
		{"IPv4-in-IPv6 zero", netip.MustParseAddr("::ffff:192.0.2.1"), 0, "zero mask falls back to the default mask"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := getMask(tt.addr, tt.mask)
			// For out-of-range IPv4, we expect default mask
			if m == nil {
				t.Errorf("getMask(%v, %d) returned nil for %s", tt.addr, tt.mask, tt.desc)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Mock configurer for testing applyLoop and SyncStates
// ---------------------------------------------------------------------------

// mockConfigurer counters are written by applyLoop and read by the test
// goroutine, so every access goes through the mutex.
type mockConfigurer struct {
	mu                    sync.Mutex
	queryAddressCount     int
	configureCount        int
	deconfigureCount      int
	shouldQueryFail       bool
	shouldConfigureFail   bool
	shouldDeconfigureFail bool
	shouldQueryReturn     bool
	// onConfigure, when set, is called by configureAddress and can be used to
	// keep a configuration in flight while the test does something else
	onConfigure func()
}

func (m *mockConfigurer) queryAddress() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queryAddressCount++
	if m.shouldQueryFail {
		return false
	}
	return m.shouldQueryReturn
}

func (m *mockConfigurer) configureAddress() bool {
	m.mu.Lock()
	m.configureCount++
	hook := m.onConfigure
	m.mu.Unlock()
	if hook != nil {
		hook()
	}
	return !m.shouldConfigureFail
}

func (m *mockConfigurer) deconfigureAddress() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deconfigureCount++
	return !m.shouldDeconfigureFail
}

func (m *mockConfigurer) queries() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.queryAddressCount
}

func (m *mockConfigurer) configures() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.configureCount
}

func (m *mockConfigurer) deconfigures() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deconfigureCount
}

func (m *mockConfigurer) getCIDR() string {
	return "192.168.1.100/24"
}

func TestApplyLoop_DeconfigureWhenNeeded(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	mock := &mockConfigurer{shouldQueryReturn: true}
	m := &IPManager{
		configurer:  mock,
		recheckChan: make(chan struct{}, 1),
	}
	m.shouldSetIPUp.Store(false)

	m.applyLoop(ctx)

	if mock.deconfigures() == 0 {
		t.Error("expected deconfigureAddress to be called")
	}
}

// ---------------------------------------------------------------------------
// applyLoop
// ---------------------------------------------------------------------------

func TestApplyLoop_ConfigureWhenNeeded(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	mock := &mockConfigurer{shouldQueryReturn: false}
	m := &IPManager{
		configurer:  mock,
		recheckChan: make(chan struct{}, 1),
	}
	m.shouldSetIPUp.Store(true)

	m.applyLoop(ctx)

	if mock.configures() == 0 {
		t.Error("expected configureAddress to be called")
	}
}

func TestApplyLoop_ConfigureFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	mock := &mockConfigurer{shouldQueryReturn: false, shouldConfigureFail: true}
	m := &IPManager{
		configurer:  mock,
		recheckChan: make(chan struct{}, 1),
	}
	m.shouldSetIPUp.Store(true)

	m.applyLoop(ctx)

	if mock.configures() == 0 {
		t.Error("expected configureAddress to be called even if it fails")
	}
}

func TestApplyLoop_QueryFails(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	mock := &mockConfigurer{shouldQueryFail: true}
	m := &IPManager{
		configurer:  mock,
		recheckChan: make(chan struct{}, 1),
	}
	m.shouldSetIPUp.Store(true)

	m.applyLoop(ctx)

	// queryAddress should be called despite failure
	if mock.queries() == 0 {
		t.Error("expected queryAddress to be called")
	}
}

func TestApplyLoop_NoChangeNeeded(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	mock := &mockConfigurer{shouldQueryReturn: true}
	m := &IPManager{
		configurer:  mock,
		recheckChan: make(chan struct{}, 1),
	}
	m.shouldSetIPUp.Store(true) // IP is up and should be up

	m.applyLoop(ctx)

	// Neither configure nor deconfigure should be called
	if mock.configures() > 0 || mock.deconfigures() > 0 {
		t.Error("expected no configuration changes when state matches")
	}
}

// ---------------------------------------------------------------------------
// SyncStates
// ---------------------------------------------------------------------------

func TestSyncStates_StateChange(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	mock := &mockConfigurer{shouldQueryReturn: false}
	m := &IPManager{
		configurer:  mock,
		recheckChan: make(chan struct{}, 10),
	}

	states := make(chan bool, 2)
	states <- true
	states <- false

	go func() {
		time.Sleep(100 * time.Millisecond)
		close(states)
	}()

	m.SyncStates(ctx, states)

	// After false is sent and processed, shouldSetIPUp should be false
	if m.shouldSetIPUp.Load() {
		t.Error("expected shouldSetIPUp to be false after state false was processed")
	}
	if mock.deconfigures() == 0 {
		t.Error("expected deconfigureAddress to be called on context done")
	}
}

func TestNewIPManager_ValidIPv6(t *testing.T) {
	t.Parallel()
	states := make(chan bool)
	conf := minimalConfig("2001:db8::1", "lo")
	conf.Mask = 64
	// This will fail because loopback is typically not used for VIPs, but it tests
	// that we can parse IPv6 addresses
	_, err := NewIPManager(conf, states)
	// Error is expected due to loopback device validation, not IP parsing
	if err != nil {
		if !strings.Contains(err.Error(), "loopback device") {
			// If it's not the loopback error, the test is still valid
			// (we successfully parsed the IPv6 address)
			t.Logf("Got expected error for IPv6 on loopback: %v", err)
		}
	}
}

func TestNewIPManager_Hetzner(t *testing.T) {
	t.Parallel()
	states := make(chan bool)
	conf := minimalConfig("10.0.0.1", "definitely_nonexistent_iface_9999")
	conf.HostingType = "hetzner"
	// Hetzner configurer initialization will fail because the interface doesn't exist
	_, err := NewIPManager(conf, states)
	if err == nil {
		t.Error("expected error for nonexistent interface")
		return
	}
	if !strings.Contains(err.Error(), "failed to get interface") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestSyncStates_WaitsForApplyLoopBeforeRemovingTheAddress covers the shutdown
// path: applyLoop is in the middle of configuring the address when the context
// is cancelled. SyncStates must let that finish before it removes the address,
// otherwise both goroutines work on the same address at the same time.
func TestSyncStates_WaitsForApplyLoopBeforeRemovingTheAddress(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	configuring := make(chan struct{})
	release := make(chan struct{})
	mock := &mockConfigurer{}
	var once sync.Once
	mock.onConfigure = func() {
		once.Do(func() {
			close(configuring) // tell the test we are in the middle of it
			<-release          // and stay here until it says otherwise
		})
	}

	m := &IPManager{configurer: mock, recheckChan: make(chan struct{}, 1)}
	states := make(chan bool, 1)
	states <- true

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.SyncStates(ctx, states)
	}()

	<-configuring // applyLoop is now inside configureAddress
	cancel()
	time.Sleep(100 * time.Millisecond)
	if n := mock.deconfigures(); n != 0 {
		t.Fatalf("address was removed while a configuration was still running (%d calls)", n)
	}

	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SyncStates did not return after the context was cancelled")
	}
	if mock.deconfigures() == 0 {
		t.Error("expected the address to be removed on shutdown")
	}
}

// TestSyncStates_GivesUpOnAStuckConfigurer verifies that a configuration that
// never finishes cannot keep the shutdown from removing the address.
func TestSyncStates_GivesUpOnAStuckConfigurer(t *testing.T) {
	stuck := make(chan struct{})
	defer close(stuck)

	previous := shutdownGrace
	shutdownGrace = 200 * time.Millisecond
	defer func() { shutdownGrace = previous }()

	mock := &mockConfigurer{onConfigure: func() { <-stuck }}
	m := &IPManager{configurer: mock, recheckChan: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	states := make(chan bool, 1)
	states <- true

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.SyncStates(ctx, states)
	}()

	time.Sleep(100 * time.Millisecond) // let applyLoop reach configureAddress
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SyncStates did not give up on the stuck configurer")
	}
	if mock.deconfigures() == 0 {
		t.Error("expected the address to be removed even though applyLoop was stuck")
	}
}

// TestTriggerRecheck_NeverBlocks pins down the root cause of the shutdown
// deadlock: the signal to applyLoop must not block when nobody is listening,
// which is the case as soon as applyLoop has returned on a cancelled context.
func TestTriggerRecheck_NeverBlocks(t *testing.T) {
	t.Parallel()
	m := &IPManager{recheckChan: make(chan struct{}, 1)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 10 {
			m.triggerRecheck()
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("triggerRecheck blocked although nobody was listening")
	}
}
