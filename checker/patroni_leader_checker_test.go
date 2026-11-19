package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
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
	ctx := t.Context()

	go func() { _ = checker.GetChangeNotificationStream(ctx, out) }()

	select {
	case v := <-out:
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

// TestGetChangeNotificationStream_Timeout verifies that a timeout waiting for a response
// causes false to be emitted.
func TestGetChangeNotificationStream_Timeout(t *testing.T) {
	t.Parallel()
	// Create a handler that delays the response beyond the client timeout
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	conf := patroniConfig(srv.URL, "/leader", "200")
	result := runStream(t, conf)
	if result != false {
		t.Error("expected false on timeout")
	}
}

// ---------------------------------------------------------------------------
// Status code logging
// ---------------------------------------------------------------------------

// streamLogs serves the given status codes in order, repeating the last one,
// and returns the log entries written while the checker polls the server
// for twice as many intervals as there are codes.
func streamLogs(t *testing.T, triggerValue string, codes ...int) []observer.LoggedEntry {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		i := min(int(calls.Add(1))-1, len(codes)-1)
		w.WriteHeader(codes[i])
	}))
	defer srv.Close()

	core, logs := observer.New(zapcore.DebugLevel)
	conf := patroniConfig(srv.URL, "/leader", triggerValue)
	conf.Logger = zap.New(core)
	checker, err := NewPatroniLeaderChecker(conf)
	if err != nil {
		t.Fatalf("NewPatroniLeaderChecker: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	out := make(chan bool)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = checker.GetChangeNotificationStream(ctx, out)
	}()
	for range 2 * len(codes) {
		select {
		case <-out:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for stream value")
		}
	}
	cancel()
	<-done
	return logs.All()
}

// countLevel returns how many entries were logged at the given level.
func countLevel(entries []observer.LoggedEntry, level zapcore.Level) int {
	n := 0
	for _, e := range entries {
		if e.Level == level {
			n++
		}
	}
	return n
}

// TestPatroniStatusLogging checks that the answers Patroni gives to a role
// check are logged once per change at the info level, and that any other
// status code produces a warning.
func TestPatroniStatusLogging(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		codes     []int
		wantInfo  int
		wantWarns int
	}{
		{"replica gets 503", []int{503}, 1, 0},
		{"leader gets 200", []int{200}, 1, 0},
		{"replica becomes leader", []int{503, 200}, 2, 0},
		{"server error", []int{500}, 0, 1},
		{"server error recovers", []int{503, 500, 503}, 2, 1}, // status line, recovery line
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			entries := streamLogs(t, "200", tt.codes...)
			if got := countLevel(entries, zapcore.InfoLevel); got != tt.wantInfo {
				t.Errorf("expected %d info entries, got %d: %v", tt.wantInfo, got, entries)
			}
			if got := countLevel(entries, zapcore.WarnLevel); got != tt.wantWarns {
				t.Errorf("expected %d warnings, got %d: %v", tt.wantWarns, got, entries)
			}
		})
	}
}
