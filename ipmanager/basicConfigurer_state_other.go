//go:build !windows

package ipmanager

// osState carries no state outside Windows: addresses are added and removed
// with the `ip` command, which needs no handle.
type osState struct{}
