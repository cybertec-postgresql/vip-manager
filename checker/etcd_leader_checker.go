package checker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	client "go.etcd.io/etcd/client/v3"
)

// EtcdLeaderChecker is used to check state of the leader key in Etcd
type EtcdLeaderChecker struct {
	key      string
	nodename string
	kapi     client.KV
}

// naming this c_conf to avoid conflict with conf in etcd_leader_checker.go
var eConf *vipconfig.Config

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

// NewEtcdLeaderChecker returns a new instance
func NewEtcdLeaderChecker(con *vipconfig.Config) (*EtcdLeaderChecker, error) {
	eConf = con
	e := &EtcdLeaderChecker{key: eConf.Key, nodename: eConf.Nodename}

	tlsConfig, err := getTransport(eConf)
	if err != nil {
		return nil, err
	}

	cfg := client.Config{
		Endpoints:            eConf.Endpoints,
		TLS:                  tlsConfig,
		DialKeepAliveTimeout: time.Second,
		Username:             eConf.EtcdUser,
		Password:             eConf.EtcdPassword,
	}
	c, err := client.New(cfg)
	if err != nil {
		return nil, err
	}
	e.kapi = c.KV
	return e, nil
}

// GetChangeNotificationStream checks the status in the loop
func (e *EtcdLeaderChecker) GetChangeNotificationStream(ctx context.Context, out chan<- bool) error {
	var state bool
checkLoop:
	for {
		resp, err := e.kapi.Get(ctx, e.key)

		if err != nil {
			if ctx.Err() != nil {
				break checkLoop
			}
			log.Printf("etcd error: %s", err)
			out <- false
			time.Sleep(time.Duration(eConf.Interval) * time.Millisecond)
			continue
		}

		for _, kv := range resp.Kvs {
			state = string(kv.Value) == e.nodename
		}

		select {
		case <-ctx.Done():
			break checkLoop
		case out <- state:
			time.Sleep(time.Duration(eConf.Interval) * time.Millisecond)
			continue
		}
	}

	return ctx.Err()
}
