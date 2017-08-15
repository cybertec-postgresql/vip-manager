package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	//"github.com/milosgajdos83/tenus"
)

var ip = flag.String("ip", "none", "Virtual IP address to configure")
var iface = flag.String("iface", "none", "Network interface to configure on")
var etcdkey = flag.String("key", "none", "Etcd key to monitor, e.g. /service/batman/leader")
var host = flag.String("host", "none", "Value to monitor for")
var endpoint = flag.String("endpoint", "http://localhost:2379", "Etcd endpoint")

func checkFlag(f *string, name string) {
	if *f == "none" || *f == "" {
		log.Fatalf("Setting %s is mandatory", name)
	}
}

func main() {
	flag.Parse()
	checkFlag(ip, "IP")
	checkFlag(iface, "network interface")
	checkFlag(etcdkey, "etcd key")
	checkFlag(host, "host name")

	states := make(chan bool)
	lc := NewEtcdLeaderChecker(*endpoint, *etcdkey, *host)

	vip := net.ParseIP(*ip)
	mask := vip.DefaultMask()

	manager := NewIPManager(&IPConfiguration{
		vip:     vip,
		netmask: mask,
		iface:   *iface,
	}, states)

	main_ctx, cancel := context.WithCancel(context.Background())

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
		lc.GetChangeNotificationStream(main_ctx, states)
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		manager.SyncStates(main_ctx, states)
		wg.Done()
	}()

	wg.Wait()
}
