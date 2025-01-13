package ipmanager

import (
	"errors"
	"net"
	"strings"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

// BasicConfigurer can be used to enable vip-management on nodes
// that handle their own network connection, in setups where it is
// sufficient to add the virtual ip using `ip addr add ...` .
// After adding the virtual ip to the specified interface,
// a gratuitous ARP package is sent out to update the tables of
// nearby routers and other devices.
type BasicConfigurer struct {
	*IPConfiguration
	ntecontext uint32 //used by Windows to delete IP address
}

func newBasicConfigurer(config *IPConfiguration) (*BasicConfigurer, error) {
	c := &BasicConfigurer{IPConfiguration: config, ntecontext: 0}
	if c.Iface.HardwareAddr == nil || c.Iface.HardwareAddr.String() == "00:00:00:00:00:00" {
		return nil, errors.New(`Cannot run vip-manager on the loopback device
as its hardware address is the local address (00:00:00:00:00:00),
which prohibits sending of gratuitous ARP messages`)
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
	for _, address := range addresses {
		if strings.Contains(address.String(), c.getCIDR()) {
			return true
		}
	}
	return false
}

const (
	MACAddressSize  = 6
	IPv4AddressSize = 4
)

// arpSendGratuitous is a function that sends gratuitous ARP requests
func (c *BasicConfigurer) createGratuitousARP() ([]byte, error) {
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
		SourceProtAddress: c.IPConfiguration.VIP.AsSlice(),
		DstHwAddress:      c.Iface.HardwareAddr, // Gratuitous ARP targets itself
		DstProtAddress:    c.IPConfiguration.VIP.AsSlice(),
	}

	// Create a packet with the layers
	buffer := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	if err := gopacket.SerializeLayers(buffer, opts, ethLayer, arpLayer); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}
