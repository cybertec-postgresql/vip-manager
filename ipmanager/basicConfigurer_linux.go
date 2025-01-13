package ipmanager

import (
	"os/exec"
)

const (
	arpRequestOp = 1
	arpReplyOp   = 2
)

// configureAddress assigns virtual IP address
func (c *BasicConfigurer) configureAddress() bool {
	log.Infof("Configuring address %s on %s", c.getCIDR(), c.Iface.Name)
	result := c.runAddressConfiguration("add")
	if result {
		if err := c.arpSendGratuitous(); err != nil {
			log.Error("Failed to send gratuitous ARP: ", err)
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
