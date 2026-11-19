//go:build linux

package ipmanager

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

func mockExecCommand(t *testing.T, fn func(context.Context, string, ...string) *exec.Cmd) {
	old := execCommand
	execCommand = fn
	t.Cleanup(func() { execCommand = old })
}

func mockSend(t *testing.T, fn func(net.Interface, []byte, uint16) error) {
	old := linuxSendPacketWithProtocolFn
	linuxSendPacketWithProtocolFn = fn
	t.Cleanup(func() { linuxSendPacketWithProtocolFn = old })
}

func mockInterfaceAddrs(t *testing.T, fn func(string) ([]net.Addr, error)) {
	old := interfaceAddrsFn
	interfaceAddrsFn = fn
	t.Cleanup(func() { interfaceAddrsFn = old })
}

// execOK and execFail stand in for the `ip` command.
func execOK(context.Context, string, ...string) *exec.Cmd { return exec.Command("true") }
func execFail(context.Context, string, ...string) *exec.Cmd {
	return exec.Command("sh", "-c", "exit 1")
}

func mustCIDR(t *testing.T, s string) net.Addr {
	t.Helper()
	ip, ipNet, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("bad test CIDR %q: %v", s, err)
	}
	return &net.IPNet{IP: ip, Mask: ipNet.Mask}
}

// linkLocalAddrs is the address list of a typical dual-stack interface.
func linkLocalAddrs(t *testing.T) []net.Addr {
	t.Helper()
	return []net.Addr{
		mustCIDR(t, "192.168.1.5/24"),
		mustCIDR(t, "fe80::1/64"),
	}
}

// loopbackAddrs is what Linux `lo` actually carries: no link-local unicast.
func loopbackAddrs(t *testing.T) []net.Addr {
	t.Helper()
	return []net.Addr{
		mustCIDR(t, "127.0.0.1/8"),
		mustCIDR(t, "::1/128"),
	}
}

func testConfigurer(vip string, mask net.IPMask) *BasicConfigurer {
	return &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     netip.MustParseAddr(vip),
			Netmask: mask,
			Iface: net.Interface{
				Name:         "eth0",
				HardwareAddr: net.HardwareAddr{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF},
			},
		},
	}
}

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
		{name: "zero", input: 0x0000, want: 0x0000},
		{name: "ETH_P_ARP", input: 0x0806, want: 0x0608},
		{name: "ETH_P_ALL", input: 0x0003, want: 0x0300},
		{name: "max value", input: 0xFFFF, want: 0xFFFF},
		{name: "asymmetric value", input: 0x1234, want: 0x3412},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := htons(tt.input); got != tt.want {
				t.Errorf("htons(0x%04X) = 0x%04X, want 0x%04X", tt.input, got, tt.want)
			}
		})
	}
}

func TestHtons_Reversible(t *testing.T) {
	t.Parallel()

	// htons should be its own inverse (calling it twice returns the original value)
	for _, val := range []uint16{0x0000, 0x0001, 0x0100, 0x1234, 0xABCD, 0xFFFF} {
		if reversed := htons(htons(val)); reversed != val {
			t.Errorf("htons(htons(0x%04X)) = 0x%04X, want 0x%04X", val, reversed, val)
		}
	}
}

// ---------------------------------------------------------------------------
// pickLinkLocal
// ---------------------------------------------------------------------------

func TestPickLinkLocal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		addrs   []net.Addr
		want    string
		wantErr bool
	}{
		{
			name:    "no addresses",
			addrs:   nil,
			wantErr: true,
		},
		{
			name:    "IPv4 only",
			addrs:   []net.Addr{mustCIDR(t, "192.168.1.5/24")},
			wantErr: true,
		},
		{
			name:    "loopback only, ::1 is not link-local unicast",
			addrs:   loopbackAddrs(t),
			wantErr: true,
		},
		{
			name:    "global unicast only",
			addrs:   []net.Addr{mustCIDR(t, "2001:db8::10/64")},
			wantErr: true,
		},
		{
			name:    "IPv4 link-local is not a match",
			addrs:   []net.Addr{mustCIDR(t, "169.254.1.1/16")},
			wantErr: true,
		},
		{
			name:  "link-local present",
			addrs: linkLocalAddrs(t),
			want:  "fe80::1",
		},
		{
			name:  "first link-local wins",
			addrs: []net.Addr{mustCIDR(t, "fe80::1/64"), mustCIDR(t, "fe80::2/64")},
			want:  "fe80::1",
		},
		{
			name:  "IPAddr form is accepted",
			addrs: []net.Addr{&net.IPAddr{IP: net.ParseIP("fe80::abcd")}},
			want:  "fe80::abcd",
		},
		{
			name:  "unknown Addr types are skipped",
			addrs: []net.Addr{&net.TCPAddr{IP: net.ParseIP("fe80::dead")}, mustCIDR(t, "fe80::1/64")},
			want:  "fe80::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := pickLinkLocal("eth0", tt.addrs)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("pickLinkLocal() = %s, want an error", got)
				}
				if !bytes.Contains([]byte(err.Error()), []byte("eth0")) {
					t.Errorf("error %q does not name the interface", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("pickLinkLocal() error = %v", err)
			}
			if !got.Equal(net.ParseIP(tt.want)) {
				t.Errorf("pickLinkLocal() = %s, want %s", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// linkLocalAddress
// ---------------------------------------------------------------------------

func TestBasicConfigurer_linkLocalAddress(t *testing.T) {
	mockInterfaceAddrs(t, func(name string) ([]net.Addr, error) {
		if name != "eth0" {
			t.Errorf("looked up interface %q, want eth0", name)
		}
		return linkLocalAddrs(t), nil
	})

	c := testConfigurer("2001:db8::10", net.CIDRMask(64, 128))
	got, err := c.linkLocalAddress()
	if err != nil {
		t.Fatalf("linkLocalAddress() error = %v", err)
	}
	if !got.Equal(net.ParseIP("fe80::1")) {
		t.Errorf("linkLocalAddress() = %s, want fe80::1", got)
	}
}

func TestBasicConfigurer_linkLocalAddress_LookupFails(t *testing.T) {
	wantErr := errors.New("no such interface")
	mockInterfaceAddrs(t, func(string) ([]net.Addr, error) { return nil, wantErr })

	c := testConfigurer("2001:db8::10", net.CIDRMask(64, 128))
	if _, err := c.linkLocalAddress(); !errors.Is(err, wantErr) {
		t.Fatalf("linkLocalAddress() error = %v, want %v", err, wantErr)
	}
}

func TestBasicConfigurer_linkLocalAddress_MissingInterface(t *testing.T) {
	t.Parallel()

	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			Iface: net.Interface{Name: "definitely-not-a-real-interface"},
		},
	}

	if _, err := c.linkLocalAddress(); err == nil {
		t.Fatal("linkLocalAddress() expected an error for a missing interface, got nil")
	}
}

// ---------------------------------------------------------------------------
// configureAddress, IPv6 Neighbor Advertisement
// ---------------------------------------------------------------------------

// assertNAEthernet checks the link layer of an unsolicited NA.
func assertNAEthernet(t *testing.T, parsed gopacket.Packet, srcMAC net.HardwareAddr) {
	t.Helper()

	ethLayer := parsed.Layer(layers.LayerTypeEthernet)
	if ethLayer == nil {
		t.Fatal("missing Ethernet layer")
	}
	eth := ethLayer.(*layers.Ethernet)

	wantMulticastMAC := net.HardwareAddr{0x33, 0x33, 0x00, 0x00, 0x00, 0x01}
	if !bytes.Equal(eth.DstMAC, wantMulticastMAC) {
		t.Errorf("Ethernet DstMAC = %v, want all-nodes multicast %v", eth.DstMAC, wantMulticastMAC)
	}
	if !bytes.Equal(eth.SrcMAC, srcMAC) {
		t.Errorf("Ethernet SrcMAC = %v, want %v", eth.SrcMAC, srcMAC)
	}
	if eth.EthernetType != layers.EthernetTypeIPv6 {
		t.Errorf("Ethernet EthernetType = %v, want IPv6", eth.EthernetType)
	}
}

// assertNAIPv6 checks the network layer of an unsolicited NA.
func assertNAIPv6(t *testing.T, parsed gopacket.Packet, wantSrc net.IP) {
	t.Helper()

	ipv6Layer := parsed.Layer(layers.LayerTypeIPv6)
	if ipv6Layer == nil {
		t.Fatal("missing IPv6 layer")
	}
	ipv6 := ipv6Layer.(*layers.IPv6)

	if ipv6.Version != 6 {
		t.Errorf("IPv6 Version = %d, want 6", ipv6.Version)
	}
	if ipv6.HopLimit != 255 {
		t.Errorf("IPv6 HopLimit = %d, want 255", ipv6.HopLimit)
	}
	if ipv6.NextHeader != layers.IPProtocolICMPv6 {
		t.Errorf("IPv6 NextHeader = %v, want ICMPv6", ipv6.NextHeader)
	}
	if allNodes := net.ParseIP("ff02::1"); !ipv6.DstIP.Equal(allNodes) {
		t.Errorf("IPv6 DstIP = %s, want %s", ipv6.DstIP, allNodes)
	}
	// The VIP is still tentative under DAD, so the NA must be sourced from the
	// interface's link-local address.
	if !ipv6.SrcIP.Equal(wantSrc) {
		t.Errorf("IPv6 SrcIP = %s, want the link-local address %s", ipv6.SrcIP, wantSrc)
	}
}

// assertNAMessage checks the ICMPv6 header and the advertisement itself.
func assertNAMessage(t *testing.T, parsed gopacket.Packet, wantTarget netip.Addr, wantMAC net.HardwareAddr) {
	t.Helper()

	icmpLayer := parsed.Layer(layers.LayerTypeICMPv6)
	if icmpLayer == nil {
		t.Fatal("missing ICMPv6 layer")
	}
	icmp := icmpLayer.(*layers.ICMPv6)
	if icmp.TypeCode.Type() != layers.ICMPv6TypeNeighborAdvertisement {
		t.Errorf("ICMPv6 Type = %v, want NA (%d)", icmp.TypeCode.Type(), layers.ICMPv6TypeNeighborAdvertisement)
	}
	if icmp.Checksum == 0 {
		t.Error("ICMPv6 checksum was not computed")
	}

	naLayer := parsed.Layer(layers.LayerTypeICMPv6NeighborAdvertisement)
	if naLayer == nil {
		t.Fatal("missing ICMPv6NeighborAdvertisement layer")
	}
	na := naLayer.(*layers.ICMPv6NeighborAdvertisement)

	if na.Flags&0x20 == 0 {
		t.Errorf("NA Override flag not set, flags = 0x%x", na.Flags)
	}
	if na.Flags&0x40 != 0 {
		t.Errorf("NA Solicited flag must be clear on an unsolicited NA, flags = 0x%x", na.Flags)
	}
	if !net.IP(na.TargetAddress).Equal(net.IP(wantTarget.AsSlice())) {
		t.Errorf("NA TargetAddress = %s, want %s", na.TargetAddress, wantTarget)
	}
	if len(na.Options) != 1 {
		t.Fatalf("NA Options length = %d, want 1", len(na.Options))
	}
	if na.Options[0].Type != layers.ICMPv6OptTargetAddress {
		t.Errorf("NA Option Type = %v, want TargetLinkLayerAddress", na.Options[0].Type)
	}
	if !bytes.Equal(na.Options[0].Data, wantMAC) {
		t.Errorf("NA Option Data = %v, want MAC %v", na.Options[0].Data, wantMAC)
	}
}

// TestBasicConfigurer_configureAddress_IPv6_NAPacketContent validates the full
// content of the unsolicited Neighbor Advertisement that is sent after a new
// IPv6 address is bound to the interface.
func TestBasicConfigurer_configureAddress_IPv6_NAPacketContent(t *testing.T) {
	mockExecCommand(t, execOK)
	mockInterfaceAddrs(t, func(string) ([]net.Addr, error) { return linkLocalAddrs(t), nil })

	c := testConfigurer("2001:db8::10", net.CIDRMask(64, 128))

	sendCalled := false
	mockSend(t, func(iface net.Interface, packet []byte, protocol uint16) error {
		sendCalled = true

		if iface.Name != c.Iface.Name {
			t.Errorf("sent on interface %q, want %q", iface.Name, c.Iface.Name)
		}
		if protocol != syscall.ETH_P_IPV6 {
			t.Fatalf("protocol = %d, want ETH_P_IPV6 (%d)", protocol, syscall.ETH_P_IPV6)
		}

		parsed := gopacket.NewPacket(packet, layers.LayerTypeEthernet, gopacket.Default)
		assertNAEthernet(t, parsed, c.Iface.HardwareAddr)
		assertNAIPv6(t, parsed, net.ParseIP("fe80::1"))
		assertNAMessage(t, parsed, c.VIP, c.Iface.HardwareAddr)
		return nil
	})

	if got := c.configureAddress(t.Context()); !got {
		t.Fatal("configureAddress() = false, want true")
	}
	if !sendCalled {
		t.Fatal("the NA send seam was never invoked, so the assertions above did not run")
	}
}

// TestBasicConfigurer_configureAddress_IPv6_NoLinkLocal verifies that an
// interface without an IPv6 link-local address produces a warning and no send,
// while the address itself stays configured.
func TestBasicConfigurer_configureAddress_IPv6_NoLinkLocal(t *testing.T) {
	mockExecCommand(t, execOK)
	// Exactly what Linux loopback carries: no fe80::/10 address.
	mockInterfaceAddrs(t, func(string) ([]net.Addr, error) { return loopbackAddrs(t), nil })
	mockSend(t, func(net.Interface, []byte, uint16) error {
		t.Fatal("send must not be called when no IPv6 link-local address is available")
		return nil
	})

	c := testConfigurer("2001:db8::10", net.CIDRMask(64, 128))
	if got := c.configureAddress(t.Context()); !got {
		t.Fatal("configureAddress() = false, want true, the address was still added")
	}
}

// TestBasicConfigurer_configureAddress_IPv6_RunAddrConfigFails verifies that
// when runAddressConfiguration fails for an IPv6 VIP, configureAddress returns
// false and never attempts to send a Neighbor Advertisement.
func TestBasicConfigurer_configureAddress_IPv6_RunAddrConfigFails(t *testing.T) {
	mockExecCommand(t, execFail)
	mockInterfaceAddrs(t, func(string) ([]net.Addr, error) { return linkLocalAddrs(t), nil })
	mockSend(t, func(net.Interface, []byte, uint16) error {
		t.Fatal("send must not be called when runAddressConfiguration fails")
		return nil
	})

	c := testConfigurer("2001:db8::10", net.CIDRMask(64, 128))
	if got := c.configureAddress(t.Context()); got {
		t.Fatal("configureAddress() = true, want false when runAddressConfiguration fails")
	}
}

// TestBasicConfigurer_configureAddress_IPv6_SendNAFails verifies that when the
// NA send fails, configureAddress still returns true (the address was added)
// and handles the error gracefully.
func TestBasicConfigurer_configureAddress_IPv6_SendNAFails(t *testing.T) {
	mockExecCommand(t, execOK)
	mockInterfaceAddrs(t, func(string) ([]net.Addr, error) { return linkLocalAddrs(t), nil })

	sendCalled := false
	mockSend(t, func(_ net.Interface, _ []byte, protocol uint16) error {
		sendCalled = true
		if protocol != syscall.ETH_P_IPV6 {
			t.Errorf("protocol = %d, want ETH_P_IPV6 (%d)", protocol, syscall.ETH_P_IPV6)
		}
		return errors.New("mock send error: permission denied")
	})

	c := testConfigurer("2001:db8::10", net.CIDRMask(64, 128))
	if got := c.configureAddress(t.Context()); !got {
		t.Fatal("configureAddress() = false, want true even when the NA send fails")
	}
	if !sendCalled {
		t.Fatal("the NA send seam was never invoked")
	}
}

// TestBasicConfigurer_configureAddress_IPv6_ComposeNAFails covers the branch
// where building the NA packet fails: the address stays configured and nothing
// is put on the wire.
func TestBasicConfigurer_configureAddress_IPv6_ComposeNAFails(t *testing.T) {
	mockExecCommand(t, execOK)
	mockInterfaceAddrs(t, func(string) ([]net.Addr, error) { return linkLocalAddrs(t), nil })
	mockSerialize(t, func(gopacket.SerializeBuffer, gopacket.SerializeOptions, ...gopacket.SerializableLayer) error {
		return errors.New("mock serialize error")
	})
	mockSend(t, func(net.Interface, []byte, uint16) error {
		t.Fatal("send must not be called when the NA cannot be composed")
		return nil
	})

	c := testConfigurer("2001:db8::10", net.CIDRMask(64, 128))
	if got := c.configureAddress(t.Context()); !got {
		t.Fatal("configureAddress() = false, want true, the address was still added")
	}
}

// ---------------------------------------------------------------------------
// configureAddress, IPv4 gratuitous ARP
// ---------------------------------------------------------------------------

// TestBasicConfigurer_configureAddress_IPv4 verifies the IPv4 branch: after a
// successful address add, a gratuitous ARP is composed and sent on the
// interface with the ARP ethertype.
func TestBasicConfigurer_configureAddress_IPv4(t *testing.T) {
	mockExecCommand(t, execOK)
	mockInterfaceAddrs(t, func(string) ([]net.Addr, error) {
		t.Error("the IPv4 path must not look up link-local addresses")
		return nil, errors.New("unexpected lookup")
	})

	c := testConfigurer("192.168.1.100", net.CIDRMask(24, 32))
	hwAddr := c.Iface.HardwareAddr

	sendCalled := false
	mockSend(t, func(_ net.Interface, packet []byte, protocol uint16) error {
		sendCalled = true
		if protocol != syscall.ETH_P_ARP {
			t.Errorf("protocol = %d, want ETH_P_ARP (%d)", protocol, syscall.ETH_P_ARP)
		}
		parsed := gopacket.NewPacket(packet, layers.LayerTypeEthernet, gopacket.Default)
		ethLayer := parsed.Layer(layers.LayerTypeEthernet)
		if ethLayer == nil {
			t.Fatal("missing Ethernet layer in ARP packet")
		}
		if eth := ethLayer.(*layers.Ethernet); eth.EthernetType != layers.EthernetTypeARP {
			t.Errorf("Ethernet type = %v, want ARP", eth.EthernetType)
		}
		arpLayer := parsed.Layer(layers.LayerTypeARP)
		if arpLayer == nil {
			t.Fatal("missing ARP layer")
		}
		arp := arpLayer.(*layers.ARP)
		if !bytes.Equal(arp.SourceProtAddress, c.VIP.AsSlice()) {
			t.Errorf("ARP SourceProtAddress = %v, want %v", arp.SourceProtAddress, c.VIP.AsSlice())
		}
		if !bytes.Equal(arp.SourceHwAddress, hwAddr) {
			t.Errorf("ARP SourceHwAddress = %v, want %v", arp.SourceHwAddress, hwAddr)
		}
		return nil
	})

	if got := c.configureAddress(t.Context()); !got {
		t.Fatal("configureAddress() = false, want true")
	}
	if !sendCalled {
		t.Fatal("the ARP send seam was never invoked, so the assertions above did not run")
	}
}

// TestBasicConfigurer_configureAddress_IPv4In6 verifies that a ::ffff:a.b.c.d
// VIP takes the ARP path, not the Neighbor Advertisement path, even though
// netip.Addr.Is6 reports true for it.
func TestBasicConfigurer_configureAddress_IPv4In6(t *testing.T) {
	mockExecCommand(t, execOK)
	mockInterfaceAddrs(t, func(string) ([]net.Addr, error) {
		t.Error("an IPv4-in-IPv6 VIP must not take the Neighbor Advertisement path")
		return nil, errors.New("unexpected lookup")
	})

	c := testConfigurer("::ffff:192.0.2.1", net.CIDRMask(24, 32))

	sendCalled := false
	mockSend(t, func(_ net.Interface, packet []byte, protocol uint16) error {
		sendCalled = true
		if protocol != syscall.ETH_P_ARP {
			t.Errorf("protocol = %d, want ETH_P_ARP (%d)", protocol, syscall.ETH_P_ARP)
		}
		parsed := gopacket.NewPacket(packet, layers.LayerTypeEthernet, gopacket.Default)
		arpLayer := parsed.Layer(layers.LayerTypeARP)
		if arpLayer == nil {
			t.Fatal("missing ARP layer")
		}
		arp := arpLayer.(*layers.ARP)
		want := net.ParseIP("192.0.2.1").To4()
		if !bytes.Equal(arp.SourceProtAddress, want) {
			t.Errorf("ARP SourceProtAddress = %v, want the unmapped %v", arp.SourceProtAddress, want)
		}
		return nil
	})

	if got := c.configureAddress(t.Context()); !got {
		t.Fatal("configureAddress() = false, want true")
	}
	if !sendCalled {
		t.Fatal("the ARP send seam was never invoked")
	}
}

// TestBasicConfigurer_configureAddress_IPv4_SendARPFails verifies that when the
// gratuitous ARP send fails, configureAddress still returns true and handles
// the error gracefully via a warning log.
func TestBasicConfigurer_configureAddress_IPv4_SendARPFails(t *testing.T) {
	mockExecCommand(t, execOK)

	sendCalled := false
	mockSend(t, func(net.Interface, []byte, uint16) error {
		sendCalled = true
		return errors.New("mock ARP send error")
	})

	c := testConfigurer("192.168.1.100", net.CIDRMask(24, 32))
	if got := c.configureAddress(t.Context()); !got {
		t.Fatal("configureAddress() = false, want true even when the ARP send fails")
	}
	if !sendCalled {
		t.Fatal("the ARP send seam was never invoked")
	}
}

// TestBasicConfigurer_configureAddress_IPv4_ComposeARPFails covers the branch
// where building the ARP packet fails.
func TestBasicConfigurer_configureAddress_IPv4_ComposeARPFails(t *testing.T) {
	mockExecCommand(t, execOK)
	mockSerialize(t, func(gopacket.SerializeBuffer, gopacket.SerializeOptions, ...gopacket.SerializableLayer) error {
		return errors.New("mock serialize error")
	})
	mockSend(t, func(net.Interface, []byte, uint16) error {
		t.Fatal("send must not be called when the ARP packet cannot be composed")
		return nil
	})

	c := testConfigurer("192.168.1.100", net.CIDRMask(24, 32))
	if got := c.configureAddress(t.Context()); !got {
		t.Fatal("configureAddress() = false, want true, the address was still added")
	}
}

// ---------------------------------------------------------------------------
// deconfigureAddress
// ---------------------------------------------------------------------------

func TestBasicConfigurer_deconfigureAddress_Mocked(t *testing.T) {
	tests := []struct {
		name string
		vip  string
		mask net.IPMask
		cmd  func(context.Context, string, ...string) *exec.Cmd
		want bool
	}{
		{name: "IPv4 success", vip: "192.168.1.100", mask: net.CIDRMask(24, 32), cmd: execOK, want: true},
		{name: "IPv4 failure", vip: "192.168.1.100", mask: net.CIDRMask(24, 32), cmd: execFail, want: false},
		{name: "IPv6 success", vip: "2001:db8::10", mask: net.CIDRMask(64, 128), cmd: execOK, want: true},
		{name: "IPv6 failure", vip: "2001:db8::10", mask: net.CIDRMask(64, 128), cmd: execFail, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			var gotArgs []string
			mockExecCommand(t, func(_ context.Context, name string, args ...string) *exec.Cmd {
				gotArgs = append([]string{name}, args...)
				return tt.cmd(t.Context(), name, args...)
			})

			c := testConfigurer(tt.vip, tt.mask)
			if got := c.deconfigureAddress(t.Context()); got != tt.want {
				t.Errorf("deconfigureAddress() = %v, want %v", got, tt.want)
			}

			want := []string{"ip", "addr", "delete", c.getCIDR(), "dev", c.Iface.Name}
			if len(gotArgs) != len(want) {
				t.Fatalf("ran %v, want %v", gotArgs, want)
			}
			for i := range want {
				if gotArgs[i] != want[i] {
					t.Fatalf("ran %v, want %v", gotArgs, want)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// runAddressConfiguration
// ---------------------------------------------------------------------------

func TestBasicConfigurer_runAddressConfiguration_ErrorExit(t *testing.T) {
	mockExecCommand(t, execFail)

	c := testConfigurer("2001:db8::10", net.CIDRMask(64, 128))
	if c.runAddressConfiguration(t.Context(), "add") {
		t.Fatal("runAddressConfiguration() = true, want false")
	}
}

// TestBasicConfigurer_runAddressConfiguration_CommandMissing covers the
// non-ExitError branch, where the `ip` binary itself cannot be started.
func TestBasicConfigurer_runAddressConfiguration_CommandMissing(t *testing.T) {
	mockExecCommand(t, func(context.Context, string, ...string) *exec.Cmd {
		return exec.Command("definitely-not-a-real-binary-zzz")
	})

	c := testConfigurer("192.168.1.100", net.CIDRMask(24, 32))
	if c.runAddressConfiguration(t.Context(), "add") {
		t.Fatal("runAddressConfiguration() = true, want false when the command cannot be started")
	}
}

// ---------------------------------------------------------------------------
// sendPacketLinux and sendPacketLinuxWithProtocol (require root)
// ---------------------------------------------------------------------------

// firstEthernetInterface returns an interface that is up and has a real MAC, or
// nil if the host has none.
func firstEthernetInterface(t *testing.T) *net.Interface {
	t.Helper()

	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("failed to get network interfaces: %v", err)
	}
	for i := range ifaces {
		iface := &ifaces[i]
		if iface.Flags&net.FlagUp != 0 &&
			len(iface.HardwareAddr) == 6 &&
			iface.HardwareAddr.String() != "00:00:00:00:00:00" {
			return iface
		}
	}
	return nil
}

func TestSendPacketLinux_ValidInterface(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("sendPacketLinux tests require root privileges")
	}

	testIface := firstEthernetInterface(t)
	if testIface == nil {
		t.Skip("no suitable network interface found for testing")
	}

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

	// A well-formed frame on a real, up interface must go out cleanly.
	if err := sendPacketLinux(*testIface, packet); err != nil {
		t.Errorf("sendPacketLinux() error = %v, want nil on %s", err, testIface.Name)
	}
}

func TestSendPacketLinuxWithProtocol_IPv6(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("sendPacketLinuxWithProtocol tests require root privileges")
	}

	testIface := firstEthernetInterface(t)
	if testIface == nil {
		t.Skip("no suitable network interface found for testing")
	}

	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     netip.MustParseAddr("2001:db8::10"),
			Netmask: net.CIDRMask(64, 128),
			Iface:   *testIface,
		},
	}

	packet, err := c.createGratuitousNA(net.ParseIP("fe80::1"))
	if err != nil {
		t.Fatalf("failed to create Neighbor Advertisement: %v", err)
	}

	// Exercise the real raw socket with the IPv6 ethertype, which is otherwise
	// only ever reached through the mocked seam.
	if err := sendPacketLinuxWithProtocol(*testIface, packet, syscall.ETH_P_IPV6); err != nil {
		t.Errorf("sendPacketLinuxWithProtocol() error = %v, want nil on %s", err, testIface.Name)
	}
}

func TestSendPacketLinux_InvalidInterface(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("sendPacketLinux tests require root privileges")
	}

	// An interface index that cannot exist must be rejected by bind().
	invalidIface := net.Interface{Index: 99999, Name: "nonexistent0"}

	err := sendPacketLinux(invalidIface, make([]byte, 64))
	if err == nil {
		t.Fatal("sendPacketLinux() = nil, want an error for a nonexistent interface index")
	}
	if !errors.Is(err, syscall.ENODEV) && !errors.Is(err, syscall.EINVAL) {
		t.Errorf("sendPacketLinux() error = %v, want ENODEV or EINVAL", err)
	}
}

// ---------------------------------------------------------------------------
// privileged end-to-end coverage against the real `ip` command
// ---------------------------------------------------------------------------

func TestBasicConfigurer_configureAddress_RequiresRoot(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test must run as non-root to verify permission checks")
	}

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

	if c.configureAddress(t.Context()) {
		t.Error("configureAddress() should fail without root privileges")
	}
}

func TestBasicConfigurer_deconfigureAddress_RequiresRoot(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test must run as non-root to verify permission checks")
	}

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

	if c.deconfigureAddress(t.Context()) {
		t.Error("deconfigureAddress() should fail without root privileges")
	}
}

// requireIPCommand skips the test when the `ip` binary is unavailable, so the
// privileged tests do not fail merely because iproute2 is not installed.
func requireIPCommand(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("the ip command is not available: ", err)
	}
}

func TestBasicConfigurer_runAddressConfiguration_NonexistentInterface(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("runAddressConfiguration tests require root privileges")
	}
	requireIPCommand(t)

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

	for _, action := range []string{"add", "delete"} {
		if c.runAddressConfiguration(t.Context(), action) {
			t.Errorf("runAddressConfiguration(%s) should fail for a non-existent interface", action)
		}
	}
}

// TestBasicConfigurer_Integration_LoopbackAddRemove drives the real `ip`
// command end to end for both address families.
func TestBasicConfigurer_Integration_LoopbackAddRemove(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("integration test requires root privileges")
	}
	requireIPCommand(t)

	lo, err := net.InterfaceByName("lo")
	if err != nil {
		t.Fatalf("failed to get loopback interface: %v", err)
	}

	tests := []struct {
		name string
		vip  string
		mask net.IPMask
	}{
		{name: "IPv4", vip: "192.0.2.99", mask: net.CIDRMask(32, 32)},     // TEST-NET-1
		{name: "IPv6", vip: "2001:db8::99", mask: net.CIDRMask(128, 128)}, // RFC 3849
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &BasicConfigurer{
				IPConfiguration: &IPConfiguration{
					VIP:     netip.MustParseAddr(tt.vip),
					Netmask: tt.mask,
					Iface: net.Interface{
						Index:        lo.Index,
						MTU:          lo.MTU,
						Name:         lo.Name,
						HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
						Flags:        lo.Flags,
					},
				},
			}
			defer c.deconfigureAddress(t.Context())

			// Announcing on loopback cannot work, but the address must still be
			// added and the failure must be swallowed.
			if !c.configureAddress(t.Context()) {
				t.Fatal("configureAddress() = false, want true")
			}
			if !c.queryAddress(t.Context()) {
				t.Error("queryAddress() = false after a successful configureAddress()")
			}
			if !c.deconfigureAddress(t.Context()) {
				t.Fatal("deconfigureAddress() = false, want true")
			}
			if c.queryAddress(t.Context()) {
				t.Error("queryAddress() = true after a successful deconfigureAddress()")
			}
		})
	}
}

// TestBasicConfigurer_Integration_RealInterfaceAddRemove exercises the full
// path, including the raw socket send, on a real Ethernet interface.
func TestBasicConfigurer_Integration_RealInterfaceAddRemove(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("integration test requires root privileges")
	}
	requireIPCommand(t)

	testIface := firstEthernetInterface(t)
	if testIface == nil || testIface.Name == "lo" {
		t.Skip("no suitable network interface found for testing")
	}

	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     netip.MustParseAddr("192.0.2.88"),
			Netmask: net.CIDRMask(32, 32),
			Iface:   *testIface,
		},
	}
	defer c.deconfigureAddress(t.Context())

	if !c.configureAddress(t.Context()) {
		t.Fatal("configureAddress() = false, want true")
	}
	if !c.queryAddress(t.Context()) {
		t.Error("queryAddress() = false after a successful configureAddress()")
	}
	if !c.deconfigureAddress(t.Context()) {
		t.Fatal("deconfigureAddress() = false, want true")
	}
	if c.queryAddress(t.Context()) {
		t.Error("queryAddress() = true after a successful deconfigureAddress()")
	}
}
