package checker

import (
	"context"
	"errors"
)

var ErrUnsupportedEndpointType = errors.New("given endpoint type not supported")

type LeaderChecker interface {
	GetChangeNotificationStream(ctx context.Context, out chan<- bool, interval int) error
}

func NewLeaderChecker(endpointType, endpoint, key, nodename string, etcd_user string, etcd_password string) (LeaderChecker, error) {
	var lc LeaderChecker
	var err error

	switch endpointType {
	case "consul":
		lc, err = NewConsulLeaderChecker(endpoint, key, nodename)
	case "etcd":
		lc, err = NewEtcdLeaderChecker(endpoint, key, nodename, etcd_user, etcd_password)
	default:
		err = ErrUnsupportedEndpointType
	}

	return lc, err
}
