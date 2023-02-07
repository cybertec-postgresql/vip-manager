// +build generate

package iphlpapi

//go:generate go run golang.org/x/sys/windows/mkwinsyscall -output ziphlapi.go iphlpapi.go
