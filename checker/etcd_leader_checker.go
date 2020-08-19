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
	"os"
	"time"

	"github.com/coreos/etcd/client"
	"github.com/cybertec-postgresql/vip-manager/vipconfig"
)

const (
	envEtcdCaFile   = "ETCD_TRUSTED_CA_FILE"
	envEtcdCertFile = "ETCD_CERT_FILE"
	envEtcdKeyFile  = "ETCD_KEY_FILE"
)

type EtcdLeaderChecker struct {
	key      string
	nodename string
	kapi     client.KeysAPI
}

//naming this c_conf to avoid conflict with conf in etcd_leader_checker.go
var eConf vipconfig.Config

func getConfigParameter(conf string, env string) string {
	if conf == "none" || conf == "" {
		return os.Getenv(env)
	}

	return conf
}

func getTransport(conf vipconfig.Config) (client.CancelableTransport, error) {
	var caCertPool *x509.CertPool

	// create valid CertPool only if the ca certificate file exists
	if caCertFile := getConfigParameter(conf.Etcd_ca_file, envEtcdCaFile); caCertFile != "" {
		caCert, err := ioutil.ReadFile(caCertFile)
		if err != nil {
			return nil, fmt.Errorf("cannot load CA file: %s", err)
		}

		caCertPool = x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
	}

	certFile := getConfigParameter(conf.Etcd_cert_file, envEtcdCertFile)

	keyFile := getConfigParameter(conf.Etcd_key_file, envEtcdKeyFile)

	var certificates []tls.Certificate

	// create valid []Certificate only if the client cert and key files exists
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("cannot load client cert or key file: %s", err)
		}

		certificates = []tls.Certificate{cert}
	}

	var tlsClientConfig *tls.Config

	if certificates != nil || caCertPool != nil {
		tlsClientConfig = &tls.Config{
			RootCAs:      caCertPool,
			Certificates: certificates,
		}
	}

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

//func NewEtcdLeaderChecker(endpoint, key, nodename string, etcd_user string, etcd_password string) (*EtcdLeaderChecker, error) {
func NewEtcdLeaderChecker(con vipconfig.Config) (*EtcdLeaderChecker, error) {
	eConf = con
	e := &EtcdLeaderChecker{key: eConf.Key, nodename: eConf.Nodename}

	transport, err := getTransport(e_conf)
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
