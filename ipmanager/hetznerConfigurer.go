package ipmanager

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"
)

// hetznerAPI is the base URL of the Hetzner Robot API, a field of the
// configurer so that the tests can point it at a local server
const hetznerAPI = "https://robot-ws.your-server.de"

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
	*IPConfiguration
	cachedState     int
	lastAPICheck    time.Time
	verbose         bool
	credentialsFile string
	apiURL          string
	client          *http.Client
	getOutboundIP   func() (net.IP, error)
}

func newHetznerConfigurer(config *IPConfiguration, verbose bool) (*HetznerConfigurer, error) {
	c := &HetznerConfigurer{
		IPConfiguration: config,
		cachedState:     unknown,
		lastAPICheck:    time.Unix(0, 0),
		verbose:         verbose,
		credentialsFile: "/etc/hetzner",
		apiURL:          hetznerAPI,
		client:          newHetznerClient(),
		getOutboundIP:   getOutboundIP,
	}
	return c, nil
}

// newHetznerClient returns a client that talks IPv4 only, which is all the
// Hetzner Robot API listens on. Forcing that was the reason this code used to
// shell out to "curl --ipv4", which put the API password into the command line
// of a process, and /proc/<pid>/cmdline is readable by every local user.
func newHetznerClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				if network == "tcp" || network == "tcp6" {
					network = "tcp4"
				}
				return dialer.DialContext(ctx, network, address)
			},
		},
	}
}

/**
 * In order to tell the Hetzner API to route the failover-ip to
 * this machine, we must attach our own IP address to the API request.
 */
func getOutboundIP() (net.IP, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil || conn == nil {
		return nil, fmt.Errorf("error dialing 8.8.8.8 to retrieve preferred outbound IP: %w", err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP, nil
}

// readCredentials reads the username and the password for the Robot API from
// the credentials file, which looks like
//
//	user="myUsername"
//	pass="myPassword"
//
// Spaces around the "=" and missing quotes are accepted as well. The values
// used to be cut out of the line by offset, which silently dropped the last
// character of an unquoted value.
func (c *HetznerConfigurer) readCredentials() (user string, password string, err error) {
	f, err := os.Open(c.credentialsFile)
	if err != nil {
		log.Error("can't open passwordfile", err)
		return "", "", err
	}
	defer f.Close()

	// the file holds the password of an account that can reroute the failover
	// IP, so complain when it is readable by more than its owner
	if info, statErr := f.Stat(); statErr == nil && runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode&0o077 != 0 {
			log.Warnf("Credentials file %s is accessible by group or others (mode %#o), consider \"chmod 600 %s\"",
				c.credentialsFile, mode, c.credentialsFile)
		}
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), "=")
		if !found {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "user", "username":
			user = value
		case "pass", "password":
			password = value
		}
	}
	if err := scanner.Err(); err != nil {
		log.Error("error reading credentials file", err)
		return "", "", fmt.Errorf("error reading credentials file: %w", err)
	}
	if user == "" || password == "" {
		log.Infoln("Couldn't retrieve username or password from file", c.credentialsFile)
		return "", "", errors.New("couldn't retrieve username or password from file")
	}
	return user, password, nil
}

// queryFailover asks the Robot API about the failover IP. If post is set, the
// failover IP is rerouted to this machine, otherwise the current route is
// returned unchanged.
func (c *HetznerConfigurer) queryFailover(post bool) (string, error) {
	user, password, err := c.readCredentials()
	if err != nil {
		return "", err
	}

	endpoint := c.apiURL + "/failover/" + c.VIP.String()
	var req *http.Request
	if post {
		myOwnIP, err := c.getOutboundIP()
		if err != nil {
			log.Error("Error determining this machine's IP address.", err)
			return "", fmt.Errorf("error determining this machine's IP address: %w", err)
		}
		log.Infof("my_own_ip: %s", myOwnIP.String())

		form := url.Values{"active_server_ip": {myOwnIP.String()}}
		req, err = http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return "", fmt.Errorf("cannot build the request to %s: %w", endpoint, err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		log.Debugf("POST %s active_server_ip=%s", endpoint, myOwnIP.String())
	} else {
		req, err = http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			return "", fmt.Errorf("cannot build the request to %s: %w", endpoint, err)
		}
		log.Debugf("GET %s", endpoint)
	}
	// the credentials travel in the Authorization header, never in the
	// command line or the URL
	req.SetBasicAuth(user, password)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request to the Hetzner API failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("cannot read the answer of the Hetzner API: %w", err)
	}
	// the API describes its errors in the body, which getActiveIPFromJSON
	// reports, so an unexpected status code is only interesting without one
	if resp.StatusCode >= 300 && len(body) == 0 {
		return "", fmt.Errorf("the Hetzner API answered with status %s", resp.Status)
	}

	return string(body), nil
}

/**
 * This function is used to parse the response which comes from the
 * curlQueryFailover function and in turn from the curl calls to the API.
 */
func (c *HetznerConfigurer) getActiveIPFromJSON(str string) (net.IP, error) {
	var f map[string]interface{}

	log.Debugf("JSON response: %s\n", str)

	err := json.Unmarshal([]byte(str), &f)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}

	if f["error"] != nil {
		errormap := f["error"].(map[string]interface{})

		log.Errorf("There was an error accessing the Hetzner API!\n"+
			" status: %f\n code: %s\n message: %s\n",
			errormap["status"].(float64),
			errormap["code"].(string),
			errormap["message"].(string))
		return nil, errors.New("error response from Hetzner API returned")
	}

	if f["failover"] != nil {
		failovermap := f["failover"].(map[string]interface{})

		ip := failovermap["ip"].(string)
		netmask := failovermap["netmask"].(string)
		serverIP := failovermap["server_ip"].(string)
		serverNumber := failovermap["server_number"].(float64)
		activeServerIP := failovermap["active_server_ip"].(string)

		log.Infoln("Result of the failover query was: ",
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
		log.Info("Cached state was too old.")
		c.cachedState = unknown
	} else {
		/** no need to check, we can use "cached" state if set.
		 * if it is set to UNKNOWN, a check will be done.
		 */
		switch c.cachedState {
		case configured:
			return true
		case released:
			return false
		}
	}

	str, err := c.queryFailover(false)
	if err != nil {
		c.cachedState = unknown
		return false
	}
	c.lastAPICheck = time.Now()

	currentFailoverDestinationIP, err := c.getActiveIPFromJSON(str)
	if err != nil {
		c.cachedState = unknown
		return false
	}

	myOwnIP, err := c.getOutboundIP()
	if err != nil {
		log.Error("Error determining this machine's IP address.", err)
		c.cachedState = unknown
		return false
	}

	if currentFailoverDestinationIP.Equal(myOwnIP) {
		//We "are" the current failover destination.
		c.cachedState = configured
		return true
	}

	c.cachedState = released
	return false
}

func (c *HetznerConfigurer) configureAddress() bool {
	//log.Printf("Configuring address %s on %s", m.GetCIDR(), m.iface.Name)

	return c.runAddressConfiguration()
}

func (c *HetznerConfigurer) deconfigureAddress() bool {
	//The address doesn't need deconfiguring since Hetzner API
	// is used to point the VIP address somewhere else.
	c.cachedState = released
	return true
}

func (c *HetznerConfigurer) runAddressConfiguration() bool {
	str, err := c.queryFailover(true)
	if err != nil {
		log.Infof("Error while configuring Hetzner failover-ip! Error message: %s", err)
		c.cachedState = unknown
		return false
	}
	currentFailoverDestinationIP, err := c.getActiveIPFromJSON(str)
	if err != nil {
		c.cachedState = unknown
		return false
	}

	c.lastAPICheck = time.Now()

	myOwnIP, err := c.getOutboundIP()
	if err != nil {
		log.Error("Error determining this machine's IP address.", err)
		c.cachedState = unknown
		return false
	}

	if currentFailoverDestinationIP.Equal(myOwnIP) {
		//We "are" the current failover destination.
		log.Info("Failover was successfully executed!")
		c.cachedState = configured
		return true
	}

	log.Infof("The failover command was issued, but the current Failover destination (%s) is different from what it should be (%s).",
		currentFailoverDestinationIP.String(),
		myOwnIP.String())
	//Something must have gone wrong while trying to switch IP's...
	c.cachedState = unknown
	return false
}
