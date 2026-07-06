//go:build !windows

package vipconfig

import (
	"os"

	"golang.org/x/sys/unix"
)

// redirectStdStreams points the process's stdout (fd 1) and stderr (fd 2) at f
// at the file-descriptor level. This captures not only writes via os.Stdout /
// os.Stderr but also anything the Go runtime writes directly to those
// descriptors (e.g. panic stack traces) and output from child processes that
// inherit them. It is called again after the log file is reopened on SIGHUP so
// the redirect follows the freshly created file.
func redirectStdStreams(f *os.File) error {
	fd := int(f.Fd())
	if err := unix.Dup2(fd, int(os.Stdout.Fd())); err != nil {
		return err
	}
	return unix.Dup2(fd, int(os.Stderr.Fd()))
}
