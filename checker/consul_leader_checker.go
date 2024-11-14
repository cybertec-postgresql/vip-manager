package checker

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"github.com/hashicorp/consul/api"
)

// ConsulLeaderChecker is used to check state of the leader key in Consul
type ConsulLeaderChecker struct {
	*vipconfig.Config
	*api.Client
}

// NewConsulLeaderChecker returns a new instance
func NewConsulLeaderChecker(con *vipconfig.Config) (lc *ConsulLeaderChecker, err error) {
	lc = &ConsulLeaderChecker{Config: con}

	url, err := url.Parse(con.Endpoints[0])
	if err != nil {
		return nil, err
	}

	config := &api.Config{
		Address:  fmt.Sprintf("%s:%s", url.Hostname(), url.Port()),
		Scheme:   url.Scheme,
		WaitTime: time.Second,
		Token:    cmp.Or(con.ConsulToken, ""),
	}

	if lc.Client, err = api.NewClient(config); err != nil {
		return nil, err
	}

	return lc, nil
}

// GetChangeNotificationStream checks the status in the loop
func (c *ConsulLeaderChecker) GetChangeNotificationStream(ctx context.Context, out chan<- bool) error {
	kv := c.Client.KV()

	queryOptions := &api.QueryOptions{
		RequireConsistent: true,
	}

checkLoop:
	for {
		resp, _, err := kv.Get(c.TriggerKey, queryOptions)
		if err != nil {
			if ctx.Err() != nil {
				break checkLoop
			}
			c.Logger.Sugar().Error("consul error: ", err)
			out <- false
			time.Sleep(time.Duration(c.Interval) * time.Millisecond)
			continue
		}
		if resp == nil {
			c.Logger.Sugar().Errorf("Cannot get variable for key %s. Will try again in a second.", c.TriggerKey)
			out <- false
			time.Sleep(time.Duration(c.Interval) * time.Millisecond)
			continue
		}

		state := string(resp.Value) == c.TriggerValue
		queryOptions.WaitIndex = resp.ModifyIndex

		select {
		case <-ctx.Done():
			break checkLoop
		case out <- state:
			time.Sleep(time.Duration(c.Interval) * time.Millisecond)
			continue
		}
	}

	return ctx.Err()
}
