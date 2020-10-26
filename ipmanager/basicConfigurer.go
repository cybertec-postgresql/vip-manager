package ipmanager

import (
	"errors"
	"net"
	"strings"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	arp "github.com/mdlayher/arp"
)

// BasicConfigurer can be used to enable vip-management on nodes
// that handle their own network connection, in setups where it is
// sufficient to add the virtual ip using `ip addr add ...` .
// After adding the virtual ip to the specified interface,
// a gratuitous ARP package is sent out to update the tables of
// nearby routers and other devices.
type BasicConfigurer struct {
	config     *vipconfig.Config
	arpClient  *arp.Client
	ntecontext uint32 //used by Windows to delete IP address
}

func newBasicConfigurer(config *vipconfig.Config) (*BasicConfigurer, error) {
	c := &BasicConfigurer{config: config, ntecontext: 0}
	//TODO: move into config validator in vipconfig/config.go
	if c.config.ParsedIface.HardwareAddr == nil || c.config.ParsedIface.HardwareAddr.String() == "00:00:00:00:00:00" {
		return nil, errors.New(`Cannot run vip-manager on the loopback device
as its hardware address is the local address (00:00:00:00:00:00),
which prohibits sending of gratuitous ARP messages`)
	}
	return c, nil
}

// queryAddress returns true if the address is assigned
func (c *BasicConfigurer) queryAddress() bool {
	iface, err := net.InterfaceByName(c.config.ParsedIface.Name)
	if err != nil {
		return false
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, address := range addresses {
		if strings.Contains(address.String(), c.config.ParsedIP.String()) {
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
