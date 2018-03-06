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

func (c *ConsulLeaderChecker) GetChangeNotificationStream(ctx context.Context, out chan<- bool, interval int) error {
	kv := c.apiClient.KV()

	queryOptions := &api.QueryOptions{
		RequireConsistent: true,
	}

checkLoop:
	for {
		resp, _, err := kv.Get(c.key, queryOptions)
		if err != nil {
			if ctx.Err() != nil {
				break checkLoop
			}
			log.Printf("consul error: %s", err)
			out <- false
			time.Sleep(time.Duration(interval) * time.Millisecond)
			continue
		}
		if resp == nil {
			log.Printf("Cannot get variable for key %s. Will try again in a second.", c.key)
			out <- false
			time.Sleep(time.Duration(interval) * time.Millisecond)
			continue
		}

		state := string(resp.Value) == c.nodename
		queryOptions.WaitIndex = resp.ModifyIndex

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
