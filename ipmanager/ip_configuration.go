package ipmanager

import (
	"fmt"
	"net"
	"net/netip"

	"go.uber.org/zap"
)

// IPConfiguration holds the configuration for VIP manager
type IPConfiguration struct {
	VIP        netip.Addr
	Netmask    net.IPMask
	Iface      net.Interface
	RetryNum   int
	RetryAfter int
	Logger     *zap.SugaredLogger
}

// log returns the logger of the configuration. A configuration built without
// one - every test does that - discards the messages instead of panicking,
// which is what the package level logger this replaces used to do.
func (c *IPConfiguration) log() *zap.SugaredLogger {
	if c.Logger == nil {
		return zap.NewNop().Sugar()
	}
	return c.Logger
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
