package checker

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"net/http"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
)

// PatroniLeaderChecker will use Patroni REST API to check the trigger value.
// --trigger-key is used to specify the endpoint to check, e.g. /leader.
// --trigger-value is used to specify the HTTP code to expect, e.g. 200.
type PatroniLeaderChecker struct {
	*vipconfig.Config
	*http.Client
	requestLog *logThrottler // throttles repeated request failures
	statusLog  *logThrottler // throttles repeated unexpected status codes
	lastStatus int           // last expected status code, to log only changes
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
		Config:     conf,
		Client:     client,
		requestLog: newLogThrottler(conf.Logger),
		statusLog:  newLogThrottler(conf.Logger),
	}, nil
}

// logStatus reports the status code returned by Patroni. The trigger value and
// 503 are the two answers Patroni gives to a role check, e.g. every replica
// gets 503 from /leader, so they are logged once per change at the info level.
// Any other code points to a problem and is reported as a throttled warning.
func (c *PatroniLeaderChecker) logStatus(url string, code int) {
	if strconv.Itoa(code) != c.TriggerValue && code != http.StatusServiceUnavailable {
		c.statusLog.warn(fmt.Sprintf("REST API returned unexpected status code %d for %s (expected %s or %d)",
			code, url, c.TriggerValue, http.StatusServiceUnavailable))
		return
	}
	c.statusLog.success(fmt.Sprintf("REST API at %s returned expected status code %d again", url, code))
	if code != c.lastStatus {
		c.Logger.Sugar().Info("Current status code from Patroni: ", code)
		c.lastStatus = code
	}
}

// GetChangeNotificationStream checks the status in the loop
func (c *PatroniLeaderChecker) GetChangeNotificationStream(ctx context.Context, out chan<- bool) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(c.Interval) * time.Millisecond):
			url := c.Endpoints[0] + c.TriggerKey
			r, err := c.Get(url)
			if err != nil {
				c.requestLog.error(fmt.Sprintf("REST API error connecting to %s: %v", url, err))
				// Signal false on connection error so VIP is removed if endpoint is unreachable
				// Guard the send with ctx to avoid deadlock during shutdown
				select {
				case out <- false:
				case <-ctx.Done():
					return nil
				}
				continue
			}
			r.Body.Close() // throw away the body
			c.requestLog.success(fmt.Sprintf("REST API at %s is reachable again", url))
			c.logStatus(url, r.StatusCode)
			select {
			case out <- strconv.Itoa(r.StatusCode) == c.TriggerValue:
			case <-ctx.Done():
				return nil
			}
		}
	}
}
