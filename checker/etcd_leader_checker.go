package checker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/coreos/etcd/client"
	"github.com/cybertec-postgresql/vip-manager/vipconfig"
)

// EtcdLeaderChecker is used to check state of the leader key in Etcd
type EtcdLeaderChecker struct {
	key      string
	nodename string
	kapi     client.KeysAPI
}

//naming this c_conf to avoid conflict with conf in etcd_leader_checker.go
var eConf *vipconfig.Config

func getTransport(conf *vipconfig.Config) (client.CancelableTransport, error) {
	var caCertPool *x509.CertPool

	// create valid CertPool only if the ca certificate file exists
	if conf.EtcdCAFile != "" {
		caCert, err := ioutil.ReadFile(conf.EtcdCAFile)
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

	// TODO: make these timeouts adjustable
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		Dial: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).Dial,
		TLSClientConfig:     tlsClientConfig,
		TLSHandshakeTimeout: 10 * time.Second,
	}, nil
}

// NewEtcdLeaderChecker returns a new instance
func NewEtcdLeaderChecker(con *vipconfig.Config) (*EtcdLeaderChecker, error) {
	eConf = con
	e := &EtcdLeaderChecker{key: eConf.Key, nodename: eConf.Nodename}

	transport, err := getTransport(eConf)
	if err != nil {
		return nil, err
	}

	cfg := client.Config{
		Endpoints:               eConf.Endpoints,
		Transport:               transport,
		HeaderTimeoutPerRequest: time.Second,
		Username:                eConf.EtcdUser,
		Password:                eConf.EtcdPassword,
	}
	c, err := client.New(cfg)
	if err != nil {
		return nil, err
	}
	e.kapi = client.NewKeysAPI(c)
	return e, nil
}

// GetChangeNotificationStream checks the status in the loop
func (e *EtcdLeaderChecker) GetChangeNotificationStream(ctx context.Context, out chan<- bool) error {
	clientOptions := &client.GetOptions{
		Quorum:    true,
		Recursive: false,
	}

checkLoop:
	for {
		resp, err := e.kapi.Get(ctx, e.key, clientOptions)

		if err != nil {
			if ctx.Err() != nil {
				break checkLoop
			}
			log.Printf("etcd error: %s", err)
			out <- false
			time.Sleep(time.Duration(eConf.Interval) * time.Millisecond)
			continue
		}

		state := resp.Node.Value == e.nodename

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
