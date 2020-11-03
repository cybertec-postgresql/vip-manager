package ipmanager

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
)

// type to track cached state
const (
	unknown    = iota // c0 == 0
	configured = iota // c1 == 1
	released   = iota // c2 == 2
)

// The HetznerConfigurer can be used to enable vip-management on nodes
// rented in a Hetzner Datacenter.
// Since Hetzner provides an API that handles failover-ip routing,
// this API is used to manage the vip, whenever hostintype `hetzner` is set.
type HetznerConfigurer struct {
	config       *vipconfig.Config
	cachedState  int
	lastAPICheck time.Time
	verbose      bool
}

func newHetznerConfigurer(config *vipconfig.Config) (*HetznerConfigurer, error) {
	c := &HetznerConfigurer{
		config:       config,
		cachedState:  unknown,
		lastAPICheck: time.Unix(0, 0),
	}

	return c, nil
}

//TODO: obsolete this and instead make the "outbound ip" configurable, maybe with a default set in vipconfig/config.go ?
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

func (c *HetznerConfigurer) callFailoverAPI(post bool) (string, error) {

	if c.config.HetznerRobotUsername == "" || c.config.HetznerRobotPassword == "" {
		//TODO: move to config validation block in config.go
		return "", nil
	}

	/**
	 * If post is set to true, a failover will be triggered.
	 * If it is set to false, the current state (i.e. route)
	 * for the failover-ip will be retrieved.
	 */
	var req *http.Request
	var err error

	if post {
		myOwnIP := getOutboundIP()
		if myOwnIP == nil {
			log.Printf("Error determining this machine's IP address.")
			return "", errors.New("Error determining this machine's IP address")
		}
		log.Printf("my_own_ip: %s\n", myOwnIP.String())

		body := "\"active_server_ip\"=" + myOwnIP.String()

		req, err = http.NewRequest("POST",
			"https://robot-ws.your-server.de/failover/"+c.config.IP,
			strings.NewReader(body))

		// cmd = exec.Command("curl",
		// 	"--ipv4",
		// 	"-u", user+":"+password,
		// 	"https://robot-ws.your-server.de/failover/"+c.IPConfiguration.VIP.String(),
		// 	"-d", "active_server_ip="+myOwnIP.String())

		// if c.verbose {
		// 	log.Printf("%s %s %s '%s' %s %s %s",
		// 		"curl",
		// 		"--ipv4",
		// 		"-u", user+":XXXXXX",
		// 		"https://robot-ws.your-server.de/failover/"+c.IPConfiguration.VIP.String(),
		// 		"-d", "active_server_ip="+myOwnIP.String())
		// }
	} else {
		req, err = http.NewRequest("GET",
			"https://robot-ws.your-server.de/failover/"+c.config.IP,
			nil)

		// cmd = exec.Command("curl",
		// 	"--ipv4",
		// 	"-u", user+":"+password,
		// 	"https://robot-ws.your-server.de/failover/"+c.IPConfiguration.VIP.String())

		// if c.verbose {
		// 	log.Printf("%s %s %s %s %s",
		// 		"curl",
		// 		"--ipv4",
		// 		"-u", user+":XXXXXX",
		// 		"https://robot-ws.your-server.de/failover/"+c.IPConfiguration.VIP.String())
		// }
	}

	if err != nil {
		log.Printf("Failed to create a HTTP request to retrieve the current target for the failover IP: %e", err)
		return "", err
	}

	req.SetBasicAuth(c.config.HetznerRobotUsername, c.config.HetznerRobotPassword)

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
 * callFailoverAPI function and in turn from the calls to the API.
 */
func (c *HetznerConfigurer) getActiveIPFromJSON(str string) (net.IP, error) {
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

		log.Printf("There was an error accessing the Hetzner API!\n"+
			" status: %f\n code: %s\n message: %s\n",
			errormap["status"].(float64),
			errormap["code"].(string),
			errormap["message"].(string))
		return nil, errors.New("Hetzner API returned error response")
	}

	if f["failover"] != nil {
		failovermap := f["failover"].(map[string]interface{})

		ip := failovermap["ip"].(string)
		netmask := failovermap["netmask"].(string)
		serverIP := failovermap["server_ip"].(string)
		serverNumber := failovermap["server_number"].(float64)
		activeServerIP := failovermap["active_server_ip"].(string)

		log.Println("Result of the failover query was: ",
			"failover-ip=", ip,
			"netmask=", netmask,
			"server_ip=", serverIP,
			"server_number=", serverNumber,
			"active_server_ip=", activeServerIP,
		)

		return net.ParseIP(activeServerIP), nil

	}

	return nil, errors.New("why did we end up here?")
}

func (c *HetznerConfigurer) queryAddress() bool {
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

	str, err := c.callFailoverAPI(false)
	if err != nil {
		//TODO
		c.cachedState = unknown
	} else {
		c.lastAPICheck = time.Now()
	}

	currentFailoverDestinationIP, err := c.getActiveIPFromJSON(str)
	if err != nil {
		//TODO
		c.cachedState = unknown
	}

	if currentFailoverDestinationIP.Equal(getOutboundIP()) {
		//We "are" the current failover destination.
		c.cachedState = configured
		return true
	}

	c.cachedState = released
	return false
}

func (c *HetznerConfigurer) configureAddress() bool {
	//log.Printf("Configuring address %s on %s", m.GetCIDR(), m.iface.Name)

	return c.runAddressConfiguration("set")
}

func (c *HetznerConfigurer) deconfigureAddress() bool {
	//The address doesn't need deconfiguring since Hetzner API
	// is used to point the VIP address somewhere else.
	c.cachedState = released
	return true
}

func (c *HetznerConfigurer) runAddressConfiguration(action string) bool {
	str, err := c.callFailoverAPI(true)
	if err != nil {
		log.Printf("Error while configuring Hetzner failover-ip! Error message: %s", err)
		c.cachedState = unknown
		return false
	}
	currentFailoverDestinationIP, err := c.getActiveIPFromJSON(str)
	if err != nil {
		c.cachedState = unknown
		return false
	}

	c.lastAPICheck = time.Now()

	if currentFailoverDestinationIP.Equal(getOutboundIP()) {
		//We "are" the current failover destination.
		log.Printf("Failover was successfully executed!")
		c.cachedState = configured
		return true
	}

	log.Printf("The failover command was issued, but the current Failover destination (%s) is different from what it should be (%s).",
		currentFailoverDestinationIP.String(),
		getOutboundIP().String())
	//Something must have gone wrong while trying to switch IP's...
	c.cachedState = unknown
	return false
}

func (c *HetznerConfigurer) cleanupArp() {
	// dummy function as the usage of interfaces requires us to have this function.
	// It is sufficient for the leader to tell Hetzner to switch the IP, no cleanup needed.
}
