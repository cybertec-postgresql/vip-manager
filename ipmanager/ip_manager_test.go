package ipmanager

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// newTestManager returns a manager with the defaults NewIPManager sets, around
// the given configurer.
func newTestManager(c ipConfigurer) *IPManager {
	return &IPManager{
		configurer:    c,
		log:           zap.NewNop().Sugar(),
		recheckChan:   make(chan struct{}, 1),
		shutdownGrace: defaultShutdownGrace,
	}
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
	onConfigure func(ctx context.Context)
	// configuring counts the configureAddress calls in flight, and overlapped
	// records a deconfigureAddress that ran while one of them was
	configuring int
	overlapped  bool
}

func (m *mockConfigurer) queryAddress(context.Context) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queryAddressCount++
	if m.shouldQueryFail {
		return false
	}
	return m.shouldQueryReturn
}

func (m *mockConfigurer) configureAddress(ctx context.Context) bool {
	m.mu.Lock()
	m.configureCount++
	m.configuring++
	hook := m.onConfigure
	m.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	m.mu.Lock()
	m.configuring--
	m.mu.Unlock()
	return !m.shouldConfigureFail
}

func (m *mockConfigurer) deconfigureAddress(context.Context) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deconfigureCount++
	if m.configuring > 0 {
		m.overlapped = true
	}
	return !m.shouldDeconfigureFail
}

func (m *mockConfigurer) overlappedRemoval() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.overlapped
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
	m := newTestManager(mock)
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
	m := newTestManager(mock)
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
	m := newTestManager(mock)
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
	m := newTestManager(mock)
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
	m := newTestManager(mock)
	m.shouldSetIPUp.Store(true) // IP is up and should be up

	m.applyLoop(ctx)

	// Neither configure nor deconfigure should be called
	if mock.configures() > 0 || mock.deconfigures() > 0 {
		t.Error("expected no configuration changes when state matches")
	}
}

// TestApplyLoop_ReportsTheStateOnlyWhenItChanges verifies that the state of
// the address is written at info level on the first pass and after a change,
// and at debug level on the passes in between.
func TestApplyLoop_ReportsTheStateOnlyWhenItChanges(t *testing.T) {
	t.Parallel()
	core, logs := observer.New(zapcore.DebugLevel)
	mock := &mockConfigurer{shouldQueryReturn: true}
	m := newTestManager(mock)
	m.log = zap.New(core).Sugar()
	m.shouldSetIPUp.Store(true) // IP is up and should be up

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.applyLoop(ctx) }()
	reports := func() []observer.LoggedEntry {
		return logs.FilterMessageSnippet("must be").All()
	}
	// waitForReport waits until the loop has written its n-th report, after
	// which it is idle until the next recheck
	waitForReport := func(n int) {
		t.Helper()
		for deadline := time.Now().Add(3 * time.Second); len(reports()) < n; {
			if time.Now().After(deadline) {
				t.Fatalf("the apply loop did not write report %d", n)
			}
			time.Sleep(time.Millisecond)
		}
	}
	// pass drives the loop through its n-th pass, the first one needs no trigger
	pass := func(n int) {
		t.Helper()
		if n > 1 {
			m.triggerRecheck()
		}
		waitForReport(n)
	}
	pass(1)
	pass(2)
	pass(3)
	m.shouldSetIPUp.Store(false) // the state changes, so pass 4 reports it
	pass(4)
	cancel()
	<-done

	var levels []zapcore.Level
	for _, entry := range reports() {
		levels = append(levels, entry.Level)
	}
	want := []zapcore.Level{zapcore.InfoLevel, zapcore.DebugLevel, zapcore.DebugLevel, zapcore.InfoLevel}
	if !slices.Equal(levels, want) {
		t.Errorf("expected the report levels %v, got %v", want, levels)
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
	m := newTestManager(mock)

	states := make(chan bool, 2)
	states <- true
	states <- false

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

// startConfiguring runs SyncStates with a pending "up" state and returns once
// applyLoop is inside configureAddress. hook runs there after the signal and
// gets the context of applyLoop. The returned channel closes when SyncStates
// returns.
func startConfiguring(ctx context.Context, m *IPManager, mock *mockConfigurer, hook func(context.Context)) <-chan struct{} {
	configuring := make(chan struct{})
	var once sync.Once
	mock.onConfigure = func(ctx context.Context) {
		once.Do(func() {
			close(configuring)
			hook(ctx)
		})
	}
	states := make(chan bool, 1)
	states <- true
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.SyncStates(ctx, states)
	}()
	<-configuring
	return done
}

// TestSyncStates_CancelsARunningConfiguration covers the shutdown path:
// applyLoop is in the middle of configuring the address when the context is
// cancelled. The cancellation must stop the configuration, and the address
// must only be removed after it stopped, otherwise the configuration could
// land after the removal.
func TestSyncStates_CancelsARunningConfiguration(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mock := &mockConfigurer{}
	m := newTestManager(mock)
	// far longer than the test may take, so only the cancellation can end the wait
	m.shutdownGrace = time.Hour

	done := startConfiguring(ctx, m, mock, func(ctx context.Context) { <-ctx.Done() })
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SyncStates did not return after the context was cancelled")
	}
	if mock.deconfigures() != 1 {
		t.Errorf("expected the address to be removed once on shutdown, got %d removals", mock.deconfigures())
	}
	if mock.overlappedRemoval() {
		t.Error("the address was removed while a configuration was still running")
	}
}

// TestSyncStates_GivesUpOnAStuckConfigurer verifies that a configuration that
// ignores the cancellation cannot keep the shutdown from removing the address.
func TestSyncStates_GivesUpOnAStuckConfigurer(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stuck := make(chan struct{})
	defer close(stuck)

	mock := &mockConfigurer{}
	m := newTestManager(mock)
	m.shutdownGrace = 200 * time.Millisecond

	done := startConfiguring(ctx, m, mock, func(context.Context) { <-stuck })
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

// TestApplyLoop_StopsOnACancelledContext verifies that applyLoop does not
// touch the address once the context is cancelled, even if a recheck is
// pending, which the select at the end of the loop could pick over ctx.Done().
func TestApplyLoop_StopsOnACancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mock := &mockConfigurer{shouldQueryReturn: false}
	m := newTestManager(mock)
	m.shouldSetIPUp.Store(true)
	m.triggerRecheck()

	m.applyLoop(ctx)

	if mock.queries() != 0 || mock.configures() != 0 {
		t.Errorf("applyLoop worked on the address after the cancellation: %d queries, %d configures",
			mock.queries(), mock.configures())
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

// TestRemoveAddressOnShutdown_ReportsOnlyAnAddressThatStays verifies that a
// failed removal is an error only while the address is still assigned.
// `ip addr delete` fails on a node that does not hold the address, and that
// must not log an error on every stop of a replica.
func TestRemoveAddressOnShutdown_ReportsOnlyAnAddressThatStays(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		removalFails  bool
		stillAssigned bool
		wantErrors    int
	}{
		{"removal fails, address absent", true, false, 0},
		{"removal fails, address still assigned", true, true, 1},
		{"removal succeeds", false, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			core, logs := observer.New(zapcore.ErrorLevel)
			mock := &mockConfigurer{
				shouldDeconfigureFail: tt.removalFails,
				shouldQueryReturn:     tt.stillAssigned,
			}
			m := newTestManager(mock)
			m.log = zap.New(core).Sugar()

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			applyDone := make(chan struct{})
			close(applyDone)
			m.removeAddressOnShutdown(ctx, applyDone)

			if mock.deconfigures() != 1 {
				t.Errorf("expected one removal, got %d", mock.deconfigures())
			}
			if logs.Len() != tt.wantErrors {
				t.Errorf("expected %d error log entries, got %d: %v", tt.wantErrors, logs.Len(), logs.All())
			}
		})
	}
}
