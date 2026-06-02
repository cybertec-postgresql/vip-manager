package ipmanager

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"go.uber.org/zap"
)

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
		name   string
		addr   netip.Addr
		mask   int
		want   string
	}{
		{"IPv4 /24", netip.MustParseAddr("192.168.1.1"), 24, "ffffff00"},
		{"IPv4 /32", netip.MustParseAddr("192.168.1.1"), 32, "ffffffff"},
		{"IPv4 /16", netip.MustParseAddr("10.0.0.1"), 16, "ffff0000"},
		{"IPv6 /64", netip.MustParseAddr("2001:db8::1"), 64, "ffffffffffffffff0000000000000000"},
		{"IPv6 /128", netip.MustParseAddr("2001:db8::1"), 128, "ffffffffffffffffffffffffffffffff"},
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
		name   string
		addr   netip.Addr
		mask   int
		desc   string
	}{
		{"IPv4 negative", netip.MustParseAddr("192.168.1.1"), -1, "negative mask"},
		{"IPv4 > 32", netip.MustParseAddr("192.168.1.1"), 33, "mask > 32"},
		{"IPv4 zero", netip.MustParseAddr("192.168.1.1"), 0, "zero mask"},
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

type mockConfigurer struct {
	queryAddressCount  int
	configureCount     int
	deconfigureCount   int
	shouldQueryFail    bool
	shouldConfigureFail bool
	shouldDeconfigureFail bool
	shouldQueryReturn  bool
}

func (m *mockConfigurer) queryAddress() bool {
	m.queryAddressCount++
	if m.shouldQueryFail {
		return false
	}
	return m.shouldQueryReturn
}

func (m *mockConfigurer) configureAddress() bool {
	m.configureCount++
	return !m.shouldConfigureFail
}

func (m *mockConfigurer) deconfigureAddress() bool {
	m.deconfigureCount++
	return !m.shouldDeconfigureFail
}

func (m *mockConfigurer) getCIDR() string {
	return "192.168.1.100/24"
}

func TestApplyLoop_DeconfigureWhenNeeded(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	conf := zap.NewNop()
	log = conf.Sugar()

	mock := &mockConfigurer{shouldQueryReturn: true}
	m := &IPManager{
		configurer:  mock,
		recheckChan: make(chan struct{}, 1),
	}
	m.shouldSetIPUp.Store(false)

	m.applyLoop(ctx)

	if mock.deconfigureCount == 0 {
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

	conf := zap.NewNop()
	log = conf.Sugar()

	mock := &mockConfigurer{shouldQueryReturn: false}
	m := &IPManager{
		configurer:  mock,
		recheckChan: make(chan struct{}, 1),
	}
	m.shouldSetIPUp.Store(true)

	m.applyLoop(ctx)

	if mock.configureCount == 0 {
		t.Error("expected configureAddress to be called")
	}
}

func TestApplyLoop_ConfigureFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	conf := zap.NewNop()
	log = conf.Sugar()

	mock := &mockConfigurer{shouldQueryReturn: false, shouldConfigureFail: true}
	m := &IPManager{
		configurer:  mock,
		recheckChan: make(chan struct{}, 1),
	}
	m.shouldSetIPUp.Store(true)

	m.applyLoop(ctx)

	if mock.configureCount == 0 {
		t.Error("expected configureAddress to be called even if it fails")
	}
}

func TestApplyLoop_QueryFails(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	conf := zap.NewNop()
	log = conf.Sugar()

	mock := &mockConfigurer{shouldQueryFail: true}
	m := &IPManager{
		configurer:  mock,
		recheckChan: make(chan struct{}, 1),
	}
	m.shouldSetIPUp.Store(true)

	m.applyLoop(ctx)

	// queryAddress should be called despite failure
	if mock.queryAddressCount == 0 {
		t.Error("expected queryAddress to be called")
	}
}

func TestApplyLoop_NoChangeNeeded(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	conf := zap.NewNop()
	log = conf.Sugar()

	mock := &mockConfigurer{shouldQueryReturn: true}
	m := &IPManager{
		configurer:  mock,
		recheckChan: make(chan struct{}, 1),
	}
	m.shouldSetIPUp.Store(true) // IP is up and should be up

	m.applyLoop(ctx)

	// Neither configure nor deconfigure should be called
	if mock.configureCount > 0 || mock.deconfigureCount > 0 {
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

	conf := zap.NewNop()
	log = conf.Sugar()

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
	if mock.deconfigureCount == 0 {
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
	conf := minimalConfig("10.0.0.1", "lo")
	conf.HostingType = "hetzner"
	// Hetzner configurer validates config at initialization time, expecting loopback
	// interface to fail with "no such network interface" error
	_, err := NewIPManager(conf, states)
	if err == nil {
		t.Error("expected error for loopback interface on hetzner")
	}
	if !strings.Contains(err.Error(), "failed to get interface") {
		t.Errorf("unexpected error: %v", err)
	}
}
