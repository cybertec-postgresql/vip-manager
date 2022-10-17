#!/bin/sh


set -eu -o pipefail
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

# testing parameters
vip=10.0.2.123

function get_dev {
    # select a suitable device for testing purposes
    # * a device that is an "ether"
    # * and isn't a nil hardware address
    # strip suffix from name (veth3@if8 -> veth3)
    ip -oneline link show | grep link/ether | grep -v 00:00:00:00:00:00 | cut -d ":" -f2 | cut -d "@" -f 1 | head -n1
}

dev="`get_dev`"
# prerequisite test: do we have a suitable device?
test -n "$dev"

#cleanup
function cleanup {
    if test -f .ncatPid
    then
        kill `cat .ncatPid` 2> /dev/null || true
        rm .ncatPid
    fi
    if test -f .vipPid
    then
        kill `cat .vipPid` 2> /dev/null || true
        rm .vipPid
    fi
    if test -f .etcdPid
    then
        kill `cat .etcdPid` 2> /dev/null || true
        rm .etcdPid
    fi
    if test -f .failed 
    then
        echo -e "${RED}### Some tests failed! ###${NC}"
        rm .failed
    fi
}
trap cleanup EXIT

# prerequisite test: vip should not yet be registered
! ip address show dev $dev | grep $vip

# run etcd with podman/docker maybe?
# podman rm etcd || true
# podman run -d --name etcd -p 2379:2379 -e "ETCD_ENABLE_V2=true" -e "ALLOW_NONE_AUTHENTICATION=yes" bitnami/etcd

# run etcd locally maybe?
#etcd --enable-v2 &
#echo $! > .etcdPid
sleep 2

# simulate server, e.g. postgres
ncat -vlk 0.0.0.0 12345  -e "/bin/echo $HOSTNAME" &
echo $! > .ncatPid

curl -s -XDELETE http://127.0.0.1:2379/v2/keys/service/pgcluster/leader ||true

touch .failed
./vip-manager --interface $dev --ip $vip --netmask 32 --trigger-key service/pgcluster/leader --trigger-value $HOSTNAME & #2>&1 &
echo $! > .vipPid
sleep 2

# test 1: vip should still not be registered
! ip address show dev $dev | grep $vip

# simulate patroni member promoting to leader
curl -s -XPUT http://127.0.0.1:2379/v2/keys/service/pgcluster/leader -d value=$HOSTNAME | jq .
sleep 2

# test 2: vip should now be registered
ip address show dev $dev | grep $vip

ncat -vzw 1 $vip 12345

# simulate leader change

curl -s -XPUT http://127.0.0.1:2379/v2/keys/service/pgcluster/leader -d value=0xGARBAGE | jq .
sleep 2

# test 3: vip should be deregistered again
! ip address show dev $dev | grep $vip

! ncat -vzw 1 $vip 12345

rm .failed
echo -e "${GREEN}### You've reached the end of the script, all \"tests\" have successfully been passed! ###${NC}"
