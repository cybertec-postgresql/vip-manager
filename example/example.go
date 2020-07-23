package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"

	"github.com/cybertec-postgresql/vip-manager/iphlpapi"
	"golang.org/x/sys/windows"
)

func PrintIPs() {
	addrs, err := net.InterfaceAddrs()
	fmt.Println("\n========== IPs ==============")
	if err == nil {
		for _, v := range addrs {
			fmt.Printf("%+v\n", v)

		}
	}
}

func main() {
	ifaces, err := net.Interfaces()
	fmt.Println("\n====== Interfaces ===========")
	if err == nil {
		for _, v := range ifaces {
			fmt.Printf("%+v\n", v)

		}
	}

	PrintIPs()

	ip := binary.BigEndian.Uint32([]byte{127, 0, 0, 42})
	mask := binary.BigEndian.Uint32([]byte{255, 255, 255, 0})
	var (
		ntecontext  *uint32
		nteinstance *uint32
	)
	iface, _ := net.InterfaceByIndex(1)
	fmt.Printf("===== Add IP %d/%d to interface %+v =====\n", ip, mask, iface.Name)
	err = iphlpapi.AddIPAddress(ip, mask, 1, ntecontext, nteinstance)
	if err.(syscall.Errno) == windows.ERROR_INVALID_PARAMETER {
		fmt.Println("We've fucked up with an invalid parameter")
		fmt.Println(err)
	}
	fmt.Printf("NTEContext: %d; NTEInstance: %d\n", ntecontext, nteinstance)

	PrintIPs()
}
