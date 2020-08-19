package vipconfig

import (
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

func defineFlags() {
	// When adding new flags here, consider adding them to the Config struct above
	// and then make sure to insert them into the conf instance in NewConfig down below.
	pflag.String("config", "", "Location of the configuration file.")
	pflag.Bool("version", false, "Show the version number.")

	pflag.String("ip", "", "Virtual IP address to configure.")
	pflag.String("netmask", "", "The netmask used for the IP address. Defaults to -1 which assigns ipv4 default mask.")
	pflag.String("interface", "", "Network interface to configure on .")

	pflag.String("trigger-key", "", "Key in the DCS to monitor, e.g. \"/service/batman/leader\".")
	pflag.String("trigger-value", "", "Value to monitor for.")

	pflag.String("dcs-type", "etcd", "Type of endpoint used for key storage. Supported values: etcd, consul.")
	// note: can't put a default value into dcs-endpoints as that would mess with applying default localhost when using consul
	pflag.String("dcs-endpoints", "", "DCS endpoint(s), separate multiple endpoints using commas. (default \"http://127.0.0.1:2379\" or \"http://127.0.0.1:8500\" depending on dcs-type.)")
	pflag.String("etcd-user", "", "Username for etcd DCS endpoints.")
	pflag.String("etcd-password", "", "Password for etcd DCS endpoints.")
	pflag.String("consul-token", "", "Token for consul DCS endpoints.")

	pflag.String("interval", "1000", "DCS scan interval in milliseconds.")
	pflag.String("manager-type", "basic", "Type of VIP-management to be used. Supported values: basic, hetzner.")

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
		"host":          "trigger-value",
	}

	for k, v := range deprecated {
		if viper.IsSet(k) {

			// if the key is still set after replacing, that means we're dealing with env variables. pointless to emit deprecation warning "etcd_user is deprecated" when user specified VIP_ETCD_USER.
			// if the key is no longer set after replacing, that means that the key was in fact present with an underscore in the config file.
			if !viper.IsSet(strings.ReplaceAll(v, "_", "-")) {
				log.Printf("Parameter \"%s\" has been deprecated, please use \"%s\" instead", k, v)
			}

			if viper.IsSet(v) {
				// don't forget to reset the desired replacer when exiting
				replacer := strings.NewReplacer("-", "_")
				defer viper.SetEnvKeyReplacer(replacer)

				// Check if there is only a collision because ENV vars always use _ instead of - and the deprecated mapping only maps from *_* to *-*.
				testReplacer := strings.NewReplacer("", "") // just don't replace anything
				viper.SetEnvKeyReplacer(testReplacer)
				if viper.IsSet(v) {
					log.Printf("conflicting settings: %s and %s are both specified…", k, v)

					if viper.Get(k) == viper.Get(v) {
						log.Printf("… But no conflicting settings: %s and %s are equal…ignoring.", viper.GetString(k), viper.GetString(v))
						continue
					}

					return fmt.Errorf("…conflicting values: %s and %s", viper.GetString(k), viper.GetString(v))
				}
			}
			// if this is a valid mapping due to deprecation, set the new key explicitly to the value of the deprecated key.
			viper.Set(v, viper.Get(k))
		}
	}
	return nil
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

func checkSetting(name string) bool {
	if !viper.IsSet(name) {
		log.Printf("Setting %s is mandatory", name)
		return false
	}
	return true
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

// NewConfig returns a new Config instance
func NewConfig() (*Config, error) {
	var err error

	defineFlags()
	pflag.Parse()
	// import pflags into viper
	_ = viper.BindPFlags(pflag.CommandLine)

	// make viper look for env variables that are prefixed VIP_...
	// e.g.: viper.getString("ip") will return the value of env variable VIP_IP
	viper.SetEnvPrefix("vip")
	viper.AutomaticEnv()
	//replace dashes (in flags) with underscores (in ENV vars)
	// so that e.g. viper.GetString("dcs-endpoints") will return value of VIP_DCS_ENDPOINTS
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
		log.Printf("Using config from file: %s\n", viper.ConfigFileUsed())
	}

	if err = mapDeprecated(); err != nil {
		return nil, err
	}

	setDefaults()

	// convert string of csv to String Slice
	if viper.IsSet("dcs-endpoints") {
		endpointsString := viper.GetString("dcs-endpoints")
		if strings.Contains(endpointsString, ",") {
			viper.Set("dcs-endpoints", strings.Split(endpointsString, ","))
		}
	}

	// apply defaults for endpoints
	if !viper.IsSet("dcs-endpoints") {
		log.Println("No dcs-endpoints specified, trying to use localhost with standard ports!")

		switch viper.GetString("dcs-type") {
		case "consul":
			viper.Set("dcs-endpoints", []string{"http://127.0.0.1:8500"})
		case "etcd":
			viper.Set("dcs-endpoints", []string{"http://127.0.0.1:2379"})
		}
	}

	// set trigger-value to hostname if nothing is specified
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

	if err = checkMandatory(); err != nil {
		return nil, err
	}

	// this will print password and token, so need to reconsider...
	// b, err := json.MarshalIndent(conf, "", "  ")
	// if err == nil {
	// 	log.Printf("This is the config that will be used:\n %v", string(b))
	// }

	return &conf, nil
}
