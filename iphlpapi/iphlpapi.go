//go:build windows
// +build windows

package iphlpapi

//sys	AddIPAddress(Address uint32, IpMask uint32, IfIndex uint32, NTEContext *uint32, NTEInstance *uint32) (errcode error) = iphlpapi.AddIPAddress
//sys	DeleteIPAddress(NTEContext uint32) (errcode error) = iphlpapi.DeleteIPAddress
