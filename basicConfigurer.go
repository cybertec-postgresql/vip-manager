package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"syscall"

	arp "github.com/mdlayher/arp"
)

/**
 * The BasicConfigurer can be used to enable vip-management on nodes
 * that handle their own network connection, in setups where it is
 * sufficient to add the virtual ip using `ip addr add ...` .
 * After adding the virtual ip to the specified interface,
 * a gratuitous ARP package is sent out to update the tables of
 * nearby routers and other devices.
 */

const (
	arpReplyOp = 2
)

type BasicConfigurer struct {
	*IPConfiguration
	arpClient *arp.Client
}

func NewBasicConfigurer(config *IPConfiguration) (*BasicConfigurer, error) {
	c := &BasicConfigurer{IPConfiguration: config}

	arpClient, err := arp.Dial(&c.iface)
	if err != nil {
		log.Printf("Problems with producing the arp client: %s", err)
		return nil, err
	}
	c.arpClient = arpClient

	return c, nil
}

func (c *BasicConfigurer) ARPSendGratuitous() error {
	gratuitousPackage, err := arp.NewPacket(
		arpReplyOp,
		c.iface.HardwareAddr,
		c.vip,
		ethernetBroadcast,
		net.IPv4bcast,
	)

	if err != nil {
		log.Printf("Gratuitous arp package is malformed: %s", err)
		return err
	}

	err = c.arpClient.WriteTo(gratuitousPackage, ethernetBroadcast)
	if err != nil {
		log.Printf("Cannot send gratuitous arp message: %s", err)
		return err
	}

	return nil
}

func (c *BasicConfigurer) QueryAddress() bool {
	cmd := exec.Command("ip", "addr", "show", c.iface.Name)

	lookup := fmt.Sprintf("inet %s", c.GetCIDR())
	result := false

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}

	err = cmd.Start()
	if err != nil {
		panic(err)
	}

	scn := bufio.NewScanner(stdout)

	for scn.Scan() {
		line := scn.Text()
		if strings.Contains(line, lookup) {
			result = true
		}
	}

	cmd.Wait()

	return result
}

func (c *BasicConfigurer) ConfigureAddress() bool {
	log.Printf("Configuring address %s on %s", c.GetCIDR(), c.iface.Name)

	result := c.runAddressConfiguration("add")

	if result == true {
		// For now it is save to say that also working even if a
		// gratuitous arp message could not be send but logging an
		// errror should be enough.
		c.ARPSendGratuitous()
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
	err := cmd.Run()

	switch exit := err.(type) {
	case *exec.ExitError:
		if status, ok := exit.Sys().(syscall.WaitStatus); ok {
			if status.ExitStatus() == 2 {
				// Already exists
				return true
			} else {
				log.Printf("Got error %s", status)
			}
		}

		return false
	}
	if err != nil {
		log.Printf("Error running ip address %s %s on %s: %s",
			action, c.vip, c.iface.Name, err)
		return false
	}
	return true
}

func (c *BasicConfigurer) GetCIDR() string {
	return fmt.Sprintf("%s/%d", c.vip.String(), NetmaskSize(c.netmask))
}

func (c *BasicConfigurer) cleanupArp() {
	c.arpClient.Close()
}
