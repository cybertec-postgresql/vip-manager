package ipmanager

import (
	"fmt"
	"net"
	"net/netip"

	"go.uber.org/zap"
)

// nopLog is used by configurations built without a logger, as tests do.
var nopLog = zap.NewNop().Sugar()

// IPConfiguration holds the configuration for VIP manager
type IPConfiguration struct {
	VIP        netip.Addr
	Netmask    net.IPMask
	Iface      net.Interface
	RetryNum   int
	RetryAfter int
	Log        *zap.SugaredLogger
}

// log returns the logger of the configuration. The configurers embed
// IPConfiguration, so they log through it instead of a package variable, which
// NewIPManager used to overwrite while other goroutines were reading it.
func (c *IPConfiguration) log() *zap.SugaredLogger {
	if c.Log == nil {
		return nopLog
	}
	return c.Log
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
