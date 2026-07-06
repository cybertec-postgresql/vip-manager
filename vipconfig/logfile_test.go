package vipconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReopenableFile_ReopenSwapsInode verifies the core log-rotation behaviour:
// after the current file is renamed aside (as logrotate would do), Reopen makes
// subsequent writes land in a freshly created file at the original path, while
// the renamed file keeps only what was written before the reopen.
func TestReopenableFile_ReopenSwapsInode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vip-manager.log")

	rf, err := newReopenableFile(path, false)
	if err != nil {
		t.Fatalf("newReopenableFile: %v", err)
	}

	if _, err := rf.Write([]byte("before-rotate\n")); err != nil {
		t.Fatalf("write before rotate: %v", err)
	}
	if err := rf.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Simulate logrotate moving the current file out of the way.
	rotated := path + ".1"
	if err := os.Rename(path, rotated); err != nil {
		t.Fatalf("rename: %v", err)
	}

	if err := rf.Reopen(); err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	if _, err := rf.Write([]byte("after-rotate\n")); err != nil {
		t.Fatalf("write after rotate: %v", err)
	}
	if err := rf.Sync(); err != nil {
		t.Fatalf("sync after rotate: %v", err)
	}

	rotatedContent := readFile(t, rotated)
	if !strings.Contains(rotatedContent, "before-rotate") {
		t.Errorf("rotated file should contain pre-rotate line, got %q", rotatedContent)
	}
	if strings.Contains(rotatedContent, "after-rotate") {
		t.Errorf("rotated file should NOT contain post-rotate line, got %q", rotatedContent)
	}

	freshContent := readFile(t, path)
	if !strings.Contains(freshContent, "after-rotate") {
		t.Errorf("fresh file should contain post-rotate line, got %q", freshContent)
	}
	if strings.Contains(freshContent, "before-rotate") {
		t.Errorf("fresh file should NOT contain pre-rotate line, got %q", freshContent)
	}
}

func TestReopenableFile_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vip-manager.log")
	if err := os.WriteFile(path, []byte("existing\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rf, err := newReopenableFile(path, false)
	if err != nil {
		t.Fatalf("newReopenableFile: %v", err)
	}
	if _, err := rf.Write([]byte("added\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = rf.Sync()

	content := readFile(t, path)
	if !strings.Contains(content, "existing") || !strings.Contains(content, "added") {
		t.Errorf("expected both existing and added content, got %q", content)
	}
}

func TestReopenableFile_NewFileError(t *testing.T) {
	// A path whose parent directory does not exist must fail to open.
	path := filepath.Join(t.TempDir(), "nonexistent-dir", "vip-manager.log")
	if _, err := newReopenableFile(path, false); err == nil {
		t.Error("expected error opening file in nonexistent directory")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
