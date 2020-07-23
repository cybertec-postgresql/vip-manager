package main

import (
	"log"
	"os/exec"
)

func (c *BasicConfigurer) ConfigureAddress() bool {
	log.Printf("Configuring address %s on %s", c.GetCIDR(), c.iface.Name)

	result := c.runAddressConfiguration("add")

	if result {
		// For now it is save to say that also working even if a
		// gratuitous arp message could not be send but logging an
		// errror should be enough.
		_ = c.ARPSendGratuitous()
	}

	return result
}

func (c *BasicConfigurer) DeconfigureAddress() bool {
	log.Printf("Removing address %s on %s", c.GetCIDR(), c.iface.Name)
	return c.runAddressConfiguration("delete")
}

func (c *BasicConfigurer) runAddressConfiguration(action string) bool {
	cmd := exec.Command("ip", "addr", action,
		c.GetCIDR(),
		"dev", c.iface.Name)
	output, err := cmd.CombinedOutput()

	switch err.(type) {
	case *exec.ExitError:
		log.Printf("Got error %s", output)

		return false
	}
	if err != nil {
		log.Printf("Error running ip address %s %s on %s: %s",
			action, c.vip, c.iface.Name, err)
		return false
	}
	return true
}
