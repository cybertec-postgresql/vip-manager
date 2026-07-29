package ipmanager

import (
	"bytes"
	"net"
	"net/netip"
	"testing"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

func testIPConfiguration(vip string) *IPConfiguration {
	return &IPConfiguration{
		VIP:     netip.MustParseAddr(vip),
		Netmask: net.CIDRMask(24, 32),
		Iface: net.Interface{
			Name:         "test0",
			HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		},
	}
}

// ---------------------------------------------------------------------------
// newBasicConfigurer
// ---------------------------------------------------------------------------

func TestNewBasicConfigurer_LoopbackMAC(t *testing.T) {
	t.Parallel()

	cfg := &IPConfiguration{
		VIP:     netip.MustParseAddr("192.168.1.10"),
		Netmask: net.CIDRMask(24, 32),
		Iface: net.Interface{
			Name:         "lo",
			HardwareAddr: net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		},
	}

	_, err := newBasicConfigurer(cfg)
	if err == nil {
		t.Fatal("expected error for loopback hardware address, got nil")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("loopback")) {
		t.Errorf("expected error to mention loopback, got: %v", err)
	}
}

func TestNewBasicConfigurer_NilHardwareAddr(t *testing.T) {
	t.Parallel()

	cfg := &IPConfiguration{
		VIP:     netip.MustParseAddr("192.168.1.10"),
		Netmask: net.CIDRMask(24, 32),
		Iface: net.Interface{
			Name:         "eth0",
			HardwareAddr: nil,
		},
	}

	_, err := newBasicConfigurer(cfg)
	if err == nil {
		t.Fatal("expected error for nil hardware address, got nil")
	}
}

func TestNewBasicConfigurer_Success(t *testing.T) {
	t.Parallel()

	cfg := testIPConfiguration("192.168.1.10")
	c, err := newBasicConfigurer(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected configurer, got nil")
	}
	if c.IPConfiguration != cfg {
		t.Error("configurer did not retain the provided IPConfiguration")
	}
	if c.ntecontext != 0 {
		t.Errorf("expected ntecontext to be initialized to 0, got %d", c.ntecontext)
	}
}

// ---------------------------------------------------------------------------
// queryAddress
// ---------------------------------------------------------------------------

func TestBasicConfigurer_queryAddress_MissingInterface(t *testing.T) {
	t.Parallel()

	c := &BasicConfigurer{
		IPConfiguration: testIPConfiguration("192.168.1.10"),
	}

	// The interface name is fake, so the lookup fails and queryAddress must
	// report that the address is not assigned.
	if got := c.queryAddress(); got {
		t.Errorf("queryAddress() = %v, want false for non-existent interface", got)
	}
}

// ---------------------------------------------------------------------------
// createGratuitousARP
// ---------------------------------------------------------------------------

func TestBasicConfigurer_createGratuitousARP(t *testing.T) {
	t.Parallel()

	c := &BasicConfigurer{
		IPConfiguration: testIPConfiguration("192.168.1.10"),
	}

	packet, err := c.createGratuitousARP()
	if err != nil {
		t.Fatalf("createGratuitousARP() error = %v", err)
	}

	parsed := gopacket.NewPacket(packet, layers.LayerTypeEthernet, gopacket.Default)

	ethLayer := parsed.Layer(layers.LayerTypeEthernet)
	if ethLayer == nil {
		t.Fatal("missing Ethernet layer")
	}
	eth := ethLayer.(*layers.Ethernet)

	if !bytes.Equal(eth.SrcMAC, c.Iface.HardwareAddr) {
		t.Errorf("Ethernet source MAC = %v, want %v", eth.SrcMAC, c.Iface.HardwareAddr)
	}
	wantBroadcast := net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	if !bytes.Equal(eth.DstMAC, wantBroadcast) {
		t.Errorf("Ethernet destination MAC = %v, want broadcast", eth.DstMAC)
	}
	if eth.EthernetType != layers.EthernetTypeARP {
		t.Errorf("Ethernet type = %v, want ARP", eth.EthernetType)
	}

	arpLayer := parsed.Layer(layers.LayerTypeARP)
	if arpLayer == nil {
		t.Fatal("missing ARP layer")
	}
	arp := arpLayer.(*layers.ARP)

	if arp.AddrType != layers.LinkTypeEthernet {
		t.Errorf("ARP hardware type = %v, want Ethernet", arp.AddrType)
	}
	if arp.Protocol != layers.EthernetTypeIPv4 {
		t.Errorf("ARP protocol type = %v, want IPv4", arp.Protocol)
	}
	if arp.HwAddressSize != MACAddressSize {
		t.Errorf("ARP hardware address size = %d, want %d", arp.HwAddressSize, MACAddressSize)
	}
	if arp.ProtAddressSize != IPv4AddressSize {
		t.Errorf("ARP protocol address size = %d, want %d", arp.ProtAddressSize, IPv4AddressSize)
	}
	if arp.Operation != layers.ARPReply {
		t.Errorf("ARP operation = %d, want reply", arp.Operation)
	}
	if !bytes.Equal(arp.SourceHwAddress, c.Iface.HardwareAddr) {
		t.Errorf("ARP source hardware address = %v, want %v", arp.SourceHwAddress, c.Iface.HardwareAddr)
	}
	if !bytes.Equal(arp.SourceProtAddress, c.VIP.AsSlice()) {
		t.Errorf("ARP source protocol address = %v, want %v", arp.SourceProtAddress, c.VIP.AsSlice())
	}
	if !bytes.Equal(arp.DstHwAddress, c.Iface.HardwareAddr) {
		t.Errorf("ARP destination hardware address = %v, want %v", arp.DstHwAddress, c.Iface.HardwareAddr)
	}
	if !bytes.Equal(arp.DstProtAddress, c.VIP.AsSlice()) {
		t.Errorf("ARP destination protocol address = %v, want %v", arp.DstProtAddress, c.VIP.AsSlice())
	}
}
