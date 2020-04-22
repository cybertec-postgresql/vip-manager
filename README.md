# vip-manager

Manages a virtual IP based on state kept in etcd or Consul. Monitors state in etcd 

## building
1. To make sure that internal includes (the vipconfig and the checker package) are satisfied, place the base directory of this project properly into your `$GOPATH`.
    The resulting location should be `$GOPATH/src/github.com/cybertec-postgresql/vip-manager/`. The easiest way to do this is:
    ```go get github.com/cybertec-postgresql/vip-manager```
2. Build the binary using `make`.
3. To build your own .deb or .rpm, `fpm` is required.
    Install it, add it to your path and try running `make package`, which will generate a .deb package and will also convert that into a matching .rpm file.
> note: on debianoids, rpmbuild will be required to create the rpm package...

## Installing on debian

* Install the debian package. Currently you will have to build the package yourself. Prebuilt packages are coming soon.
* Edit `/etc/default/vip-manager`. See the configuration section for details.
* Start and enable vip-manager service with systemctl.

## Installing by hand

* Build the vip-manager binary using go. 
* Install service file from `package/scripts/vip-manager.service` to `/etc/systemd/system/`
* Install configuration file from `package/config/vip-manager.default` to `/etc/default/vip-manager`
* Edit config and start the service.

## Configuration
The configuration can be passed to the executable through argument flags or through a YAML config file. Run `vip-manager --help` to see the available flags.

> The location of the YAML config file can be specified with the -config flag.
> An exemplary config file is installed into `/etc/default/vip-manager_default.yml` or is available in the vipconfig directory in the repository of the software.

These configuration keys are currently mandatory:

| Variable  | Example  | Description                                                                              |
|-----------|----------|------------------------------------------------------------------------------------------|
| VIP_IP    | 10.1.2.3 | Virtual IP that is being managed                                                         |
| VIP_MASK  | 24       | Netmask of the virtual IP                                                                |
| VIP_IFACE | eth0     | Network interface to configure the IP address on. Usually the primary network interface. |
| VIP_KEY   | /service/batman/leader | Key to monitor. Must match `<namespace>/<scope>/leader` from Patroni.      |
| VIP_HOST  | serverX  | Value to trigger on. Must match `<name>` from Patroni.                                   |
| VIP_TYPE  | etcd     | Type of endpoint (etcd or consul)                                                        |
| VIP_ENDPOINT | http://10.1.2.3:2379 | Location of one or more endpoints (etcd or consul). Separate multiple endpoints with commas |

### Configuration - Hetzner

To use vip-manager with Hetzner Robot API you need to configure the
`/etc/hetzner` credentials file.

Hetzner has two kinds of VIPs: the floating-IP and the failover-IP.

For both kinds of VIPs you'll need to set up the failover-ip on all
servers on the respective interface.

vip-manager will **not** add or remove the VIP on the current master
node interface, Hetzner will route it to the current one.

#### FailoverIP

[Hetzner failover-IP documentation](https://wiki.hetzner.de/index.php/Failover/en)
[Hetzner Robot failover-IP API documentation](https://robot.your-server.de/doc/webservice/en.html#failover)

* set `hosting_type` to `hetzner` in `/etc/default/vip-manager.yml`
* configure credentials in `/etc/hetzner`:

```
user="myUsername"
pass="myPassword"
```

#### FloatingIP

[Hetzner floating-IP documentation](https://wiki.hetzner.de/index.php/CloudServer/en#What_are_floating_IPs_and_how_do_they_work.3F)
[Hetzner Cloud failover-IP API documentation](https://docs.hetzner.cloud/#floating-ips)

* set `hosting_type` to `hetzner_floating_ip` in `/etc/default/vip-manager.yml`
* configure credentials in `/etc/hetzner`:

```
# This is the API_TOKEN, that you need to get from console.hetzner.cloud -> project -> access
tokn='DXuia61JJaLJ2Je2jZjrnQ4zm7VcLTYvoo9dV5hpNGwgvM8mI9790niVt1IbN0sE'
# You can retrieve the IP ID with:
# `curl -H "Authorization: Bearer $tokn" 'https://api.hetzner.cloud/v1/floating_ips'`
ipid='123456'
# You can retrieve the server ID with:
# `curl -H "Authorization: Bearer $tokn" 'https://api.hetzner.cloud/v1/servers'`
serv='7890123'
```

## Authors

* Cybertec Schönig & Schönig GmbH, https://www.cybertec-postgresql.com
* Tomáš Pospíšek @ Sourcepole.ch
