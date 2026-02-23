package ipmanager

import (
	"strings"
	"testing"

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
