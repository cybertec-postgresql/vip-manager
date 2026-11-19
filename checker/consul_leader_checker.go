package checker

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
	"github.com/hashicorp/consul/api"
	"go.uber.org/zap"
)

// ConsulLeaderChecker is used to check state of the leader key in Consul
type ConsulLeaderChecker struct {
	*vipconfig.Config
	*api.Client               // the client of the endpoint in use
	clients     []*api.Client // one client per endpoint, in the order of Endpoints
	current     int           // index into Endpoints of the endpoint in use
	queryLog    *logThrottler // throttles repeated failures to read the key
}

// consulClientConfig turns one endpoint of the configuration into a client
// configuration. It rejects an endpoint that has no hostname.
func consulClientConfig(con *vipconfig.Config, endpoint string) (*api.Config, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse consul endpoint URL %s: %w", endpoint, err)
	}

	if u.Hostname() == "" {
		return nil, fmt.Errorf("invalid consul endpoint URL: hostname is empty in %s", endpoint)
	}

	return &api.Config{
		// Host keeps the brackets of an IPv6 address, Hostname() drops them
		Address:  u.Host,
		Scheme:   u.Scheme,
		WaitTime: time.Second,
		Token:    cmp.Or(con.ConsulToken, ""),
	}, nil
}

// NewConsulLeaderChecker returns a new instance
func NewConsulLeaderChecker(con *vipconfig.Config) (lc *ConsulLeaderChecker, err error) {
	lc = &ConsulLeaderChecker{Config: con, queryLog: newLogThrottler(con.Logger)}

	// The client of every endpoint is built here, so that a typo in the second
	// endpoint is reported at startup and not hours later, when the first one
	// goes away.
	for _, endpoint := range con.Endpoints {
		config, err := consulClientConfig(con, endpoint)
		if err != nil {
			return nil, err
		}
		client, err := api.NewClient(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create consul client for endpoint %s: %w", endpoint, err)
		}
		lc.clients = append(lc.clients, client)
	}
	if len(lc.clients) == 0 {
		return nil, fmt.Errorf("no consul endpoint is configured")
	}
	lc.Client = lc.clients[0]

	return lc, nil
}

// nextEndpoint moves on to the next configured endpoint and wraps around after
// the last one. With a single endpoint it does nothing.
func (c *ConsulLeaderChecker) nextEndpoint() {
	c.current = (c.current + 1) % len(c.clients)
	c.Client = c.clients[c.current]
}

// getKey reads the trigger key. Every member of a consul cluster answers for
// the same key, so when the endpoint in use fails, getKey asks the remaining
// endpoints in the same pass. It returns an error only when all of them
// failed, and the checker then keeps the endpoint it started the pass with.
func (c *ConsulLeaderChecker) getKey(ctx context.Context, queryOptions *api.QueryOptions) (*api.KVPair, error) {
	started := c.current
	var firstErr error
	for range c.clients {
		// the context aborts a blocking query when the shutdown starts
		resp, _, err := c.KV().Get(c.TriggerKey, queryOptions.WithContext(ctx))
		if err == nil {
			if c.current != started {
				c.Logger.Sugar().Warnf("Consul endpoint %s failed, using %s now: %v",
					c.Endpoints[started], c.Endpoints[c.current], firstErr)
			}
			return resp, nil
		}
		if ctx.Err() != nil {
			return nil, err
		}
		if firstErr == nil {
			firstErr = err
		}
		c.nextEndpoint()
	}
	return nil, firstErr
}

// GetChangeNotificationStream checks the status in the loop
func (c *ConsulLeaderChecker) GetChangeNotificationStream(ctx context.Context, out chan<- bool) error {
	queryOptions := &api.QueryOptions{
		RequireConsistent: true,
	}

checkLoop:
	for {
		resp, err := c.getKey(ctx, queryOptions)
		if err != nil {
			if ctx.Err() != nil {
				break checkLoop
			}
			// The message is constant on purpose. The error text names the
			// endpoint, and a message that changes restarts the throttling.
			c.queryLog.error("Cannot read the key from any consul endpoint", zap.Error(err))
			// Signal false on connection error so VIP is removed if endpoint is unreachable
			// Guard the send with ctx to avoid deadlock during shutdown
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
