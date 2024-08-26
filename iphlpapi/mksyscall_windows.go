//go:build generate
// +build generate

package iphlpapi

//go:generate go run golang.org/x/sys/windows/mkwinsyscall -output ziphlapi_windows.go iphlpapi_windows.go
