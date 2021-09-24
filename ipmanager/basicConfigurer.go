package ipmanager

import (
	"errors"
	"net"
	"strings"

	arp "github.com/mdlayher/arp"
)

// BasicConfigurer can be used to enable vip-management on nodes
// that handle their own network connection, in setups where it is
// sufficient to add the virtual ip using `ip addr add ...` .
// After adding the virtual ip to the specified interface,
// a gratuitous ARP package is sent out to update the tables of
// nearby routers and other devices.
type BasicConfigurer struct {
	*IPConfiguration
	arpClient  *arp.Client
	ntecontext uint32 //used by Windows to delete IP address
}

func newBasicConfigurer(config *IPConfiguration) (*BasicConfigurer, error) {
	if config.Iface == nil {
		return nil, errors.New("invalid network interface")
	}

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
		if strings.Contains(address.String(), c.VIP.String()) {
			return true
		}
	}
	return false
}

func (c *BasicConfigurer) cleanupArp() {
	if c.arpClient != nil {
		c.arpClient.Close()
	}
}
