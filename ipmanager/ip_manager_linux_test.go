//go:build linux

package ipmanager

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestGetNetIface_InterfaceDown verifies that getNetIface returns an error
// when the interface exists but is not up. Creates a dummy interface and
// brings it down. Requires root privileges on Linux.
func TestGetNetIface_InterfaceDown(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("creating down interface requires root privileges on Linux")
	}

	dummyName := "viptest_dummy0"

	// Create dummy interface
	cmd := exec.Command("ip", "link", "add", dummyName, "type", "dummy")
	if err := cmd.Run(); err != nil {
		t.Skipf("failed to create dummy interface: %v", err)
	}

	// Ensure cleanup
	t.Cleanup(func() {
		_ = exec.Command("ip", "link", "delete", dummyName).Run()
	})

	// Interface is created in DOWN state by default, but let's be explicit
	cmd = exec.Command("ip", "link", "set", dummyName, "down")
	if err := cmd.Run(); err != nil {
		t.Skipf("failed to bring dummy interface down: %v", err)
	}

	// Now test getNetIface with the down interface
	_, err := getNetIface(dummyName)
	if err == nil {
		t.Errorf("expected error for down interface %s, got nil", dummyName)
	}
	if !strings.Contains(err.Error(), "is not up") {
		t.Errorf("expected 'is not up' error for interface %s, got: %v", dummyName, err)
	}
}
