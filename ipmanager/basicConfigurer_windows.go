package ipmanager

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"

	"golang.org/x/sys/windows"

	"github.com/cybertec-postgresql/vip-manager/iphlpapi"
)

// Seams over the Win32 calls and the interface lookup, so the dispatch and the
// failure paths can be tested without touching the host's network stack.
var (
	addIPAddressFn     = iphlpapi.AddIPAddress
	deleteIPAddressFn  = iphlpapi.DeleteIPAddress
	initUnicastRowFn   = iphlpapi.InitializeUnicastIpAddressEntry
	createUnicastFn    = iphlpapi.CreateUnicastIpAddressEntry
	deleteUnicastFn    = iphlpapi.DeleteUnicastIpAddressEntry
	interfaceByNameFn  = net.InterfaceByName
	errNoAddressToDrop = errors.New("no address was configured by this instance")
)

// configureAddress assigns virtual IP address.
//
// Unlike on Linux, no gratuitous ARP or Neighbor Advertisement is composed
// here: Windows runs Duplicate Address Detection and announces the new address
// to the link itself when it is added through the iphlpapi calls below.
func (c *BasicConfigurer) configureAddress() bool {
	log.Infof("Configuring address %s on %s", c.getCIDR(), c.Iface.Name)

	iface, err := interfaceByNameFn(c.Iface.Name)
	if err != nil {
		log.Error("Failed to access interface: ", err)
		return false
	}

	// Is4 alone would miss a ::ffff:a.b.c.d VIP, which is an IPv4 address.
	if c.VIP.Is4() || c.VIP.Is4In6() {
		err = c.addIPv4Address(iface)
	} else {
		err = c.addIPv6Address(iface)
	}
	if err != nil {
		log.Error("Failed to add address: ", err)
		return false
	}

	log.Debug("Windows announces the new address to the link itself, " +
		"no gratuitous ARP or Neighbor Advertisement is sent by vip-manager")
	return true
}

// addIPv4Address adds an IPv4 VIP through the legacy, IPv4-only API.
func (c *BasicConfigurer) addIPv4Address(iface *net.Interface) error {
	// getMask() picks the width from what netip reports, and a ::ffff:a.b.c.d
	// VIP is reported as IPv6, so c.Netmask can be 16 bytes wide here. Rebuild
	// the mask from its prefix length rather than reading its first four bytes,
	// which would silently turn a /64 into a /32.
	prefix := netmaskSize(c.Netmask)
	if prefix > 32 {
		return fmt.Errorf("prefix length /%d is out of range for the IPv4 address %s", prefix, c.VIP.Unmap())
	}
	var (
		ip          = binary.LittleEndian.Uint32(c.VIP.Unmap().AsSlice())
		mask        = binary.LittleEndian.Uint32(net.CIDRMask(prefix, 32))
		nteinstance uint32
	)
	return addIPAddressFn(ip, mask, uint32(iface.Index), &c.ntecontext, &nteinstance)
}

// addIPv6Address adds an IPv6 VIP. AddIPAddress cannot be used here: it takes
// a 32 bit address and is IPv4 only.
func (c *BasicConfigurer) addIPv6Address(iface *net.Interface) error {
	row := &windows.MibUnicastIpAddressRow{}
	initUnicastRowFn(row)

	row.InterfaceIndex = uint32(iface.Index)
	row.Address.Family = windows.AF_INET6
	row.Address.Addr = c.VIP.As16()
	row.OnLinkPrefixLength = uint8(netmaskSize(c.Netmask))

	if err := createUnicastFn(row); err != nil {
		return err
	}
	c.ipv6row = row
	return nil
}

// deconfigureAddress drops virtual IP address
func (c *BasicConfigurer) deconfigureAddress() bool {
	log.Infof("Removing address %s on %s", c.getCIDR(), c.Iface.Name)

	var err error
	switch {
	case c.ipv6row != nil:
		err = deleteUnicastFn(c.ipv6row)
		if err == nil {
			c.ipv6row = nil
		}
	case c.ntecontext != 0:
		err = deleteIPAddressFn(c.ntecontext)
		if err == nil {
			c.ntecontext = 0
		}
	default:
		err = errNoAddressToDrop
	}

	if err != nil {
		log.Errorf("Failed to remove address %s: %v", c.getCIDR(), err)
		return false
	}
	return true
}
