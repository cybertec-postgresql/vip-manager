#!/bin/bash


set -eu -o pipefail
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

# testing parameters
vip=10.0.2.123

function get_dev {
    # select a suitable device for testing purposes
    # * a device that is an "ether"
    # * state is UP not DOWN
    # * and isn't a nil hardware address
    # strip suffix from name (veth3@if8 -> veth3)
    ip -oneline link show | grep link/ether | grep state.UP | grep -v 00:00:00:00:00:00 | cut -d ":" -f2 | cut -d "@" -f 1 | head -n1
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
        #rm vip-manager.log
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
    #podman stop etcd
}
trap cleanup EXIT

# prerequisite test 0: vip should not yet be registered
! ip address show dev $dev | grep $vip

# run etcd with podman/docker maybe?
# podman rm etcd || true
# podman run --rm -d --name etcd -p 2379:2379 -e "ETCD_ENABLE_V2=true" -e "ALLOW_NONE_AUTHENTICATION=yes" -v `pwd`/test/certs/:/certs:Z quay.io/coreos/etcd /usr/local/bin/etcd --trusted-ca-file=/certs/etcd_server_ca.crt --client-cert-auth --cert-file=/certs/etcd_server.crt --key-file=/certs/etcd_server.key --listen-client-urls https://127.0.0.1:2379 --advertise-client-urls https://127.0.0.1:2379

# run etcd locally maybe?
#etcd --enable-v2 --trusted-ca-file=test/certs/etcd_server_ca.crt --client-cert-auth --cert-file=test/certs/etcd_server.crt --key-file=test/certs/etcd_server.key --listen-client-urls https://127.0.0.1:2379  --advertise-client-urls https://127.0.0.1:2379 &
#echo $! > .etcdPid
sleep 2

# simulate server, e.g. postgres
ncat -vlk 0.0.0.0 12345  -e "/bin/echo $HOSTNAME" &
echo $! > .ncatPid

etcdctl --cert test/certs/etcd_client.crt --key test/certs/etcd_client.key --cacert test/certs/etcd_server_ca.crt del service/pgcluster/leader || true

touch .failed
./vip-manager --etcd-cert-file test/certs/etcd_client.crt --etcd-key-file test/certs/etcd_client.key --etcd-ca-file test/certs/etcd_server_ca.crt --dcs-endpoints https://127.0.0.1:2379 --interface $dev --ip $vip --netmask 32 --trigger-key service/pgcluster/leader --trigger-value $HOSTNAME &> vip-manager.log &
echo $! > .vipPid
sleep 2

# test 1: vip should still not be registered
! ip address show dev $dev | grep $vip

# simulate patroni member promoting to leader
etcdctl --cert test/certs/etcd_client.crt --key test/certs/etcd_client.key --cacert test/certs/etcd_server_ca.crt put service/pgcluster/leader $HOSTNAME
sleep 2

# we're just checking whether vip-manager picked up the change, for some reason, we can't run an elevated container of quay.io/coreos/etcd
grep 'state is false, desired true' vip-manager.log

rm .failed
echo -e "${GREEN}### You've reached the end of the script, all \"tests\" have successfully been passed! ###${NC}"
