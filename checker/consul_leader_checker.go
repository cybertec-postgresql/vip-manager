package checker

import (
	"context"
	"log"
	"net/url"
	"time"

	"github.com/hashicorp/consul/api"
)

type ConsulLeaderChecker struct {
	key       string
	nodename  string
	apiClient *api.Client
}

func NewConsulLeaderChecker(endpoint, key, nodename string) (*ConsulLeaderChecker, error) {
	lc := &ConsulLeaderChecker{
		key:      key,
		nodename: nodename,
	}

	url, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	address := url.Hostname() + ":" + url.Port()

	config := &api.Config{
		Address:  address,
		Scheme:   url.Scheme,
		WaitTime: time.Second,
	}

	apiClient, err := api.NewClient(config)
	if err != nil {
		return nil, err
	}

	lc.apiClient = apiClient

	return lc, nil
}

func (c *ConsulLeaderChecker) GetChangeNotificationStream(ctx context.Context, out chan<- bool) error {
	kv := c.apiClient.KV()
	resp, _, err := kv.Get(c.key, &api.QueryOptions{RequireConsistent: true})
	if err != nil {
		return err
	}

	if resp == nil {
		return ErrKeyDoesNotExist
	}

	state := string(resp.Value) == c.nodename
	out <- state

	after := resp.ModifyIndex

checkLoop:
	for {
		queryOptions := &api.QueryOptions{
			RequireConsistent: true,
			WaitIndex:         after,
		}

		resp, _, err := kv.Get(c.key, queryOptions)
		if err != nil {
			if ctx.Err() != nil {
				break checkLoop
			}
			out <- false
			log.Printf("consul error: %s", err)
			time.Sleep(1 * time.Second)
			continue
		}

		after = resp.ModifyIndex
		state = string(resp.Value) == c.nodename

		select {
		case <-ctx.Done():
			break checkLoop
		case out <- state:
			continue
		}
	}

	return ctx.Err()
}
