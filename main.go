package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"

	"github.com/cybertec-postgresql/vip-manager/checker"
	//"github.com/milosgajdos83/tenus"
)

var ip = flag.String("ip", "none", "Virtual IP address to configure")
var mask = flag.Int("mask", -1, "The netmask used for the IP address. Defaults to -1 which assigns ipv4 default mask.")
var iface = flag.String("iface", "none", "Network interface to configure on")
var key = flag.String("key", "none", "key to monitor, e.g. /service/batman/leader")
var host = flag.String("host", "none", "Value to monitor for")
var endpointType = flag.String("type", "etcd", "type of endpoint used for key storage. Supported values: etcd, consul")
var endpoint = flag.String("endpoint", "http://localhost:2379[,http://host:port,..]", "endpoint")
var interval = flag.Int("interval", 1000, "DCS scan interval in milliseconds")

func checkFlag(f *string, name string) {
	if *f == "none" || *f == "" {
		log.Fatalf("Setting %s is mandatory", name)
	}
}

func getMask(vip net.IP, mask *int) net.IPMask {
	if *mask > 0 || *mask < 33 {
		return net.CIDRMask(*mask, 32)
	}
	return vip.DefaultMask()
}

func getNetIface(iface *string) *net.Interface {
	netIface, err := net.InterfaceByName(*iface)
	if err != nil {
		log.Fatalf("Obtaining the interface raised an error: %s", err)
	}
	return netIface
}

func main() {
	flag.Parse()
	checkFlag(ip, "IP")
	checkFlag(iface, "network interface")
	checkFlag(key, "key")
	checkFlag(host, "host name")

	states := make(chan bool)
	lc, err := checker.NewLeaderChecker(*endpointType, *endpoint, *key, *host)
	if err != nil {
		log.Fatalf("Failed to initialize leader checker: %s", err)
	}

	vip := net.ParseIP(*ip)
	vipMask := getMask(vip, mask)
	netIface := getNetIface(iface)
	manager, err := NewIPManager(
		&IPConfiguration{
			vip:     vip,
			netmask: vipMask,
			iface:   *netIface,
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
		err := lc.GetChangeNotificationStream(mainCtx, states, *interval)
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
