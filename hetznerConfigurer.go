package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"time"
)

/**
 * The HetznerConfigurer can be used to enable vip-management on nodes
 * rented in a Hetzner Datacenter.
 * Since Hetzner provides an API that handles failover-ip routing,
 * this API is used to manage the vip, whenever hostintype `hetzner` is set.
 */

const (
	UNKNOWN    = iota // c0 == 0
	CONFIGURED = iota // c1 == 1
	RELEASED   = iota // c2 == 2
)

type HetznerConfigurer struct {
	*IPConfiguration
	cachedState  int
	lastAPICheck time.Time
	verbose      bool
}

func NewHetznerConfigurer(config *IPConfiguration, verbose bool) (*HetznerConfigurer, error) {
	c := &HetznerConfigurer{
		IPConfiguration: config,
		cachedState: UNKNOWN,
		lastAPICheck: time.Unix(0, 0),
		verbose: verbose}

	return c, nil
}

/**
 * In order to tell the Hetzner API to route the failover-ip to
 * this machine, we must attach our own IP address to the API request.
 */
func getOutboundIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil || conn == nil {
		log.Println("error dialing 8.8.8.8 to retrieve preferred outbound IP", err)
		return nil
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP
}

func (c *HetznerConfigurer) curlQueryFailover(post bool) (string, error) {
	/**
	 * The credentials for the API are loaded from a file stored in /etc/hetzner .
	 */
	//TODO: make credentialsFile dynamically changeable?
	credentialsFile := "/etc/hetzner"
	f, err := os.Open(credentialsFile)
	if err != nil {
		log.Println("can't open passwordfile", err)
		return "", err
	}
	defer f.Close()

	/**
	 * The retrieval of username and password from the file is rather static,
	 * so the credentials file must conform to the offsets down below perfectly.
	 */
	var user string
	var password string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		switch line[:4] {
		case "user":
			user = line[6 : len(line)-1]
		case "pass":
			password = line[6 : len(line)-1]
		}
	}
	if user == "" || password == "" {
		log.Println("Couldn't retrieve username or password from file", credentialsFile)
		return "", errors.New("Couldn't retrieve username or password from file")
	}

	/**
	 * As Hetzner API only allows IPv4 connections, we rely on curl
	 * instead of GO's own http package,
	 * as selecting IPv4 transport there doesn't seem trivial.
	 *
	 * If post is set to true, a failover will be triggered.
	 * If it is set to false, the current state (i.e. route)
	 * for the failover-ip will be retrieved.
	 */
	var cmd *exec.Cmd
	if post == true {
		my_own_ip := getOutboundIP()
		if my_own_ip == nil {
			log.Printf("Error determining this machine's IP address.")
			return "", errors.New("Error determining this machine's IP address")
		}
		log.Printf("my_own_ip: %s\n", my_own_ip.String())

		cmd = exec.Command("curl",
		                   "--ipv4",
		                   "-u", user+":"+password,
		                   "https://robot-ws.your-server.de/failover/"+c.vip.String(),
		                   "-d", "active_server_ip="+my_own_ip.String())

		if c.verbose {
			log.Printf("%s %s %s '%s' %s %s %s",
			           "curl",
			           "--ipv4",
			           "-u", user+":XXXXXX",
			           "https://robot-ws.your-server.de/failover/"+c.vip.String(),
			           "-d", "active_server_ip="+my_own_ip.String())
		}
	} else {
		cmd = exec.Command("curl",
		                   "--ipv4",
		                   "-u", user+":"+password,
		                   "https://robot-ws.your-server.de/failover/"+c.vip.String())

		if c.verbose {
			log.Printf("%s %s %s %s %s",
			           "curl",
			           "--ipv4",
			           "-u", user+":XXXXXX",
			           "https://robot-ws.your-server.de/failover/"+c.vip.String())
		}
	}

	out, err := cmd.Output()

	if err != nil {
		return "", err
	}

	retStr := string(out[:])

	return retStr, nil
}

/**
 * This function is used to parse the response which comes from the
 * curlQueryFailover function and in turn from the curl calls to the API.
 */
func (c *HetznerConfigurer) getActiveIpFromJson(str string) (net.IP, error) {
	var f map[string]interface{}

	if c.verbose {
		log.Printf("JSON response: %s\n", str)
	}

	err := json.Unmarshal([]byte(str), &f)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	if f["error"] != nil {
		errormap := f["error"].(map[string]interface{})

		log.Printf("There was an error accessing the Hetzner API!\n" +
		           " status: %f\n code: %s\n message: %s\n",
		           errormap["status"].(float64),
		           errormap["code"].(string),
		           errormap["message"].(string))
		return nil, errors.New("Hetzner API returned error response.")
	}

	if f["failover"] != nil {
		failovermap := f["failover"].(map[string]interface{})

		ip := failovermap["ip"].(string)
		netmask := failovermap["netmask"].(string)
		server_ip := failovermap["server_ip"].(string)
		server_number := failovermap["server_number"].(float64)
		active_server_ip := failovermap["active_server_ip"].(string)

		log.Println("Result of the failover query was: ",
			"failover-ip=", ip,
			"netmask=", netmask,
			"server_ip=", server_ip,
			"server_number=", server_number,
			"active_server_ip=", active_server_ip,
		)

		return net.ParseIP(active_server_ip), nil

	}

	return nil, errors.New("why did we end up here?")
}

func (c *HetznerConfigurer) QueryAddress() bool {
	if (time.Since(c.lastAPICheck) / time.Hour) > 1 {
		/**We need to recheck the status!
		 * Don't check too often because of stupid API rate limits
		 */
		log.Println("Cached state was too old.")
		c.cachedState = UNKNOWN
	} else {
		/** no need to check, we can use "cached" state if set.
		 * if it is set to UNKOWN, a check will be done.
		 */
		if c.cachedState == CONFIGURED {
			return true
		} else if c.cachedState == RELEASED {
			return false
		}
	}

	str, err := c.curlQueryFailover(false)
	if err != nil {
		//TODO
		c.cachedState = UNKNOWN
	} else {
		c.lastAPICheck = time.Now()
	}

	currentFailoverDestinationIP, err := c.getActiveIpFromJson(str)
	if err != nil {
		//TODO
		c.cachedState = UNKNOWN
	}

	if currentFailoverDestinationIP.Equal(getOutboundIP()) {
		//We "are" the current failover destination.
		c.cachedState = CONFIGURED
		return true
	} else {
		c.cachedState = RELEASED
	}

	return false
}

func (c *HetznerConfigurer) ConfigureAddress() bool {
	//log.Printf("Configuring address %s on %s", m.GetCIDR(), m.iface.Name)

	return c.runAddressConfiguration("set")
}

func (c *HetznerConfigurer) DeconfigureAddress() bool {
	//The adress doesn't need deconfiguring since Hetzner API
	// is used to point the VIP adress somewhere else.
	c.cachedState = RELEASED
	return true
}

func (c *HetznerConfigurer) runAddressConfiguration(action string) bool {
	str, err := c.curlQueryFailover(true)
	if err != nil {
		log.Printf("Error while configuring Hetzner failover-ip! errormessage: %s", err)
		c.cachedState = UNKNOWN
		return false
	}
	currentFailoverDestinationIP, err := c.getActiveIpFromJson(str)
	if err != nil {
		c.cachedState = UNKNOWN
		return false
	}

	c.lastAPICheck = time.Now()

	if currentFailoverDestinationIP.Equal(getOutboundIP()) {
		//We "are" the current failover destination.
		log.Printf("Failover was successfully executed!")
		c.cachedState = CONFIGURED
		return true
	} else {
		log.Printf("The failover command was issued, but the current Failover destination (%s) is different from what it should be (%s).",
		           currentFailoverDestinationIP.String(),
		           getOutboundIP().String())
		//Something must have gone wrong while trying to switch IP's...
		c.cachedState = UNKNOWN
		return false
	}

	return true
}

func (c *HetznerConfigurer) GetCIDR() string {
	return fmt.Sprintf("%s/%d", c.vip.String(), NetmaskSize(c.netmask))
}

func (c *HetznerConfigurer) cleanupArp() {
	// dummy function as the usage of interfaces requires us to have this function.
	// It is sufficient for the leader to tell Hetzner to switch the IP, no cleanup needed.
}
