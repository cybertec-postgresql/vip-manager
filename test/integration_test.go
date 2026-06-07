package test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cybertec-postgresql/vip-manager/checker"
	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"go.uber.org/zap"
)

// TestPatroniCheckerHandlesDisconnection simulates issue #336:
// Verifies that when Patroni becomes unreachable, the checker sends false
// states to signal VIP removal.
//
// This test reproduces the scenario where:
// 1. Patroni is initially reachable (server running)
// 2. Patroni becomes unreachable (server stopped/network down)
// 3. The checker should detect this and send false to remove the VIP
func TestPatroniCheckerHandlesDisconnection(t *testing.T) {
	// Start a Patroni mock server that returns leader status (200)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	conf := &vipconfig.Config{
		Endpoints:    []string{server.URL},
		TriggerKey:   "/leader",
		TriggerValue: "200",
		Interval:     10, // 10ms for fast tests
		Logger:       zap.NewNop(),
	}

	patroniChecker, err := checker.NewPatroniLeaderChecker(conf)
	if err != nil {
		t.Fatalf("NewPatroniLeaderChecker: %v", err)
	}

	out := make(chan bool, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start the checker in a goroutine
	go func() { _ = patroniChecker.GetChangeNotificationStream(ctx, out) }()

	// Should initially receive true (server is up)
	select {
	case state := <-out:
		if !state {
			t.Error("expected true when Patroni is reachable")
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for initial state")
	}

	// Now close the server to simulate Patroni becoming unreachable
	server.Close()

	// Should eventually receive false (connection failure)
	foundFalse := false
	deadline := time.Now().Add(1 * time.Second)
	for !foundFalse && time.Now().Before(deadline) {
		select {
		case state := <-out:
			if !state {
				foundFalse = true
				t.Logf("correctly received false when Patroni became unreachable")
			}
		case <-time.After(50 * time.Millisecond):
			// retry
		}
	}

	if !foundFalse {
		t.Error("expected false to be sent when Patroni becomes unreachable")
	}
}
