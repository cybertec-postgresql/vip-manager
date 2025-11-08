[![License: MIT](https://img.shields.io/badge/License-BSD-green.svg)](https://opensource.org/licenses/BSD-2)
![Build&Test](https://github.com/cybertec-postgresql/vip-manager/workflows/Go%20Build%20&%20Test/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/cybertec-postgresql/vip-manager)](https://goreportcard.com/report/github.com/cybertec-postgresql/vip-manager)
[![Release](https://img.shields.io/github/release/cybertec-postgresql/vip-manager.svg?include_prereleases)](https://github.com/cybertec-postgresql/vip-manager/releases/latest)
[![Github All Releases](https://img.shields.io/github/downloads/cybertec-postgresql/vip-manager/total?style=flat-square)](https://github.com/cybertec-postgresql/vip-manager/releases)

# vip-manager

Manages a virtual IP based on state kept in `etcd`, `Consul` or using `Patroni` REST API

## Table of Contents

- [Prerequisites](#prerequisites)
- [Building](#building)
- [Installing from package](#installing-from-package)
- [Installing from source](#installing-from-source)
- [Environment prerequisites](#environment-prerequisites)
- [PostgreSQL prerequisites](#postgresql-prerequisites)
- [Configuration](#configuration)
- [Configuration - Hetzner](#configuration---hetzner)
  - [Credential File - Hetzmer](#credential-file---hetzner)
- [Debugging](#debugging)
- [Author](#author)

## Prerequisites

- `go` >= 1.19
- `make` (optional)
- `goreleaser` (optional)

## Building

1. clone this repo

```shell
git clone https://github.com/cybertec-postgresql/vip-manager.git
```

1. Build the binary using `make` or `go build`.

1. To build your own packages (.deb, .rpm, .zip, etc.), run

```shell
make package
```

or

```shell
goreleaser release --snapshot --skip-publish --rm-dist
```

## Installing from package

You can download .rpm or .deb packages here, on the [Releases](https://github.com/cybertec-postgresql/vip-manager/releases) page.
On Debian and Ubuntu, the universe repositories should provide you with vip-manager, though the version may be not as recent.

> [!IMPORTANT]
Our packages are probably not compatible with the one from those repositories, do not try to install them side-by-side.

## Installing from source

- Follow the steps to [build](#building) vip-manager.
- Run `DESTDIR=/tmp make install` to copy the binary, service files and config file into the destination of your choice.
- Edit config to your needs, then run `systemctl daemon-reload`, then `systemctl start vip-manager`.

> [!NOTE]
systemd will only pick the service files up if you chose a `DESTDIR` so that it can find it. Usually `DESTDIR=''` should work.

## Environment prerequisites

When vip-manager is in charge of registering and deregistering the VIP locally, it needs superuser privileges to do so.
This is not required when vip-manager is used to manage a VIP through some API, e.g. Hetzner Robot API or Hetzner Cloud API.

> [!NOTE]
> At some point it would be great to reduce this requirement to only the `CAP_NET_RAW` and `CAP_NET_ADMIN` capabilities, which could be added by a superuser to the vip-manager binary _once_.
> Right now, this is not possible since vip-manager launches plain shell commands to register and deregister virtual IP addresses locally (at least on linux), so the whole user would need these privileges.
> When vip-manager is eventually taught to directly use a library that directly uses the Linux kernel's API to register/deregister the VIP, the capabilities set for the binary will suffice.

## PostgreSQL prerequisites

For any virtual IP based solutions to work in general with Postgres you need to make sure that it is configured to automatically scan and bind
to all found network interfaces. So something like `*` or `0.0.0.0` (IPv4 only) is needed for the `listen_addresses` parameter
to activate the automatic binding. This again might not be suitable for all use cases where security is paramount for example.

### nonlocal bind

If you can't set `listen_addresses` to a wildcard address, you can explicitly specify only those adresses that you want to listen to.
However, if you add the virtual IP to those addresses, PostgreSQL will fail to start when that address is not yet registered on one of the interfaces of the machine.
You need to configure the kernel to allow "nonlocal bind" of IP (v4) addresses:

- temporarily:

```bash
sysctl -w net.ipv4.ip_nonlocal_bind=1
```

- permanently:

```bash
echo "net.ipv4.ip_nonlocal_bind = 1"  >> /etc/sysctl.conf
sysctl -p
```

## Configuration

The configuration can be passed to the executable through argument flags, environment variables or through a YAML config file. Run `vip-manager --help` to see the available flags.

> [!NOTE]
> The location of the YAML config file can be specified with the --config flag.
> An exemplary config file is installed into `/etc/default/vip-manager.yml` or is available in the vipconfig directory in the repository of the software.

Configuration is now (from release v1.0 on) handled using the [`viper`](https://github.com/spf13/viper) library.
This means that environment variables, command line flags, and config files can be used to configure vip-manager.
When using different configuration sources simultaneously, this is the precedence order:

- flag
- env
- config

> [!NOTE]
> So flags always overwrite env variables and entries from the config file. Env variables overwrite the config file entries.

All flags and file entries are written in lower case. To make longer multi-word flags and entries readable, they are separated by dashes, e.g. `retry-num`.

If you put a flag or file entry into uppercase and replace dashes with underscores, you end up with the format of environment variables. To avoid overlapping configuration with other applications, the env variables are additionall prefixed with `VIP_`, e.g. `VIP_RETRY_NUM`.

This is a list of all avaiable configuration items:

| flag/yaml key     | env notation          | required  | example                     | description |
| ----------------- | --------------------- | --------- | --------------------------- | ----------- |
| `ip`              | `VIP_IP`              | yes       | `10.10.10.123`              | The virtual IP address that will be managed. |
| `netmask`         | `VIP_NETMASK`         | yes       | `24`                        | The netmask that is associated with the subnet that the virtual IP `vip` is part of. |
| `interface`       | `VIP_INTERFACE`       | yes       | `eth0`                      | A local network interface on the machine that runs vip-manager. Required when using `manager-type=basic`. The vip will be added to and removed from this interface. |
| `trigger-key`     | `VIP_TRIGGER_KEY`     | yes       | `/service/pgcluster/leader` | The key in the DCS or the Patroni REST endpoint (e.g. `/leader`) that will be monitored by vip-manager. Must match `<namespace>/<scope>/leader` from Patroni config. When the value returned by the DCS equals `trigger-value`, vip-manager will make sure that the virtual IP is registered to this machine. If it does not match, vip-manager makes sure that the virtual IP is not registered to this machine. |
| `trigger-value`   | `VIP_TRIGGER_VALUE`   | no        | `pgcluster_member_1`        | The value that the DCS' answer for `trigger-key` will be matched to. Must match `<name>` from Patroni config for DCS or the HTTP response for Patroni REST API. This is usually set to the name of the Patroni cluster member that this vip-manager instance is associated with. Defaults to the machine's hostname or to 200 for Patroni. |
| `manager-type`    | `VIP_MANAGER_TYPE`    | no        | `basic`                     | Either `basic` or `hetzner`. This describes the mechanism that is used to manage the virtual IP. Defaults to `basic`. |
| `dcs-type`        | `VIP_DCS_TYPE`        | no        | `etcd`                      | The type of DCS that vip-manager will use to monitor the `trigger-key`. Defaults to `etcd`. |
| `dcs-endpoints`   | `VIP_DCS_ENDPOINTS`   | no        | `http://10.10.11.1:2379`    | A url that defines where to reach the DCS or Patroni REST API. Multiple endpoints can be passed to the flag or env variable using a comma-separated-list. In the config file, a list can be specified, see the sample config for an example. Defaults to `http://127.0.0.1:2379` for `dcs-type=etcd`, `http://127.0.0.1:8500` for `dcs-type=consul` and `http://127.0.0.1:8008` for `dcs-type=patroni`. |
| `etcd-user`       | `VIP_ETCD_USER`       | no        | `patroni`                   | A username that is allowed to look at the `trigger-key` in an etcd DCS. Optional when using `dcs-type=etcd` . |
| `etcd-password`   | `VIP_ETCD_PASSWORD`   | no        | `snakeoil`                  | The password for `etcd-user`. Optional when using `dcs-type=etcd` . Requires that `etcd-user` is also set. |
| `consul-token`    | `VIP_CONSUL_TOKEN`    | no        | `snakeoil`                  | A token that can be used with the consul-API for authentication. Optional when using `dcs-type=consul` . |
| `interval`        | `VIP_INTERVAL`        | no        | `1000`                      | The time vip-manager main loop sleeps before checking for changes. Measured in ms. Defaults to `1000`. Doesn't affect etcd checker since v2.3.0. |
| `retry-after`     | `VIP_RETRY_AFTER`     | no        | `250`                       | The time to wait before retrying interactions with components outside of vip-manager. Measured in ms. Defaults to `250`. |
| `retry-num`       | `VIP_RETRY_NUM`       | no        | `3`                         | The number of times interactions with components outside of vip-manager are retried. Defaults to `3`. |
| `etcd-ca-file`    | `VIP_ETCD_CA_FILE`    | no        | `/etc/etcd/ca.cert.pem`     | A certificate authority file that can be used to verify the certificate provided by etcd endpoints. Make sure to change `dcs-endpoints` to reflect that `https` is used. |
| `etcd-cert-file   | `VIP_ETCD_CERT_FILE`  | no        | `/etc/etcd/client.cert.pem` | A client certificate that is used to authenticate against etcd endpoints. Requires `etcd-ca-file` to be set as well. |
| `etcd-key-file`   | `VIP_ETCD_KEY_FILE`   | no        | `/etc/etcd/client.key.pem`  | A private key for the client certificate, used to decrypt messages sent by etcd endpoints. Required when `etcd-cert-file` is specified. |
| `verbose`         | `VIP_VERBOSE`         | no        | `true`                      | Enable more verbose logging. Currently only the manager-type=hetzner provides additional logs. |

## Configuration - Patroni REST API

To directly use the Patroni REST API, simply set `dcs-type` to `patroni` and `trigger-key` to `/leader`. The defaults for `dcs-endpoints` (`http://127.0.0.1:8008`) and `trigger-value` (200) for the Patroni checker should work in most cases.

## Configuration - Hetzner

To use vip-manager with Hetzner Robot API you need a Credential file, set `hosting_type` to `hetzner` in `/etc/default/vip-manager.yml`
and your Floating-IP must be added on all Servers.
The Floating-IP (VIP) will not be added or removed on the current Master node interface, Hetzner will route it to the current one.

### Credential File - Hetzner

Add the File `/etc/hetzner` with your Username and Password

```shell
user="myUsername"
pass="myPassword"
```

## Debugging

Either:

- run `vip-manager` with `--verbose` flag or
- set `verbose` to `true` in `/etc/default/vip-manager.yml`
- set `VIP_VERBOSE=true`

> [!NOTE]
> Currently only supported for `hetzner`

## Author

CYBERTEC PostgreSQL International GmbH, <https://www.cybertec-postgresql.com>
