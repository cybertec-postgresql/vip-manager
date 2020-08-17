package vipconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Config represents the configuration of the VIP manager
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

	ConsulToken string `yaml:"consul_token"`

	Interval int `yaml:"interval"` //milliseconds

	RetryAfter int `yaml:"retry_after"` //milliseconds
	RetryNum   int `yaml:"retry_num"`
}

func checkSetting(name string) bool {
	if !viper.IsSet(name) {
		log.Printf("Setting %s is mandatory", name)
		return false
	}
	return true
}

func setDefaults() {
	defaults := map[string]string{
		"dcs-type":    "etcd",
		"interval":    "1000",
		"hostingtype": "basic",
		"retry-num":   "3",
		"retry-after": "250",
	}

	for k, v := range defaults {
		if !viper.IsSet(k) {
			viper.SetDefault(k, v)
		}
	}
}

func checkMandatory() error {
	mandatory := []string{
		"ip",
		"netmask",
		"interface",
		"trigger-key",
		"trigger-value",
		"dcs-endpoints",
	}
	success := true
	for _, v := range mandatory {
		success = checkSetting(v) && success
	}
	if !success {
		return errors.New("one or more mandatory settings were not set")
	}
	return nil
}

func defineFlags() {
	pflag.String("config", "", "Location of the configuration file.")
	pflag.Bool("version", false, "Show the version number.")

	pflag.String("ip", "", "Virtual IP address to configure")
	pflag.String("netmask", "", "The netmask used for the IP address. Defaults to -1 which assigns ipv4 default mask.")
	pflag.String("interface", "", "Network interface to configure on")

	pflag.String("trigger-key", "", "key to monitor, e.g. /service/batman/leader")
	pflag.String("trigger-value", "", "Value to monitor for")

	pflag.String("dcs-type", "etcd", "type of endpoint used for key storage. Supported values: etcd, consul")
	pflag.String("dcs-endpoints", "", "DCS endpoints")
	pflag.String("etcd-user", "", "username for etcd DCS endpoints")
	pflag.String("etcd-password", "", "password for etcd DCS endpoints")
	pflag.String("consul-token", "", "token for consul DCS endpoints")

	pflag.String("interval", "1000", "DCS scan interval in milliseconds")
	pflag.String("manager-type", "basic", "type of hosting. Supported values: basic, hetzner")

	// old CLI flags, now deprecated:
	pflag.String("mask", "", "")
	_ = pflag.CommandLine.MarkDeprecated("mask", "use --netmask instead")
	pflag.String("hostingtype", "", "")
	_ = pflag.CommandLine.MarkDeprecated("hostingtype", "use --manager-type instead")
	pflag.String("endpoint", "", "")
	_ = pflag.CommandLine.MarkDeprecated("endpoint", "use --dcs-endpoints instead")
	pflag.String("type", "", "")
	_ = pflag.CommandLine.MarkDeprecated("type", "use --dcs-type instead")
	pflag.String("etcd_password", "", "")
	_ = pflag.CommandLine.MarkDeprecated("etcd_password", "use --etcd-password instead")
	pflag.String("etcd_user", "", "")
	_ = pflag.CommandLine.MarkDeprecated("etcd_user", "use --etcd-user instead")
	pflag.String("consul_token", "", "")
	_ = pflag.CommandLine.MarkDeprecated("consul_token", "use --consul-token instead")
	pflag.String("nodename", "", "")
	_ = pflag.CommandLine.MarkDeprecated("nodename", "use --trigger-value instead")
	pflag.String("key", "", "")
	_ = pflag.CommandLine.MarkDeprecated("key", "use --trigger-key instead")
	pflag.String("iface", "", "")
	_ = pflag.CommandLine.MarkDeprecated("iface", "use --interface instead")

	pflag.CommandLine.SortFlags = false
}

func mapDeprecated() error {
	deprecated := map[string]string{
		// "deprecated" : "new",
		"mask":          "netmask",
		"iface":         "interface",
		"key":           "trigger-key",
		"nodename":      "trigger-value",
		"etcd_user":     "etcd-user",
		"etcd_password": "etcd-password",
		"type":          "dcs-type",
		"endpoint":      "dcs-endpoints",
		"endpoints":     "dcs-endpoints",
		"hostingtype":   "manager-type",
		"hosting_type":  "manager-type",
		"endpoint_type": "dcs-type",
		"retry_num":     "retry-num",
		"retry_after":   "retry-after",
		"consul_token":  "consul-token",
	}

	for k, v := range deprecated {
		if viper.IsSet(k) {
			log.Printf("Parameter \"%s\" has been deprecated, please use \"%s\" instead", k, v)
			if viper.IsSet(v) {
				log.Printf("conflicting settings: %s and %s are both specified", k, v)
				return fmt.Errorf("values: %s and %s are both specified", viper.GetString(k), viper.GetString(v))
			}
			viper.Set(v, viper.Get(k))
		}
	}
	return nil
}

// NewConfig returns a new Config instance
func NewConfig() (*Config, error) {
	var err error

	defineFlags()
	//put existing flags into pflags:
	pflag.Parse()
	//import pflags into viper
	_ = viper.BindPFlags(pflag.CommandLine)

	// make viper look for env variables that are prefixed VIP_...
	// viper.getString("IP") will thus check env variable VIP_IP
	viper.SetEnvPrefix("vip")
	viper.AutomaticEnv()
	//replace dashes (in flags) with underscores (in ENV vars)
	// so that viper.GetString("dcs-endpoints") will get VIP_DCS_ENDPOINTS
	replacer := strings.NewReplacer("-", "_")
	viper.SetEnvKeyReplacer(replacer)

	// viper precedence order
	// - explicit call to Set
	// - flag
	// - env
	// - config
	// - key/value store
	// - default

	// if a configfile has been passed, make viper read it
	if viper.IsSet("config") {
		viper.SetConfigFile(viper.GetString("config"))

		err := viper.ReadInConfig() // Find and read the config file
		if err != nil {             // Handle errors reading the config file
			return nil, fmt.Errorf("Fatal error reading config file: %w", err)
		}
		fmt.Printf("Using config from file: %s\n", viper.ConfigFileUsed())
	}

	if err = mapDeprecated(); err != nil {
		return nil, err
	}

	setDefaults()

	//convert string of csv to String Slice
	if viper.IsSet("dcs-endpoints") {
		endpointsString := viper.GetString("dcs-endpoints")
		if strings.Contains(endpointsString, ",") {
			viper.Set("dcs-endpoints", strings.Split(endpointsString, ","))
		}
	}

	//apply defaults for endpoints
	if !viper.IsSet("dcs-endpoints") {
		log.Println("No dcs-endpoints specified, trying to use localhost with standard ports!")

		switch viper.GetString("dcs-type") {
		case "consul":
			viper.Set("dcs-endpoints", []string{"http://127.0.0.1:8500"})
		case "etcd":
			viper.Set("dcs-endpoints", []string{"http://127.0.0.1:2379"})
		}
	}

	if len(viper.GetString("trigger-value")) == 0 {
		triggerValue, err := os.Hostname()
		if err != nil {
			log.Printf("No trigger-value specified, hostname could not be retrieved: %s", err)
		} else {
			log.Printf("No trigger-value specified, instead using hostname: %v", triggerValue)
			viper.Set("trigger-value", triggerValue)
		}
	}

	conf := Config{
		IP:           viper.GetString("ip"),
		Mask:         viper.GetInt("netmask"),
		Iface:        viper.GetString("interface"),
		HostingType:  viper.GetString("manager-type"),
		Key:          viper.GetString("trigger-key"),
		Nodename:     viper.GetString("trigger-value"),
		EndpointType: viper.GetString("dcs-type"),
		Endpoints:    viper.GetStringSlice("dcs-endpoints"),
		EtcdUser:     viper.GetString("etcd-user"),
		EtcdPassword: viper.GetString("etcd-password"),
		ConsulToken:  viper.GetString("consul-token"),
		Interval:     viper.GetInt("interval"),
		RetryAfter:   viper.GetInt("retry-after"),
		RetryNum:     viper.GetInt("retry-num"),
	}

	b, err := json.MarshalIndent(conf, "", "  ")
	if err == nil {
		fmt.Println(string(b))
	}

	if err = checkMandatory(); err != nil {
		return nil, err
	}

	return &conf, nil
}
