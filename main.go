package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/cybertec-postgresql/vip-manager/checker"
	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var configFile = flag.String("config", "", "Location of the configuration file.")
var versionHint = flag.Bool("version", false, "Show the version number.")

// deprecated flags below. add new parameters to the config struct and write them into vip-manager.yml
var ip = flag.String("ip", "none", "Virtual IP address to configure")
var mask = flag.Int("mask", -1, "The netmask used for the IP address. Defaults to -1 which assigns ipv4 default mask.")
var iface = flag.String("iface", "none", "Network interface to configure on")
var key = flag.String("key", "none", "key to monitor, e.g. /service/batman/leader")
var host = flag.String("host", "none", "Value to monitor for")
var etcd_user = flag.String("etcd_user", "none", "username that can be used to access the key in etcd")
var etcd_password = flag.String("etcd_password", "none", "password for the etcd_user")

var endpointType = flag.String("type", "etcd", "type of endpoint used for key storage. Supported values: etcd, consul")
var endpoint = flag.String("endpoint", "none", "DCS endpoint")
var interval = flag.Int("interval", 1000, "DCS scan interval in milliseconds")

var hostingType = flag.String("hostingtype", "basic", "type of hosting. Supported values: self, hetzner")

var conf vipconfig.Config

func checkFlag(f string, name string) bool {
	if f == "none" || f == "" {
		log.Printf("Setting %s is mandatory", name)
		return false
	}
	return true
}

func getMask(vip net.IP, mask int) net.IPMask {
	if mask > 0 || mask < 33 {
		return net.CIDRMask(mask, 32)
	}
	return vip.DefaultMask()
}

func getNetIface(iface string) *net.Interface {
	netIface, err := net.InterfaceByName(iface)
	if err != nil {
		log.Fatalf("Obtaining the interface raised an error: %s", err)
	}
	return netIface
}

func setDefaults() {
	defaults := map[string]string{
		"mask":         "-1",
		"endpointType": "etcd",
		"interval":     "1000",
		"hostingtype":  "basic",
		"retrynum":     "3",
		"retryafter":   "250",
	}

	for k, v := range defaults {
		viper.SetDefault(k, v)
	}
}

func setAlias() {
	aliases := map[string]string{
		// "name that we'll use": "legacy name, e.g. in flag",
		"hosting_type":  "hostingtype",
		"endpoint_type": "type",
		"interface":     "iface",
		"node_name":     "nodename",
	}

	for k, v := range aliases {
		viper.RegisterAlias(k, v)
	}
}

func checkMandatory() {
	mandatory := []string{
		"ip",
		"mask",
		"iface",
		"key",
		"nodename",
		"endpoints",
	}

	success := true
	for _, v := range mandatory {
		if v != "endpoints" {
			success = checkFlag(viper.GetString(v), v) && success
		} else {
			success = checkFlag(viper.GetStringSlice(v)[0], v) && success
		}
	}
	if success == false {
		log.Fatal("one or more mandatory settings were not set.")
	}
}

func populateConf() {

}

func main() {
	//put existing flags into pflags:
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()
	//import pflags into viper
	viper.BindPFlags(pflag.CommandLine)

	// make viper look for env variables that are prefixed VIP_...
	// viper.getString("IP") will thus check env variable VIP_IP
	viper.SetEnvPrefix("vip")
	viper.AutomaticEnv()

	// viper precedence order
	// - explicit call to Set
	// - flag
	// - env
	// - config
	// - key/value store
	// - default

	setDefaults()
	setAlias()

	// if a configfile has been passed, make viper read it
	if viper.IsSet("configfile") {
		viper.SetConfigFile(viper.GetString("configfile"))

		err := viper.ReadInConfig() // Find and read the config file
		if err != nil {             // Handle errors reading the config file
			panic(fmt.Errorf("Fatal error reading config file: %s \n", err))
		}
		fmt.Printf("Using config from file: %s\n", viper.ConfigFileUsed())
	}

	if *versionHint == true {
		fmt.Println("version 0.6.1")
		return
	}

	if viper.IsSet("endpoint") && !viper.IsSet("endpoints") {
		endpoint := viper.GetString("endpoint")
		var endpoints []string
		if strings.Contains(endpoint, ",") {
			endpoints = strings.Split(endpoint, ",")
		} else {
			endpoints[0] = endpoint
		}
		viper.Set("endpoints", endpoints)
	}

	if !viper.IsSet("endpoints") {
		log.Println("No etcd/consul endpoints specified, trying to use localhost with standard ports!")
		switch conf.Endpoint_type {
		case "consul":
			viper.Set("endpoints", []string{"http://127.0.0.1:8500"})
		case "etcd":
			viper.Set("endpoints", []string{"http://127.0.0.1:2379"})
		}
	}

	if len(viper.GetString("node_name")) == 0 {
		node_name, err := os.Hostname()
		if err != nil {
			log.Printf("No nodename specified, hostname could not be retrieved: %s\n", err)
		} else {
			log.Printf("No nodename specified, instead using hostname: %v\n", node_name)
			viper.Set("node_name", node_name)
		}
	}

	checkMandatory()

	conf = vipconfig.Config{
		Ip:            viper.GetString("ip"),
		Mask:          viper.GetInt("mask"),
		Iface:         viper.GetString("iface"),
		HostingType:   viper.GetString("hosting_type"),
		Key:           viper.GetString("key"),
		Nodename:      viper.GetString("node_name"),
		Endpoint_type: viper.GetString("endpoint_type"),
		Endpoints:     viper.GetStringSlice("endpoints"),
		Etcd_user:     viper.GetString("etcd_user"),
		Etcd_password: viper.GetString("etcd_password"),
		Consul_token:  viper.GetString("consul_token"),
		Interval:      viper.GetInt("interval"),
		Retry_after:   viper.GetInt("retry_after"),
		Retry_num:     viper.GetInt("retry_num"),
	}

	fmt.Printf("%+v\n", conf)

	b, err := json.MarshalIndent(conf, "", "  ")
	if err == nil {
		fmt.Println(string(b))
	}

	return

	states := make(chan bool)
	lc, err := checker.NewLeaderChecker(conf)
	if err != nil {
		log.Fatalf("Failed to initialize leader checker: %s\n", err)
	}

	vip := net.ParseIP(conf.Ip)
	vipMask := getMask(vip, conf.Mask)
	netIface := getNetIface(conf.Iface)
	manager, err := NewIPManager(
		*hostingType,
		&IPConfiguration{
			vip:         vip,
			netmask:     vipMask,
			iface:       *netIface,
			Retry_num:   conf.Retry_num,
			Retry_after: conf.Retry_after,
		},
		states,
	)
	if err != nil {
		log.Fatalf("Problems with generating the virtual ip manager: %s\n", err)
	}

	mainCtx, cancel := context.WithCancel(context.Background())

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)

		<-c

		log.Printf("Received exit signal\n")
		cancel()
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		err := lc.GetChangeNotificationStream(mainCtx, states)
		if err != nil && err != context.Canceled {
			log.Fatalf("Leader checker returned the following error: %s\n", err)
		}
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		manager.SyncStates(mainCtx, states)
		wg.Done()
	}()

	wg.Wait()
}
