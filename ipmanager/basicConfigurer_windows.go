package ipmanager

import (
	"encoding/binary"
	"net"

	"github.com/cybertec-postgresql/vip-manager/iphlpapi"
)

// configureAddress assigns virtual IP address
func (c *BasicConfigurer) configureAddress() bool {
	log.Infof("Configuring address %s on %s", c.getCIDR(), c.Iface.Name)
	var (
		ip          = binary.LittleEndian.Uint32(c.VIP.AsSlice())
		mask        = binary.LittleEndian.Uint32(c.Netmask)
		nteinstance uint32
	)
	iface, err := net.InterfaceByName(c.Iface.Name)
	if err != nil {
		log.Error("Failed to access interface: ", err)
		return false
	}
	err = iphlpapi.AddIPAddress(ip, mask, uint32(iface.Index), &c.ntecontext, &nteinstance)
	if err != nil {
		log.Error("Failed to add address: ", err)
		return false
	}

	if err := c.arpSendGratuitous(); err != nil {
		log.Error("Failed to send gratuitous ARP: ", err)
	}
	return true
}

// deconfigureAddress drops virtual IP address
func (c *BasicConfigurer) deconfigureAddress() bool {
	log.Infof("Removing address %s on %s", c.getCIDR(), c.Iface.Name)
	err := iphlpapi.DeleteIPAddress(c.ntecontext)
	if err != nil {
		log.Errorf("Failed to remove address %s: %v", c.getCIDR(), err)
		return false
	}
	return true
}
