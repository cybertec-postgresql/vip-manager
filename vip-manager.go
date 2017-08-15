package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coreos/etcd/client"
	//"github.com/milosgajdos83/tenus"
)

type IPConfiguration struct {
	vip     net.IP
	netmask net.IPMask
	iface   string
}

func (c *IPConfiguration) GetCIDR() string {
	return fmt.Sprintf("%s/%d", c.vip.String(), NetmaskSize(c.netmask))
}

type IPManager struct {
	*IPConfiguration

	states        <-chan bool
	current_state bool
	state_lock    sync.Mutex
	recheck       *sync.Cond
}

func NewIPManager(config *IPConfiguration, states <-chan bool) *IPManager {
	m := &IPManager{
		IPConfiguration: config,
		states:          states,
		current_state:   false,
	}

	m.recheck = sync.NewCond(&m.state_lock)

	return m
}

func (m *IPManager) applyLoop(ctx context.Context) {
	for {
		actual_state := m.QueryAddress()
		m.state_lock.Lock()
		desired_state := m.current_state
		log.Printf("IP address %s state is %t, desired %t", m.GetCIDR(), actual_state, desired_state)
		if actual_state != desired_state {
			m.state_lock.Unlock()
			if desired_state {
				m.ConfigureAddress()
			} else {
				m.DeconfigureAddress()
			}
		} else {
			// Wait for notification
			m.recheck.Wait()
			// Want to query actual state anyway, so unlock
			m.state_lock.Unlock()

			// Check if we should exit
			select {
			case <-ctx.Done():
				m.DeconfigureAddress()
				return
			default:
			}
		}
	}
}

func (m *IPManager) SyncStates(ctx context.Context, states <-chan bool) {
	ticker := time.NewTicker(10 * time.Second)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		m.applyLoop(ctx)
		wg.Done()
	}()

	for {
		select {
		case new_state := <-states:
			m.state_lock.Lock()
			if m.current_state != new_state {
				m.current_state = new_state
				m.recheck.Broadcast()
			}
			m.state_lock.Unlock()
		case <-ticker.C:
			m.recheck.Broadcast()
		case <-ctx.Done():
			m.recheck.Broadcast()
			wg.Wait()
			return
		}
	}
}

func (m *IPManager) ARPQueryDuplicates() bool {
	c := exec.Command("arping",
		"-D", "-c", "2", "-q", "-w", "3",
		"-I", m.iface, m.vip.String())
	err := c.Run()
	if err != nil {
		return false
	}
	return true
}

func (m *IPManager) QueryAddress() bool {
	c := exec.Command("ip", "addr", "show", m.iface)

	lookup := fmt.Sprintf("inet %s", m.GetCIDR())
	result := false

	stdout, err := c.StdoutPipe()
	if err != nil {
		panic(err)
	}

	err = c.Start()
	if err != nil {
		panic(err)
	}

	scn := bufio.NewScanner(stdout)

	for scn.Scan() {
		line := scn.Text()
		if strings.Contains(line, lookup) {
			result = true
		}
	}

	c.Wait()

	return result
}

func (m *IPManager) ConfigureAddress() bool {
	log.Printf("Configuring address %s on %s", m.GetCIDR(), m.iface)
	return m.runAddressConfiguration("add")
}

func (m *IPManager) DeconfigureAddress() bool {
	log.Printf("Removing address %s on %s", m.GetCIDR(), m.iface)
	return m.runAddressConfiguration("delete")
}

func (m *IPManager) runAddressConfiguration(action string) bool {
	c := exec.Command("ip", "addr", action,
		m.GetCIDR(),
		"dev", m.iface)
	err := c.Run()

	switch exit := err.(type) {
	case *exec.ExitError:
		if status, ok := exit.Sys().(syscall.WaitStatus); ok {
			if status.ExitStatus() == 2 {
				// Already exists
				return true
			} else {
				log.Printf("Got error %s", status)
			}
		}

		return false
	}
	if err != nil {
		log.Printf("Error running ip address %s %s on %s: %s",
			action, m.vip, m.iface, err)
		return false
	}
	return true
}

type EtcdLeaderChecker struct {
	key      string
	nodename string
	kapi     client.KeysAPI
}

func NetmaskSize(mask net.IPMask) int {
	ones, bits := mask.Size()
	if bits == 0 {
		panic("Invalid mask")
	}
	return ones
}

func NewEtcdLeaderChecker(endpoint, key, nodename string) *EtcdLeaderChecker {
	e := &EtcdLeaderChecker{key: key, nodename: nodename}

	cfg := client.Config{
		Endpoints:               []string{endpoint},
		Transport:               client.DefaultTransport,
		HeaderTimeoutPerRequest: time.Second,
	}

	c, err := client.New(cfg)

	if err != nil {
		panic(err)
	}

	e.kapi = client.NewKeysAPI(c)

	return e
}

func (e *EtcdLeaderChecker) GetChangeNotificationStream(ctx context.Context, out chan<- bool) error {
	resp, err := e.kapi.Get(ctx, e.key, &client.GetOptions{Quorum: true})
	if err != nil {
		panic(err)
	}

	state := resp.Node.Value == e.nodename
	out <- state

	after := resp.Node.ModifiedIndex

	w := e.kapi.Watcher(e.key, &client.WatcherOptions{AfterIndex: after, Recursive: false})
checkLoop:
	for {
		resp, err := w.Next(ctx)

		if err != nil {
			if ctx.Err() != nil {
				break checkLoop
			}
			out <- false
			log.Printf("etcd error: %s", err)
			time.Sleep(1 * time.Second)
			continue
		}

		state = resp.Node.Value == e.nodename

		select {
		case <-ctx.Done():
			break checkLoop
		case out <- state:
			continue
		}
	}

	return ctx.Err()
}

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
