package vipconfig

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Config represents the configuration of the VIP manager
type Config struct {
	IP    string `mapstructure:"ip"`
	Mask  int    `mapstructure:"netmask"`
	Iface string `mapstructure:"interface"`

	HostingType string `mapstructure:"manager-type"`

	Key      string `mapstructure:"trigger-key"`
	Nodename string `mapstructure:"trigger-value"` //hostname to trigger on. usually the name of the host where this vip-manager runs.

	EndpointType string   `mapstructure:"dcs-type"`
	Endpoints    []string `mapstructure:"dcs-endpoints"`

	EtcdUser     string `mapstructure:"etcd-user"`
	EtcdPassword string `mapstructure:"etcd-password"`
	EtcdCAFile   string `mapstructure:"etcd-ca-file"`
	EtcdCertFile string `mapstructure:"etcd-cert-file"`
	EtcdKeyFile  string `mapstructure:"etcd-key-file"`

	ConsulToken string `mapstructure:"consul-token"`

	HetznerUser          string `mapstructure:"hetzner-user"`
	HetznerPassword      string `mapstructure:"hetzner-password"`
	HetznerCloudToken    string `mapstructure:"hetzner-cloud-token"`
	HetznerCloudIpId     string `mapstructure:"hetzner-cloud-ip-id"`
	HetznerCloudServerId string `mapstructure:"hetzner-cloud-server-id"`

	Interval int `mapstructure:"interval"` //milliseconds

	RetryAfter int `mapstructure:"retry-after"` //milliseconds
	RetryNum   int `mapstructure:"retry-num"`

	Verbose bool `mapstructure:"verbose"`
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
	pflag.String("etcd-ca-file", "", "Trusted CA certificate for the etcd server.")
	pflag.String("etcd-cert-file", "", "Client certificate used for authentiaction with etcd.")
	pflag.String("etcd-key-file", "", "Private key matching etcd-cert-file to decrypt messages sent from etcd.")

	pflag.String("consul-token", "", "Token for consul DCS endpoints.")

	pflag.String("hetzner-user", "", "Username for authenticating with the Hetzner Robot API.")
	pflag.String("hetzner-password", "", "Password for authenticating with the Hetzner Robot API.")
	pflag.String("hetzner-cloud-token", "", "Token for accessing Hetzner Cloud.")
	pflag.String("hetzner-cloud-ip-id", "", "ID of the Hetzner Floating IP to be managed.")
	pflag.String("hetzner-cloud-server-id", "", "ID of the Hetzner Cloud server to be managed.")

	pflag.String("interval", "1000", "DCS scan interval in milliseconds.")
	pflag.String("manager-type", "basic", "Type of VIP-management to be used. Supported values: basic, hetzner.")

	pflag.Bool("verbose", false, "Be verbose. Currently only implemented for manager-type=hetzner and manager-type=hetzner_floating_ip.")

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

	complaints := []string{}
	errors := false
	for k, v := range deprecated {
		if viper.IsSet(k) {

			if _, exists := os.LookupEnv("VIP_" + strings.ToUpper(k)); !exists {
				// using deprecated key in config file (as not exists in ENV)
				complaints = append(complaints, fmt.Sprintf("Parameter \"%s\" has been deprecated, please use \"%s\" instead", k, v))
			} else {
				if strings.ReplaceAll(k, "_", "-") != v {
					// this string is not a direct replacement (e.g. etcd-user replaces etcd-user, i.e. in both cases VIP_ETCD_USER is the valid env key)
					// for example, complain about VIP_IFACE, but not VIP_CONSUL_TOKEN or VIP_ETCD_USER...
					complaints = append(complaints, fmt.Sprintf("Parameter \"%s\" has been deprecated, please use \"%s\" instead", "VIP_"+strings.ToUpper(k), "VIP_"+strings.ReplaceAll(strings.ToUpper(v), "-", "_")))
				} else {
					continue
				}
			}

			if viper.IsSet(v) {
				// don't forget to reset the desired replacer when exiting
				replacer := strings.NewReplacer("-", "_")
				defer viper.SetEnvKeyReplacer(replacer)

				// Check if there is only a collision because ENV vars always use _ instead of - and the deprecated mapping only maps from *_* to *-*.
				testReplacer := strings.NewReplacer("", "") // just don't replace anything
				viper.SetEnvKeyReplacer(testReplacer)
				if viper.IsSet(v) {
					complaints = append(complaints, fmt.Sprintf("Conflicting settings: %s or %s and %s or %s are both specified…", k, "VIP_"+strings.ToUpper(k), v, "VIP_"+strings.ReplaceAll(strings.ToUpper(v), "-", "_")))

					if viper.Get(k) == viper.Get(v) {
						complaints = append(complaints, fmt.Sprintf("… But no conflicting values: %s and %s are equal…ignoring.", viper.GetString(k), viper.GetString(v)))
						continue
					} else {
						complaints = append(complaints, fmt.Sprintf("…conflicting values: %s and %s", viper.GetString(k), viper.GetString(v)))
						errors = true
						continue
					}
				}
			}
			// if this is a valid mapping due to deprecation, set the new key explicitly to the value of the deprecated key.
			viper.Set(v, viper.Get(k))
			// "unset" the deprecated setting so it will not show up in our config later
			viper.Set(k, "")

		}
	}
	for c := range complaints {
		log.Println(complaints[c])
	}
	if errors {
		log.Fatal("Cannot continue due to conflicts.")
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

// if reason is set, but implied is not set, return false.
func checkImpliedSetting(implied string, reason string) bool {
	if viper.IsSet(reason) && !viper.IsSet(implied) {
		log.Printf("Setting %s is mandatory when setting %s is specified.", implied, reason)
		return false
	}
	return true
}

// Some settings imply that another setting must be set as well.
func checkImpliedMandatory() error {
	mandatory := map[string]string{
		// "implied" : "reason"
		"etcd-user":     "etcd-password",
		"etcd-key-file": "etcd-cert-file",
		"etcd-ca-file":  "etcd-cert-file",
	}
	success := true
	for k, v := range mandatory {
		success = checkImpliedSetting(k, v) && success
	}
	if !success {
		return errors.New("one or more implied mandatory settings were not set")
	}
	return nil
}

func printSettings() {
	s := []string{}

	for k, v := range viper.AllSettings() {
		if v != "" {
			switch k {
			case "etcd-password":
				fallthrough
			case "consul-token":
			    fallthrough
			case "hetzner-password":
			    fallthrough
			case "hetzner-cloud-token":
				s = append(s, fmt.Sprintf("\t%s : *****\n", k))
			default:
				s = append(s, fmt.Sprintf("\t%s : %v\n", k, v))
			}
		}
	}

	sort.Strings(s)
	log.Println("This is the config that will be used:")
	for k := range s {
		fmt.Print(s[k])
	}
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

	if err = checkMandatory(); err != nil {
		return nil, err
	}

	if err = checkImpliedMandatory(); err != nil {
		return nil, err
	}

	conf := &Config{}
	err = viper.Unmarshal(conf)
	if err != nil {
		log.Fatalf("unable to decode viper config into config struct, %v", err)
	}

	printSettings()

	return conf, nil
}
