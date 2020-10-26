package ipmanager

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
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
	config       *vipconfig.Config
	cachedState  int
	lastAPICheck time.Time
}

func newHetznerFloatingIPConfigurer(config *vipconfig.Config) (*HetznerFloatingIPConfigurer, error) {
	c := &HetznerFloatingIPConfigurer{
		config:       config,
		cachedState:  unknown,
		lastAPICheck: time.Unix(0, 0),
	}
	return c, nil
}

/**
 * In order to tell the Hetzner API to route the floating-ip to
 * this machine, we must attach our own server ID to the API request.
 */

func (c *HetznerFloatingIPConfigurer) curlQueryFloatingIP(post bool) (string, error) {
	//TODO: add appropriate config validators in config.go
	// if token == "" || ip_id == "" || server_id == "" {
	// 	log.Println("Couldn't retrieve API token, IP ID or server ID from file", credentialsFile)
	// 	return "", errors.New("Couldn't retrieve API token, IP ID or server ID from file")
	// }

	// c.serverID, err = strconv.ParseInt(server_id, 10, 64)
	// if err != nil {
	// 	log.Println("Couldn't convert server id (serv) to number from file", credentialsFile)
	// 	return "", errors.New("Couldn't convert server id to number from file")
	// }

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
	var req *http.Request
	var err error

	if post {
		log.Printf("my_own_id: %d\n", c.config.HetznerCloudServerID)

		body := "{\"server\":" + string(c.config.HetznerCloudServerID) + "}"

		req, err = http.NewRequest("POST",
			"https://api.hetzner.cloud/v1/floating_ips/"+c.config.IP+"/actions/assign",
			strings.NewReader(body))

		// cmd = exec.Command("curl",
		// 	"--ipv4",
		// 	"-X", "POST",
		// 	"-H", "Content-Type: application/json",
		// 	"-H", "Authorization: Bearer "+token,
		// 	"-d", "{\"server\": "+server_id+"}",
		// 	"https://api.hetzner.cloud/v1/floating_ips/"+ip_id+"/actions/assign")

		// if c.config.Verbose {
		// 	log.Printf("%s %s %s %s %s %s %s %s %s %s",
		// 		"curl",
		// 		"--ipv4",
		// 		"-X", "POST",
		// 		"-H", "'Content-Type: application/json'",
		// 		"-H", "'Authorization: Bearer "+token[:8]+"...'",
		// 		"-d", "{\"server\": "+server_id+"}'",
		// 		"https://api.hetzner.cloud/v1/floating_ips/"+ip_id+"/actions/assign")
		// }
	} else {
		req, err = http.NewRequest("GET",
			"https://api.hetzner.cloud/v1/floating_ips/"+c.config.IP,
			nil)

		// cmd = exec.Command("curl",
		// 	"--ipv4",
		// 	"-H", "Authorization: Bearer "+token,
		// 	"https://api.hetzner.cloud/v1/floating_ips/"+ip_id)

		// if c.config.Verbose {
		// 	log.Printf("%s %s %s %s %s",
		// 		"curl",
		// 		"--ipv4",
		// 		"-H", "'Authorization: Bearer "+token[:8]+"...'",
		// 		"https://api.hetzner.cloud/v1/floating_ips/"+ip_id)
		// }
	}

	if err != nil {
		log.Printf("Failed to create a HTTP request to retrieve the current target for the failover IP: %e", err)
		return "", err
	}

	req.Header.Add("Authorization", "Bearer "+c.config.HetznerCloudToken)
	req.Header.Add("Content-Type", "application/json")

	client := &http.Client{
		Timeout: time.Second * 5,
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to send a HTTP request to retrieve the current target for the failover IP: %e", err)
		return "", err
	}

	defer resp.Body.Close()
	bytes, err := ioutil.ReadAll(resp.Body)

	if err != nil {
		return "", err
	}

	retStr := string(bytes)

	return retStr, nil
}

/**
 * This function is used to parse the response which comes from the
 * curlQueryFloatingIP function and in turn from the curl calls to the API.
 */
func (c *HetznerFloatingIPConfigurer) getActiveServerIDFromJSON(str string) (int, error) {
	var f map[string]interface{}

	if c.config.Verbose {
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
		return 0, errors.New("hetzner API returned error response")
	}

	if f["floating_ip"] != nil {
		//TODO: maybe find a source for the int size of hetzner server ids?
		var serverNumber int

		floatingIPMap := f["floating_ip"].(map[string]interface{})

		ip := floatingIPMap["ip"].(string)
		if floatingIPMap["server"] != nil {
			serverNumber = floatingIPMap["server"].(int)
		} else {
			return 0, errors.New("VIP is not assigned yet")
		}

		log.Println("Result of the failover query was: ",
			"failover-ip=", ip,
			"server_number=", serverNumber,
		)

		return serverNumber, nil
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
		 * if it is set to UNKNOWN, a check will be done.
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

	currentFailoverDestinationServerID, err := c.getActiveServerIDFromJSON(str)
	if err != nil {
		//TODO
		c.cachedState = unknown
	}

	if c.config.HetznerCloudServerID != 0 &&
		currentFailoverDestinationServerID == c.config.HetznerCloudServerID {
		//We "are" the current failover destination.
		c.cachedState = configured
		return true
	}
	c.cachedState = released

	return false
}

func (c *HetznerFloatingIPConfigurer) configureAddress() bool {
	//log.Printf("Configuring address %s on %s", m.GetCIDR(), m.iface.Name)

	return c.runAddressConfiguration("set")
}

func (c *HetznerFloatingIPConfigurer) deconfigureAddress() bool {
	//The address doesn't need deconfiguring since Hetzner API
	// is used to point the VIP address somewhere else.
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
	currentFailoverDestinationServerID, err := c.getActiveServerIDFromJSON(str)
	if err != nil {
		c.cachedState = unknown
		return false
	}

	c.lastAPICheck = time.Now()

	if currentFailoverDestinationServerID != 0 &&
		currentFailoverDestinationServerID == c.config.HetznerCloudServerID {
		//We "are" the current failover destination.
		log.Printf("Failover was successfully executed!")
		c.cachedState = configured
		return true
	}

	log.Printf("The failover command was issued, but the current Failover destination (%d) is different from what it should be (%d).",
		currentFailoverDestinationServerID,
		c.config.HetznerCloudServerID)
	//Something must have gone wrong while trying to switch IP's...
	c.cachedState = unknown
	return false
}

func (c *HetznerFloatingIPConfigurer) cleanupArp() {
	// dummy function as the usage of interfaces requires us to have this function.
	// It is sufficient for the leader to tell Hetzner to switch the IP, no cleanup needed.
}
