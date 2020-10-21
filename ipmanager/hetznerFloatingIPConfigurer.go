package ipmanager

import (
	"bufio"
	"encoding/json"
	"errors"
	"log"
	"os"
	"os/exec"
	"time"
	"strconv"
)

/**
 * The HetznerFloatingIPConfigurer can be used to enable vip-management on
 * cloud nodes rented in a Hetzner Datacenter.
 * Since Hetzner provides an API that handles floating-ip routing,
 * this API is used to manage the vip, whenever hostintype `hetzner_floating_ip`
 * is set.
 *
 * Hetzner Floating IP documentation: https://docs.hetzner.cloud/#floating-ips
 */

/* The constants
 * - unknown
 * - configured
 * - released
 * are defined in hetznerConfigurer.go
 */

type HetznerFloatingIPConfigurer struct {
	*IPConfiguration
	cachedState  int
	serverID     int64
	lastAPICheck time.Time
	verbose      bool
}

func newHetznerFloatingIPConfigurer(config *IPConfiguration, verbose bool) (*HetznerFloatingIPConfigurer, error) {
	c := &HetznerFloatingIPConfigurer{
		IPConfiguration: config,
		cachedState:     unknown,
		serverID:        0,
		lastAPICheck:    time.Unix(0, 0),
		verbose:         verbose}

	return c, nil
}

/**
 * In order to tell the Hetzner API to route the floating-ip to
 * this machine, we must attach our own server ID to the API request.
 */

func (c *HetznerFloatingIPConfigurer) curlQueryFloatingIP(post bool) (string, error) {
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
	var token string
	var ip_id string
	var server_id string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		switch line[:4] {
		case "tokn":
			token     = line[6 : len(line)-1]
		case "serv":
			server_id = line[6 : len(line)-1]
		case "ipid":
			ip_id     = line[6 : len(line)-1]
		}
	}
	if token == "" || ip_id == "" || server_id == "" {
		log.Println("Couldn't retrieve API token, IP ID or server ID from file", credentialsFile)
		return "", errors.New("Couldn't retrieve API token, IP ID or server ID from file")
	}

	c.serverID, err = strconv.ParseInt(server_id, 10, 64)
	if err != nil {
		log.Println("Couldn't convert server id (serv) to number from file", credentialsFile)
		return "", errors.New("Couldn't convert server id to number from file")
	}

	/**
	 * The hetznerFloatingIPConfigurer was copy/paste/modify adopted from the
	 * hetznerConfigurer. hetznerConfigurer claims that the Hetzner API only
	 * allows IPv4 connections, and therefore curl is being used instead of
	 * instead of GO's own http package. I did not verify this for the
	 * Hetzner Cloud/FloatingIP API so we're also using curl here.
	 *
	 * If post is set to true, a failover will be triggered.
	 * If it is set to false, the current state (i.e. route)
	 * for the floating-ip will be retrieved.
	 */
	var cmd *exec.Cmd
	if post {
		log.Printf("my_own_id: %s\n", c.serverID)

		cmd = exec.Command("curl",
		                   "--ipv4",
		                   "-X", "POST",
		                   "-H", "Content-Type: application/json",
		                   "-H", "Authorization: Bearer "+token,
		                   "-d", "{\"server\": "+server_id+"}",
		                   "https://api.hetzner.cloud/v1/floating_ips/"+ip_id+"/actions/assign")

		if c.verbose {
			log.Printf("%s %s %s %s %s %s %s %s %s %s",
			           "curl",
			           "--ipv4",
			           "-X", "POST",
			           "-H", "'Content-Type: application/json'",
			           "-H", "'Authorization: Bearer "+token[:8]+"...'",
			           "-d", "{\"server\": "+server_id+"}'",
			           "https://api.hetzner.cloud/v1/floating_ips/"+ip_id+"/actions/assign")
		}
	} else {
		cmd = exec.Command("curl",
		                   "--ipv4",
		                   "-H", "Authorization: Bearer "+token,
		                   "https://api.hetzner.cloud/v1/floating_ips/"+ip_id)

		if c.verbose {
			log.Printf("%s %s %s %s %s",
			           "curl",
			           "--ipv4",
			           "-H", "'Authorization: Bearer "+token[:8]+"...'",
			           "https://api.hetzner.cloud/v1/floating_ips/"+ip_id)
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
 * curlQueryFloatingIP function and in turn from the curl calls to the API.
 */
func (c *HetznerFloatingIPConfigurer) getActiveServerIDFromJson(str string) (int64, error) {
	var f map[string]interface{}

	if c.verbose {
		log.Printf("JSON response: %s\n", str)
	}

	err := json.Unmarshal([]byte(str), &f)
	if err != nil {
		log.Println(err)
		return 0, err
	}

	if f["error"] != nil {
		/* just print the original JSON error */
		log.Printf("There was an error accessing the Hetzner API!\n %s\n", str)
		return 0, errors.New("Hetzner API returned error response.")
	}

	if f["floating_ip"] != nil {
		var server_number int64

		floating_ip_map := f["floating_ip"].(map[string]interface{})

		ip := floating_ip_map["ip"].(string)
		if floating_ip_map["server"] != nil {
			server_number = int64(floating_ip_map["server"].(float64))
		} else {
			return 0, errors.New("VIP is not assigned yet")
		}

		log.Println("Result of the failover query was: ",
			"failover-ip=",   ip,
			"server_number=", server_number,
		)

		return server_number, nil
	}

	return 0, errors.New("why did we end up here?")
}

/* checks if this vip-manager instance owns the VIP */
func (c *HetznerFloatingIPConfigurer) queryAddress() bool {
	if (time.Since(c.lastAPICheck) / time.Hour) > 1 {
		/**We need to recheck the status!
		 * Don't check too often because of stupid API rate limits
		 */
		log.Println("Cached state was too old.")
		c.cachedState = unknown
	} else {
		/** no need to check, we can use "cached" state if set.
		 * if it is set to UNKOWN, a check will be done.
		 */
		if c.cachedState == configured {
			return true
		} else if c.cachedState == released {
			return false
		}
	}

	str, err := c.curlQueryFloatingIP(false)
	if err != nil {
		//TODO
		c.cachedState = unknown
	} else {
		c.lastAPICheck = time.Now()
	}

	currentFailoverDestinationServerID, err := c.getActiveServerIDFromJson(str)
	if err != nil {
		//TODO
		c.cachedState = unknown
	}

	if c.serverID != 0 &&
	   currentFailoverDestinationServerID == c.serverID {
		//We "are" the current failover destination.
		c.cachedState = configured
		return true
	} else {
		c.cachedState = released
	}

	return false
}

func (c *HetznerFloatingIPConfigurer) configureAddress() bool {
	//log.Printf("Configuring address %s on %s", m.GetCIDR(), m.iface.Name)

	return c.runAddressConfiguration("set")
}

func (c *HetznerFloatingIPConfigurer) deconfigureAddress() bool {
	//The adress doesn't need deconfiguring since Hetzner API
	// is used to point the VIP adress somewhere else.
	c.cachedState = released
	return true
}

func (c *HetznerFloatingIPConfigurer) runAddressConfiguration(action string) bool {
	str, err := c.curlQueryFloatingIP(true)
	if err != nil {
		log.Printf("Error while configuring Hetzner floating-ip! Error message: %s", err)
		c.cachedState = unknown
		return false
	}
	currentFailoverDestinationServerID, err := c.getActiveServerIDFromJson(str)
	if err != nil {
		c.cachedState = unknown
		return false
	}

	c.lastAPICheck = time.Now()

	if currentFailoverDestinationServerID != 0 &&
	   currentFailoverDestinationServerID == c.serverID {
		//We "are" the current failover destination.
		log.Printf("Failover was successfully executed!")
		c.cachedState = configured
		return true
	}

	log.Printf("The failover command was issued, but the current Failover destination (%d) is different from what it should be (%d).",
	           currentFailoverDestinationServerID,
		   c.serverID)
	//Something must have gone wrong while trying to switch IP's...
	c.cachedState = unknown
	return false
}

func (c *HetznerFloatingIPConfigurer) cleanupArp() {
	// dummy function as the usage of interfaces requires us to have this function.
	// It is sufficient for the leader to tell Hetzner to switch the IP, no cleanup needed.
}

