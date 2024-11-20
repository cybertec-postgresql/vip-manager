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
		return nil, err
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
	return &EtcdLeaderChecker{conf, c}, err
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

// get gets the current leader from etcd
func (elc *EtcdLeaderChecker) get(ctx context.Context, out chan<- bool) {
	resp, err := elc.Get(ctx, elc.TriggerKey)
	if err != nil {
		elc.Logger.Error("Failed to get etcd value:", zap.Error(err))
		out <- false
		return
	}
	for _, kv := range resp.Kvs {
		elc.Logger.Sugar().Info("Current leader from DCS:", string(kv.Value))
		out <- string(kv.Value) == elc.TriggerValue
	}
}

// watch monitors the leader change from etcd
func (elc *EtcdLeaderChecker) watch(ctx context.Context, out chan<- bool) error {
	elc.Logger.Sugar().Info("Setting WATCH on ", elc.TriggerKey)
	watchChan := elc.Watch(ctx, elc.TriggerKey)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case watchResp := <-watchChan:
			if watchResp.Canceled {
				watchChan = elc.Watch(ctx, elc.TriggerKey)
				elc.Logger.Sugar().Info("Resetting cancelled WATCH on ", elc.TriggerKey)
				continue
			}
			if err := watchResp.Err(); err != nil {
				elc.get(ctx, out) // RPC failed, try to get the key directly to be on the safe side
				continue
			}
			for _, event := range watchResp.Events {
				out <- string(event.Kv.Value) == elc.TriggerValue
				elc.Logger.Sugar().Info("Current leader from DCS: ", string(event.Kv.Value))
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
