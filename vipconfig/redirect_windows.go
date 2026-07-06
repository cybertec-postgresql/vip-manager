//go:build windows

package vipconfig

import (
	"os"

	"golang.org/x/sys/windows"
)

// redirectStdStreams points the process's standard output and error handles at
// f. On Windows this updates the process' STD_OUTPUT_HANDLE / STD_ERROR_HANDLE,
// which is picked up by child processes and by code that resolves the handles
// at write time. Note that Go's already-initialised os.Stdout / os.Stderr keep
// their original handles, so this is best-effort; SIGHUP-based log rotation is
// a Unix concept and Windows is not the primary target for this feature.
func redirectStdStreams(f *os.File) error {
	h := windows.Handle(f.Fd())
	if err := windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, h); err != nil {
		return err
	}
	return windows.SetStdHandle(windows.STD_ERROR_HANDLE, h)
}
