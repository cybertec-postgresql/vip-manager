//go:build windows
// +build windows

// +build generate

package iphlpapi

//go:generate go run golang.org/x/sys/windows/mkwinsyscall -output ziphlapi_windows.go iphlpapi.go
