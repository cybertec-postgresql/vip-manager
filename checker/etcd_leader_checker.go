package checker

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/coreos/etcd/client"
)

type EtcdLeaderChecker struct {
	key      string
	nodename string
	kapi     client.KeysAPI
}

func NewEtcdLeaderChecker(endpoint, key, nodename string, etcd_user string, etcd_password string) (*EtcdLeaderChecker, error) {
	e := &EtcdLeaderChecker{key: key, nodename: nodename}

	cfg := client.Config{
		Endpoints:               strings.Split(endpoint, ","),
		Transport:               client.DefaultTransport,
		HeaderTimeoutPerRequest: time.Second,
		Username:				 etcd_user,
		Password:				 etcd_password,
	}

	c, err := client.New(cfg)

	if err != nil {
		return nil, err
	}

	e.kapi = client.NewKeysAPI(c)

	return e, nil
}

func (e *EtcdLeaderChecker) GetChangeNotificationStream(ctx context.Context, out chan<- bool, interval int) error {
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
			time.Sleep(time.Duration(interval) * time.Millisecond)
			continue
		}

		state := resp.Node.Value == e.nodename

		select {
		case <-ctx.Done():
			break checkLoop
		case out <- state:
			time.Sleep(time.Duration(interval) * time.Millisecond)
			continue
		}
	}

	return ctx.Err()
}
