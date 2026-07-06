package vipconfig

import (
	"os"
	"sync"
)

// reopenableFile is a zapcore.WriteSyncer that wraps a log file and can close
// and reopen it on demand. This allows log-rotation tools such as logrotate to
// move the current log file aside and have vip-manager start writing to a fresh
// file (created by logrotate) after receiving a SIGHUP, instead of continuing to
// write to the now-unlinked inode.
type reopenableFile struct {
	path string
	mu   sync.Mutex // guards f; Reopen may race with Write/Sync from log goroutines

	// captureStd, when true, redirects the process's stdout/stderr file
	// descriptors to the log file whenever it is (re)opened, so that panics and
	// any output not routed through the logger are also captured.
	captureStd bool
	f          *os.File
}

// newReopenableFile opens (creating if necessary) the log file at path in append
// mode and returns a reopenableFile ready to be used as a zapcore.WriteSyncer.
// When captureStd is true, the process's stdout and stderr are redirected to the
// file as well (see redirectStdStreams).
func newReopenableFile(path string, captureStd bool) (*reopenableFile, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	r := &reopenableFile{path: path, captureStd: captureStd, f: f}
	if captureStd {
		if err := redirectStdStreams(f); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return r, nil
}

// Write implements io.Writer.
func (r *reopenableFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Write(p)
}

// Sync implements zapcore.WriteSyncer by flushing the underlying file.
func (r *reopenableFile) Sync() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Sync()
}

// Reopen flushes and closes the current log file, then opens the configured path
// again. After logrotate has renamed the old file, this makes vip-manager write
// to the newly created file. The old handle is always closed, even if reopening
// the path fails.
func (r *reopenableFile) Reopen() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	_ = r.f.Sync()
	_ = r.f.Close()

	f, err := os.OpenFile(r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	r.f = f
	if r.captureStd {
		return redirectStdStreams(f)
	}
	return nil
}
