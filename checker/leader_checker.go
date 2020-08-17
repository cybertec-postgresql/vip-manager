package checker

import (
	"context"
	"errors"

	"github.com/cybertec-postgresql/vip-manager/vipconfig"
)

// ErrUnsupportedEndpointType is returned for an unsupported endpoint
var ErrUnsupportedEndpointType = errors.New("given endpoint type not supported")

// LeaderChecker is the interface for checking leadership
type LeaderChecker interface {
	GetChangeNotificationStream(ctx context.Context, out chan<- bool) error
}

// NewLeaderChecker returns a new LeaderChecker instance depending on the configuration
func NewLeaderChecker(con *vipconfig.Config) (LeaderChecker, error) {
	var lc LeaderChecker
	var err error

	switch con.EndpointType {
	case "consul":
		lc, err = NewConsulLeaderChecker(con)
	case "etcd":
		lc, err = NewEtcdLeaderChecker(con)
	default:
		err = ErrUnsupportedEndpointType
	}

	return lc, err
}
