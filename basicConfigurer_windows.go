package main

import (
	"encoding/binary"
	"log"
	"net"

	"github.com/cybertec-postgresql/vip-manager/iphlpapi"
)

func (c *BasicConfigurer) ConfigureAddress() bool {
	log.Printf("Configuring address %s on %s", c.GetCIDR(), c.iface.Name)
	var (
		ip          uint32 = binary.LittleEndian.Uint32(c.vip.To4())
		mask        uint32 = binary.LittleEndian.Uint32(c.netmask)
		nteinstance uint32
	)
	iface, err := net.InterfaceByName(c.iface.Name)
	if err != nil {
		log.Printf("Got error: %v", err)
		return false
	}
	err = iphlpapi.AddIPAddress(ip, mask, uint32(iface.Index), &c.ntecontext, &nteinstance)
	if err != nil {
		log.Printf("Got error: %v", err)
		return false
	}
	// For now it is save to say that also working even if a
	// gratuitous arp message could not be send but logging an
	// errror should be enough.
	_ = c.ARPSendGratuitous()
	return true
}

func (c *BasicConfigurer) DeconfigureAddress() bool {
	log.Printf("Removing address %s on %s", c.GetCIDR(), c.iface.Name)
	err := iphlpapi.DeleteIPAddress(c.ntecontext)
	if err != nil {
		log.Printf("Got error: %v", err)
		return false
	}
	return true
}
