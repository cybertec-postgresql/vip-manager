package checker

import (
	"context"
	"log"
	"net/url"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"github.com/hashicorp/consul/api"
)

type ConsulLeaderChecker struct {
	key       string
	nodename  string
	apiClient *api.Client
}

//naming this c_conf to avoid conflict with conf in etcd_leader_checker.go
var c_conf vipconfig.Config

//func NewConsulLeaderChecker(endpoint, key, nodename string) (*ConsulLeaderChecker, error) {
func NewConsulLeaderChecker(con vipconfig.Config) (*ConsulLeaderChecker, error) {
	c_conf = con

	lc := &ConsulLeaderChecker{
		key:      c_conf.Key,
		nodename: c_conf.Nodename,
	}

	url, err := url.Parse(c_conf.Endpoints[0])
	if err != nil {
		return nil, err
	}
	address := url.Hostname() + ":" + url.Port()

	config := &api.Config{
		Address:  address,
		Scheme:   url.Scheme,
		WaitTime: time.Second,
	}

	if c_conf.Consul_token != ""{
		config.Token = c_conf.Consul_token
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
			time.Sleep(time.Duration(c_conf.Interval) * time.Millisecond)
			continue
		}
		if resp == nil {
			log.Printf("Cannot get variable for key %s. Will try again in a second.", c.key)
			out <- false
			time.Sleep(time.Duration(c_conf.Interval) * time.Millisecond)
			continue
		}

		state := string(resp.Value) == c.nodename
		queryOptions.WaitIndex = resp.ModifyIndex

		select {
		case <-ctx.Done():
			break checkLoop
		case out <- state:
			time.Sleep(time.Duration(c_conf.Interval) * time.Millisecond)
			continue
		}
	}

	return ctx.Err()
}
