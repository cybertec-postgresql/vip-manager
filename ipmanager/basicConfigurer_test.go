package ipmanager

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"go.uber.org/zap"
)

// mockLogger silences the package logger for the duration of a test.
func mockLogger(t *testing.T) {
	old := log
	log = zap.NewNop().Sugar()
	t.Cleanup(func() { log = old })
}

// mockSerialize replaces the packet serializer, so tests can force a failure.
func mockSerialize(t *testing.T, fn func(gopacket.SerializeBuffer, gopacket.SerializeOptions, ...gopacket.SerializableLayer) error) {
	old := serializePacketLayers
	serializePacketLayers = fn
	t.Cleanup(func() { serializePacketLayers = old })
}

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

func TestBasicConfigurer_queryAddress_RealInterface_NotAssigned(t *testing.T) {
	t.Parallel()

	// Get the loopback interface which should exist on all systems
	lo, err := net.InterfaceByName("lo")
	if err != nil {
		t.Skip("loopback interface not available")
	}

	// Use an address that is very unlikely to be assigned to loopback
	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     netip.MustParseAddr("203.0.113.99"), // TEST-NET-3 (RFC 5737)
			Netmask: net.CIDRMask(32, 32),
			Iface: net.Interface{
				Name:         lo.Name,
				HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			},
		},
	}

	// Should return false since this address is not assigned
	if got := c.queryAddress(); got {
		t.Errorf("queryAddress() = %v, want false for unassigned address", got)
	}
}

func TestBasicConfigurer_queryAddress_RealInterface_Assigned(t *testing.T) {
	t.Parallel()

	// Get the loopback interface
	lo, err := net.InterfaceByName("lo")
	if err != nil {
		t.Skip("loopback interface not available")
	}

	// Get addresses assigned to loopback
	addrs, err := lo.Addrs()
	if err != nil || len(addrs) == 0 {
		t.Skip("cannot get addresses for loopback interface")
	}

	// Find an IPv4 address
	var testAddr netip.Addr
	var testMask net.IPMask
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok {
			if ipNet.IP.To4() != nil {
				testAddr, err = netip.ParseAddr(ipNet.IP.String())
				if err != nil {
					continue
				}
				testMask = ipNet.Mask
				break
			}
		}
	}

	if !testAddr.IsValid() {
		t.Skip("no IPv4 address found on loopback")
	}

	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     testAddr,
			Netmask: testMask,
			Iface: net.Interface{
				Name:         lo.Name,
				HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			},
		},
	}

	// Should return true since this address is actually assigned to loopback
	if got := c.queryAddress(); !got {
		t.Errorf("queryAddress() = %v, want true for assigned address %s", got, testAddr)
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

func TestBasicConfigurer_createGratuitousNA(t *testing.T) {
	t.Parallel()

	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP: netip.MustParseAddr("2001:db8::10"),
			Iface: net.Interface{
				HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			},
		},
	}
	sourceIP := net.ParseIP("fe80::1")

	packet, err := c.createGratuitousNA(sourceIP)
	if err != nil {
		t.Fatalf("createGratuitousNA() error = %v", err)
	}

	parsed := gopacket.NewPacket(packet, layers.LayerTypeEthernet, gopacket.Default)
	eth := parsed.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
	wantMulticast := net.HardwareAddr{0x33, 0x33, 0x00, 0x00, 0x00, 0x01}
	if !bytes.Equal(eth.DstMAC, wantMulticast) {
		t.Errorf("Ethernet destination MAC = %v, want %v", eth.DstMAC, wantMulticast)
	}
	if eth.EthernetType != layers.EthernetTypeIPv6 {
		t.Errorf("Ethernet type = %v, want IPv6", eth.EthernetType)
	}

	ipv6 := parsed.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
	if ipv6.HopLimit != 255 {
		t.Errorf("IPv6 hop limit = %d, want 255", ipv6.HopLimit)
	}
	if !ipv6.SrcIP.Equal(sourceIP) || !ipv6.DstIP.Equal(net.ParseIP("ff02::1")) {
		t.Errorf("IPv6 addresses = %s -> %s, want %s -> ff02::1", ipv6.SrcIP, ipv6.DstIP, sourceIP)
	}

	icmp := parsed.Layer(layers.LayerTypeICMPv6).(*layers.ICMPv6)
	if icmp.TypeCode.Type() != layers.ICMPv6TypeNeighborAdvertisement {
		t.Errorf("ICMPv6 type = %v, want Neighbor Advertisement", icmp.TypeCode.Type())
	}
	na := parsed.Layer(layers.LayerTypeICMPv6NeighborAdvertisement).(*layers.ICMPv6NeighborAdvertisement)
	if na.Flags != 0x20 {
		t.Errorf("NA flags = 0x%x, want Override flag 0x20", na.Flags)
	}
	if !net.IP(na.TargetAddress).Equal(c.VIP.AsSlice()) {
		t.Errorf("NA target = %s, want %s", na.TargetAddress, c.VIP)
	}
	if len(na.Options) != 1 || na.Options[0].Type != layers.ICMPv6OptTargetAddress ||
		!bytes.Equal(na.Options[0].Data, c.Iface.HardwareAddr) {
		t.Errorf("NA target link-layer option = %#v, want interface MAC", na.Options)
	}
}

// ---------------------------------------------------------------------------
// createGratuitousNA - checksum and error paths
// ---------------------------------------------------------------------------

// icmpv6ChecksumValid recomputes the Internet checksum over the IPv6
// pseudo-header and the ICMPv6 message. Because the message still carries its
// own checksum, a correct packet sums to zero.
func icmpv6ChecksumValid(src, dst net.IP, payload []byte) bool {
	pseudo := make([]byte, 0, 40+len(payload))
	pseudo = append(pseudo, src.To16()...)
	pseudo = append(pseudo, dst.To16()...)
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(payload)))
	pseudo = append(pseudo, length[:]...)
	pseudo = append(pseudo, 0, 0, 0, byte(layers.IPProtocolICMPv6))
	pseudo = append(pseudo, payload...)

	if len(pseudo)%2 == 1 {
		pseudo = append(pseudo, 0)
	}

	var sum uint32
	for i := 0; i < len(pseudo); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(pseudo[i : i+2]))
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return uint16(^sum) == 0
}

func TestBasicConfigurer_createGratuitousNA_Checksum(t *testing.T) {
	t.Parallel()

	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP: netip.MustParseAddr("2001:db8::10"),
			Iface: net.Interface{
				HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			},
		},
	}
	sourceIP := net.ParseIP("fe80::1")

	packet, err := c.createGratuitousNA(sourceIP)
	if err != nil {
		t.Fatalf("createGratuitousNA() error = %v", err)
	}

	parsed := gopacket.NewPacket(packet, layers.LayerTypeEthernet, gopacket.Default)
	icmpLayer := parsed.Layer(layers.LayerTypeICMPv6)
	if icmpLayer == nil {
		t.Fatal("missing ICMPv6 layer")
	}
	icmp := icmpLayer.(*layers.ICMPv6)
	if icmp.Checksum == 0 {
		t.Fatal("ICMPv6 checksum is zero, SetNetworkLayerForChecksum had no effect")
	}

	// The ICMPv6 message is everything after the 14 byte Ethernet header and
	// the 40 byte IPv6 header.
	const l2l3 = 14 + 40
	if len(packet) <= l2l3 {
		t.Fatalf("packet is only %d bytes, too short to hold an ICMPv6 message", len(packet))
	}
	if !icmpv6ChecksumValid(sourceIP, net.ParseIP("ff02::1"), packet[l2l3:]) {
		t.Errorf("ICMPv6 checksum 0x%04x does not validate over the IPv6 pseudo-header", icmp.Checksum)
	}
}

func TestBasicConfigurer_createGratuitousNA_SerializeFails(t *testing.T) {
	wantErr := errors.New("mock serialize error")
	mockSerialize(t, func(gopacket.SerializeBuffer, gopacket.SerializeOptions, ...gopacket.SerializableLayer) error {
		return wantErr
	})

	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP: netip.MustParseAddr("2001:db8::10"),
			Iface: net.Interface{
				HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			},
		},
	}

	if _, err := c.createGratuitousNA(net.ParseIP("fe80::1")); !errors.Is(err, wantErr) {
		t.Fatalf("createGratuitousNA() error = %v, want %v", err, wantErr)
	}
}

func TestBasicConfigurer_createGratuitousARP_SerializeFails(t *testing.T) {
	wantErr := errors.New("mock serialize error")
	mockSerialize(t, func(gopacket.SerializeBuffer, gopacket.SerializeOptions, ...gopacket.SerializableLayer) error {
		return wantErr
	})

	c := &BasicConfigurer{IPConfiguration: testIPConfiguration("192.168.1.10")}

	if _, err := c.createGratuitousARP(); !errors.Is(err, wantErr) {
		t.Fatalf("createGratuitousARP() error = %v, want %v", err, wantErr)
	}
}

// TestBasicConfigurer_createGratuitousARP_IPv4In6 pins down that a
// ::ffff:a.b.c.d VIP still yields the 4 byte protocol address that
// ProtAddressSize promises.
func TestBasicConfigurer_createGratuitousARP_IPv4In6(t *testing.T) {
	t.Parallel()

	c := &BasicConfigurer{
		IPConfiguration: &IPConfiguration{
			VIP:     netip.MustParseAddr("::ffff:192.0.2.1"),
			Netmask: net.CIDRMask(24, 32),
			Iface: net.Interface{
				Name:         "test0",
				HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			},
		},
	}

	packet, err := c.createGratuitousARP()
	if err != nil {
		t.Fatalf("createGratuitousARP() error = %v", err)
	}

	parsed := gopacket.NewPacket(packet, layers.LayerTypeEthernet, gopacket.Default)
	arpLayer := parsed.Layer(layers.LayerTypeARP)
	if arpLayer == nil {
		t.Fatal("missing ARP layer")
	}
	arp := arpLayer.(*layers.ARP)

	want := net.ParseIP("192.0.2.1").To4()
	if !bytes.Equal(arp.SourceProtAddress, want) {
		t.Errorf("ARP SourceProtAddress = %v, want %v", arp.SourceProtAddress, want)
	}
	if !bytes.Equal(arp.DstProtAddress, want) {
		t.Errorf("ARP DstProtAddress = %v, want %v", arp.DstProtAddress, want)
	}
}

// TestBasicConfigurer_queryAddress_NoSubstringMatch pins down the address
// comparison. The addresses were compared as text before, so an interface
// carrying 127.0.0.1/8 answered "yes, 27.0.0.1/8 is assigned here" - and the
// manager, believing the virtual IP was already up, never configured it.
func TestBasicConfigurer_queryAddress_NoSubstringMatch(t *testing.T) {
	t.Parallel()

	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skipf("cannot list interfaces: %v", err)
	}
	for _, iface := range ifaces {
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			assigned, err := netip.ParsePrefix(address.String())
			if err != nil || !assigned.Addr().Is4() {
				continue
			}
			// dropping the first character keeps a valid prefix whenever the
			// first octet has more than one digit, and that prefix is a
			// substring of the assigned one - 127.0.0.1/8 -> 27.0.0.1/8
			other, err := netip.ParsePrefix(assigned.String()[1:])
			if err != nil || other.Addr() == assigned.Addr() {
				continue
			}
			c := &BasicConfigurer{IPConfiguration: &IPConfiguration{
				VIP:     other.Addr(),
				Netmask: net.CIDRMask(other.Bits(), 32),
				Iface:   iface,
			}}
			if c.queryAddress() {
				t.Errorf("queryAddress() reported %s as assigned to %s, which only carries %s",
					other, iface.Name, assigned)
			}
			return
		}
	}
	t.Skip("no interface with a suitable IPv4 address found")
}

// TestBasicConfigurer_createGratuitousARP_IPv6 verifies that an IPv6 address is
// refused instead of being squeezed into the four byte address fields of an
// ARP packet.
func TestBasicConfigurer_createGratuitousARP_IPv6(t *testing.T) {
	t.Parallel()

	c := &BasicConfigurer{IPConfiguration: &IPConfiguration{
		VIP:     netip.MustParseAddr("2001:db8::1"),
		Netmask: net.CIDRMask(64, 128),
		Iface: net.Interface{
			Name:         "test0",
			HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		},
	}}
	if _, err := c.createGratuitousARP(); err == nil {
		t.Fatal("expected an error for an IPv6 virtual IP, got nil")
	}
}
