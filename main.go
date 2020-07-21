package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"

	"gopkg.in/yaml.v2"

	"github.com/cybertec-postgresql/vip-manager/checker"
	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	//"github.com/milosgajdos83/tenus"
)

var configFile = flag.String("config", "", "Location of the configuration file.")
var versionHint = flag.Bool("version", false, "Show the version number.")

// deprecated flags below. add new parameters to the config struct and write them into vip-manager.yml
var ip = flag.String("ip", "none", "Virtual IP address to configure")
var mask = flag.Int("mask", -1, "The netmask used for the IP address. Defaults to -1 which assigns ipv4 default mask.")
var iface = flag.String("iface", "none", "Network interface to configure on")
var key = flag.String("key", "none", "key to monitor, e.g. /service/batman/leader")
var host = flag.String("host", "none", "Value to monitor for")
var etcdUser = flag.String("etcd_user", "none", "username that can be used to access the key in etcd")
var etcdPassword = flag.String("etcd_password", "none", "password for the etcd_user")

var endpointType = flag.String("type", "etcd", "type of endpoint used for key storage. Supported values: etcd, consul")
var endpoints = flag.String("endpoint", "http://localhost:2379[,http://host:port,..]", "endpoint")
var interval = flag.Int("interval", 1000, "DCS scan interval in milliseconds")

var hostingType = flag.String("hostingtype", "basic", "type of hosting. Supported values: self, hetzner")

var conf vipconfig.Config

func checkFlag(f string, name string) {
	if f == "none" || f == "" {
		log.Fatalf("Setting %s is mandatory", name)
	}
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

func main() {
	flag.Parse()

	if *versionHint {
		fmt.Println("version 0.6.1")
		return
	}

	// split "http[s]://localhost:2379[,http[s]://host:port,..]" into individual strings
	endpointArray := strings.Split(*endpoints, ",")

	//introduce parsed values into conf
	conf = vipconfig.Config{IP: *ip, Mask: *mask, Iface: *iface, HostingType: *hostingType,
		Key: *key, Nodename: *host, EndpointType: *endpointType, Endpoints: endpointArray,
		EtcdUser: *etcdUser, EtcdPassword: *etcdPassword, Interval: *interval}

	if *configFile != "" {
		yamlFile, err := ioutil.ReadFile(*configFile)
		if err != nil {
			log.Fatal("couldn't open config File!", err)
		}
		log.Printf("reading config from %s", *configFile)
		err = yaml.Unmarshal(yamlFile, &conf)
		if err != nil {
			log.Fatalf("Error while reading config file: %v", err)
		}
	} else {
		log.Printf("No config file specified, using arguments only.")
	}

	checkFlag(conf.IP, "IP")
	checkFlag(conf.Iface, "network interface")
	checkFlag(conf.Key, "key")

	if len(conf.Endpoints) == 0 {
		log.Print("No etcd/consul endpoints specified, trying to use localhost with standard ports!")
		switch conf.EndpointType {
		case "consul":
			conf.Endpoints[0] = "http://127.0.0.1:2379"
		case "etcd":
			conf.Endpoints[0] = "http://127.0.0.1:8500"
		}
	}

	if conf.Nodename == "" {
		nodename, err := os.Hostname()
		if err != nil {
			log.Fatalf("No nodename specified, hostname could not be retrieved: %s", err)
		} else {
			log.Printf("No nodename specified, instead using hostname: %v", nodename)
			conf.Nodename = nodename
		}
	}

	if conf.RetryNum == 0 {
		log.Println("Number of retries (retry_num) was not set or set to 0. It needs to be set to something more than 0 for vip-manager to work. Will set it to 3 by default.")
		conf.RetryNum = 3
		conf.RetryAfter = 250
	}

	states := make(chan bool)
	lc, err := checker.NewLeaderChecker(conf)
	if err != nil {
		log.Fatalf("Failed to initialize leader checker: %s", err)
	}

	vip := net.ParseIP(conf.IP)
	vipMask := getMask(vip, conf.Mask)
	netIface := getNetIface(conf.Iface)
	manager, err := NewIPManager(
		conf.Hosting_type,
		&IPConfiguration{
			vip:        vip,
			netmask:    vipMask,
			iface:      *netIface,
			RetryNum:   conf.RetryNum,
			RetryAfter: conf.RetryAfter,
		},
		states,
	)
	if err != nil {
		log.Fatalf("Problems with generating the virtual ip manager: %s", err)
	}

	mainCtx, cancel := context.WithCancel(context.Background())

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)

		<-c

		log.Printf("Received exit signal")
		cancel()
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		err := lc.GetChangeNotificationStream(mainCtx, states)
		if err != nil && err != context.Canceled {
			log.Fatalf("Leader checker returned the following error: %s", err)
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
