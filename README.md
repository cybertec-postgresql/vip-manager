# vip-manager

Manages a virtual IP based on state kept in etcd or Consul. Monitors state in etcd 

## building
1. To make sure that internal includes (the vipconfig and the checker package) are satisfied, place the base directory of this project properly into your `$GOPATH`.
    The resulting location should be `$GOPATH/src/github.com/cybertec-postgresql/vip-manager/`. The easiest way to do this is:
    ```go get github.com/cybertec-postgresql/vip-manager```

2. Please use go dep to install all dependencies. Install it, add it to your path...
    Then run `dep ensure` from this directory. This should read the `Gopkg.lock` file and download all dependencies.

3. Afterwards try building vip-manager, using `make` .

4. To build your own .deb or .rpm, `npm` is required.
    Install it, add it to your path and try running `make package`, which will generate a .deb package and will also convert that into a matching .rpm file.

## Installing on debian

* Install the debian package. Currently you will have to build the package yourself. Prebuilt packages are coming soon.
* Edit `/etc/patroni/vip.conf`. See the configuration section for details.
* Start and enable vip-manager service with systemctl.

## Installing by hand

* Build the vip-manager binary using go. 
* Install service file from `package/scripts/vip-manager.service` to `/etc/systemd/system/`
* Install configuration file from `package/config/vip-manager.default` to `/etc/patroni/vip.conf`
* Edit config and start the service.

## Configuration
The configuration can be passed to the executable through argument flags or through a YAML config file.
> The location of the YAML config file can be specified with the -config flag.
> An exemplary config file is installed into `/etc/default/vip-manager_default.yml` or is available in the vipconfig directory in the repository of the software.

These configuration keys are currently mandatory:

| Variable  | Example  | Description                                                                              |
|-----------|----------|------------------------------------------------------------------------------------------|
| VIP_IP    | 10.1.2.3 | Virtual IP that is being managed                                                         |
| VIP_MASK  | 24       | Netmask of the virtual IP                                                                |
| VIP_IFACE | eth0     | Network interface to configure the IP address on. Usually the primary network interface. |
| VIP_KEY   | /service/batman/leader | Key to monitor. Must match  scope from Patroni postgres.yml                |
| VIP_HOST  | serverX  | Value to trigger on. Must match name from Patroni.                                       |
| VIP_TYPE  | etcd     | Type of endpoint (etcd or consul)                                                        |
| VIP_ENDPOINT | http://10.1.2.3:2379 | Location of endpoint (etcd or consul)                                     |



## Author

Cybertec Schönig & Schönig GmbH, https://www.cybertec-postgresql.com
