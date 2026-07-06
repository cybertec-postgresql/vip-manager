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
	f    *os.File
}

// newReopenableFile opens (creating if necessary) the log file at path in append
// mode and returns a reopenableFile ready to be used as a zapcore.WriteSyncer.
func newReopenableFile(path string) (*reopenableFile, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &reopenableFile{path: path, f: f}, nil
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
	return nil
}
