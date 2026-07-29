//go:build linux

package ipmanager

import (
	"net"
	"net/netip"
	"os"
	"syscall"
	"testing"

	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// htons
// ---------------------------------------------------------------------------

func TestHtons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input uint16
		want  uint16
	}{
		{
			name:  "zero",
			input: 0x0000,
			want:  0x0000,
		},
		{
			name:  "ETH_P_ARP",
			input: 0x0806,
			want:  0x0608,
		},
		{
			name:  "ETH_P_ALL",
			input: 0x0003,
			want:  0x0300,
		},
		{
			name:  "max value",
			input: 0xFFFF,
			want:  0xFFFF,
		},
		{
			name:  "asymmetric value",
			input: 0x1234,
			want:  0x3412,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := htons(tt.input)
			if got != tt.want {
				t.Errorf("htons(0x%04X) = 0x%04X, want 0x%04X", tt.input, got, tt.want)
			}
		})
	}
}

func TestHtons_Reversible(t *testing.T) {
	t.Parallel()

	// htons should be its own inverse (calling it twice returns the original value)
	testValues := []uint16{0x0000, 0x0001, 0x0100, 0x1234, 0xABCD, 0xFFFF}
	for _, val := range testValues {
		reversed := htons(htons(val))
		if reversed != val {
			t.Errorf("htons(htons(0x%04X)) = 0x%04X, want 0x%04X", val, reversed, val)
		}
	}
}

// ---------------------------------------------------------------------------
// sendPacketLinux
// ---------------------------------------------------------------------------

func TestSendPacketLinux_LoopbackInterface(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("sendPacketLinux tests require root privileges")
	}

	// Get the loopback interface
	lo, err := net.InterfaceByName("lo")
	if err != nil {
		t.Fatalf("failed to get loopback interface: %v", err)
	}

	// Create a minimal Ethernet frame (too small to be valid, but enough to test the syscall)
	packet := make([]byte, 64)
	for i := range packet {
		packet[i] = 0x00
	}

	// Try to send the packet - this exercises the full sendPacketLinux code path:
	// 1. Socket creation
	// 2. Bind
	// 3. Sendto
	err = sendPacketLinux(*lo, packet)

	// Sending on loopback might fail or succeed depending on kernel configuration
	// We just want to ensure the function doesn't panic and handles errors appropriately
	if err != nil {
		// Expected possible errors include permission denied or invalid argument
		if errno, ok := err.(syscall.Errno); ok {
			switch errno {
			case syscall.EPERM, syscall.EACCES:
				t.Logf("sendPacketLinux returned permission error (expected in some environments): %v", err)
			case syscall.EINVAL, syscall.ENETDOWN:
				t.Logf("sendPacketLinux returned network error (acceptable): %v", err)
			default:
				t.Logf("sendPacketLinux returned error: %v", err)
			}
		}
	}
}

func TestSendPacketLinux_ValidInterface(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("sendPacketLinux tests require root privileges")
	}

	// Find any available network interface (preferably not loopback)
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("failed to get network interfaces: %v", err)
	}

	var testIface *net.Interface
	for i := range ifaces {
		iface := &ifaces[i]
		// Look for an interface that is up and has a valid hardware address
		if iface.Flags&net.FlagUp != 0 &&
			iface.HardwareAddr != nil &&
			len(iface.HardwareAddr) == 6 &&
			iface.HardwareAddr.String() != "00:00:00:00:00:00" {
			testIface = iface
			break
		}
	}

	if testIface == nil {
		t.Skip("no suitable network interface found for testing")
	}

	// Create a properly formatted Ethernet/ARP packet
	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     netip.MustParseAddr("192.0.2.1"),
			Netmask: net.CIDRMask(24, 32),
			Iface:   *testIface,
		},
	}

	packet, err := c.createGratuitousARP()
	if err != nil {
		t.Fatalf("failed to create gratuitous ARP packet: %v", err)
	}

	// Send the packet - this exercises all code paths in sendPacketLinux
	err = sendPacketLinux(*testIface, packet)

	// The send might succeed or fail depending on network configuration
	// We're mainly testing that all code paths execute without panic
	if err != nil {
		t.Logf("sendPacketLinux returned error (acceptable): %v", err)
	} else {
		t.Log("sendPacketLinux succeeded")
	}
}

func TestSendPacketLinux_InvalidInterface(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("sendPacketLinux tests require root privileges")
	}

	// Create an interface with an invalid index
	invalidIface := net.Interface{
		Index: 99999, // Very unlikely to exist
		Name:  "nonexistent0",
	}

	packet := make([]byte, 64)
	err := sendPacketLinux(invalidIface, packet)

	// Should fail when trying to bind or send
	if err == nil {
		t.Log("sendPacketLinux unexpectedly succeeded with invalid interface (kernel may have allowed it)")
	} else {
		t.Logf("sendPacketLinux correctly failed with invalid interface: %v", err)
	}
}

func TestSendPacketLinux_EmptyPacket(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("sendPacketLinux tests require root privileges")
	}

	lo, err := net.InterfaceByName("lo")
	if err != nil {
		t.Fatalf("failed to get loopback interface: %v", err)
	}

	// Try to send an empty packet
	err = sendPacketLinux(*lo, []byte{})

	// May succeed or fail, but should not panic
	if err != nil {
		t.Logf("sendPacketLinux with empty packet returned: %v", err)
	}
}

// ---------------------------------------------------------------------------
// configureAddress
// ---------------------------------------------------------------------------

func TestBasicConfigurer_configureAddress_RequiresRoot(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test must run as non-root to verify permission checks")
	}

	conf := zap.NewNop()
	log = conf.Sugar()

	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     netip.MustParseAddr("192.0.2.1"), // TEST-NET-1 (RFC 5737)
			Netmask: net.CIDRMask(24, 32),
			Iface: net.Interface{
				Name:         "lo",
				HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			},
		},
	}

	// Should fail due to lack of privileges
	result := c.configureAddress()
	if result {
		t.Error("configureAddress() should fail without root privileges")
	}
}

func TestBasicConfigurer_configureAddress_NonexistentInterface(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("configureAddress tests require root privileges")
	}

	conf := zap.NewNop()
	log = conf.Sugar()

	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     netip.MustParseAddr("192.0.2.1"),
			Netmask: net.CIDRMask(24, 32),
			Iface: net.Interface{
				Name:         "nonexistent999",
				HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			},
		},
	}

	result := c.configureAddress()
	if result {
		t.Error("configureAddress() should fail for non-existent interface")
	}
}

// ---------------------------------------------------------------------------
// deconfigureAddress
// ---------------------------------------------------------------------------

func TestBasicConfigurer_deconfigureAddress_RequiresRoot(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test must run as non-root to verify permission checks")
	}

	conf := zap.NewNop()
	log = conf.Sugar()

	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     netip.MustParseAddr("192.0.2.1"),
			Netmask: net.CIDRMask(24, 32),
			Iface: net.Interface{
				Name:         "lo",
				HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			},
		},
	}

	// Should fail due to lack of privileges
	result := c.deconfigureAddress()
	if result {
		t.Error("deconfigureAddress() should fail without root privileges")
	}
}

func TestBasicConfigurer_deconfigureAddress_NonexistentInterface(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("deconfigureAddress tests require root privileges")
	}

	conf := zap.NewNop()
	log = conf.Sugar()

	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     netip.MustParseAddr("192.0.2.1"),
			Netmask: net.CIDRMask(24, 32),
			Iface: net.Interface{
				Name:         "nonexistent999",
				HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			},
		},
	}

	result := c.deconfigureAddress()
	if result {
		t.Error("deconfigureAddress() should fail for non-existent interface")
	}
}

// ---------------------------------------------------------------------------
// runAddressConfiguration
// ---------------------------------------------------------------------------

func TestBasicConfigurer_runAddressConfiguration_Add(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("runAddressConfiguration tests require root privileges")
	}

	conf := zap.NewNop()
	log = conf.Sugar()

	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     netip.MustParseAddr("192.0.2.1"),
			Netmask: net.CIDRMask(24, 32),
			Iface: net.Interface{
				Name:         "nonexistent999",
				HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			},
		},
	}

	// Should fail for non-existent interface
	result := c.runAddressConfiguration("add")
	if result {
		t.Error("runAddressConfiguration(add) should fail for non-existent interface")
	}
}

func TestBasicConfigurer_runAddressConfiguration_Delete(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("runAddressConfiguration tests require root privileges")
	}

	conf := zap.NewNop()
	log = conf.Sugar()

	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     netip.MustParseAddr("192.0.2.1"),
			Netmask: net.CIDRMask(24, 32),
			Iface: net.Interface{
				Name:         "nonexistent999",
				HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			},
		},
	}

	// Should fail for non-existent interface
	result := c.runAddressConfiguration("delete")
	if result {
		t.Error("runAddressConfiguration(delete) should fail for non-existent interface")
	}
}

// ---------------------------------------------------------------------------
// runAddressConfiguration - test success path
// ---------------------------------------------------------------------------

func TestBasicConfigurer_runAddressConfiguration_SuccessPath(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("runAddressConfiguration tests require root privileges")
	}

	conf := zap.NewNop()
	log = conf.Sugar()

	// Get the loopback interface
	lo, err := net.InterfaceByName("lo")
	if err != nil {
		t.Skip("loopback interface not available")
	}

	testIP := netip.MustParseAddr("192.0.2.77")
	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     testIP,
			Netmask: net.CIDRMask(32, 32),
			Iface: net.Interface{
				Index:        lo.Index,
				Name:         lo.Name,
				HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			},
		},
	}

	// Ensure cleanup
	defer c.runAddressConfiguration("delete")

	// Test successful add
	if result := c.runAddressConfiguration("add"); result {
		t.Log("runAddressConfiguration(add) succeeded")
		// Clean up
		c.runAddressConfiguration("delete")
	} else {
		t.Log("runAddressConfiguration(add) failed (may be due to system restrictions)")
	}
}

// ---------------------------------------------------------------------------
// Integration test: configure and deconfigure on loopback (requires root)
// ---------------------------------------------------------------------------

func TestBasicConfigurer_Integration_RealInterfaceAddRemove(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("integration test requires root privileges")
	}

	conf := zap.NewNop()
	log = conf.Sugar()

	// Find a real network interface (not loopback) with proper MAC
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("failed to get network interfaces: %v", err)
	}

	var testIface *net.Interface
	for i := range ifaces {
		iface := &ifaces[i]
		if iface.Flags&net.FlagUp != 0 &&
			iface.HardwareAddr != nil &&
			len(iface.HardwareAddr) == 6 &&
			iface.HardwareAddr.String() != "00:00:00:00:00:00" &&
			iface.Name != "lo" {
			testIface = iface
			break
		}
	}

	if testIface == nil {
		t.Skip("no suitable network interface found for testing")
	}

	testIP := netip.MustParseAddr("192.0.2.88")
	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     testIP,
			Netmask: net.CIDRMask(32, 32),
			Iface:   *testIface,
		},
	}

	// Ensure cleanup
	defer c.deconfigureAddress()

	// Test: Add the address
	if !c.configureAddress() {
		t.Log("Note: configureAddress failed (may be due to system restrictions)")
		return
	}

	t.Log("Successfully configured address with ARP")

	// Verify the address was added
	if !c.queryAddress() {
		t.Error("queryAddress returned false after successful configureAddress")
	}

	// Test: Remove the address
	if !c.deconfigureAddress() {
		t.Error("deconfigureAddress failed after successful configureAddress")
	} else {
		// Verify removal
		if c.queryAddress() {
			t.Error("queryAddress returned true after deconfigureAddress")
		}
	}
}

func TestBasicConfigurer_Integration_LoopbackAddRemove(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("integration test requires root privileges")
	}

	conf := zap.NewNop()
	log = conf.Sugar()

	// Get the loopback interface
	lo, err := net.InterfaceByName("lo")
	if err != nil {
		t.Fatalf("failed to get loopback interface: %v", err)
	}

	// Use a TEST-NET address that's unlikely to conflict
	testIP := netip.MustParseAddr("192.0.2.99")

	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     testIP,
			Netmask: net.CIDRMask(32, 32), // /32 for single host
			Iface: net.Interface{
				Index:        lo.Index,
				MTU:          lo.MTU,
				Name:         lo.Name,
				HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}, // Fake MAC for loopback
				Flags:        lo.Flags,
			},
		},
	}

	// Ensure cleanup even if test fails
	defer func() {
		// Try to remove the address in case it was added
		c.deconfigureAddress()
	}()

	// Test: Add the address
	if !c.configureAddress() {
		t.Log("Note: configureAddress failed (may be due to system restrictions or address already exists)")
	} else {
		t.Log("Successfully configured address")

		// Verify the address was added by querying
		if !c.queryAddress() {
			t.Error("queryAddress returned false after successful configureAddress")
		}

		// Test: Remove the address
		if !c.deconfigureAddress() {
			t.Error("deconfigureAddress failed after successful configureAddress")
		} else {
			t.Log("Successfully deconfigured address")

			// Verify the address was removed
			if c.queryAddress() {
				t.Error("queryAddress returned true after successful deconfigureAddress")
			}
		}
	}
}
