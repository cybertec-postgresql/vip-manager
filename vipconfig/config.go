package vipconfig

type Config struct {
	IP    string `yaml:"ip"`
	Mask  int    `yaml:"mask"`
	Iface string `yaml:"iface"`

	HostingType string `yaml:"hosting_type"`

	Key      string `yaml:"key"`
	Nodename string `yaml:"nodename"` //hostname to trigger on. usually the name of the host where this vip-manager runs.

	EndpointType string   `yaml:"endpoint_type"`
	Endpoints    []string `yaml:"endpoints"`
	EtcdUser     string   `yaml:"etcd_user"`
	EtcdPassword string   `yaml:"etcd_password"`
	EtcdCAFile   string   `yaml:"etcd_ca_file"`
	EtcdCertFile string   `yaml:"etcd_cert_file"`
	EtcdKeyFile  string   `yaml:"etcd_key_file"`  

	ConsulToken string `yaml:"consul_token"`

  Interval int `yaml:"interval"` //milliseconds

	RetryAfter int `yaml:"retry_after"` //milliseconds
	RetryNum   int `yaml:"retry_num"`
}
