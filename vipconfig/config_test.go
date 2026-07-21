package vipconfig

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// ---------------------------------------------------------------------------
// checkSetting
// ---------------------------------------------------------------------------

func TestCheckSetting_NotSet(t *testing.T) {
	v := viper.New()
	if checkSetting(v, "nonexistent-key") {
		t.Error("expected false for unset key")
	}
}

func TestCheckSetting_Set(t *testing.T) {
	v := viper.New()
	v.Set("some-key", "value")
	if !checkSetting(v, "some-key") {
		t.Error("expected true for set key")
	}
}

// ---------------------------------------------------------------------------
// checkImpliedSetting
// ---------------------------------------------------------------------------

func TestCheckImpliedSetting_ReasonNotSet(t *testing.T) {
	v := viper.New()
	// reason not set => implied is not required
	if !checkImpliedSetting(v, "etcd-user", "etcd-password") {
		t.Error("expected true when reason is not set")
	}
}

func TestCheckImpliedSetting_BothSet(t *testing.T) {
	v := viper.New()
	v.Set("etcd-password", "secret")
	v.Set("etcd-user", "admin")
	if !checkImpliedSetting(v, "etcd-user", "etcd-password") {
		t.Error("expected true when both implied and reason are set")
	}
}

func TestCheckImpliedSetting_ReasonSetImpliedMissing(t *testing.T) {
	v := viper.New()
	v.Set("etcd-password", "secret")
	// etcd-user not set
	if checkImpliedSetting(v, "etcd-user", "etcd-password") {
		t.Error("expected false when reason is set but implied is not")
	}
}

// ---------------------------------------------------------------------------
// checkMandatory
// ---------------------------------------------------------------------------

func TestCheckMandatory_AllMissing(t *testing.T) {
	v := viper.New()
	if err := checkMandatory(v); err == nil {
		t.Error("expected error when all mandatory settings are missing")
	}
}

func TestCheckMandatory_PartiallySet(t *testing.T) {
	v := viper.New()
	v.Set("ip", "10.0.0.1")
	v.Set("netmask", 24)
	// interface, trigger-key, trigger-value, dcs-endpoints still missing
	if err := checkMandatory(v); err == nil {
		t.Error("expected error when some mandatory settings are missing")
	}
}

func TestCheckMandatory_AllSet(t *testing.T) {
	v := viper.New()
	v.Set("ip", "10.0.0.1")
	v.Set("netmask", 24)
	v.Set("interface", "eth0")
	v.Set("trigger-key", "/service/pgcluster/leader")
	v.Set("trigger-value", "host1")
	v.Set("dcs-endpoints", []string{"http://127.0.0.1:2379"})
	if err := checkMandatory(v); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// checkImpliedMandatory
// ---------------------------------------------------------------------------

func TestCheckImpliedMandatory_NoneSet(t *testing.T) {
	v := viper.New()
	if err := checkImpliedMandatory(v); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestCheckImpliedMandatory_EtcdPasswordWithoutUser(t *testing.T) {
	v := viper.New()
	v.Set("etcd-password", "secret")
	if err := checkImpliedMandatory(v); err == nil {
		t.Error("expected error: etcd-password set without etcd-user")
	}
}

func TestCheckImpliedMandatory_EtcdCertFileWithoutKeyFile(t *testing.T) {
	v := viper.New()
	v.Set("etcd-cert-file", "/path/to/cert")
	if err := checkImpliedMandatory(v); err == nil {
		t.Error("expected error: etcd-cert-file set without etcd-key-file")
	}
}

func TestCheckImpliedMandatory_EtcdCertFileWithoutCAFile(t *testing.T) {
	v := viper.New()
	v.Set("etcd-cert-file", "/path/to/cert")
	v.Set("etcd-key-file", "/path/to/key")
	// etcd-ca-file still missing
	if err := checkImpliedMandatory(v); err == nil {
		t.Error("expected error: etcd-cert-file set without etcd-ca-file")
	}
}

func TestCheckImpliedMandatory_AllEtcdTLSSet(t *testing.T) {
	v := viper.New()
	v.Set("etcd-user", "admin")
	v.Set("etcd-password", "secret")
	v.Set("etcd-cert-file", "/path/to/cert")
	v.Set("etcd-key-file", "/path/to/key")
	v.Set("etcd-ca-file", "/path/to/ca")
	if err := checkImpliedMandatory(v); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// setDefaults
// ---------------------------------------------------------------------------

func TestSetDefaults_DcsTypeDefaultsToEtcd(t *testing.T) {
	v := viper.New()
	v.Set("trigger-value", "host1")
	setDefaults(v)
	if got := v.GetString("dcs-type"); got != "etcd" {
		t.Errorf("expected dcs-type=etcd, got %q", got)
	}
}

func TestSetDefaults_ManagerTypeDefaultsToBasic(t *testing.T) {
	v := viper.New()
	v.Set("trigger-value", "host1")
	setDefaults(v)
	if got := v.GetString("manager-type"); got != "basic" {
		t.Errorf("expected manager-type=basic, got %q", got)
	}
}

func TestSetDefaults_EndpointsDefaultEtcd(t *testing.T) {
	v := viper.New()
	v.Set("trigger-value", "host1")
	setDefaults(v)
	endpoints := v.GetStringSlice("dcs-endpoints")
	if len(endpoints) == 0 || endpoints[0] != "http://127.0.0.1:2379" {
		t.Errorf("expected etcd default endpoint, got %v", endpoints)
	}
}

func TestSetDefaults_EndpointsDefaultConsul(t *testing.T) {
	v := viper.New()
	v.Set("dcs-type", "consul")
	v.Set("trigger-value", "host1")
	setDefaults(v)
	endpoints := v.GetStringSlice("dcs-endpoints")
	if len(endpoints) == 0 || endpoints[0] != "http://127.0.0.1:8500" {
		t.Errorf("expected consul default endpoint, got %v", endpoints)
	}
}

func TestSetDefaults_EndpointsDefaultPatroni(t *testing.T) {
	v := viper.New()
	v.Set("dcs-type", "patroni")
	v.Set("trigger-value", "host1")
	setDefaults(v)
	endpoints := v.GetStringSlice("dcs-endpoints")
	if len(endpoints) == 0 || endpoints[0] != "http://127.0.0.1:8008/" {
		t.Errorf("expected patroni default endpoint, got %v", endpoints)
	}
}

func TestSetDefaults_PatroniTriggerKeyDefault(t *testing.T) {
	v := viper.New()
	v.Set("dcs-type", "patroni")
	v.Set("trigger-value", "host1")
	setDefaults(v)
	if got := v.GetString("trigger-key"); got != "/leader" {
		t.Errorf("expected trigger-key=/leader for patroni, got %q", got)
	}
}

func TestSetDefaults_PatroniTriggerValueDefault(t *testing.T) {
	v := viper.New()
	v.Set("dcs-type", "patroni")
	setDefaults(v)
	if got := v.GetString("trigger-value"); got != "200" {
		t.Errorf("expected trigger-value=200 for patroni, got %q", got)
	}
}

func TestSetDefaults_TriggerValueFallsBackToHostname(t *testing.T) {
	v := viper.New()
	// dcs-type will default to etcd; no trigger-value set
	setDefaults(v)
	hostname, err := os.Hostname()
	if err != nil {
		t.Skip("hostname unavailable, skipping")
	}
	if got := v.GetString("trigger-value"); got != hostname {
		t.Errorf("expected trigger-value=%q (hostname), got %q", hostname, got)
	}
}

func TestSetDefaults_ExplicitEndpointsNotOverridden(t *testing.T) {
	v := viper.New()
	v.Set("dcs-endpoints", []string{"http://192.168.1.1:2379"})
	v.Set("trigger-value", "host1")
	setDefaults(v)
	endpoints := v.GetStringSlice("dcs-endpoints")
	if len(endpoints) == 0 || endpoints[0] != "http://192.168.1.1:2379" {
		t.Errorf("explicit endpoint should not be overridden, got %v", endpoints)
	}
}

func TestSetDefaults_RetryNumZeroResetsToDefault(t *testing.T) {
	v := viper.New()
	v.Set("trigger-value", "host1")
	v.Set("retry-num", 0)
	setDefaults(v)
	if got := v.GetInt("retry-num"); got != 3 {
		t.Errorf("expected retry-num=3 when set to 0, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// loadConfigFile
// ---------------------------------------------------------------------------

func TestLoadConfigFile_ConfigKeyNotSet(t *testing.T) {
	v := viper.New()
	if err := loadConfigFile(v); err != nil {
		t.Errorf("expected nil when config key is not set, got: %v", err)
	}
}

func TestLoadConfigFile_NonexistentFile(t *testing.T) {
	v := viper.New()
	v.Set("config", "/nonexistent/path/config.yml")
	if err := loadConfigFile(v); err == nil {
		t.Error("expected error for nonexistent config file")
	}
}

func TestLoadConfigFile_ValidYAML(t *testing.T) {
	v := viper.New()
	content := []byte("ip: 10.0.0.1\nnetmask: 24\ninterface: eth0\n")
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	v.Set("config", path)
	if err := loadConfigFile(v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := v.GetString("ip"); got != "10.0.0.1" {
		t.Errorf("expected ip=10.0.0.1, got %q", got)
	}
	if got := v.GetInt("netmask"); got != 24 {
		t.Errorf("expected netmask=24, got %d", got)
	}
}

func TestLoadConfigFile_InvalidYAML(t *testing.T) {
	v := viper.New()
	content := []byte("this: is: not: valid: yaml: :\n")
	path := filepath.Join(t.TempDir(), "bad.yml")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	v.Set("config", path)
	if err := loadConfigFile(v); err == nil {
		t.Error("expected error for invalid YAML")
	}
}

// ---------------------------------------------------------------------------
// printSettings
// ---------------------------------------------------------------------------

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	return buf.String()
}

func TestPrintSettings_MasksSensitiveValues(t *testing.T) {
	v := viper.New()
	v.Set("etcd-password", "supersecret")
	v.Set("consul-token", "mytoken")
	v.Set("ip", "10.0.0.1")

	out := captureStdout(t, func() { printSettings(v) })

	if strings.Contains(out, "supersecret") {
		t.Error("etcd-password value should be masked")
	}
	if strings.Contains(out, "mytoken") {
		t.Error("consul-token value should be masked")
	}
	if !strings.Contains(out, "*****") {
		t.Error("expected masked placeholder '*****' in output")
	}
	if !strings.Contains(out, "10.0.0.1") {
		t.Error("expected non-sensitive ip value to appear in output")
	}
}

func TestPrintSettings_EmptySettings(t *testing.T) {
	v := viper.New()
	// should not panic with no settings
	captureStdout(t, func() { printSettings(v) })
}

// ---------------------------------------------------------------------------
// initLogger
// ---------------------------------------------------------------------------

func TestInitLogger_NonVerbose(t *testing.T) {
	conf := &Config{Verbose: false}
	conf.initLogger()
	if conf.Logger == nil {
		t.Fatal("expected non-nil logger")
	}
	_ = conf.Logger.Sync()
}

func TestInitLogger_Verbose(t *testing.T) {
	conf := &Config{Verbose: true}
	conf.initLogger()
	if conf.Logger == nil {
		t.Fatal("expected non-nil logger")
	}
	_ = conf.Logger.Sync()
}

// ---------------------------------------------------------------------------
// defineFlags
// ---------------------------------------------------------------------------

func TestDefineFlags_AllFlagsPresent(t *testing.T) {
	expected := []string{
		"config", "version",
		"ip", "netmask", "interface",
		"trigger-key", "trigger-value",
		"dcs-type", "dcs-endpoints",
		"etcd-user", "etcd-password", "etcd-ca-file", "etcd-cert-file", "etcd-key-file",
		"consul-token",
		"interval", "manager-type",
		"retry-after", "retry-num",
		"verbose",
	}
	flags := defineFlags()
	for _, name := range expected {
		if flags.Lookup(name) == nil {
			t.Errorf("expected flag %q to be defined", name)
		}
	}
}

func TestDefineFlags_Defaults(t *testing.T) {
	flags := defineFlags()
	cases := []struct {
		flag string
		want string
	}{
		{"dcs-type", "etcd"},
		{"manager-type", "basic"},
		{"interval", "1000"},
		{"retry-after", "250"},
		{"retry-num", "3"},
		{"verbose", "false"},
		{"version", "false"},
	}
	for _, tc := range cases {
		if got := flags.Lookup(tc.flag).DefValue; got != tc.want {
			t.Errorf("flag %q default: got %q, want %q", tc.flag, got, tc.want)
		}
	}
}

func TestDefineFlags_SortFlagsDisabled(t *testing.T) {
	flags := defineFlags()
	if flags.SortFlags {
		t.Error("expected SortFlags=false")
	}
}

func TestDefineFlags_ParseValues(t *testing.T) {
	flags := defineFlags()
	args := []string{"--ip=10.0.0.1", "--netmask=24", "--dcs-type=consul"}
	if err := flags.Parse(args); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, _ := flags.GetString("ip"); got != "10.0.0.1" {
		t.Errorf("ip: got %q, want 10.0.0.1", got)
	}
	if got, _ := flags.GetString("netmask"); got != "24" {
		t.Errorf("netmask: got %q, want \"24\"", got)
	}
	if got, _ := flags.GetString("dcs-type"); got != "consul" {
		t.Errorf("dcs-type: got %q, want consul", got)
	}
}

// ---------------------------------------------------------------------------
// newConfig
// ---------------------------------------------------------------------------

// minimalConfigFile writes a valid config YAML to a temp file and returns its path.
func minimalConfigFile(t *testing.T, extra ...string) string {
	t.Helper()
	base := `
ip: 10.0.0.1
netmask: 24
interface: eth0
trigger-key: /service/pgcluster/leader
trigger-value: host1
dcs-type: etcd
dcs-endpoints:
  - http://127.0.0.1:2379
`
	content := base + strings.Join(extra, "\n")
	path := filepath.Join(t.TempDir(), "vip-manager.yml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNewConfig_ValidConfigFile(t *testing.T) {
	path := minimalConfigFile(t)
	conf, err := newConfig([]string{fmt.Sprintf("--config=%s", path)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conf.IP != "10.0.0.1" {
		t.Errorf("IP: got %q, want 10.0.0.1", conf.IP)
	}
	if conf.Mask != 24 {
		t.Errorf("Mask: got %d, want 24", conf.Mask)
	}
	if conf.Iface != "eth0" {
		t.Errorf("Iface: got %q, want eth0", conf.Iface)
	}
	if conf.Logger == nil {
		t.Error("expected non-nil logger")
	}
}

func TestNewConfig_FlagOverridesFile(t *testing.T) {
	path := minimalConfigFile(t)
	conf, err := newConfig([]string{
		fmt.Sprintf("--config=%s", path),
		"--trigger-value=overridden",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conf.TriggerValue != "overridden" {
		t.Errorf("TriggerValue: got %q, want overridden", conf.TriggerValue)
	}
}

func TestNewConfig_MissingMandatory(t *testing.T) {
	// no config file, no flags → mandatory settings missing
	_, err := newConfig([]string{})
	if err == nil {
		t.Error("expected error for missing mandatory settings")
	}
}

func TestNewConfig_NonexistentConfigFile(t *testing.T) {
	_, err := newConfig([]string{"--config=/nonexistent/path.yml"})
	if err == nil {
		t.Error("expected error for nonexistent config file")
	}
}

func TestNewConfig_CSVEndpoints(t *testing.T) {
	path := minimalConfigFile(t)
	conf, err := newConfig([]string{
		fmt.Sprintf("--config=%s", path),
		"--dcs-endpoints=http://127.0.0.1:2379,http://127.0.0.2:2379",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(conf.Endpoints) != 2 {
		t.Errorf("expected 2 endpoints, got %d: %v", len(conf.Endpoints), conf.Endpoints)
	}
}

func TestNewConfig_EnvVarOverride(t *testing.T) {
	path := minimalConfigFile(t)
	t.Setenv("VIP_TRIGGER_VALUE", "from-env")
	conf, err := newConfig([]string{fmt.Sprintf("--config=%s", path)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conf.TriggerValue != "from-env" {
		t.Errorf("TriggerValue: got %q, want from-env", conf.TriggerValue)
	}
}

func TestNewConfig_InvalidFlag(t *testing.T) {
	_, err := newConfig([]string{"--nonexistent-flag=value"})
	if err == nil {
		t.Error("expected error for unknown flag")
	}
}

// ---------------------------------------------------------------------------
// NewConfig (public API)
// ---------------------------------------------------------------------------

func TestNewConfig_CreatesConfig(t *testing.T) {
	// NewConfig reads from os.Args, so we need to set them via os.Args
	// Save original os.Args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	path := minimalConfigFile(t)
	os.Args = []string{oldArgs[0], fmt.Sprintf("--config=%s", path)}
	
	conf, err := NewConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conf == nil {
		t.Fatal("expected non-nil config")
	}
	if conf.IP != "10.0.0.1" {
		t.Errorf("IP: got %q, want 10.0.0.1", conf.IP)
	}
}
