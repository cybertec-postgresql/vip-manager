package vipconfig

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config represents the configuration of the VIP manager
type Config struct {
	IP    string `mapstructure:"ip"`
	Mask  int    `mapstructure:"netmask"`
	Iface string `mapstructure:"interface"`

	HostingType string `mapstructure:"manager-type"`

	TriggerKey   string `mapstructure:"trigger-key"`
	TriggerValue string `mapstructure:"trigger-value"` //hostname to trigger on. usually the name of the host where this vip-manager runs.

	EndpointType string   `mapstructure:"dcs-type"`
	Endpoints    []string `mapstructure:"dcs-endpoints"`

	EtcdUser     string `mapstructure:"etcd-user"`
	EtcdPassword string `mapstructure:"etcd-password"`
	EtcdCAFile   string `mapstructure:"etcd-ca-file"`
	EtcdCertFile string `mapstructure:"etcd-cert-file"`
	EtcdKeyFile  string `mapstructure:"etcd-key-file"`

	ConsulToken string `mapstructure:"consul-token"`

	Interval int `mapstructure:"interval"` //milliseconds

	RetryAfter int `mapstructure:"retry-after"` //milliseconds
	RetryNum   int `mapstructure:"retry-num"`

	Verbose bool `mapstructure:"verbose"`

	LogFile string `mapstructure:"log-file"`

	Logger *zap.Logger

	// logReopener is set when logging to a file so that the file can be closed
	// and reopened on SIGHUP (see ReopenLog). It is nil when logging to stdout.
	logReopener *reopenableFile
}

func defineFlags() *pflag.FlagSet {
	// When adding new flags here, consider adding them to the Config struct above
	// and then make sure to insert them into the conf instance in NewConfig down below.
	flags := pflag.NewFlagSet("vip-manager", pflag.ContinueOnError)

	flags.String("config", "", "Location of the configuration file.")
	flags.Bool("version", false, "Show the version number.")

	flags.String("ip", "", "Virtual IP address to configure.")
	flags.String("netmask", "", "The netmask used for the IP address. Defaults to -1 which assigns ipv4 default mask.")
	flags.String("interface", "", "Network interface to configure on .")

	flags.String("trigger-key", "", "Key in the DCS to monitor, e.g. \"/service/batman/leader\".")
	flags.String("trigger-value", "", "Value to monitor for.")

	flags.String("dcs-type", "etcd", "Type of endpoint used for key storage. Supported values: etcd, consul, patroni.")
	// note: can't put a default value into dcs-endpoints as that would mess with applying default localhost when using consul
	flags.String("dcs-endpoints", "", "DCS endpoint(s), separate multiple endpoints using commas. (default \"http://127.0.0.1:2379\", \"http://127.0.0.1:8500\" or \"http://127.0.0.1:8008/\" depending on dcs-type.)")
	flags.String("etcd-user", "", "Username for etcd DCS endpoints.")
	flags.String("etcd-password", "", "Password for etcd DCS endpoints.")
	flags.String("etcd-ca-file", "", "Trusted CA certificate for the etcd server.")
	flags.String("etcd-cert-file", "", "Client certificate used for authentiaction with etcd.")
	flags.String("etcd-key-file", "", "Private key matching etcd-cert-file to decrypt messages sent from etcd.")

	flags.String("consul-token", "", "Token for consul DCS endpoints.")

	flags.Int("interval", 1000, "DCS scan interval in milliseconds.")
	flags.String("manager-type", "basic", "Type of VIP-management to be used. Supported values: basic, hetzner.")

	flags.Int("retry-after", 250, "Time to wait before retrying interactions with outside components in milliseconds.")
	flags.Int("retry-num", 3, "Number of times interactions with outside components are retried.")

	flags.Bool("verbose", false, "Be verbose. Currently only implemented for manager-type=hetzner .")

	flags.String("log-file", "", "Path to a log file. If empty, logs are written to stdout. Send SIGHUP to reopen the file after rotation (e.g. logrotate).")

	flags.SortFlags = false
	return flags
}

func setDefaults(v *viper.Viper) {
	defaults := map[string]any{
		"manager-type": "basic",
		"dcs-type":     "etcd",
		"interval":     1000,
		"retry-after":  250,
		"retry-num":    3,
	}

	for k, val := range defaults {
		if !v.IsSet(k) {
			v.SetDefault(k, val)
		}
	}

	// apply defaults for endpoints
	if !v.IsSet("dcs-endpoints") {
		fmt.Println("No dcs-endpoints specified, trying to use localhost with standard ports!")
		switch v.GetString("dcs-type") {
		case "consul":
			v.Set("dcs-endpoints", []string{"http://127.0.0.1:8500"})
		case "etcd", "etcd3":
			v.Set("dcs-endpoints", []string{"http://127.0.0.1:2379"})
		case "patroni":
			v.Set("dcs-endpoints", []string{"http://127.0.0.1:8008/"})
		}
	}

	// set trigger-key to '/leader' if DCS type is patroni and nothing is specified
	if v.GetString("trigger-key") == "" && v.GetString("dcs-type") == "patroni" {
		v.Set("trigger-key", "/leader")
	}

	// set trigger-value to default value if nothing is specified
	if triggerValue := v.GetString("trigger-value"); triggerValue == "" {
		var err error
		if v.GetString("dcs-type") == "patroni" {
			triggerValue = "200"
		} else {
			triggerValue, err = os.Hostname()
		}
		if err != nil {
			fmt.Printf("No trigger-value specified, hostname could not be retrieved: %s", err)
		} else {
			fmt.Printf("No trigger-value specified, instead using: %v", triggerValue)
			v.Set("trigger-value", triggerValue)
		}
	}

	// set retry-num to default if not set or set to zero
	if retryNum := v.GetInt("retry-num"); retryNum <= 0 {
		v.Set("retry-num", 3)
	}
}

func checkSetting(v *viper.Viper, name string) bool {
	if !v.IsSet(name) {
		fmt.Printf("Setting %s is mandatory", name)
		return false
	}
	return true
}

func checkMandatory(v *viper.Viper) error {
	mandatory := []string{
		"ip",
		"netmask",
		"interface",
		"trigger-key",
		"trigger-value",
		"dcs-endpoints",
	}
	success := true
	for _, name := range mandatory {
		success = checkSetting(v, name) && success
	}
	if !success {
		return errors.New("one or more mandatory settings were not set")
	}
	return checkImpliedMandatory(v)
}

// if reason is set, but implied is not set, return false.
func checkImpliedSetting(v *viper.Viper, implied string, reason string) bool {
	if v.IsSet(reason) && !v.IsSet(implied) {
		fmt.Printf("Setting %s is mandatory when setting %s is specified.", implied, reason)
		return false
	}
	return true
}

// Some settings imply that another setting must be set as well.
func checkImpliedMandatory(v *viper.Viper) error {
	mandatory := map[string]string{
		// "implied" : "reason"
		"etcd-user":     "etcd-password",
		"etcd-key-file": "etcd-cert-file",
		"etcd-ca-file":  "etcd-cert-file",
	}
	success := true
	for k, reason := range mandatory {
		success = checkImpliedSetting(v, k, reason) && success
	}
	if !success {
		return errors.New("one or more implied mandatory settings were not set")
	}
	return nil
}

func printSettings(v *viper.Viper) {
	s := []string{}

	for k, val := range v.AllSettings() {
		if val != "" {
			switch k {
			case "etcd-password":
				fallthrough
			case "consul-token":
				s = append(s, fmt.Sprintf("\t%s : *****\n", k))
			default:
				s = append(s, fmt.Sprintf("\t%s : %v\n", k, val))
			}
		}
	}

	sort.Strings(s)
	fmt.Println("This is the config that will be used:")
	for k := range s {
		fmt.Print(s[k])
	}
}

func loadConfigFile(v *viper.Viper) error {
	if v.IsSet("config") {
		v.SetConfigFile(v.GetString("config"))
		if err := v.ReadInConfig(); err != nil {
			return err
		}
		fmt.Printf("Using config from file: %s\n", v.ConfigFileUsed())
	}
	return nil
}

// NewConfig returns a new Config instance
func NewConfig() (*Config, error) {
	return newConfig(os.Args[1:])
}

func newConfig(args []string) (*Config, error) {
	var err error

	v := viper.New()
	flags := defineFlags()
	if err = flags.Parse(args); err != nil {
		return nil, fmt.Errorf("error parsing flags: %w", err)
	}
	// import pflags into viper
	_ = v.BindPFlags(flags)

	// make viper look for env variables that are prefixed VIP_...
	// e.g.: v.GetString("ip") will return the value of env variable VIP_IP
	v.SetEnvPrefix("vip")
	v.AutomaticEnv()
	//replace dashes (in flags) with underscores (in ENV vars)
	// so that e.g. v.GetString("dcs-endpoints") will return value of VIP_DCS_ENDPOINTS
	replacer := strings.NewReplacer("-", "_")
	v.SetEnvKeyReplacer(replacer)

	// viper precedence order
	// - explicit call to Set
	// - flag
	// - env
	// - config
	// - key/value store
	// - default

	// if a configfile has been passed, make viper read it
	if err = loadConfigFile(v); err != nil {
		return nil, fmt.Errorf("fatal error reading config file: %w", err)
	}

	// convert string of csv to String Slice
	if endpointsString := v.GetString("dcs-endpoints"); endpointsString != "" && strings.Contains(endpointsString, ",") {
		v.Set("dcs-endpoints", strings.Split(endpointsString, ","))
	}
	setDefaults(v)
	if err = checkMandatory(v); err != nil {
		return nil, err
	}

	conf := &Config{}
	if err = v.Unmarshal(conf); err != nil {
		zap.L().Fatal("unable to decode viper config into config struct, %v", zap.Error(err))
	}

	conf.initLogger()
	printSettings(v)

	return conf, nil
}

func (conf *Config) initLogger() {
	level := zap.NewAtomicLevelAt(map[bool]zapcore.Level{
		false: zap.InfoLevel,
		true:  zap.DebugLevel}[conf.Verbose])

	// copied from "zap.NewProductionEncoderConfig" with some updates
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalColorLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,

		EncodeCaller: map[bool]zapcore.CallerEncoder{
			false: nil,
			true:  zapcore.ShortCallerEncoder}[conf.Verbose],
	}

	// When no log file is configured, keep the original stdout behaviour.
	if conf.LogFile == "" {
		lcfg := zap.Config{
			Level:       level,
			Development: false,
			Sampling: &zap.SamplingConfig{
				Initial:    100,
				Thereafter: 100,
			},
			Encoding:      "console",
			EncoderConfig: encoderConfig,

			// Use "/dev/null" to discard all
			OutputPaths:      []string{"stdout"},
			ErrorOutputPaths: []string{"stderr"},
		}
		var err error
		conf.Logger, err = lcfg.Build()
		if err != nil {
			panic(err)
		}
		return
	}

	// A log file is configured: build the core manually so that we control the
	// underlying writer and can close/reopen it on SIGHUP (see ReopenLog).
	reopener, err := newReopenableFile(conf.LogFile)
	if err != nil {
		panic(err)
	}
	conf.logReopener = reopener

	// ANSI colour codes do not belong in a log file, so use the plain encoder.
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(reopener),
		level,
	)
	// Match the sampling used by the stdout logger above.
	core = zapcore.NewSamplerWithOptions(core, time.Second, 100, 100)

	// AddCaller + AddStacktrace mirror what zap.Config.Build() adds by default.
	conf.Logger = zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
}

// ReopenLog closes and reopens the configured log file so log-rotation tools
// (e.g. logrotate) can move the current file aside and have vip-manager write
// to a freshly created file. It is a no-op when logging to stdout.
func (conf *Config) ReopenLog() error {
	if conf.logReopener == nil {
		return nil
	}
	return conf.logReopener.Reopen()
}
