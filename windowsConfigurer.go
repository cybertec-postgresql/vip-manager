package main

import (
	"bufio"
	"fmt"
	"log"
	"os/exec"
	"strings"
//	"syscall"
)

/**
 * The BasicConfigurer can be used to enable vip-management on nodes
 * that handle their own network connection, in setups where it is
 * sufficient to add the virtual ip using `ip addr add ...` .
 * After adding the virtual ip to the specified interface,
 * a gratuitous ARP package is sent out to update the tables of
 * nearby routers and other devices.
 */

type WindowsConfigurer struct {
	*IPConfiguration
}

func NewWindowsConfigurer(config *IPConfiguration) (*WindowsConfigurer, error) {
	c := &WindowsConfigurer{IPConfiguration: config}
	return c, nil
}

func (c *WindowsConfigurer) QueryAddress() bool {
	cmd := exec.Command("netsh", "interface", "ipv4", "show", "addresses", c.iface.Name)

	//only looks for the virtual IP, doesn't check if the netmask is also correct with the linux version.
	lookup := fmt.Sprintf("%s", c.vip.String())
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

func (c *WindowsConfigurer) ConfigureAddress() bool {
	log.Printf("Configuring address %s on %s", c.GetCIDR(), c.iface.Name)

	result := c.runAddressConfiguration("add")

	//TODO: need ARP handling?

	return result
}

func (c *WindowsConfigurer) DeconfigureAddress() bool {
	log.Printf("Removing address %s on %s", c.GetCIDR(), c.iface.Name)
	return c.runAddressConfiguration("delete")
}

func (c *WindowsConfigurer) runAddressConfiguration(action string) bool {
	var cmd *exec.Cmd
	switch action {
	case "add":
		cmd = exec.Command("netsh", "interface", "ipv4", action, "address", c.iface.Name, c.GetCIDR())
	case "delete":
		cmd = exec.Command("netsh", "interface", "ipv4", action, "address", c.iface.Name, c.vip.String())
	}
	//cmd := exec.Command("netsh", "interface", "ipv4", action, "address", c.iface.Name, c.GetCIDR())
	output, err := cmd.CombinedOutput()

	//TODO: exit status mapping? it seems like netsh always returns 1 upon error,
	//		regardless of specific error...
	//		parsing the error message is also stupid due to different error messages based on locale...

	// switch exit := err.(type) {
	// case *exec.ExitError:
	// 	if status, ok := exit.Sys().(syscall.WaitStatus); ok {
	// 		//if status.ExitStatus() == 2 {
	// 			// Already exists
	// 		//	return true
	// 		//} else {
	// 			log.Printf("Got error %s", status)
	// 		//}
	// 	}

	// 	return false
	// }

	if err != nil {
		log.Printf("Error running ip address %s %s on %s: %s",
			action, c.vip, c.iface.Name, err)
		log.Printf("Output produced by the command: \n%s\n", output)
		return false
	}
	return true
}

func (c *WindowsConfigurer) GetCIDR() string {
	return fmt.Sprintf("%s/%d", c.vip.String(), NetmaskSize(c.netmask))
}

func (c *WindowsConfigurer) cleanupArp() {
	return
}
