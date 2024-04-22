package checker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	rpcv3 "go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"
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
		DialKeepAliveTimeout: 5 * time.Second,
		DialKeepAliveTime:    5 * time.Second,
		Username:             conf.EtcdUser,
		Password:             conf.EtcdPassword,
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
	resp, err := elc.Get(ctx, elc.Key)
	if err != nil {
		log.Printf("etcd error: %s", err)
		out <- false
		return
	}
	for _, kv := range resp.Kvs {
		log.Printf("Current Leader from DCS: %s", kv.Value)
		out <- string(kv.Value) == elc.Nodename
	}
}

// watch monitors the leader change from etcd
func (elc *EtcdLeaderChecker) watch(ctx context.Context, out chan<- bool) error {
	watchChan := elc.Watch(ctx, elc.Key)
	log.Println("set WATCH on " + elc.Key)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case watchResp := <-watchChan:
			if err := watchResp.Err(); err != nil {
				if errors.Is(err, rpcv3.ErrCompacted) { // revision is compacted, try direct get key
					elc.get(ctx, out)
				} else {
					log.Printf("etcd watcher returned error: %s", err)
					out <- false
				}
				continue
			}
			for _, event := range watchResp.Events {
				out <- string(event.Kv.Value) == elc.Nodename
				log.Printf("Current Leader from DCS: %s", event.Kv.Value)
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
