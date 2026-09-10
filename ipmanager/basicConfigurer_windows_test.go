//go:build windows

package ipmanager

import (
	"errors"
	"net"
	"net/netip"
	"testing"

	"go.uber.org/zap"
	"golang.org/x/sys/windows"
)

// mockLogger silences the package logger for the duration of a test.
func mockLogger(t *testing.T) {
	old := log
	log = zap.NewNop().Sugar()
	t.Cleanup(func() { log = old })
}

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

func mockInterfaceByName(t *testing.T, fn func(string) (*net.Interface, error)) {
	old := interfaceByNameFn
	interfaceByNameFn = fn
	t.Cleanup(func() { interfaceByNameFn = old })
}

func mockAddIPAddress(t *testing.T, fn func(uint32, uint32, uint32, *uint32, *uint32) error) {
	old := addIPAddressFn
	addIPAddressFn = fn
	t.Cleanup(func() { addIPAddressFn = old })
}

func mockDeleteIPAddress(t *testing.T, fn func(uint32) error) {
	old := deleteIPAddressFn
	deleteIPAddressFn = fn
	t.Cleanup(func() { deleteIPAddressFn = old })
}

func mockUnicastEntry(t *testing.T,
	initFn func(*windows.MibUnicastIpAddressRow),
	createFn func(*windows.MibUnicastIpAddressRow) error,
	deleteFn func(*windows.MibUnicastIpAddressRow) error,
) {
	oldInit, oldCreate, oldDelete := initUnicastRowFn, createUnicastFn, deleteUnicastFn
	initUnicastRowFn, createUnicastFn, deleteUnicastFn = initFn, createFn, deleteFn
	t.Cleanup(func() {
		initUnicastRowFn, createUnicastFn, deleteUnicastFn = oldInit, oldCreate, oldDelete
	})
}

// failUnicast is the default for tests that must not reach the IPv6 path.
func failUnicast(t *testing.T) {
	t.Helper()
	mockUnicastEntry(t,
		func(*windows.MibUnicastIpAddressRow) {
			t.Error("InitializeUnicastIpAddressEntry must not be called on the IPv4 path")
		},
		func(*windows.MibUnicastIpAddressRow) error {
			t.Error("CreateUnicastIpAddressEntry must not be called on the IPv4 path")
			return nil
		},
		func(*windows.MibUnicastIpAddressRow) error {
			t.Error("DeleteUnicastIpAddressEntry must not be called on the IPv4 path")
			return nil
		},
	)
}

// failAddIPAddress is the default for tests that must not reach the IPv4 path.
func failAddIPAddress(t *testing.T) {
	t.Helper()
	mockAddIPAddress(t, func(uint32, uint32, uint32, *uint32, *uint32) error {
		t.Error("AddIPAddress must not be called on the IPv6 path")
		return nil
	})
}

func windowsConfigurer(vip string, mask net.IPMask) *BasicConfigurer {
	return &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     netip.MustParseAddr(vip),
			Netmask: mask,
			Iface: net.Interface{
				Name:         "Ethernet",
				HardwareAddr: net.HardwareAddr{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF},
			},
		},
	}
}

const testIfaceIndex = 42

func mockIfaceLookupOK(t *testing.T) {
	t.Helper()
	mockInterfaceByName(t, func(name string) (*net.Interface, error) {
		if name != "Ethernet" {
			t.Errorf("looked up interface %q, want Ethernet", name)
		}
		return &net.Interface{Index: testIfaceIndex, Name: name}, nil
	})
}

// ---------------------------------------------------------------------------
// configureAddress
// ---------------------------------------------------------------------------

func TestBasicConfigurer_configureAddress_Windows_IPv4(t *testing.T) {
	mockLogger(t)
	mockIfaceLookupOK(t)
	failUnicast(t)

	addCalled := false
	mockAddIPAddress(t, func(address, mask, ifIndex uint32, nteContext, nteInstance *uint32) error {
		addCalled = true
		// 192.168.1.100 in the host's little-endian byte order.
		if want := uint32(0x6401A8C0); address != want {
			t.Errorf("address = 0x%08X, want 0x%08X", address, want)
		}
		if want := uint32(0x00FFFFFF); mask != want {
			t.Errorf("mask = 0x%08X, want 0x%08X", mask, want)
		}
		if ifIndex != testIfaceIndex {
			t.Errorf("ifIndex = %d, want %d", ifIndex, testIfaceIndex)
		}
		if nteContext == nil || nteInstance == nil {
			t.Fatal("AddIPAddress was called with a nil out parameter")
		}
		*nteContext = 7
		return nil
	})

	c := windowsConfigurer("192.168.1.100", net.CIDRMask(24, 32))
	if got := c.configureAddress(); !got {
		t.Fatal("configureAddress() = false, want true")
	}
	if !addCalled {
		t.Fatal("AddIPAddress was never called")
	}
	if c.ntecontext != 7 {
		t.Errorf("ntecontext = %d, want the value returned by AddIPAddress (7)", c.ntecontext)
	}
	if c.ipv6row != nil {
		t.Error("the IPv4 path must not record an IPv6 row")
	}
}

// TestBasicConfigurer_configureAddress_Windows_IPv4In6 pins down that a
// ::ffff:a.b.c.d VIP takes the IPv4 path with the unmapped 4 byte address,
// rather than being truncated to the first four bytes of its 16 byte form.
// getMask() hands such a VIP a 16 byte mask, so the mask must be rebuilt from
// its prefix length as well.
func TestBasicConfigurer_configureAddress_Windows_IPv4In6(t *testing.T) {
	mockLogger(t)
	mockIfaceLookupOK(t)
	failUnicast(t)

	addCalled := false
	mockAddIPAddress(t, func(address, mask, _ uint32, nteContext, _ *uint32) error {
		addCalled = true
		// 192.0.2.1, not the leading zeroes of ::ffff:192.0.2.1.
		if want := uint32(0x010200C0); address != want {
			t.Errorf("address = 0x%08X, want 0x%08X", address, want)
		}
		if want := uint32(0x00FFFFFF); mask != want {
			t.Errorf("mask = 0x%08X, want 0x%08X", mask, want)
		}
		*nteContext = 3
		return nil
	})

	c := windowsConfigurer("::ffff:192.0.2.1", getMask(netip.MustParseAddr("::ffff:192.0.2.1"), 24))
	if got := c.configureAddress(); !got {
		t.Fatal("configureAddress() = false, want true")
	}
	if !addCalled {
		t.Fatal("AddIPAddress was never called")
	}
}

func TestBasicConfigurer_configureAddress_Windows_IPv6(t *testing.T) {
	mockLogger(t)
	mockIfaceLookupOK(t)
	failAddIPAddress(t)

	vip := netip.MustParseAddr("2001:db8::10")
	initCalled, createCalled := false, false
	mockUnicastEntry(t,
		func(row *windows.MibUnicastIpAddressRow) {
			initCalled = true
			if row == nil {
				t.Fatal("InitializeUnicastIpAddressEntry was called with a nil row")
			}
		},
		func(row *windows.MibUnicastIpAddressRow) error {
			createCalled = true
			if !initCalled {
				t.Error("the row must be initialized before it is submitted")
			}
			if row.Address.Family != windows.AF_INET6 {
				t.Errorf("Address.Family = %d, want AF_INET6 (%d)", row.Address.Family, windows.AF_INET6)
			}
			if row.Address.Addr != vip.As16() {
				t.Errorf("Address.Addr = %v, want %v", row.Address.Addr, vip.As16())
			}
			if row.InterfaceIndex != testIfaceIndex {
				t.Errorf("InterfaceIndex = %d, want %d", row.InterfaceIndex, testIfaceIndex)
			}
			if row.OnLinkPrefixLength != 64 {
				t.Errorf("OnLinkPrefixLength = %d, want 64", row.OnLinkPrefixLength)
			}
			return nil
		},
		func(*windows.MibUnicastIpAddressRow) error { return nil },
	)

	c := windowsConfigurer("2001:db8::10", net.CIDRMask(64, 128))
	if got := c.configureAddress(); !got {
		t.Fatal("configureAddress() = false, want true")
	}
	if !createCalled {
		t.Fatal("CreateUnicastIpAddressEntry was never called")
	}
	if c.ipv6row == nil {
		t.Error("the IPv6 row was not retained for the matching delete")
	}
	if c.ntecontext != 0 {
		t.Errorf("ntecontext = %d, want 0 on the IPv6 path", c.ntecontext)
	}
}

func TestBasicConfigurer_configureAddress_Windows_Failures(t *testing.T) {
	tests := []struct {
		name  string
		vip   string
		mask  net.IPMask
		setup func(*testing.T)
	}{
		{
			name: "interface lookup fails",
			vip:  "192.168.1.100",
			mask: net.CIDRMask(24, 32),
			setup: func(t *testing.T) {
				mockInterfaceByName(t, func(string) (*net.Interface, error) {
					return nil, errors.New("no such interface")
				})
				mockAddIPAddress(t, func(uint32, uint32, uint32, *uint32, *uint32) error {
					t.Error("AddIPAddress must not be called when the interface lookup fails")
					return nil
				})
			},
		},
		{
			name: "AddIPAddress fails",
			vip:  "192.168.1.100",
			mask: net.CIDRMask(24, 32),
			setup: func(t *testing.T) {
				mockIfaceLookupOK(t)
				mockAddIPAddress(t, func(uint32, uint32, uint32, *uint32, *uint32) error {
					return errors.New("access denied")
				})
			},
		},
		{
			name: "prefix too long for an IPv4 address",
			vip:  "::ffff:192.0.2.1",
			mask: net.CIDRMask(64, 128),
			setup: func(t *testing.T) {
				mockIfaceLookupOK(t)
				failUnicast(t)
				mockAddIPAddress(t, func(uint32, uint32, uint32, *uint32, *uint32) error {
					t.Error("AddIPAddress must not be called with a prefix wider than /32")
					return nil
				})
			},
		},
		{
			name: "CreateUnicastIpAddressEntry fails",
			vip:  "2001:db8::10",
			mask: net.CIDRMask(64, 128),
			setup: func(t *testing.T) {
				mockIfaceLookupOK(t)
				mockUnicastEntry(t,
					func(*windows.MibUnicastIpAddressRow) {},
					func(*windows.MibUnicastIpAddressRow) error { return errors.New("access denied") },
					func(*windows.MibUnicastIpAddressRow) error { return nil },
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockLogger(t)
			tt.setup(t)

			c := windowsConfigurer(tt.vip, tt.mask)
			if got := c.configureAddress(); got {
				t.Error("configureAddress() = true, want false")
			}
			if c.ipv6row != nil {
				t.Error("a failed add must not record an IPv6 row")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// deconfigureAddress
// ---------------------------------------------------------------------------

func TestBasicConfigurer_deconfigureAddress_Windows_IPv4(t *testing.T) {
	mockLogger(t)

	deleteCalled := false
	mockDeleteIPAddress(t, func(nteContext uint32) error {
		deleteCalled = true
		if nteContext != 7 {
			t.Errorf("DeleteIPAddress(%d), want the stored context 7", nteContext)
		}
		return nil
	})

	c := windowsConfigurer("192.168.1.100", net.CIDRMask(24, 32))
	c.ntecontext = 7

	if got := c.deconfigureAddress(); !got {
		t.Fatal("deconfigureAddress() = false, want true")
	}
	if !deleteCalled {
		t.Fatal("DeleteIPAddress was never called")
	}
	if c.ntecontext != 0 {
		t.Errorf("ntecontext = %d, want 0 after a successful delete", c.ntecontext)
	}
}

func TestBasicConfigurer_deconfigureAddress_Windows_IPv6(t *testing.T) {
	mockLogger(t)

	row := &windows.MibUnicastIpAddressRow{InterfaceIndex: testIfaceIndex}
	deleteCalled := false
	mockUnicastEntry(t,
		func(*windows.MibUnicastIpAddressRow) {},
		func(*windows.MibUnicastIpAddressRow) error { return nil },
		func(got *windows.MibUnicastIpAddressRow) error {
			deleteCalled = true
			if got != row {
				t.Error("DeleteUnicastIpAddressEntry was called with a different row than was created")
			}
			return nil
		},
	)

	c := windowsConfigurer("2001:db8::10", net.CIDRMask(64, 128))
	c.ipv6row = row

	if got := c.deconfigureAddress(); !got {
		t.Fatal("deconfigureAddress() = false, want true")
	}
	if !deleteCalled {
		t.Fatal("DeleteUnicastIpAddressEntry was never called")
	}
	if c.ipv6row != nil {
		t.Error("the IPv6 row must be cleared after a successful delete")
	}
}

func TestBasicConfigurer_deconfigureAddress_Windows_Failures(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *BasicConfigurer)
	}{
		{
			name: "nothing was configured",
			setup: func(t *testing.T, _ *BasicConfigurer) {
				mockDeleteIPAddress(t, func(uint32) error {
					t.Error("DeleteIPAddress must not be called when no address was added")
					return nil
				})
			},
		},
		{
			name: "DeleteIPAddress fails",
			setup: func(t *testing.T, c *BasicConfigurer) {
				c.ntecontext = 7
				mockDeleteIPAddress(t, func(uint32) error { return errors.New("access denied") })
			},
		},
		{
			name: "DeleteUnicastIpAddressEntry fails",
			setup: func(t *testing.T, c *BasicConfigurer) {
				c.ipv6row = &windows.MibUnicastIpAddressRow{}
				mockUnicastEntry(t,
					func(*windows.MibUnicastIpAddressRow) {},
					func(*windows.MibUnicastIpAddressRow) error { return nil },
					func(*windows.MibUnicastIpAddressRow) error { return errors.New("access denied") },
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockLogger(t)

			c := windowsConfigurer("192.168.1.100", net.CIDRMask(24, 32))
			tt.setup(t, c)

			if got := c.deconfigureAddress(); got {
				t.Error("deconfigureAddress() = true, want false")
			}
		})
	}
}
