package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"go.uber.org/zap"
)

func patroniConfig(endpoint, triggerKey, triggerValue string) *vipconfig.Config {
	return &vipconfig.Config{
		Endpoints:    []string{endpoint},
		TriggerKey:   triggerKey,
		TriggerValue: triggerValue,
		Interval:     1, // 1 ms – fast for unit tests
		Logger:       zap.NewNop(),
	}
}

// runStream starts GetChangeNotificationStream in a goroutine and returns the
// first value emitted on out, canceling the context afterwards. Fails the test
// if no value arrives within 2 s.
func runStream(t *testing.T, conf *vipconfig.Config) bool {
	t.Helper()
	checker, err := NewPatroniLeaderChecker(conf)
	if err != nil {
		t.Fatalf("NewPatroniLeaderChecker: %v", err)
	}

	out := make(chan bool, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = checker.GetChangeNotificationStream(ctx, out) }()

	select {
	case v := <-out:
		cancel()
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream value")
		return false
	}
}

// ---------------------------------------------------------------------------
// NewPatroniLeaderChecker
// ---------------------------------------------------------------------------

// TestNewPatroniLeaderChecker_TLSError ensures that a missing cert file causes
// construction to fail (error originates from getTransport).
func TestNewPatroniLeaderChecker_TLSError(t *testing.T) {
	t.Parallel()
	conf := patroniConfig("http://127.0.0.1:8008", "/leader", "200")
	conf.EtcdCertFile = "/nonexistent/client.crt"
	conf.EtcdKeyFile = "/nonexistent/client.key"
	_, err := NewPatroniLeaderChecker(conf)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "cannot load client cert or key file") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ---------------------------------------------------------------------------
// GetChangeNotificationStream
// ---------------------------------------------------------------------------

// TestGetChangeNotificationStream_HTTPError verifies that a connection failure
// causes false to be sent on the output channel.
func TestGetChangeNotificationStream_HTTPError(t *testing.T) {
	t.Parallel()
	// Use a server that we close immediately so all requests get "connection refused".
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()

	conf := patroniConfig(srv.URL, "/leader", "200")
	result := runStream(t, conf)
	if result != false {
		t.Errorf("expected false on connection error, got true")
	}
}

// TestGetChangeNotificationStream_StatusMatch verifies that when the server
// returns the expected status code the stream emits true.
func TestGetChangeNotificationStream_StatusMatch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK) // 200
	}))
	defer srv.Close()

	conf := patroniConfig(srv.URL, "/leader", "200")
	if !runStream(t, conf) {
		t.Error("expected true when status code matches trigger value")
	}
}

// TestGetChangeNotificationStream_StatusNoMatch verifies that a different
// status code causes false to be emitted.
func TestGetChangeNotificationStream_StatusNoMatch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable) // 503
	}))
	defer srv.Close()

	conf := patroniConfig(srv.URL, "/leader", "200")
	if runStream(t, conf) {
		t.Error("expected false when status code does not match trigger value")
	}
}

// TestGetChangeNotificationStream_NonSuccessMatch verifies that a non-2xx
// status code that happens to equal the trigger value still emits true
// (the warning log does not prevent correct evaluation).
func TestGetChangeNotificationStream_NonSuccessMatch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable) // 503
	}))
	defer srv.Close()

	// Patroni uses 503 to signal "not the leader" – but if an operator
	// configures trigger-value=503 they expect true here.
	conf := patroniConfig(srv.URL, "/leader", "503")
	if !runStream(t, conf) {
		t.Error("expected true when non-2xx status code matches trigger value")
	}
}
