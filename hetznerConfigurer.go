package main

import(
	"bufio"
	"log"
	"net"
	"os/exec"
	"os"
	"errors"
	"encoding/json"
)

type HetznerConfigurer struct {
	*IPConfiguration
}

func NewHetznerConfigurer(config *IPConfiguration) (*HetznerConfigurer, error){
	c := &HetznerConfigurer{IPConfiguration : config}

	return c, nil
}

func getOutboundIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Println("error dialing 8.8.8.8 to retrieve preferred outbound IP", err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP
}

func (c *HetznerConfigurer) curlQueryFailover(post bool) (string, error) {
	//TODO: make credentialsFile dynamically changeable?
	credentialsFile  := "/etc/hetzner"
	f, err := os.Open(credentialsFile)
	if err != nil {
		log.Println("can't open passwordfile", err)
		return "", err
	}
	defer f.Close()

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

	var cmd *exec.Cmd
	if post == true {
		my_own_ip := getOutboundIP()
		if my_own_ip == nil {
			log.Printf("Error determining this machine's IP address.")
			return "", errors.New("Error determining this machine's IP address")
		}
		log.Printf("my_own_ip: %s\n", my_own_ip.String())

		cmd = exec.Command("curl", "--ipv4", "-u", user+":"+password, "https://robot-ws.your-server.de/failover/"+c.vip.String(), "-d", "active_server_ip="+my_own_ip.String())
	} else {
		cmd = exec.Command("curl", "--ipv4", "-u", user+":"+password, "https://robot-ws.your-server.de/failover/"+c.vip.String())
	}

	out, err := cmd.Output()

	if err != nil {
		return "", err
	}

	retStr := string(out[:])

	return retStr, nil
}

func getActiveIpFromJson(str string) (net.IP, error){
	var f map[string]interface{}

	err := json.Unmarshal([]byte(str), &f)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	if f["error"] != nil {
		errormap := f["error"].(map[string]interface{})

		log.Printf("There was an error accessing the Hetzner API!\n status: %s\n code: %s\n message: %s\n", errormap["status"].(float64),	errormap["code"].(string), errormap["message"].(string))
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

	str, err:= c.curlQueryFailover(false)
	if err != nil {
		//TODO
	}

	currentFailoverDestinationIP, err:=getActiveIpFromJson(str)
	if err != nil {
		//TODO
	}
	if currentFailoverDestinationIP.Equal(getOutboundIP()) {
		//We "are" the current failover destination.
		return true;
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
	return true
}

func (c *HetznerConfigurer) runAddressConfiguration(action string) bool {
	str, err:= c.curlQueryFailover(true)
	if err != nil {
		log.Printf("Error while configuring Hetzner failover-ip! errormessage: %s", err)
		return false
	}
	currentFailoverDestinationIP, err:=getActiveIpFromJson(str)
	if err != nil {
		return false
	}
	if currentFailoverDestinationIP.Equal(getOutboundIP()) {
		//We "are" the current failover destination.
		log.Printf("Failover was successfully executed!")
		return true;
	}else{
		log.Printf("The failover command was issued, but the current Failover destination (%s) is different from what it should be (%s).", currentFailoverDestinationIP.String(), getOutboundIP().String)
		//Something must have gone wrong while trying to switch IP's...
		return false;
	}

	return true
}

func (c *HetznerConfigurer) GetCIDR() string {
	return c.GetCIDR()
}

func (c *HetznerConfigurer) cleanupArp() {

}