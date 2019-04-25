package checker

import (
	"context"
	"log"
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

//func NewEtcdLeaderChecker(endpoint, key, nodename string, etcd_user string, etcd_password string) (*EtcdLeaderChecker, error) {
func NewEtcdLeaderChecker(con vipconfig.Config) (*EtcdLeaderChecker, error) {
	e_conf = con
	e := &EtcdLeaderChecker{key: e_conf.Key, nodename: e_conf.Nodename}

	cfg := client.Config{
		Endpoints:               e_conf.Endpoints,
		Transport:               client.DefaultTransport,
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
