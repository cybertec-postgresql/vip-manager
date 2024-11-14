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
}

// NewPatroniLeaderChecker returns a new instance
func NewPatroniLeaderChecker(conf *vipconfig.Config) (*PatroniLeaderChecker, error) {
	return &PatroniLeaderChecker{conf}, nil
}

// GetChangeNotificationStream checks the status in the loop
func (c *PatroniLeaderChecker) GetChangeNotificationStream(ctx context.Context, out chan<- bool) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(c.Interval) * time.Millisecond):
			r, err := http.Get(c.Endpoints[0] + c.TriggerKey)
			if err != nil {
				c.Logger.Sugar().Error("patroni REST API error:", err)
				continue
			}
			out <- strconv.Itoa(r.StatusCode) == c.TriggerValue
		}
	}
}
