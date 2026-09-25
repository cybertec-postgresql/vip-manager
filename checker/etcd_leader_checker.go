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
	"go.uber.org/zap/zapcore"
)

// EtcdLeaderChecker is used to check state of the leader key in Etcd
type EtcdLeaderChecker struct {
	*vipconfig.Config
	*clientv3.Client
	getLog    *logThrottler // throttles repeated failures to read the key
	lastValue string        // last value read, to log only changes
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
		Logger:               clientLogger(conf),
	}
	c, err := clientv3.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd at endpoints %v: %w", conf.Endpoints, err)
	}
	return &EtcdLeaderChecker{
		Config: conf,
		Client: c,
		getLog: newLogThrottler(conf.Logger),
	}, nil
}

// clientLogger returns the logger handed over to the etcd client. Unless
// verbose logging is requested, the retry chatter of the client is limited to
// errors, because it repeats once per scan interval while etcd is unreachable.
func clientLogger(conf *vipconfig.Config) *zap.Logger {
	if conf.Verbose {
		return conf.Logger
	}
	return conf.Logger.WithOptions(zap.IncreaseLevel(zapcore.ErrorLevel))
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

// get reads the current value from etcd and sends whether it matches
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
		elc.getLog.error("Failed to get value from etcd",
			zap.String("key", elc.TriggerKey),
			zap.Error(err))
		send(false)
		return
	}
	if resp == nil {
		elc.getLog.error("Received nil response from etcd", zap.String("key", elc.TriggerKey))
		send(false)
		return
	}
	if len(resp.Kvs) == 0 {
		elc.getLog.info("No value found for the key - DCS may not have set it yet",
			zap.String("key", elc.TriggerKey))
		send(false)
		return
	}
	elc.getLog.success("Successfully read the value from etcd again", zap.String("key", elc.TriggerKey))
	for _, kv := range resp.Kvs {
		value := string(kv.Value)
		// the value is read once per interval, so only report changes
		if value != elc.lastValue {
			elc.Logger.Sugar().Info("Current value from DCS: ", value)
			elc.lastValue = value
		}
		send(value == elc.TriggerValue)
	}
}

// GetChangeNotificationStream reads the leader key from etcd once per interval
func (elc *EtcdLeaderChecker) GetChangeNotificationStream(ctx context.Context, out chan<- bool) error {
	defer elc.Close()
	for {
		elc.get(ctx, out)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(elc.Interval) * time.Millisecond):
		}
	}
}
