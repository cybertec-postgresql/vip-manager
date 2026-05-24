package checker

import (
	"errors"
	"testing"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"go.uber.org/zap"
)

func newLeaderCheckerConfig(endpointType string, endpoints []string) *vipconfig.Config {
	return &vipconfig.Config{
		EndpointType: endpointType,
		Endpoints:    endpoints,
		Logger:       zap.NewNop(),
	}
}

func TestNewLeaderChecker_Unsupported(t *testing.T) {
	t.Parallel()
	_, err := NewLeaderChecker(newLeaderCheckerConfig("zookeeper", []string{"http://127.0.0.1:2181"}))
	if !errors.Is(err, ErrUnsupportedEndpointType) {
		t.Errorf("expected ErrUnsupportedEndpointType, got %v", err)
	}
}

func TestNewLeaderChecker_Empty(t *testing.T) {
	t.Parallel()
	_, err := NewLeaderChecker(newLeaderCheckerConfig("", []string{"http://127.0.0.1:2379"}))
	if !errors.Is(err, ErrUnsupportedEndpointType) {
		t.Errorf("expected ErrUnsupportedEndpointType, got %v", err)
	}
}

func TestNewLeaderChecker_Consul(t *testing.T) {
	t.Parallel()
	lc, err := NewLeaderChecker(newLeaderCheckerConfig("consul", []string{"http://127.0.0.1:8500"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := lc.(*ConsulLeaderChecker); !ok {
		t.Errorf("expected *ConsulLeaderChecker, got %T", lc)
	}
}

func TestNewLeaderChecker_Etcd(t *testing.T) {
	t.Parallel()
	lc, err := NewLeaderChecker(newLeaderCheckerConfig("etcd", []string{"http://127.0.0.1:2379"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := lc.(*EtcdLeaderChecker); !ok {
		t.Errorf("expected *EtcdLeaderChecker, got %T", lc)
	}
}

func TestNewLeaderChecker_Etcd3(t *testing.T) {
	t.Parallel()
	lc, err := NewLeaderChecker(newLeaderCheckerConfig("etcd3", []string{"http://127.0.0.1:2379"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := lc.(*EtcdLeaderChecker); !ok {
		t.Errorf("expected *EtcdLeaderChecker, got %T", lc)
	}
}

func TestNewLeaderChecker_Patroni(t *testing.T) {
	t.Parallel()
	lc, err := NewLeaderChecker(newLeaderCheckerConfig("patroni", []string{"http://127.0.0.1:8008"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := lc.(*PatroniLeaderChecker); !ok {
		t.Errorf("expected *PatroniLeaderChecker, got %T", lc)
	}
}
