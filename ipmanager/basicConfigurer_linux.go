package ipmanager

import (
	"log"
	"net"
	"os/exec"
	"time"

	arp "github.com/mdlayher/arp"
)

const (
	arpRequestOp = 1
	arpReplyOp   = 2
)

var (
	ethernetBroadcast = net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
)

// configureAddress assigns virtual IP address
func (c *BasicConfigurer) configureAddress() bool {
	if c.arpClient == nil {
		err := c.createArpClient()
		if err != nil {
			log.Fatalf("Couldn't create an Arp client: %s", err)
		}
	}

	log.Printf("Configuring address %s on %s", c.config.ParsedCIDR, c.config.ParsedIface.Name)

	result := c.runAddressConfiguration("add")

	if result {
		// For now it is save to say that also working even if a
		// gratuitous arp message could not be send but logging an
		// errror should be enough.
		_ = c.arpSendGratuitous()
	}

	return result
}

// deconfigureAddress drops virtual IP address
func (c *BasicConfigurer) deconfigureAddress() bool {
	log.Printf("Removing address %s on %s", c.config.ParsedCIDR, c.config.ParsedIface.Name)
	return c.runAddressConfiguration("delete")
}

func (c *BasicConfigurer) runAddressConfiguration(action string) bool {
	cmd := exec.Command("ip", "addr", action,
		c.config.ParsedCIDR,
		"dev", c.config.ParsedIface.Name)
	output, err := cmd.CombinedOutput()

	switch err.(type) {
	case *exec.ExitError:
		log.Printf("Got error %s", output)

		return false
	}
	if err != nil {
		log.Printf("Error running ip address %s %s on %s: %s",
			action, c.config.ParsedIP, c.config.ParsedIface.Name, err)
		return false
	}
	return true
}

func (c *BasicConfigurer) createArpClient() error {
	var err error
	var arpClient *arp.Client
	for i := 0; i < c.config.RetryNum; i++ {
		arpClient, err = arp.Dial(c.config.ParsedIface)
		if err != nil {
			log.Printf("Problems with producing the arp client: %s", err)
		} else {
			break
		}
		time.Sleep(time.Duration(c.config.RetryAfter) * time.Millisecond)
	}
	if err != nil {
		log.Print("too many retries")
		return err
	}
	c.arpClient = arpClient
	return nil
}

// sends a gratuitous ARP request and reply
func (c *BasicConfigurer) arpSendGratuitous() error {
	/* While RFC 2002 does not say whether a gratuitous ARP request or reply is preferred
	 * to update ones neighbours' MAC tables, the Wireshark Wiki recommends sending both.
	 *		https://wiki.wireshark.org/Gratuitous_ARP
	 * This site also recommends sending a reply, as requests might be ignored by some hardware:
	 *		https://support.citrix.com/article/CTX112701
	 */
	gratuitousReplyPackage, err := arp.NewPacket(
		arpReplyOp,
		c.config.ParsedIface.HardwareAddr,
		c.config.ParsedIP,
		c.config.ParsedIface.HardwareAddr,
		c.config.ParsedIP,
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
		arpRequestDestMac = c.config.ParsedIface.HardwareAddr
	}

	gratuitousRequestPackage, err := arp.NewPacket(
		arpRequestOp,
		c.config.ParsedIface.HardwareAddr,
		c.config.ParsedIP,
		arpRequestDestMac,
		c.config.ParsedIP,
	)
	if err != nil {
		log.Printf("Gratuitous arp request package is malformed: %s", err)
		return err
	}

	for i := 0; i < c.config.RetryNum; i++ {
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
		time.Sleep(time.Duration(c.config.RetryAfter) * time.Millisecond)
	}
	if err != nil {
		log.Print("too many retries")
		return err
	}

	return nil
}
