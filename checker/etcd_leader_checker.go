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

type EtcdLeaderChecker struct {
	key      string
	nodename string
	kapi     client.KeysAPI
}

//naming this c_conf to avoid conflict with conf in etcd_leader_checker.go
var e_conf vipconfig.Config

func getTransport() (client.CancelableTransport, error) {
	if os.Getenv("ETCD_CLIENT_CERT_AUTH") != "true" {
		return client.DefaultTransport, nil
	}

	cert, err := ioutil.ReadFile(os.Getenv("ETCD_TRUSTED_CA_FILE"))
	if err != nil {
		return nil, fmt.Errorf("cannot load CA file: %s", err)
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(cert)

	cer, err := tls.LoadX509KeyPair(os.Getenv("ETCD_CERT_FILE"), os.Getenv("ETCD_KEY_FILE"))
	if err != nil {
		return nil, fmt.Errorf("cannot load client cert or key file: %s", err)
	}

	tlsClientConfig := &tls.Config{
		RootCAs:      caCertPool,
		Certificates: []tls.Certificate{cer},
	}

	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		Dial: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).Dial,
		TLSClientConfig: tlsClientConfig,
		TLSHandshakeTimeout: 10 * time.Second,
	}, nil
}

//func NewEtcdLeaderChecker(endpoint, key, nodename string, etcd_user string, etcd_password string) (*EtcdLeaderChecker, error) {
func NewEtcdLeaderChecker(con vipconfig.Config) (*EtcdLeaderChecker, error) {
	e_conf = con
	e := &EtcdLeaderChecker{key: e_conf.Key, nodename: e_conf.Nodename}

	transport, err := getTransport()
	if err != nil {
		return nil, err
	}

	cfg := client.Config{
		Endpoints:               e_conf.Endpoints,
		Transport:               transport,
		HeaderTimeoutPerRequest: time.Second,
		Username:                e_conf.Etcd_user,
		Password:                e_conf.Etcd_password,
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
			time.Sleep(time.Duration(e_conf.Interval) * time.Millisecond)
			continue
		}

		state := resp.Node.Value == e.nodename

		select {
		case <-ctx.Done():
			break checkLoop
		case out <- state:
			time.Sleep(time.Duration(e_conf.Interval) * time.Millisecond)
			continue
		}
	}

	return ctx.Err()
}
