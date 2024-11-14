package checker

import (
	"context"
	"net/url"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"github.com/hashicorp/consul/api"
)

// ConsulLeaderChecker is used to check state of the leader key in Consul
type ConsulLeaderChecker struct {
	key       string
	nodename  string
	apiClient *api.Client
}

// naming this cConf to avoid conflict with conf in etcd_leader_checker.go
var cConf *vipconfig.Config

// NewConsulLeaderChecker returns a new instance
func NewConsulLeaderChecker(con *vipconfig.Config) (*ConsulLeaderChecker, error) {
	cConf = con
	lc := &ConsulLeaderChecker{
		key:      cConf.TriggerKey,
		nodename: cConf.TriggerValue,
	}

	url, err := url.Parse(cConf.Endpoints[0])
	if err != nil {
		return nil, err
	}
	address := url.Hostname() + ":" + url.Port()

	config := &api.Config{
		Address:  address,
		Scheme:   url.Scheme,
		WaitTime: time.Second,
	}

	if cConf.ConsulToken != "" {
		config.Token = cConf.ConsulToken
	}

	apiClient, err := api.NewClient(config)
	if err != nil {
		return nil, err
	}

	lc.apiClient = apiClient

	return lc, nil
}

// GetChangeNotificationStream checks the status in the loop
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
			cConf.Logger.Sugar().Error("consul error: ", err)
			out <- false
			time.Sleep(time.Duration(cConf.Interval) * time.Millisecond)
			continue
		}
		if resp == nil {
			cConf.Logger.Sugar().Errorf("Cannot get variable for key %s. Will try again in a second.", c.key)
			out <- false
			time.Sleep(time.Duration(cConf.Interval) * time.Millisecond)
			continue
		}

		state := string(resp.Value) == c.nodename
		queryOptions.WaitIndex = resp.ModifyIndex

		select {
		case <-ctx.Done():
			break checkLoop
		case out <- state:
			time.Sleep(time.Duration(cConf.Interval) * time.Millisecond)
			continue
		}
	}

	return ctx.Err()
}
