package ipmanager

import (
	"fmt"
	"net"
)

type IPConfiguration struct {
	VIP        net.IP
	Netmask    net.IPMask
	Iface      net.Interface
	RetryNum   int
	RetryAfter int
}

func (c *IPConfiguration) GetCIDR() string {
	return fmt.Sprintf("%s/%d", c.VIP.String(), NetmaskSize(c.Netmask))
}

func NetmaskSize(mask net.IPMask) int {
	ones, bits := mask.Size()
	if bits == 0 {
		panic("Invalid mask")
	}
	return ones
}
