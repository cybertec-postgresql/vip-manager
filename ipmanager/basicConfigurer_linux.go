package ipmanager

import (
	"net"
	"os/exec"
	"syscall"
)

func htons(i uint16) uint16 {
	return (i<<8)&0xff00 | i>>8
}

func sendPacketLinux(iface net.Interface, packetData []byte) error {
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(htons(syscall.ETH_P_ALL)))
	if err != nil {
		return err
	}
	defer syscall.Close(fd)

	var sll syscall.SockaddrLinklayer
	sll.Protocol = htons(syscall.ETH_P_ARP)
	sll.Ifindex = iface.Index
	sll.Hatype = syscall.ARPHRD_ETHER
	sll.Pkttype = syscall.PACKET_BROADCAST

	if err = syscall.Bind(fd, &sll); err != nil {
		return err
	}

	return syscall.Sendto(fd, packetData, 0, &sll)
}

// configureAddress assigns virtual IP address
func (c *BasicConfigurer) configureAddress() bool {
	log.Infof("Configuring address %s on %s", c.getCIDR(), c.Iface.Name)
	result := c.runAddressConfiguration("add")
	if result {
		if buff, err := c.createGratuitousARP(); err != nil {
			log.Warn("Failed to compose gratuitous ARP request: ", err)
		} else {
			if err := sendPacketLinux(c.Iface, buff); err != nil {
				log.Warn("Failed to send gratuitous ARP request: ", err)
			}
		}
	}

	return result
}

// deconfigureAddress drops virtual IP address
func (c *BasicConfigurer) deconfigureAddress() bool {
	log.Infof("Removing address %s on %s", c.getCIDR(), c.Iface.Name)
	return c.runAddressConfiguration("delete")
}

func (c *BasicConfigurer) runAddressConfiguration(action string) bool {
	cmd := exec.Command("ip", "addr", action,
		c.getCIDR(),
		"dev", c.Iface.Name)
	output, err := cmd.CombinedOutput()

	switch err.(type) {
	case *exec.ExitError:
		log.Infof("Got error %s", output)

		return false
	}
	if err != nil {
		log.Infof("Error running ip address %s %s on %s: %s",
			action, c.VIP, c.Iface.Name, err)
		return false
	}
	return true
}
