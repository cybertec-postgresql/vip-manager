package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"time"

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
	arpRequestOp = 1
	arpReplyOp   = 2
)

type BasicConfigurer struct {
	*IPConfiguration
	arpClient *arp.Client
}

func NewBasicConfigurer(config *IPConfiguration) (*BasicConfigurer, error) {
	c := &BasicConfigurer{IPConfiguration: config}

	// //this should never error out, otherwise we have bigger problems
	// local_hardware_addr, err := net.ParseMAC("00:00:00:00:00:00")
	// if err != nil {
	// 	log.Fatalf("Couldn't create a local hardware address: %s", err)
	// }

	err := c.createArpClient()
	if err != nil {
		log.Fatalf("Couldn't create an Arp client: %s", err)
	}

	if c.iface.HardwareAddr == nil || c.iface.HardwareAddr.String() == "00:00:00:00:00:00" {
		log.Fatalf("Cannot run vip-manager on the loopback device as its hardware address is the local address (00:00:00:00:00:00), which prohibits sending of gratuitous ARP messages.")
	}

	return c, nil
}

func (c *BasicConfigurer) createArpClient() error {
	var err error
	var arpClient *arp.Client
	for i := 0; i < c.Retry_num; i++ {
		arpClient, err = arp.Dial(&c.iface)
		if err != nil {
			log.Printf("Problems with producing the arp client: %s", err)
		} else {
			break
		}
		time.Sleep(time.Duration(c.Retry_after) * time.Millisecond)
	}
	if err != nil {
		log.Print("too many retries")
		return err
	}
	c.arpClient = arpClient
	return nil
}

func (c *BasicConfigurer) ARPSendGratuitous() error {
	/* While RFC 2002 does not say whether a gratuitous ARP request or reply is preferred
	 * to update ones neighbours' MAC tables, the Wireshark Wiki recommends sending both.
	 *		https://wiki.wireshark.org/Gratuitous_ARP
	 * This site also recommends sending a reply, as requests might be ignored by some hardware:
	 *		https://support.citrix.com/article/CTX112701
	 */
	gratuitousReplyPackage, err := arp.NewPacket(
		arpReplyOp,
		c.iface.HardwareAddr,
		c.vip,
		c.iface.HardwareAddr,
		c.vip,
	)
	if err != nil {
		log.Printf("Gratuitous arp reply package is malformed: %s", err)
		return err
	}

	/* RFC 2002 specifies (in section 4.6) that a gratuitous ARP request
	 * should "not set" the target Hardware Address (THA).
	 * Since the arp package offers no option to leave the THA out, we specify the Zero-MAC.
	 * If parsing that fails for some reason, we'll just use the local interface's address.
	 * The field is probably ignored by the receivers' implementation anyway.
	 */
	arpRequestDestMac, err := net.ParseMAC("00:00:00:00:00:00")
	if err != nil {
		// not entirely RFC-2002 conform but better then nothing.
		arpRequestDestMac = c.iface.HardwareAddr
	}

	gratuitousRequestPackage, err := arp.NewPacket(
		arpRequestOp,
		c.iface.HardwareAddr,
		c.vip,
		arpRequestDestMac,
		c.vip,
	)
	if err != nil {
		log.Printf("Gratuitous arp request package is malformed: %s", err)
		return err
	}

	for i := 0; i < c.Retry_num; i++ {
		errReply := c.arpClient.WriteTo(gratuitousReplyPackage, ethernetBroadcast)
		if err != nil {
			log.Printf("Couldn't write to the arpClient: %s", errReply)
		} else {
			log.Println("Sent gratuitous ARP reply")
		}

		errRequest := c.arpClient.WriteTo(gratuitousRequestPackage, ethernetBroadcast)
		if err != nil {
			log.Printf("Couldn't write to the arpClient: %s", errRequest)
		} else {
			log.Println("Sent gratuitous ARP request")
		}

		if errReply != nil || errRequest != nil {
			/* If something went wrong while sending the packages, we'll recreate the ARP client for the next try,
			 * to avoid having a stale client that gives "network is down" error.
			 */
			err = c.createArpClient()
		} else {
			//TODO: think about whether to leave this out to achieve simple repeat sending of GARP packages
			break
		}
		time.Sleep(time.Duration(c.Retry_after) * time.Millisecond)
	}
	if err != nil {
		log.Print("too many retries")
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

func (c *BasicConfigurer) GetCIDR() string {
	return fmt.Sprintf("%s/%d", c.vip.String(), NetmaskSize(c.netmask))
}

func (c *BasicConfigurer) cleanupArp() {
	if c.arpClient != nil {
		c.arpClient.Close()
	}
}
