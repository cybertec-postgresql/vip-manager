package checker

import (
	"context"
	"strconv"
	"time"

	"net/http"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
)

// PatroniLeaderChecker will use Patroni REST API to check the leader.
// --trigger-key is used to specify the endpoint to check, e.g. /leader.
// --trigger-value is used to specify the HTTP code to expect, e.g. 200.
type PatroniLeaderChecker struct {
	*vipconfig.Config
	*http.Client
}

// NewPatroniLeaderChecker returns a new instance
func NewPatroniLeaderChecker(conf *vipconfig.Config) (*PatroniLeaderChecker, error) {
	tlsConfig, err := getTransport(conf)
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   time.Second,
	}

	return &PatroniLeaderChecker{
		Config: conf,
		Client: client,
	}, nil
}

// GetChangeNotificationStream checks the status in the loop
func (c *PatroniLeaderChecker) GetChangeNotificationStream(ctx context.Context, out chan<- bool) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(c.Interval) * time.Millisecond):
			r, err := c.Client.Get(c.Endpoints[0] + c.TriggerKey)
			if err != nil {
				c.Logger.Sugar().Error("patroni REST API error:", err)
				continue
			}
			r.Body.Close() //throw away the body
			out <- strconv.Itoa(r.StatusCode) == c.TriggerValue
		}
	}
}
