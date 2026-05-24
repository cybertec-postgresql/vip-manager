package checker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	capi "github.com/hashicorp/consul/api"
	"github.com/testcontainers/testcontainers-go"
	tcconsul "github.com/testcontainers/testcontainers-go/modules/consul"
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

// ---------------------------------------------------------------------------
// Integration tests – require a running Docker daemon
// ---------------------------------------------------------------------------

const consulImage = "hashicorp/consul:1.15"
const consulTestKey = "service/batman/leader"

// startConsulContainer starts a real Consul container and returns the HTTP API
// endpoint (with scheme) and a seed client for writing test data.
// The test is skipped when Docker is not available.
func startConsulContainer(t *testing.T) (endpoint string, seed *capi.Client) {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcconsul.Run(ctx, consulImage)
	if err != nil {
		t.Skipf("cannot start consul container (Docker may be unavailable): %v", err)
	}
	testcontainers.CleanupContainer(t, ctr)

	host, err := ctr.ApiEndpoint(ctx)
	if err != nil {
		t.Fatalf("ApiEndpoint: %v", err)
	}

	cfg := capi.DefaultConfig()
	cfg.Address = host
	seed, err = capi.NewClient(cfg)
	if err != nil {
		t.Fatalf("seed api.NewClient: %v", err)
	}
	return "http://" + host, seed
}

// consulCheckerFor builds a ConsulLeaderChecker with a 1 ms poll interval for
// fast test iteration.
func consulCheckerFor(t *testing.T, endpoint, key, value string) *ConsulLeaderChecker {
	t.Helper()
	conf := &vipconfig.Config{
		Endpoints:    []string{endpoint},
		TriggerKey:   key,
		TriggerValue: value,
		Interval:     1,
		Logger:       zap.NewNop(),
	}
	checker, err := NewConsulLeaderChecker(conf)
	if err != nil {
		t.Fatalf("NewConsulLeaderChecker: %v", err)
	}
	return checker
}

// runConsulStream starts GetChangeNotificationStream in a goroutine and
// returns the output channel and a done channel carrying the final error.
func runConsulStream(ctx context.Context, c *ConsulLeaderChecker) (out chan bool, done chan error) {
	out = make(chan bool, 8)
	done = make(chan error, 1)
	go func() { done <- c.GetChangeNotificationStream(ctx, out) }()
	return
}

// receiveOne reads one value from out within 3 s or fails the test.
func receiveOne(t *testing.T, out <-chan bool) bool {
	t.Helper()
	select {
	case v := <-out:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for stream value")
		return false
	}
}

// waitDone waits for the stream goroutine to exit within 5 s (must allow for
// the consul WaitTime=1 s long-poll to expire).
func waitDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for GetChangeNotificationStream to return")
		return nil
	}
}

// TestConsulLeaderChecker_GetChangeNotificationStream_KeyAbsent verifies that
// the stream emits false when the watched key is not present in Consul.
// After cancelling, a key is injected so the goroutine can advance past the
// nil-response path (which has no inline ctx check) and exit via the select.
func TestConsulLeaderChecker_GetChangeNotificationStream_KeyAbsent(t *testing.T) {
	endpoint, seed := startConsulContainer(t)
	checker := consulCheckerFor(t, endpoint, consulTestKey, "primary")

	ctx, cancel := context.WithCancel(context.Background())
	out, done := runConsulStream(ctx, checker)

	if receiveOne(t, out) {
		t.Error("expected false for absent key, got true")
	}

	cancel()
	// The nil-response loop has no inline ctx check, so inject a key to let
	// the goroutine reach the ctx.Done() select branch and exit cleanly.
	_, _ = seed.KV().Put(&capi.KVPair{Key: consulTestKey, Value: []byte("any")}, nil)

	if err := waitDone(t, done); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestConsulLeaderChecker_GetChangeNotificationStream_MatchingValue verifies
// that the stream emits true when the key value equals TriggerValue.
func TestConsulLeaderChecker_GetChangeNotificationStream_MatchingValue(t *testing.T) {
	endpoint, seed := startConsulContainer(t)
	if _, err := seed.KV().Put(&capi.KVPair{Key: consulTestKey, Value: []byte("primary")}, nil); err != nil {
		t.Fatalf("seed Put: %v", err)
	}
	checker := consulCheckerFor(t, endpoint, consulTestKey, "primary")

	ctx, cancel := context.WithCancel(context.Background())
	out, done := runConsulStream(ctx, checker)

	if !receiveOne(t, out) {
		t.Error("expected true for matching value, got false")
	}

	cancel()
	if err := waitDone(t, done); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestConsulLeaderChecker_GetChangeNotificationStream_NonMatchingValue verifies
// that the stream emits false when the key value differs from TriggerValue.
func TestConsulLeaderChecker_GetChangeNotificationStream_NonMatchingValue(t *testing.T) {
	endpoint, seed := startConsulContainer(t)
	if _, err := seed.KV().Put(&capi.KVPair{Key: consulTestKey, Value: []byte("secondary")}, nil); err != nil {
		t.Fatalf("seed Put: %v", err)
	}
	checker := consulCheckerFor(t, endpoint, consulTestKey, "primary")

	ctx, cancel := context.WithCancel(context.Background())
	out, done := runConsulStream(ctx, checker)

	if receiveOne(t, out) {
		t.Error("expected false for non-matching value, got true")
	}

	cancel()
	if err := waitDone(t, done); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestConsulLeaderChecker_GetChangeNotificationStream_KeyChanges verifies that
// the stream picks up a value change via the blocking long-poll.
func TestConsulLeaderChecker_GetChangeNotificationStream_KeyChanges(t *testing.T) {
	endpoint, seed := startConsulContainer(t)
	if _, err := seed.KV().Put(&capi.KVPair{Key: consulTestKey, Value: []byte("primary")}, nil); err != nil {
		t.Fatalf("seed Put: %v", err)
	}
	checker := consulCheckerFor(t, endpoint, consulTestKey, "primary")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, done := runConsulStream(ctx, checker)

	// Initial value: matching → true.
	if !receiveOne(t, out) {
		t.Error("expected true for initial matching value, got false")
	}

	// Change the value while the stream is long-polling; the poll unblocks immediately.
	if _, err := seed.KV().Put(&capi.KVPair{Key: consulTestKey, Value: []byte("secondary")}, nil); err != nil {
		t.Fatalf("update Put: %v", err)
	}

	// Updated value: non-matching → false.
	if receiveOne(t, out) {
		t.Error("expected false after key change to non-matching value, got true")
	}

	cancel()
	if err := waitDone(t, done); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestConsulLeaderChecker_GetChangeNotificationStream_ErrorPath verifies that
// a KV error (unreachable server) causes the stream to emit false and that
// cancelling the context stops it cleanly.
func TestConsulLeaderChecker_GetChangeNotificationStream_ErrorPath(t *testing.T) {
	// Port 1 is closed on loopback; the TCP dial fails immediately.
	checker := consulCheckerFor(t, "http://127.0.0.1:1", consulTestKey, "primary")

	ctx, cancel := context.WithCancel(context.Background())
	out, done := runConsulStream(ctx, checker)

	if receiveOne(t, out) {
		t.Error("expected false on error path, got true")
	}

	cancel()
	if err := waitDone(t, done); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
