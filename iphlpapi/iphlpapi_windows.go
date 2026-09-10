package iphlpapi

//sys	AddIPAddress(Address uint32, IpMask uint32, IfIndex uint32, NTEContext *uint32, NTEInstance *uint32) (errcode error) = iphlpapi.AddIPAddress
//sys	DeleteIPAddress(NTEContext uint32) (errcode error) = iphlpapi.DeleteIPAddress

// AddIPAddress and DeleteIPAddress only handle IPv4. The Unicast entry calls
// below are the IPv6-capable equivalents; Windows runs Duplicate Address
// Detection and emits the unsolicited Neighbor Advertisement itself when an
// address is added through them.

//sys	InitializeUnicastIpAddressEntry(Row *windows.MibUnicastIpAddressRow) = iphlpapi.InitializeUnicastIpAddressEntry
//sys	CreateUnicastIpAddressEntry(Row *windows.MibUnicastIpAddressRow) (errcode error) = iphlpapi.CreateUnicastIpAddressEntry
//sys	DeleteUnicastIpAddressEntry(Row *windows.MibUnicastIpAddressRow) (errcode error) = iphlpapi.DeleteUnicastIpAddressEntry
