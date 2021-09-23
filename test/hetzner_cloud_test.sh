#!/usr/bin/env sh

set -eu -o pipefail
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

# testing parameters
dev=`ip link show | grep -B1 ether | cut -d ":" -f2 | head -n1 | cut -d " " -f2`
vip=192.168.1.12

HETZNER_TOKEN=
HETZNER_SERVER_ID=
HETZNER_IP_ID=

function cleanup {
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

CURL_AUTH="Authorization: Bearer $HETZNER_TOKEN"

# prerequisite test 0: vip should not yet be registered
current_id=$(curl -s -H "$CURL_AUTH" "https://api.hetzner.cloud/v1/floating_ips/$HETZNER_IP_ID" | jq '.floating_ip.server')
[ $current_id != $HETZNER_SERVER_ID ]

# run etcd with podman/docker maybe?
# podman rm etcd || true
docker stop etcd || true
docker run --rm --name etcd -p 2379:2379 -e "ETCD_ENABLE_V2=true" -e "ALLOW_NONE_AUTHENTICATION=yes" bitnami/etcd &

# run etcd locally maybe?
#etcd --enable-v2 &
#echo $! > .etcdPid
sleep 2

curl -s -XDELETE http://127.0.0.1:2379/v2/keys/service/pgcluster/leader ||true

touch .failed
./vip-manager --interface $dev --ip $vip --netmask 32 --trigger-key service/pgcluster/leader \
              --trigger-value $HOSTNAME --manager-type hetzner_floating_ip \
              --hetzner-cloud-token=$HETZNER_TOKEN --hetzner-cloud-server-id=$HETZNER_SERVER_ID \
              --hetzner-cloud-ip-id=$HETZNER_IP_ID & #2>&1 &
echo $! > .vipPid
sleep 5

# test 1: vip should still not be registered
current_id=$(curl -s -H "$CURL_AUTH" "https://api.hetzner.cloud/v1/floating_ips/$HETZNER_IP_ID" | jq '.floating_ip.server')
[ $current_id != $HETZNER_SERVER_ID ]

# simulate patroni member promoting to leader
curl -s -XPUT http://127.0.0.1:2379/v2/keys/service/pgcluster/leader -d value=$HOSTNAME | jq .
sleep 5

# test 2: vip should now be registered
current_id=$(curl -s -H "$CURL_AUTH" "https://api.hetzner.cloud/v1/floating_ips/$HETZNER_IP_ID" | jq '.floating_ip.server')
[ $current_id == $HETZNER_SERVER_ID ]

# simulate leader change

curl -s -XPUT http://127.0.0.1:2379/v2/keys/service/pgcluster/leader -d value=0xGARBAGE | jq .
sleep 5

# test 3: vip should be deregistered again
current_id=$(curl -s -H "$CURL_AUTH" "https://api.hetzner.cloud/v1/floating_ips/$HETZNER_IP_ID" | jq '.floating_ip.server')
[ $current_id != $HETZNER_SERVER_ID ]

rm .failed

echo -e "${GREEN}### You've reached the end of the script, all \"tests\" have successfully been passed! ###${NC}"
