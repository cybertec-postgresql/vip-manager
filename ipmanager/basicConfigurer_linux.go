package ipmanager

import (
	"fmt"
	"net"
	"os/exec"
	"syscall"
)

var execCommand = exec.Command
var linuxSendPacketWithProtocolFn = sendPacketLinuxWithProtocol
var interfaceAddrsFn = defaultInterfaceAddrs

// defaultInterfaceAddrs returns the addresses currently assigned to the named
// interface. It is kept in a variable so tests can drive the IPv6 branch of
// configureAddress on hosts that have no suitable interface.
func defaultInterfaceAddrs(name string) ([]net.Addr, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}
	return iface.Addrs()
}

// htons converts uint16 to network byte order
func htons(i uint16) uint16 {
	return (i<<8)&0xff00 | i>>8
}

func sendPacketLinux(iface net.Interface, packetData []byte) error {
	return sendPacketLinuxWithProtocol(iface, packetData, syscall.ETH_P_ARP)
}

func sendPacketLinuxWithProtocol(iface net.Interface, packetData []byte, protocol uint16) error {
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(htons(syscall.ETH_P_ALL)))
	if err != nil {
		return err
	}
	defer syscall.Close(fd)

	var sll syscall.SockaddrLinklayer
	sll.Protocol = htons(protocol)
	sll.Ifindex = iface.Index
	sll.Hatype = syscall.ARPHRD_ETHER
	sll.Pkttype = syscall.PACKET_BROADCAST

	if err = syscall.Bind(fd, &sll); err != nil {
		return err
	}

	return syscall.Sendto(fd, packetData, 0, &sll)
}

// pickLinkLocal returns the first IPv6 link-local unicast address found in addrs.
func pickLinkLocal(ifaceName string, addrs []net.Addr) (net.IP, error) {
	for _, addr := range addrs {
		var ip net.IP
		switch value := addr.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		}
		if ip != nil && ip.To4() == nil && ip.IsLinkLocalUnicast() {
			return ip, nil
		}
	}
	return nil, fmt.Errorf("interface %s has no IPv6 link-local address", ifaceName)
}

// linkLocalAddress returns the interface's IPv6 link-local address, which is
// used as the source of the unsolicited Neighbor Advertisement. The VIP itself
// cannot be used: right after `ip addr add` it is still tentative while
// Duplicate Address Detection runs, and RFC 4862 forbids sending from a
// tentative address.
func (c *BasicConfigurer) linkLocalAddress() (net.IP, error) {
	addrs, err := interfaceAddrsFn(c.Iface.Name)
	if err != nil {
		return nil, err
	}
	return pickLinkLocal(c.Iface.Name, addrs)
}

// sendNeighborAdvertisement announces the new owner of an IPv6 VIP to all nodes
// on the link, so neighbours replace their cached MAC address immediately
// instead of waiting for the neighbor cache entry to expire.
func (c *BasicConfigurer) sendNeighborAdvertisement() {
	sourceIP, err := c.linkLocalAddress()
	if err != nil {
		c.log().Warn("Failed to find IPv6 link-local address for Neighbor Advertisement: ", err)
		return
	}
	buff, err := c.createGratuitousNA(sourceIP)
	if err != nil {
		c.log().Warn("Failed to compose unsolicited Neighbor Advertisement: ", err)
		return
	}
	if err := linuxSendPacketWithProtocolFn(c.Iface, buff, syscall.ETH_P_IPV6); err != nil {
		c.log().Warn("Failed to send unsolicited Neighbor Advertisement: ", err)
	}
}

// sendGratuitousARP announces the new owner of an IPv4 VIP on the link.
func (c *BasicConfigurer) sendGratuitousARP() {
	buff, err := c.createGratuitousARP()
	if err != nil {
		c.log().Warn("Failed to compose gratuitous ARP request: ", err)
		return
	}
	if err := linuxSendPacketWithProtocolFn(c.Iface, buff, syscall.ETH_P_ARP); err != nil {
		c.log().Warn("Failed to send gratuitous ARP request: ", err)
	}
}

// configureAddress assigns virtual IP address
func (c *BasicConfigurer) configureAddress() bool {
	c.log().Infof("Configuring address %s on %s", c.getCIDR(), c.Iface.Name)
	if !c.runAddressConfiguration("add") {
		return false
	}
	// Is6 alone is not enough: it is also true for IPv4-in-IPv6 addresses such
	// as ::ffff:192.0.2.1, which belong on the ARP path.
	if c.VIP.Is6() && !c.VIP.Is4In6() {
		c.sendNeighborAdvertisement()
	} else {
		c.sendGratuitousARP()
	}
	return true
}

// deconfigureAddress drops virtual IP address
func (c *BasicConfigurer) deconfigureAddress() bool {
	c.log().Infof("Removing address %s on %s", c.getCIDR(), c.Iface.Name)
	return c.runAddressConfiguration("delete")
}

func (c *BasicConfigurer) runAddressConfiguration(action string) bool {
	cmd := execCommand("ip", "addr", action,
		c.getCIDR(),
		"dev", c.Iface.Name)
	output, err := cmd.CombinedOutput()

	switch err.(type) {
	case *exec.ExitError:
		c.log().Infof("Got error %s", output)

		return false
	}
	if err != nil {
		c.log().Infof("Error running ip address %s %s on %s: %s",
			action, c.VIP, c.Iface.Name, err)
		return false
	}
	return true
}
