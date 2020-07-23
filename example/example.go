package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
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

func PrintIfaces() {
	ifaces, err := net.Interfaces()
	fmt.Println("\n====== Interfaces ===========")
	if err == nil {
		for _, v := range ifaces {
			fmt.Printf("%+v\n", v)

		}
	}
}

func main() {
	PrintIfaces()
	PrintIPs()
	var (
		ip          uint32 = binary.LittleEndian.Uint32(net.ParseIP(os.Args[1]).To4())
		mask        uint32 = binary.LittleEndian.Uint32(net.IPMask(net.ParseIP(os.Args[2]).To4()))
		ifaceidx    uint32 = func() uint32 { i, _ := strconv.Atoi(os.Args[3]); return uint32(i) }()
		ntecontext  uint32 = 0
		nteinstance uint32 = 0
	)
	iface, _ := net.InterfaceByIndex(int(ifaceidx))
	fmt.Printf("===== Add IP %x/%x to interface %+v =====\n", ip, mask, iface.Name)
	err := iphlpapi.AddIPAddress(ip, mask, ifaceidx, &ntecontext, &nteinstance)
	if err != nil && err.(syscall.Errno) == windows.ERROR_INVALID_PARAMETER {
		fmt.Println("We've fucked up with an invalid parameter")
		fmt.Println(err)
		return
	}
	fmt.Printf("NTEContext: %d; NTEInstance: %d\n", ntecontext, nteinstance)
	PrintIPs()
	fmt.Printf("===== Delete IP %x/%x from interface %+v =====\n", ip, mask, iface.Name)
	err = iphlpapi.DeleteIPAddress(ntecontext)
	if err != nil {
		fmt.Println("We've fucked up and cannot delete the IP address")
		fmt.Println(err)
		return
	}
	PrintIPs()
}
