//go:build !windows

package vipconfig

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCaptureStdStreams_Subprocess verifies that opening a reopenableFile with
// captureStd=true redirects the process's stdout and stderr file descriptors to
// the log file, so that plain fmt.Print / os.Stderr writes (which do not go
// through zap) are also captured. Because the redirect mutates the process-wide
// fd 1 and 2, the actual work runs in a subprocess (re-executing this test
// binary with a helper env var set) so it cannot disturb the test runner.
func TestCaptureStdStreams_Subprocess(t *testing.T) {
	logPath := os.Getenv("VIPCONFIG_REDIRECT_TEST_LOG")
	if logPath != "" {
		// Child mode: perform the redirect and emit to stdout and stderr.
		rf, err := newReopenableFile(logPath, true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "newReopenableFile: %v\n", err)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, "captured-stdout-line")
		fmt.Fprintln(os.Stderr, "captured-stderr-line")
		_ = rf.Sync()
		os.Exit(0)
	}

	// Parent mode: re-exec this test binary running only the child branch.
	logPath = filepath.Join(t.TempDir(), "vip-manager.log")
	cmd := exec.Command(os.Args[0], "-test.run=TestCaptureStdStreams_Subprocess")
	cmd.Env = append(os.Environ(), "VIPCONFIG_REDIRECT_TEST_LOG="+logPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child process failed: %v\noutput: %s", err, out)
	}

	content := readFile(t, logPath)
	if !strings.Contains(content, "captured-stdout-line") {
		t.Errorf("expected stdout write to be captured in log file, got %q", content)
	}
	if !strings.Contains(content, "captured-stderr-line") {
		t.Errorf("expected stderr write to be captured in log file, got %q", content)
	}
}
