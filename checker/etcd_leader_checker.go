package checker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

// EtcdLeaderChecker is used to check state of the leader key in Etcd
type EtcdLeaderChecker struct {
	*vipconfig.Config
	*clientv3.Client
}

// NewEtcdLeaderChecker returns a new instance
func NewEtcdLeaderChecker(conf *vipconfig.Config) (*EtcdLeaderChecker, error) {
	tlsConfig, err := getTransport(conf)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS transport for etcd: %w", err)
	}
	cfg := clientv3.Config{
		Endpoints:            conf.Endpoints,
		TLS:                  tlsConfig,
		DialKeepAliveTimeout: time.Second,
		DialKeepAliveTime:    time.Second,
		Username:             conf.EtcdUser,
		Password:             conf.EtcdPassword,
		Logger:               conf.Logger,
	}
	c, err := clientv3.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd at endpoints %v: %w", conf.Endpoints, err)
	}
	return &EtcdLeaderChecker{conf, c}, nil
}

func getTransport(conf *vipconfig.Config) (*tls.Config, error) {
	var caCertPool *x509.CertPool
	// create valid CertPool only if the ca certificate file exists
	if conf.EtcdCAFile != "" {
		caCert, err := os.ReadFile(conf.EtcdCAFile)
		if err != nil {
			return nil, fmt.Errorf("cannot load CA file: %s", err)
		}

		caCertPool = x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
	}
	var certificates []tls.Certificate
	// create valid []Certificate only if the client cert and key files exists
	if conf.EtcdCertFile != "" && conf.EtcdKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(conf.EtcdCertFile, conf.EtcdKeyFile)
		if err != nil {
			return nil, fmt.Errorf("cannot load client cert or key file: %s", err)
		}

		certificates = []tls.Certificate{cert}
	}
	tlsClientConfig := new(tls.Config)
	if caCertPool != nil {
		tlsClientConfig.RootCAs = caCertPool
		if certificates != nil {
			tlsClientConfig.Certificates = certificates
		}
	}
	return tlsClientConfig, nil
}

// get gets the current value from etcd
func (elc *EtcdLeaderChecker) get(ctx context.Context, out chan<- bool) {
	// send guards the channel send with ctx to avoid blocking on shutdown
	send := func(state bool) {
		select {
		case out <- state:
		case <-ctx.Done():
		}
	}
	// Bound the request: the etcd client retries until the context expires,
	// so without a timeout this would block forever while etcd is unreachable
	// and never report the failure
	getCtx, cancel := context.WithTimeout(ctx, time.Duration(max(elc.Interval, 1000))*time.Millisecond)
	defer cancel()
	resp, err := elc.Get(getCtx, elc.TriggerKey)
	if err != nil {
		elc.Logger.Error("Failed to get value from etcd",
			zap.String("key", elc.TriggerKey),
			zap.Error(err))
		send(false)
		return
	}
	if resp == nil {
		elc.Logger.Error("Received nil response from etcd", zap.String("key", elc.TriggerKey))
		send(false)
		return
	}
	if len(resp.Kvs) == 0 {
		elc.Logger.Sugar().Info("No value found for key ", elc.TriggerKey, " - DCS may not have set it yet")
		send(false)
		return
	}
	for _, kv := range resp.Kvs {
		value := string(kv.Value)
		matches := value == elc.TriggerValue
		elc.Logger.Sugar().Info("Current value from DCS:", value)
		send(matches)
	}
}

// watch monitors value changes from etcd
func (elc *EtcdLeaderChecker) watch(ctx context.Context, out chan<- bool) error {
	elc.Logger.Sugar().Info("Setting WATCH on ", elc.TriggerKey)
	// WithRequireLeader makes the watch fail fast when the etcd server
	// loses its quorum instead of silently returning no events
	watchCtx := clientv3.WithRequireLeader(ctx)
	watchChan := elc.Watch(watchCtx, elc.TriggerKey)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case watchResp, ok := <-watchChan:
			if !ok || watchResp.Canceled || watchResp.Err() != nil {
				// The watch is dead. Any events that occurred while
				// the watch was down may have been lost, so after re-arming
				// the watch we re-fetch the current value via get()
				var watchErr error
				if ok {
					watchErr = watchResp.Err()
				}
				elc.Logger.Error("WATCH on key lost, re-establishing and re-syncing state",
					zap.String("key", elc.TriggerKey),
					zap.Error(watchErr))
				// Back off briefly to avoid a busy loop when etcd is unreachable
				select {
				case <-time.After(time.Second):
				case <-ctx.Done():
					return ctx.Err()
				}
				watchChan = elc.Watch(watchCtx, elc.TriggerKey)
				elc.Logger.Sugar().Info("Resetting cancelled WATCH on ", elc.TriggerKey)
				// Re-fetch the current value: events may have been missed
				// while the watch was down (e.g. a leader change)
				elc.get(ctx, out)
				continue
			}
			for _, event := range watchResp.Events {
				select {
				case out <- string(event.Kv.Value) == elc.TriggerValue:
					elc.Logger.Sugar().Info("Current value from DCS: ", string(event.Kv.Value))
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
}

// GetChangeNotificationStream monitors the leader in etcd
func (elc *EtcdLeaderChecker) GetChangeNotificationStream(ctx context.Context, out chan<- bool) error {
	defer elc.Close()
	go elc.get(ctx, out)
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	return elc.watch(wctx, out)
}
