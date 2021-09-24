package ipmanager

import (
	"fmt"
	"net"
)

// IPConfiguration holds the configuration for VIP manager
type IPConfiguration struct {
	VIP        net.IP
	Netmask    net.IPMask
	Iface      *net.Interface
	RetryNum   int
	RetryAfter int
}

// getCIDR returns the CIDR composed from the given address and mask
func (c *IPConfiguration) getCIDR() string {
	return fmt.Sprintf("%s/%d", c.VIP.String(), netmaskSize(c.Netmask))
}

func netmaskSize(mask net.IPMask) int {
	ones, bits := mask.Size()
	if bits == 0 {
		panic("Invalid mask")
	}
	return ones
}
