package ipmanager

import (
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

// BasicConfigurer can be used to enable vip-management on nodes
// that handle their own network connection, in setups where it is
// sufficient to add the virtual ip using `ip addr add ...` .
// After adding the virtual ip to the specified interface,
// a gratuitous ARP package or IPv6 Neighbor Advertisement is sent out to update the tables of
// nearby routers and other devices.
type BasicConfigurer struct {
	*IPConfiguration
	osState // platform specific handles, used by Windows to delete the address
}

func newBasicConfigurer(config *IPConfiguration) (*BasicConfigurer, error) {
	c := &BasicConfigurer{IPConfiguration: config, osState: osState{}}
	if c.Iface.HardwareAddr == nil || c.Iface.HardwareAddr.String() == "00:00:00:00:00:00" {
		return nil, errors.New(`cannot run vip-manager on the loopback device
as its hardware address is the local address (00:00:00:00:00:00),
which prohibits sending of gratuitous ARP messages and IPv6 Neighbor Advertisements`)
	}
	return c, nil
}

// queryAddress returns if the address is assigned
func (c *BasicConfigurer) queryAddress() bool {
	iface, err := net.InterfaceByName(c.Iface.Name)
	if err != nil {
		return false
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return false
	}
	// compare the parsed address, not the text: a substring match answers true
	// for 10.0.0.1/24 when the interface only carries 110.0.0.1/24, and the
	// manager would then never configure the virtual IP on that machine
	want := c.VIP.Unmap()
	wantBits := netmaskSize(c.Netmask)
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address.String())
		if err != nil {
			continue
		}
		if prefix.Addr().Unmap() == want && prefix.Bits() == wantBits {
			return true
		}
	}
	return false
}

const (
	MACAddressSize  = 6
	IPv4AddressSize = 4
)

// serializePacketLayers is gopacket.SerializeLayers, kept in a variable so
// tests can force a serialization failure.
var serializePacketLayers = gopacket.SerializeLayers

// createGratuitousNA prepares an unsolicited IPv6 Neighbor Advertisement.
func (c *BasicConfigurer) createGratuitousNA(sourceIP net.IP) ([]byte, error) {
	allNodes := net.ParseIP("ff02::1")
	ethLayer := &layers.Ethernet{
		SrcMAC:       c.Iface.HardwareAddr,
		DstMAC:       net.HardwareAddr{0x33, 0x33, 0x00, 0x00, 0x00, 0x01},
		EthernetType: layers.EthernetTypeIPv6,
	}
	ipv6Layer := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   255,
		SrcIP:      sourceIP,
		DstIP:      allNodes,
	}
	naLayer := &layers.ICMPv6NeighborAdvertisement{
		Flags:         0x20, // Override existing neighbor cache entries.
		TargetAddress: c.VIP.AsSlice(),
		Options: layers.ICMPv6Options{
			{
				Type: layers.ICMPv6OptTargetAddress,
				Data: c.Iface.HardwareAddr,
			},
		},
	}
	icmpLayer := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeNeighborAdvertisement, 0),
	}
	if err := icmpLayer.SetNetworkLayerForChecksum(ipv6Layer); err != nil {
		return nil, err
	}

	buffer := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	if err := serializePacketLayers(buffer, opts, ethLayer, ipv6Layer, icmpLayer, naLayer); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

// createGratuitousARP prepares a packet with a gratuitous ARP request.
// ARP is IPv4 only - the IPv6 equivalent is an unsolicited neighbour
// advertisement, which is not implemented, so the caller has to skip this for
// IPv6 addresses instead of putting a 16 byte address into a 4 byte field.
func (c *BasicConfigurer) createGratuitousARP() ([]byte, error) {
	if !c.VIP.Unmap().Is4() {
		return nil, fmt.Errorf("cannot send a gratuitous ARP message for the IPv6 address %s", c.VIP)
	}
	// Unmap so that a ::ffff:a.b.c.d VIP yields the 4 bytes ProtAddressSize promises.
	vip := c.VIP.Unmap().AsSlice()

	// Create the Ethernet layer
	ethLayer := &layers.Ethernet{
		SrcMAC:       c.Iface.HardwareAddr,
		DstMAC:       net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, // Broadcast
		EthernetType: layers.EthernetTypeARP,
	}

	// Create the ARP layer
	arpLayer := &layers.ARP{
		AddrType:          layers.LinkTypeEthernet,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     MACAddressSize,
		ProtAddressSize:   IPv4AddressSize,
		Operation:         layers.ARPReply, // Gratuitous ARP is sent as a reply
		SourceHwAddress:   c.Iface.HardwareAddr,
		SourceProtAddress: vip,
		DstHwAddress:      c.Iface.HardwareAddr, // Gratuitous ARP targets itself
		DstProtAddress:    vip,
	}

	// Create a packet with the layers
	buffer := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	if err := serializePacketLayers(buffer, opts, ethLayer, arpLayer); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}
