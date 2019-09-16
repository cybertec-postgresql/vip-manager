package main

import (
	"fmt"
	"net"
)

type IPConfiguration struct {
	vip         net.IP
	netmask     net.IPMask
	iface       net.Interface
	Retry_num   int
	Retry_after int
}

func (c *IPConfiguration) GetCIDR() string {
	return fmt.Sprintf("%s/%d", c.vip.String(), NetmaskSize(c.netmask))
}

func NetmaskSize(mask net.IPMask) int {
	ones, bits := mask.Size()
	if bits == 0 {
		panic("Invalid mask")
	}
	return ones
}
