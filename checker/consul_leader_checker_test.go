package checker

import (
	"strings"
	"testing"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"go.uber.org/zap"
)

func newTestConfig(endpoint string) *vipconfig.Config {
	return &vipconfig.Config{
		Endpoints: []string{endpoint},
		Logger:    zap.NewNop(),
	}
}

// TestNewConsulLeaderChecker_UnparseableURL verifies that a URL containing a
// null byte (rejected by net/url) is wrapped with context.
func TestNewConsulLeaderChecker_UnparseableURL(t *testing.T) {
	t.Parallel()
	_, err := NewConsulLeaderChecker(newTestConfig("http://invalid\x00host"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse consul endpoint URL") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestNewConsulLeaderChecker_EmptyHostname verifies that a URL with no host
// component (e.g. a bare path) is rejected with the empty-hostname sentinel.
func TestNewConsulLeaderChecker_EmptyHostname(t *testing.T) {
	t.Parallel()
	// "localhost" without a scheme parses successfully but Hostname() == ""
	_, err := NewConsulLeaderChecker(newTestConfig("localhost"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "hostname is empty") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestNewConsulLeaderChecker_ValidURL verifies that a well-formed endpoint
// does not produce a construction error (api.NewClient never fails for valid
// address strings).
func TestNewConsulLeaderChecker_ValidURL(t *testing.T) {
	t.Parallel()
	lc, err := NewConsulLeaderChecker(newTestConfig("http://127.0.0.1:8500"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lc == nil {
		t.Fatal("expected non-nil checker")
	}
}
