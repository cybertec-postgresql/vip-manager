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

## deprecated Configuration
The configuration can be passed to the executable through argument flags or through a YAML config file.
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
| VIP_ENDPOINT | http://10.1.2.3:2379 | Location of endpoint (etcd or consul)                                     |


## reworked Configuration
Configuration is now handled using the [`viper`](https://github.com/spf13/viper) library.
This means that environment variables, command line flags, and config files can be used to configure vip-manager.
When using different configuration sources simultaneously, this is the precedence order:
- flag
- env
- config

> So flags always overwrite env variables and entries from the config file. Env variables overwrite the config file entries.

All flags and file entries are written in lower case. To make longer multi-word flags and entries readable, they are separated by dashes.

> e.g. `retry-num`

If you put a flag or file entry into uppercase and replace dashes with underscores, you end up with the format of environment variables. To avoid overlapping configuration with other applications, the env variables are additionall prefixed with `VIP`.

> e.g. `VIP_RETRY_NUM`

This is a list of all avaiable configuration items:
| flag/item     | env notation        | example | description |
| ------------- | ------------------- | ------- | ----------- |
`ip`            | `VIP_IP`            | 10.10.10.123              | The virtual IP address that will be managed.
`netmask`       | `VIP_NETMASK`       | 24                        | The netmask that is associated with the subnet that the virtual IP `vip` is part of.
`manager-mode`  | `VIP_MANAGER_MODE`  | basic                     | Either `basic` or `hetzner`. This describes the mechanism that is used to manage the virtual IP. Defaults to `basic`.
`interface`     | `VIP_INTERFACE`     | eth0                      | A local network interface on the machine that runs vip-manager. Required when using `manager-mode=basic`. The vip will be added to and removed from this interface.
`interval`      | `VIP_INTERVAL`      | 1000                      | The time vip-manager main loop sleeps before checking for changes. Measured in ms. Defaults to `1000`.
`retry-after`   | `VIP_RETRY_AFTER`   | 250                       | The time to wait before retrying interactions with components outside of vip-manager. Measured in ms. Defaults to `250`.
`retry-num`     | `VIP_RETRY_NUM`     | 3                         | The number of times interactions with components outside of vip-manager are retried. Measured in ms. Defaults to `250`.
`dcs-endpoints` | `VIP_DCS_ENDPOINTS` | http://10.10.11.1:2379    | A url that defines where to reach the DCS endpoints. Multiple endpoints can be passed to the flag or env variable using a comma-separated-list. In the config file, a list can be specified, see the sample config for an example. Defaults to `http://127.0.0.1:2379` for `dcs-type=etcd` and `http://127.0.0.1:8500` for `dcs-type=consul`.
`dcs-type`      | `VIP_DCS_TYPE`      | etcd                      | The type of DCS that vip-manager will use to monitor the `trigger-key`. Defaults to `etcd`.
`etcd-password` | `VIP_ETCD_PASSWORD` | snakeoil                  | The password for `etcd-user`. Optional when using `dcs-type=etcd` . Requires that `etcd-user` is also set.
`etcd-user`     | `VIP_ETCD_USER`     | patroni                   | A username that is allowed to look at the `trigger-key` in an etcd DCS. Optional when using `dcs-type=etcd` .
`consul-token`  | `VIP_CONSUL_TOKEN`  | snakeoil                  | A token that can be used with the consul-API for authentication. Optional when using `dcs-type=consul` .
`trigger-key`   | `VIP_TRIGGER_KEY`   | /service/pgcluster/leader | The key in the DCS that will be monitored by vip-manager. When the value returned by the DCS equals `trigger-value`, vip-manager will make sure that the virtual IP is registered to this machine. If it does not match, vip-manager makes sure that the virtual IP is not registered to this machine.
`trigger-value` | `VIP_TRIGGER_VALUE` | pgcluster_member_1        | The value that the DCS' answer for `trigger-key` will be matched to. This is usually set to the name of the patroni cluster member that this vip-manager instance is associated with. Defaults to the machine's hostname.


### Configuration - Hetzner
To use vip-manager with Hetzner Robot API you need a Credential file, set hosting_type to `hetzner` and your Floating-IP must be added on all Servers.
The Floating-IP (VIP) will not be added or removed on the current Master node interface, Hetzner will route it to the current one.

Set `hosting_type` to `hetzner` in `/etc/default/vip-manager.yml`

#### Credential File
Add the File `/etc/hetzner` with your Username and Password
```
user="myUsername"
pass="myPassword"
```

## Author

Cybertec Schönig & Schönig GmbH, https://www.cybertec-postgresql.com
