package checker

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
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
	_ = checker.Client.Close()
}
