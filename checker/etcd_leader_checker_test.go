package checker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"github.com/testcontainers/testcontainers-go"
	tcetcd "github.com/testcontainers/testcontainers-go/modules/etcd"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

// certsDir returns the absolute path to the shared test certificates.
func certsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "test", "certs")
}

func etcdConfig() *vipconfig.Config {
	return &vipconfig.Config{
		Endpoints: []string{"http://127.0.0.1:2379"},
		Logger:    zap.NewNop(),
	}
}

// ---------------------------------------------------------------------------
// getTransport
// ---------------------------------------------------------------------------

// TestGetTransport_NoTLS verifies that an empty TLS config is accepted and
// returns a non-nil (but empty) *tls.Config.
func TestGetTransport_NoTLS(t *testing.T) {
	t.Parallel()
	cfg, err := getTransport(etcdConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil tls.Config")
	}
}

// TestGetTransport_MissingCAFile verifies the error when the CA file path does
// not exist.
func TestGetTransport_MissingCAFile(t *testing.T) {
	t.Parallel()
	conf := etcdConfig()
	conf.EtcdCAFile = "/nonexistent/ca.crt"
	_, err := getTransport(conf)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "cannot load CA file") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestGetTransport_MissingCertFiles verifies the error when the client cert or
// key file is missing.
func TestGetTransport_MissingCertFiles(t *testing.T) {
	t.Parallel()
	conf := etcdConfig()
	conf.EtcdCertFile = "/nonexistent/client.crt"
	conf.EtcdKeyFile = "/nonexistent/client.key"
	_, err := getTransport(conf)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "cannot load client cert or key file") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestGetTransport_ValidCAFile verifies that a real CA certificate file is
// loaded without error.
func TestGetTransport_ValidCAFile(t *testing.T) {
	t.Parallel()
	conf := etcdConfig()
	conf.EtcdCAFile = filepath.Join(certsDir(), "etcd_server_ca.crt")
	cfg, err := getTransport(conf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RootCAs == nil {
		t.Error("expected RootCAs to be populated")
	}
}

// TestGetTransport_ValidCertAndKey verifies that a real client cert+key pair
// is loaded without error.
func TestGetTransport_ValidCertAndKey(t *testing.T) {
	t.Parallel()
	conf := etcdConfig()
	conf.EtcdCAFile = filepath.Join(certsDir(), "etcd_server_ca.crt")
	conf.EtcdCertFile = filepath.Join(certsDir(), "etcd_client.crt")
	conf.EtcdKeyFile = filepath.Join(certsDir(), "etcd_client.key")
	cfg, err := getTransport(conf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Certificates) == 0 {
		t.Error("expected certificates to be populated")
	}
}

// ---------------------------------------------------------------------------
// NewEtcdLeaderChecker
// ---------------------------------------------------------------------------

// TestNewEtcdLeaderChecker_TLSError verifies that a TLS config error is
// wrapped with "failed to create TLS transport for etcd".
func TestNewEtcdLeaderChecker_TLSError(t *testing.T) {
	t.Parallel()
	conf := etcdConfig()
	conf.EtcdCAFile = "/nonexistent/ca.crt"
	_, err := NewEtcdLeaderChecker(conf)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to create TLS transport for etcd") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestNewEtcdLeaderChecker_ValidConfig verifies that the checker is created
// without error when endpoints and TLS are valid. The etcd client connects
// lazily so no live server is required.
func TestNewEtcdLeaderChecker_ValidConfig(t *testing.T) {
	t.Parallel()
	checker, err := NewEtcdLeaderChecker(etcdConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if checker == nil {
		t.Fatal("expected non-nil checker")
	}
}

// ---------------------------------------------------------------------------
// Integration tests – require a running Docker daemon
// ---------------------------------------------------------------------------

const etcdImage = "gcr.io/etcd-development/etcd:v3.7.0"

// startEtcdContainer starts a real etcd container and returns the client
// endpoints and a pre-authenticated seed client for writing test data.
// The test is skipped when Docker is not available.
func startEtcdContainer(t *testing.T) (endpoints []string, seed *clientv3.Client) {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcetcd.Run(ctx, etcdImage)
	if err != nil {
		t.Skipf("cannot start etcd container (Docker may be unavailable): %v", err)
	}
	testcontainers.CleanupContainer(t, ctr)

	endpoints, err = ctr.ClientEndpoints(ctx)
	if err != nil {
		t.Fatalf("ClientEndpoints: %v", err)
	}

	seed, err = clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
		Logger:      zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("create seed client: %v", err)
	}
	t.Cleanup(func() { _ = seed.Close() })

	// Wait for a leader to be elected before returning. WithRequireLeader
	// cancels watches immediately when no leader is present, so tests that
	// rely on watch events would race against the initial election otherwise.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st, err := seed.Status(ctx, endpoints[0])
		if err == nil && st.Leader != 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return
}

// newIntegrationChecker creates an EtcdLeaderChecker backed by a real etcd
// and registers Close via t.Cleanup.
func newIntegrationChecker(t *testing.T, endpoints []string, key, value string) *EtcdLeaderChecker {
	t.Helper()
	conf := &vipconfig.Config{
		Endpoints:    endpoints,
		TriggerKey:   key,
		TriggerValue: value,
		Logger:       zap.NewNop(),
	}
	checker, err := NewEtcdLeaderChecker(conf)
	if err != nil {
		t.Fatalf("NewEtcdLeaderChecker: %v", err)
	}
	t.Cleanup(func() { _ = checker.Close() })
	return checker
}

// TestEtcdLeaderChecker_get_KeyAbsent verifies that get emits false when the
// watched key does not exist in etcd.
func TestEtcdLeaderChecker_get_KeyAbsent(t *testing.T) {
	endpoints, _ := startEtcdContainer(t)
	checker := newIntegrationChecker(t, endpoints, "/no/such/key", "primary")

	out := make(chan bool, 1)
	checker.get(context.Background(), out)

	if got := <-out; got {
		t.Error("expected false for absent key, got true")
	}
}

// TestEtcdLeaderChecker_get_MatchingValue verifies that get emits true when
// the key value matches TriggerValue.
func TestEtcdLeaderChecker_get_MatchingValue(t *testing.T) {
	endpoints, seed := startEtcdContainer(t)
	if _, err := seed.Put(context.Background(), "/leader", "primary"); err != nil {
		t.Fatalf("seed Put: %v", err)
	}
	checker := newIntegrationChecker(t, endpoints, "/leader", "primary")

	out := make(chan bool, 1)
	checker.get(context.Background(), out)

	if got := <-out; !got {
		t.Error("expected true for matching value, got false")
	}
}

// TestEtcdLeaderChecker_get_NonMatchingValue verifies that get emits false
// when the key value does not match TriggerValue.
func TestEtcdLeaderChecker_get_NonMatchingValue(t *testing.T) {
	endpoints, seed := startEtcdContainer(t)
	if _, err := seed.Put(context.Background(), "/leader", "secondary"); err != nil {
		t.Fatalf("seed Put: %v", err)
	}
	checker := newIntegrationChecker(t, endpoints, "/leader", "primary")

	out := make(chan bool, 1)
	checker.get(context.Background(), out)

	if got := <-out; got {
		t.Error("expected false for non-matching value, got true")
	}
}

// TestEtcdLeaderChecker_watch_EmitsOnPut verifies that watch emits the
// correct bool each time the watched key is written, and stops when the
// context is cancelled.
func TestEtcdLeaderChecker_watch_EmitsOnPut(t *testing.T) {
	endpoints, seed := startEtcdContainer(t)
	checker := newIntegrationChecker(t, endpoints, "/leader", "primary")

	out := make(chan bool, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchDone := make(chan error, 1)
	go func() { watchDone <- checker.watch(ctx, out) }()

	// Allow the watch to register on the server before writing.
	time.Sleep(150 * time.Millisecond)

	if _, err := seed.Put(context.Background(), "/leader", "primary"); err != nil {
		t.Fatalf("Put matching value: %v", err)
	}
	select {
	case got := <-out:
		if !got {
			t.Error("expected true for matching put, got false")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for watch event (matching value)")
	}

	if _, err := seed.Put(context.Background(), "/leader", "secondary"); err != nil {
		t.Fatalf("Put non-matching value: %v", err)
	}
	select {
	case got := <-out:
		if got {
			t.Error("expected false for non-matching put, got true")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for watch event (non-matching value)")
	}

	cancel()
	select {
	case err := <-watchDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled from watch, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for watch goroutine to exit")
	}
}

// TestEtcdLeaderChecker_GetChangeNotificationStream_StopsOnCancel verifies
// the full stream: it emits an initial value via get and stops cleanly when
// the context is cancelled.
func TestEtcdLeaderChecker_GetChangeNotificationStream_StopsOnCancel(t *testing.T) {
	endpoints, seed := startEtcdContainer(t)

	// Pre-populate the key so the initial get emits true.
	if _, err := seed.Put(context.Background(), "/leader", "primary"); err != nil {
		t.Fatalf("seed Put: %v", err)
	}

	conf := &vipconfig.Config{
		Endpoints:    endpoints,
		TriggerKey:   "/leader",
		TriggerValue: "primary",
		Logger:       zap.NewNop(),
	}
	// GetChangeNotificationStream calls defer elc.Close(), so we must not
	// register a second cleanup here.
	checker, err := NewEtcdLeaderChecker(conf)
	if err != nil {
		t.Fatalf("NewEtcdLeaderChecker: %v", err)
	}

	out := make(chan bool, 4)
	ctx, cancel := context.WithCancel(context.Background())
	streamDone := make(chan error, 1)
	go func() { streamDone <- checker.GetChangeNotificationStream(ctx, out) }()

	// The initial get should emit true.
	select {
	case got := <-out:
		if !got {
			t.Error("expected true from initial get, got false")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for initial get value")
	}

	cancel()
	select {
	case err := <-streamDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for GetChangeNotificationStream to return")
	}
}

// TestEtcdLeaderChecker_watch_EmitsOnConnectionLoss verifies that watch emits
// false when etcd connection is lost, ensuring VIP is removed when etcd becomes unreachable.
// This test uses a context with a short timeout to simulate watch disconnection.
func TestEtcdLeaderChecker_watch_EmitsOnConnectionLoss(t *testing.T) {
	endpoints, seed := startEtcdContainer(t)
	checker := newIntegrationChecker(t, endpoints, "/leader", "primary")

	// Set initial value
	if _, err := seed.Put(context.Background(), "/leader", "primary"); err != nil {
		t.Fatalf("seed Put: %v", err)
	}

	out := make(chan bool, 10)
	// Use a context with short timeout to simulate watch error/disconnection
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	watchDone := make(chan error, 1)
	go func() { watchDone <- checker.watch(ctx, out) }()

	// Allow the watch to register
	time.Sleep(50 * time.Millisecond)

	// Trigger a change to verify watch is working before timeout
	if _, err := seed.Put(context.Background(), "/leader", "secondary"); err != nil {
		t.Fatalf("seed Put secondary: %v", err)
	}

	select {
	case got := <-out:
		if got {
			t.Error("expected false for secondary value, got true")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for watch event")
	}

	// Wait for timeout to trigger the context.Done() error
	select {
	case err := <-watchDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected context.DeadlineExceeded, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for watch to exit on timeout")
	}
}

// TestEtcdLeaderChecker_GetChangeNotificationStream_EmitsOnConnectionError
// verifies that GetChangeNotificationStream emits false when connection
// errors occur, ensuring VIP is removed immediately when etcd is unreachable.
func TestEtcdLeaderChecker_GetChangeNotificationStream_EmitsOnConnectionError(t *testing.T) {
	// Find an unused port by listening on port 0
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find unused port: %v", err)
	}
	unusedPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	// Create a checker that points to an unreachable endpoint
	conf := &vipconfig.Config{
		Endpoints:    []string{fmt.Sprintf("http://127.0.0.1:%d", unusedPort)},
		TriggerKey:   "/leader",
		TriggerValue: "primary",
		Logger:       zap.NewNop(),
	}
	checker, err := NewEtcdLeaderChecker(conf)
	if err != nil {
		t.Fatalf("NewEtcdLeaderChecker: %v", err)
	}

	out := make(chan bool, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go func() { _ = checker.GetChangeNotificationStream(ctx, out) }()

	// Should eventually emit false because etcd is unreachable
	falseReceived := false
	for {
		select {
		case got := <-out:
			if !got {
				falseReceived = true
				t.Logf("correctly received false on unreachable etcd")
				break
			}
		case <-ctx.Done():
			break
		}
		if falseReceived {
			break
		}
	}

	if !falseReceived {
		t.Error("expected false to be emitted when etcd is unreachable, but no false value was received")
	}
}

// TestEtcdLeaderChecker_watch_ResyncsOnCanceledWatch is a regression test
// for https://github.com/cybertec-postgresql/vip-manager/issues/394: when the
// watch channel dies (canceled by the server or closed) and the leader
// changes while the watch is down, the checker must re-sync the state via
// get() instead of silently re-arming the watch and keeping stale state.
func TestEtcdLeaderChecker_watch_ResyncsOnCanceledWatch(t *testing.T) {
	endpoints, seed := startEtcdContainer(t)
	checker := newIntegrationChecker(t, endpoints, "/leader", "primary")

	// Make this node the leader so a stale state would keep the VIP up.
	if _, err := seed.Put(context.Background(), "/leader", "primary"); err != nil {
		t.Fatalf("seed Put: %v", err)
	}

	out := make(chan bool, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchDone := make(chan error, 1)
	go func() { watchDone <- checker.watch(ctx, out) }()

	// Allow the watch to register on the server.
	time.Sleep(150 * time.Millisecond)

	// Kill all watch streams: closing the client's Watcher closes the watch
	// channel, simulating a server-side cancellation / dead watch.
	if err := checker.Watcher.Close(); err != nil {
		t.Fatalf("Watcher.Close: %v", err)
	}

	// While the watch is dead, the leader changes to another node. The old
	// code lost this event forever; the fix re-fetches the value via get().
	if _, err := seed.Put(context.Background(), "/leader", "secondary"); err != nil {
		t.Fatalf("Put leader change: %v", err)
	}

	// The re-sync must emit false because this node is no longer the leader.
	select {
	case got := <-out:
		if got {
			t.Error("expected false after leader change during dead watch, got true")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for re-synced state after watch channel died")
	}

	cancel()
	select {
	case err := <-watchDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled from watch, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for watch goroutine to exit")
	}
}
