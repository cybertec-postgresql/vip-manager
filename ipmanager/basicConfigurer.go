package ipmanager

import (
	"fmt"
	"log"
	"net"
	"strings"

	arp "github.com/mdlayher/arp"
)

//  The BasicConfigurer can be used to enable vip-management on nodes
//  that handle their own network connection, in setups where it is
//  sufficient to add the virtual ip using `ip addr add ...` .
//  After adding the virtual ip to the specified interface,
//  a gratuitous ARP package is sent out to update the tables of
//  nearby routers and other devices.

type BasicConfigurer struct {
	*IPConfiguration
	arpClient  *arp.Client
	ntecontext uint32 //used by Windows to delete IP address
}

func NewBasicConfigurer(config *IPConfiguration) (*BasicConfigurer, error) {
	c := &BasicConfigurer{IPConfiguration: config, ntecontext: 0}

	// //this should never error out, otherwise we have bigger problems
	// local_hardware_addr, err := net.ParseMAC("00:00:00:00:00:00")
	// if err != nil {
	// 	log.Fatalf("Couldn't create a local hardware address: %s", err)
	// }

	if c.Iface.HardwareAddr == nil || c.Iface.HardwareAddr.String() == "00:00:00:00:00:00" {
		log.Fatalf("Cannot run vip-manager on the loopback device as its hardware address is the local address (00:00:00:00:00:00), which prohibits sending of gratuitous ARP messages.")
	}

	return c, nil
}

func (c *BasicConfigurer) QueryAddress() bool {
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

func (c *BasicConfigurer) GetCIDR() string {
	return fmt.Sprintf("%s/%d", c.VIP.String(), NetmaskSize(c.Netmask))
}

func (c *BasicConfigurer) cleanupArp() {
	if c.arpClient != nil {
		c.arpClient.Close()
	}
}
