package vipconfig

import ()

type Config struct {
	Ip    string `yaml:ip`
	Mask  int    `yaml:mask`
	Iface string `yaml:iface`

	HostingType string `yaml:hosting_type`

	Key      string `yaml:key`
	Nodename string `yaml:nodename` //hostname to trigger on. usually the name of the host where this vip-manager runs.

	Endpoint_type  string   `yaml:endpoint_type`
	Endpoints      []string `yaml:endpoints`
	Etcd_user      string   `yaml:etcd_user`
	Etcd_password  string   `yaml:etcd_password`
	Etcd_ca_file   string   `yaml:etcd_ca_file`
	Etcd_cert_file string   `yaml:etcd_cert_file`
	Etcd_key_file  string   `yaml:etcd_key_file`

	Consul_token string `yaml:consul_token`

	Interval int `yaml:interval` //milliseconds

	Retry_after int `yaml:retry_after` //milliseconds
	Retry_num   int `yaml:retry_num`
}
