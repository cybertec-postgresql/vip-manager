package ipmanager

import "golang.org/x/sys/windows"

// osState holds the handles Windows needs to remove an address it added.
type osState struct {
	ntecontext uint32                          // returned by AddIPAddress, IPv4 only
	ipv6row    *windows.MibUnicastIpAddressRow // submitted to CreateUnicastIpAddressEntry
}
