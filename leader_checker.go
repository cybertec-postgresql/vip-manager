package main

import (
	"context"
	"log"
	"time"

	"github.com/coreos/etcd/client"
)

type EtcdLeaderChecker struct {
	key      string
	nodename string
	kapi     client.KeysAPI
}

func NewEtcdLeaderChecker(endpoint, key, nodename string) *EtcdLeaderChecker {
	e := &EtcdLeaderChecker{key: key, nodename: nodename}

	cfg := client.Config{
		Endpoints:               []string{endpoint},
		Transport:               client.DefaultTransport,
		HeaderTimeoutPerRequest: time.Second,
	}

	c, err := client.New(cfg)

	if err != nil {
		panic(err)
	}

	e.kapi = client.NewKeysAPI(c)

	return e
}

func (e *EtcdLeaderChecker) GetChangeNotificationStream(ctx context.Context, out chan<- bool) error {
	resp, err := e.kapi.Get(ctx, e.key, &client.GetOptions{Quorum: true})
	if err != nil {
		panic(err)
	}

	state := resp.Node.Value == e.nodename
	out <- state

	after := resp.Node.ModifiedIndex

	w := e.kapi.Watcher(e.key, &client.WatcherOptions{AfterIndex: after, Recursive: false})
checkLoop:
	for {
		resp, err := w.Next(ctx)

		if err != nil {
			if ctx.Err() != nil {
				break checkLoop
			}
			out <- false
			log.Printf("etcd error: %s", err)
			time.Sleep(1 * time.Second)
			continue
		}

		state = resp.Node.Value == e.nodename

		select {
		case <-ctx.Done():
			break checkLoop
		case out <- state:
			continue
		}
	}

	return ctx.Err()
}
