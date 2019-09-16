package checker

import (
	"context"
	"errors"
	"github.com/cybertec-postgresql/vip-manager/vipconfig"
)

var ErrUnsupportedEndpointType = errors.New("given endpoint type not supported")

type LeaderChecker interface {
	GetChangeNotificationStream(ctx context.Context, out chan<- bool) error
}

//func NewLeaderChecker(endpointType, endpoint, key, nodename string, etcd_user string, etcd_password string) (LeaderChecker, error) {
func NewLeaderChecker(con vipconfig.Config) (LeaderChecker, error) {
	var lc LeaderChecker
	var err error

	switch con.Endpoint_type {
	case "consul":
		lc, err = NewConsulLeaderChecker(con)
	case "etcd":
		lc, err = NewEtcdLeaderChecker(con)
	default:
		err = ErrUnsupportedEndpointType
	}

	return lc, err
}
