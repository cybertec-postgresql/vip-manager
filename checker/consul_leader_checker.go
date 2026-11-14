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
	queryLog *logThrottler // throttles repeated failures to read the key
	current  int           // index into Endpoints of the endpoint in use
}

// consulClientConfig turns one endpoint of the configuration into a client
// configuration, rejecting an endpoint that cannot be reached at all.
func consulClientConfig(con *vipconfig.Config, endpoint string) (*api.Config, error) {
	url, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse consul endpoint URL %s: %w", endpoint, err)
	}

	if url.Hostname() == "" {
		return nil, fmt.Errorf("invalid consul endpoint URL: hostname is empty in %s", endpoint)
	}

	return &api.Config{
		Address:  fmt.Sprintf("%s:%s", url.Hostname(), url.Port()),
		Scheme:   url.Scheme,
		WaitTime: time.Second,
		Token:    cmp.Or(con.ConsulToken, ""),
	}, nil
}

// NewConsulLeaderChecker returns a new instance
func NewConsulLeaderChecker(con *vipconfig.Config) (lc *ConsulLeaderChecker, err error) {
	lc = &ConsulLeaderChecker{Config: con, queryLog: newLogThrottler(con.Logger)}

	// every endpoint is checked here, so that a typo in the second one is
	// reported at startup and not hours later, when the first one goes away
	for _, endpoint := range con.Endpoints {
		if _, err = consulClientConfig(con, endpoint); err != nil {
			return nil, err
		}
	}

	if lc.Client, err = lc.clientFor(0); err != nil {
		return nil, err
	}

	return lc, nil
}

// clientFor returns a client for the endpoint at the given index
func (c *ConsulLeaderChecker) clientFor(index int) (*api.Client, error) {
	config, err := consulClientConfig(c.Config, c.Endpoints[index])
	if err != nil {
		return nil, err
	}
	client, err := api.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create consul client for endpoint %s: %w", c.Endpoints[index], err)
	}
	return client, nil
}

// nextEndpoint moves on to the next configured endpoint. Every member of a
// consul cluster answers for the same key, so a member that is unreachable is
// simply skipped - without this, all but the first endpoint were configured in
// vain and the whole point of listing several of them was lost.
func (c *ConsulLeaderChecker) nextEndpoint() {
	if len(c.Endpoints) < 2 {
		return
	}
	next := (c.current + 1) % len(c.Endpoints)
	client, err := c.clientFor(next)
	if err != nil { // cannot happen, the endpoints were checked at startup
		c.Logger.Sugar().Errorf("Cannot use consul endpoint %s: %v", c.Endpoints[next], err)
		return
	}
	c.Logger.Sugar().Infof("Switching from consul endpoint %s to %s", c.Endpoints[c.current], c.Endpoints[next])
	c.current, c.Client = next, client
}

// GetChangeNotificationStream checks the status in the loop
func (c *ConsulLeaderChecker) GetChangeNotificationStream(ctx context.Context, out chan<- bool) error {
	queryOptions := &api.QueryOptions{
		RequireConsistent: true,
	}

checkLoop:
	for {
		kv := c.KV() // the client changes when an endpoint becomes unreachable
		resp, _, err := kv.Get(c.TriggerKey, queryOptions)
		if err != nil {
			if ctx.Err() != nil {
				break checkLoop
			}
			c.queryLog.error(fmt.Sprintf("consul error: %v", err))
			// Signal false on connection error so VIP is removed if endpoint is unreachable
			// Guard the send with ctx to avoid deadlock during shutdown
			select {
			case out <- false:
			case <-ctx.Done():
				break checkLoop
			}
			c.nextEndpoint()
			if !c.wait(ctx) {
				break checkLoop
			}
			continue
		}
		if resp == nil {
			c.queryLog.error(fmt.Sprintf("Cannot get variable for key %s. Will try again in a second.", c.TriggerKey))
			select {
			case out <- false:
			case <-ctx.Done():
				break checkLoop
			}
			if !c.wait(ctx) {
				break checkLoop
			}
			continue
		}

		c.queryLog.success(fmt.Sprintf("Successfully read the key %s from consul again", c.TriggerKey))

		state := string(resp.Value) == c.TriggerValue
		queryOptions.WaitIndex = resp.ModifyIndex

		select {
		case <-ctx.Done():
			break checkLoop
		case out <- state:
			if !c.wait(ctx) {
				break checkLoop
			}
			continue
		}
	}

	return ctx.Err()
}

// wait sleeps for one scan interval and reports whether it is worth carrying
// on. time.Sleep() ignored the context and kept the shutdown waiting for up to
// one interval before the virtual IP could be removed.
func (c *ConsulLeaderChecker) wait(ctx context.Context) bool {
	timer := time.NewTimer(time.Duration(c.Interval) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
