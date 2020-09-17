#!/bin/sh


set -eu -o pipefail
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

# testing parameters
dev=`ip link show | grep -B1 ether | cut -d ":" -f2 | head -n1 | cut -d " " -f2`
vip=10.0.2.123

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
    podman stop etcd
}
trap cleanup EXIT

# prerequisite test 0: vip should not yet be registered
! ip address show dev $dev | grep $vip

# run etcd with podman/docker maybe?
podman run --rm -d --name etcd -p 2379:2379 -e "ETCD_ENABLE_V2=true" -e "ALLOW_NONE_AUTHENTICATION=yes" -v `pwd`/test/certs/:/certs:Z quay.io/coreos/etcd /usr/local/bin/etcd --trusted-ca-file=/certs/etcd_server_ca.crt --client-cert-auth --cert-file=/certs/etcd_server.crt --key-file=/certs/etcd_server.key --listen-client-urls https://127.0.0.1:2379 --advertise-client-urls https://127.0.0.1:2379

sleep 2

# simulate server, e.g. postgres
ncat -vlk 0.0.0.0 12345  -e "/bin/echo $HOSTNAME" &
echo $! > .ncatPid

curl -s --cert test/certs/etcd_client.crt --key test/certs/etcd_client.key --cacert test/certs/etcd_server_ca.crt -XDELETE https://127.0.0.1:2379/v2/keys/service/pgcluster/leader ||true

touch .failed
./vip-manager --etcd-cert-file test/certs/etcd_client.crt --etcd-key-file test/certs/etcd_client.key --etcd-ca-file test/certs/etcd_server_ca.crt --dcs-endpoints https://127.0.0.1:2379 --interface $dev --ip $vip --netmask 32 --trigger-key service/pgcluster/leader --trigger-value $HOSTNAME &> vip-manager.log &
echo $! > .vipPid
sleep 2

# test 1: vip should still not be registered
! ip address show dev $dev | grep $vip

# simulate patroni member promoting to leader
curl -s --cert test/certs/etcd_client.crt --key test/certs/etcd_client.key --cacert test/certs/etcd_server_ca.crt -XPUT https://127.0.0.1:2379/v2/keys/service/pgcluster/leader -d value=$HOSTNAME | jq .
sleep 2

# we're just checking whether vip-manager picked up the change, for some reason, we can't run an elevated container of quay.io/coreos/etcd
grep 'state is false, desired true' vip-manager.log

rm .failed
echo -e "${GREEN}### You've reached the end of the script, all \"tests\" have successfully been passed! ###${NC}"
