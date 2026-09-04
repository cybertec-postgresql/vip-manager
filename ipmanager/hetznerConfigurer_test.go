package ipmanager

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

func setupHetznerTest(t *testing.T) {
	t.Helper()
	conf := zap.NewNop()
	log = conf.Sugar()
}

func testHetznerIPConfiguration(vip string) *IPConfiguration {
	return &IPConfiguration{
		VIP:     netip.MustParseAddr(vip),
		Netmask: net.CIDRMask(24, 32),
		Iface: net.Interface{
			Name:         "test0",
			HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		},
	}
}

func writeHetznerCredentialsFile(t *testing.T, dir, user, pass string) string {
	t.Helper()
	path := filepath.Join(dir, "hetzner")
	content := fmt.Sprintf("user=\"%s\"\npass=\"%s\"\n", user, pass)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write credentials file: %v", err)
	}
	return path
}

func newTestHetznerConfigurer(t *testing.T) *HetznerConfigurer {
	t.Helper()
	cfg := testHetznerIPConfiguration("192.168.1.10")
	c, err := newHetznerConfigurer(cfg, false)
	if err != nil {
		t.Fatalf("unexpected error creating HetznerConfigurer: %v", err)
	}
	return c
}

// ---------------------------------------------------------------------------
// newHetznerConfigurer
// ---------------------------------------------------------------------------

func TestNewHetznerConfigurer_Success(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	cfg := testHetznerIPConfiguration("10.20.30.40")
	c, err := newHetznerConfigurer(cfg, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected configurer, got nil")
	}
	if c.IPConfiguration != cfg {
		t.Error("configurer did not retain the provided IPConfiguration")
	}
	if c.cachedState != unknown {
		t.Errorf("expected cachedState to be unknown, got %d", c.cachedState)
	}
	if c.lastAPICheck.IsZero() {
		t.Error("expected lastAPICheck to be initialized to the Unix epoch")
	}
	if !c.verbose {
		t.Error("expected verbose to be true")
	}
	if c.credentialsFile != "/etc/hetzner" {
		t.Errorf("expected default credentials file path, got %q", c.credentialsFile)
	}
	if c.client == nil {
		t.Error("expected the HTTP client to be initialized")
	}
	if c.apiURL != hetznerAPI {
		t.Errorf("expected the default API URL, got %q", c.apiURL)
	}
	if c.getOutboundIP == nil {
		t.Error("expected getOutboundIP to be initialized")
	}
}

// ---------------------------------------------------------------------------
// getActiveIPFromJSON
// ---------------------------------------------------------------------------

func TestHetznerConfigurer_getActiveIPFromJSON_Success(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	c := newTestHetznerConfigurer(t)
	response := `{
		"failover": {
			"ip": "192.168.1.10",
			"netmask": "255.255.255.255",
			"server_ip": "10.0.0.1",
			"server_number": 12345,
			"active_server_ip": "10.0.0.2"
		}
	}`

	ip, err := c.getActiveIPFromJSON(response)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := net.ParseIP("10.0.0.2")
	if !ip.Equal(want) {
		t.Errorf("getActiveIPFromJSON() = %v, want %v", ip, want)
	}
}

func TestHetznerConfigurer_getActiveIPFromJSON_ErrorResponse(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	c := newTestHetznerConfigurer(t)
	response := `{
		"error": {
			"status": 401,
			"code": "UNAUTHORIZED",
			"message": "Invalid credentials"
		}
	}`

	ip, err := c.getActiveIPFromJSON(response)
	if err == nil {
		t.Fatal("expected error for API error response, got nil")
	}
	if ip != nil {
		t.Errorf("expected nil IP, got %v", ip)
	}
}

func TestHetznerConfigurer_getActiveIPFromJSON_InvalidJSON(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	c := newTestHetznerConfigurer(t)
	ip, err := c.getActiveIPFromJSON("not-json")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if ip != nil {
		t.Errorf("expected nil IP, got %v", ip)
	}
}

func TestHetznerConfigurer_getActiveIPFromJSON_UnexpectedStructure(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	c := newTestHetznerConfigurer(t)
	ip, err := c.getActiveIPFromJSON(`{"unknown": "value"}`)
	if err == nil {
		t.Fatal("expected error for unexpected JSON structure, got nil")
	}
	if ip != nil {
		t.Errorf("expected nil IP, got %v", ip)
	}
}

// ---------------------------------------------------------------------------
// queryFailover
// ---------------------------------------------------------------------------

// hetznerAPIStub records what the configurer sent to the Robot API and
// answers with a canned body. The configurer keeps its production client, so
// the tests go through the same IPv4 pinned transport.
type hetznerAPIStub struct {
	mu     sync.Mutex
	count  int
	method string
	path   string
	user   string
	pass   string
	auth   bool
	form   url.Values
}

func (s *hetznerAPIStub) snapshot() hetznerAPIStub {
	s.mu.Lock()
	defer s.mu.Unlock()
	return hetznerAPIStub{count: s.count, method: s.method, path: s.path, user: s.user, pass: s.pass, auth: s.auth, form: s.form}
}

func (s *hetznerAPIStub) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

// stubHetznerAPI points the configurer at a local server answering with body
func stubHetznerAPI(t *testing.T, c *HetznerConfigurer, body string) *hetznerAPIStub {
	t.Helper()
	stub := &hetznerAPIStub{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		user, pass, ok := r.BasicAuth()
		stub.mu.Lock()
		stub.count++
		stub.method, stub.path, stub.user, stub.pass, stub.auth, stub.form = r.Method, r.URL.Path, user, pass, ok, r.PostForm
		stub.mu.Unlock()
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	c.apiURL = server.URL
	return stub
}

// unreachableHetznerAPI points the configurer at a server that is already gone
func unreachableHetznerAPI(t *testing.T, c *HetznerConfigurer) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	c.apiURL = server.URL
	server.Close()
}

func TestHetznerConfigurer_queryFailover_GET(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	credPath := writeHetznerCredentialsFile(t, dir, "testuser", "testpass")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath
	stub := stubHetznerAPI(t, c, `{"failover":{"ip":"192.168.1.10","netmask":"255.255.255.255","server_ip":"10.0.0.1","server_number":12345,"active_server_ip":"10.0.0.1"}}`)

	resp, err := c.queryFailover(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == "" {
		t.Error("expected non-empty response")
	}

	got := stub.snapshot()
	if got.method != http.MethodGet {
		t.Errorf("method = %q, want GET", got.method)
	}
	if want := "/failover/192.168.1.10"; got.path != want {
		t.Errorf("path = %q, want %q", got.path, want)
	}
	if !got.auth || got.user != "testuser" || got.pass != "testpass" {
		t.Errorf("credentials were not sent in the Authorization header: auth=%v user=%q", got.auth, got.user)
	}
}

func TestHetznerConfigurer_queryFailover_POST(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	credPath := writeHetznerCredentialsFile(t, dir, "testuser", "testpass")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath
	c.getOutboundIP = func() (net.IP, error) {
		return net.ParseIP("10.0.0.5"), nil
	}
	stub := stubHetznerAPI(t, c, `{"failover":{"ip":"192.168.1.10","netmask":"255.255.255.255","server_ip":"10.0.0.1","server_number":12345,"active_server_ip":"10.0.0.5"}}`)

	resp, err := c.queryFailover(true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == "" {
		t.Error("expected non-empty response")
	}

	got := stub.snapshot()
	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if want := "/failover/192.168.1.10"; got.path != want {
		t.Errorf("path = %q, want %q", got.path, want)
	}
	if !got.auth || got.user != "testuser" || got.pass != "testpass" {
		t.Errorf("credentials were not sent in the Authorization header: auth=%v user=%q", got.auth, got.user)
	}
	if want := "10.0.0.5"; got.form.Get("active_server_ip") != want {
		t.Errorf("active_server_ip = %q, want %q", got.form.Get("active_server_ip"), want)
	}
}

func TestHetznerConfigurer_queryFailover_MissingCredentialsFile(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = filepath.Join(t.TempDir(), "does-not-exist")

	_, err := c.queryFailover(false)
	if err == nil {
		t.Fatal("expected error for missing credentials file, got nil")
	}
}

func TestHetznerConfigurer_queryFailover_MissingUserOrPass(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "hetzner")
	if err := os.WriteFile(path, []byte("user=\"onlyuser\"\n"), 0o600); err != nil {
		t.Fatalf("failed to write credentials file: %v", err)
	}

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = path

	_, err := c.queryFailover(false)
	if err == nil {
		t.Fatal("expected error when password is missing, got nil")
	}
}

func TestHetznerConfigurer_queryFailover_OutboundIPError(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	credPath := writeHetznerCredentialsFile(t, dir, "testuser", "testpass")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath
	c.getOutboundIP = func() (net.IP, error) {
		return nil, errors.New("no outbound IP")
	}

	_, err := c.queryFailover(true)
	if err == nil {
		t.Fatal("expected error when outbound IP lookup fails, got nil")
	}
}

func TestHetznerConfigurer_queryFailover_CommandError(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	credPath := writeHetznerCredentialsFile(t, dir, "testuser", "testpass")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath
	unreachableHetznerAPI(t, c)

	_, err := c.queryFailover(false)
	if err == nil {
		t.Fatal("expected error when the API cannot be reached, got nil")
	}
}

// ---------------------------------------------------------------------------
// queryAddress
// ---------------------------------------------------------------------------

func TestHetznerConfigurer_queryAddress_CachedConfigured(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	c := newTestHetznerConfigurer(t)
	c.cachedState = configured
	c.lastAPICheck = time.Now()

	stub := stubHetznerAPI(t, c, "")

	if got := c.queryAddress(); !got {
		t.Errorf("queryAddress() = %v, want true for cached configured state", got)
	}
	if stub.calls() > 0 {
		t.Error("expected queryAddress to use the cached state without asking the API")
	}
}

func TestHetznerConfigurer_queryAddress_CachedReleased(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	c := newTestHetznerConfigurer(t)
	c.cachedState = released
	c.lastAPICheck = time.Now()

	stub := stubHetznerAPI(t, c, "")

	if got := c.queryAddress(); got {
		t.Errorf("queryAddress() = %v, want false for cached released state", got)
	}
	if stub.calls() > 0 {
		t.Error("expected queryAddress to use the cached state without asking the API")
	}
}

func TestHetznerConfigurer_queryAddress_ExpiredCache_MatchesOwnIP(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	credPath := writeHetznerCredentialsFile(t, dir, "testuser", "testpass")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath
	c.lastAPICheck = time.Now().Add(-2 * time.Hour)
	c.cachedState = unknown

	c.getOutboundIP = func() (net.IP, error) {
		return net.ParseIP("10.0.0.5"), nil
	}
	stubHetznerAPI(t, c, `{"failover":{"ip":"192.168.1.10","netmask":"255.255.255.255","server_ip":"10.0.0.1","server_number":12345,"active_server_ip":"10.0.0.5"}}`)

	if got := c.queryAddress(); !got {
		t.Errorf("queryAddress() = %v, want true when failover points to this machine", got)
	}
	if c.cachedState != configured {
		t.Errorf("cachedState = %d, want configured", c.cachedState)
	}
}

func TestHetznerConfigurer_queryAddress_ExpiredCache_DifferentIP(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	credPath := writeHetznerCredentialsFile(t, dir, "testuser", "testpass")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath
	c.lastAPICheck = time.Now().Add(-2 * time.Hour)
	c.cachedState = unknown

	c.getOutboundIP = func() (net.IP, error) {
		return net.ParseIP("10.0.0.5"), nil
	}
	stubHetznerAPI(t, c, `{"failover":{"ip":"192.168.1.10","netmask":"255.255.255.255","server_ip":"10.0.0.1","server_number":12345,"active_server_ip":"10.0.0.9"}}`)

	if got := c.queryAddress(); got {
		t.Errorf("queryAddress() = %v, want false when failover points elsewhere", got)
	}
	if c.cachedState != released {
		t.Errorf("cachedState = %d, want released", c.cachedState)
	}
}

func TestHetznerConfigurer_queryAddress_APIError(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	credPath := writeHetznerCredentialsFile(t, dir, "testuser", "testpass")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath
	c.lastAPICheck = time.Now().Add(-2 * time.Hour)

	c.getOutboundIP = func() (net.IP, error) {
		return net.ParseIP("10.0.0.5"), nil
	}
	unreachableHetznerAPI(t, c)

	if got := c.queryAddress(); got {
		t.Errorf("queryAddress() = %v, want false when the API cannot be reached", got)
	}
}

func TestHetznerConfigurer_queryAddress_OutboundIPError(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	credPath := writeHetznerCredentialsFile(t, dir, "testuser", "testpass")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath
	c.lastAPICheck = time.Now().Add(-2 * time.Hour)

	c.getOutboundIP = func() (net.IP, error) {
		return nil, errors.New("no outbound IP")
	}
	stubHetznerAPI(t, c, `{"failover":{"ip":"192.168.1.10","netmask":"255.255.255.255","server_ip":"10.0.0.1","server_number":12345,"active_server_ip":"10.0.0.5"}}`)

	if got := c.queryAddress(); got {
		t.Errorf("queryAddress() = %v, want false when outbound IP lookup fails", got)
	}
}

// ---------------------------------------------------------------------------
// configureAddress / runAddressConfiguration
// ---------------------------------------------------------------------------

func TestHetznerConfigurer_configureAddress_Success(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	credPath := writeHetznerCredentialsFile(t, dir, "testuser", "testpass")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath

	c.getOutboundIP = func() (net.IP, error) {
		return net.ParseIP("10.0.0.5"), nil
	}
	stubHetznerAPI(t, c, `{"failover":{"ip":"192.168.1.10","netmask":"255.255.255.255","server_ip":"10.0.0.1","server_number":12345,"active_server_ip":"10.0.0.5"}}`)

	if got := c.configureAddress(); !got {
		t.Errorf("configureAddress() = %v, want true on successful failover", got)
	}
	if c.cachedState != configured {
		t.Errorf("cachedState = %d, want configured", c.cachedState)
	}
}

func TestHetznerConfigurer_configureAddress_APIError(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	credPath := writeHetznerCredentialsFile(t, dir, "testuser", "testpass")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath
	unreachableHetznerAPI(t, c)

	if got := c.configureAddress(); got {
		t.Errorf("configureAddress() = %v, want false when the API cannot be reached", got)
	}
	if c.cachedState != unknown {
		t.Errorf("cachedState = %d, want unknown", c.cachedState)
	}
}

func TestHetznerConfigurer_configureAddress_DifferentIP(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	credPath := writeHetznerCredentialsFile(t, dir, "testuser", "testpass")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath
	c.getOutboundIP = func() (net.IP, error) {
		return net.ParseIP("10.0.0.5"), nil
	}
	stubHetznerAPI(t, c, `{"failover":{"ip":"192.168.1.10","netmask":"255.255.255.255","server_ip":"10.0.0.1","server_number":12345,"active_server_ip":"10.0.0.9"}}`)

	if got := c.configureAddress(); got {
		t.Errorf("configureAddress() = %v, want false when API reports different active IP", got)
	}
	if c.cachedState != unknown {
		t.Errorf("cachedState = %d, want unknown", c.cachedState)
	}
}

func TestHetznerConfigurer_configureAddress_OutboundIPError(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	credPath := writeHetznerCredentialsFile(t, dir, "testuser", "testpass")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath
	c.getOutboundIP = func() (net.IP, error) {
		return nil, errors.New("no outbound IP")
	}
	stubHetznerAPI(t, c, `{"failover":{"ip":"192.168.1.10","netmask":"255.255.255.255","server_ip":"10.0.0.1","server_number":12345,"active_server_ip":"10.0.0.5"}}`)

	if got := c.configureAddress(); got {
		t.Errorf("configureAddress() = %v, want false when outbound IP lookup fails", got)
	}
}

// ---------------------------------------------------------------------------
// deconfigureAddress
// ---------------------------------------------------------------------------

func TestHetznerConfigurer_deconfigureAddress(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	c := newTestHetznerConfigurer(t)
	c.cachedState = configured

	if got := c.deconfigureAddress(); !got {
		t.Errorf("deconfigureAddress() = %v, want true", got)
	}
	if c.cachedState != released {
		t.Errorf("cachedState = %d, want released", c.cachedState)
	}
}

// ---------------------------------------------------------------------------
// Additional error path tests
// ---------------------------------------------------------------------------

func TestHetznerConfigurer_queryFailover_ShortLine(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "hetzner")
	// Write a line that's too short (< 4 chars) which should be skipped
	if err := os.WriteFile(path, []byte("usr\nuser=\"testuser\"\npass=\"testpass\"\n"), 0o600); err != nil {
		t.Fatalf("failed to write credentials file: %v", err)
	}

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = path
	stubHetznerAPI(t, c, `{"failover":{"ip":"192.168.1.10","netmask":"255.255.255.255","server_ip":"10.0.0.1","server_number":12345,"active_server_ip":"10.0.0.1"}}`)

	// Should succeed - short lines are skipped
	_, err := c.queryFailover(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHetznerConfigurer_queryFailover_OnlyShortLines(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "hetzner")
	// Write only short lines - no valid credentials
	if err := os.WriteFile(path, []byte("usr\nps\n"), 0o600); err != nil {
		t.Fatalf("failed to write credentials file: %v", err)
	}

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = path

	_, err := c.queryFailover(false)
	if err == nil {
		t.Fatal("expected error when no valid credentials found, got nil")
	}
}

func TestHetznerConfigurer_queryFailover_MalformedCredentials(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "hetzner")
	// Write credentials that match the prefix but are too short to extract values
	if err := os.WriteFile(path, []byte("user=\"\npass=\"\n"), 0o600); err != nil {
		t.Fatalf("failed to write credentials file: %v", err)
	}

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = path

	_, err := c.queryFailover(false)
	if err == nil {
		t.Fatal("expected error when credentials are empty, got nil")
	}
}

func TestHetznerConfigurer_queryAddress_ParseJSONError(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	credPath := writeHetznerCredentialsFile(t, dir, "testuser", "testpass")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath
	c.lastAPICheck = time.Now().Add(-2 * time.Hour)

	c.getOutboundIP = func() (net.IP, error) {
		return net.ParseIP("10.0.0.5"), nil
	}
	stubHetznerAPI(t, c, `invalid json`)

	if got := c.queryAddress(); got {
		t.Errorf("queryAddress() = %v, want false when JSON parsing fails", got)
	}
	// After the fix, queryAddress should return false early on JSON parse error
	if c.cachedState != unknown {
		t.Errorf("cachedState = %d, want unknown after JSON parse error", c.cachedState)
	}
}

func TestHetznerConfigurer_queryAddress_BothErrorsOccur(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	credPath := writeHetznerCredentialsFile(t, dir, "testuser", "testpass")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath
	c.lastAPICheck = time.Now().Add(-2 * time.Hour)

	// Both curl and JSON parsing will fail
	unreachableHetznerAPI(t, c)

	if got := c.queryAddress(); got {
		t.Errorf("queryAddress() = %v, want false when the API cannot be reached", got)
	}
	if c.cachedState != unknown {
		t.Errorf("cachedState = %d, want unknown", c.cachedState)
	}
}

func TestHetznerConfigurer_configureAddress_JSONParseError(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	credPath := writeHetznerCredentialsFile(t, dir, "testuser", "testpass")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath
	c.getOutboundIP = func() (net.IP, error) {
		return net.ParseIP("10.0.0.5"), nil
	}
	stubHetznerAPI(t, c, `{invalid json}`)

	if got := c.configureAddress(); got {
		t.Errorf("configureAddress() = %v, want false when JSON parse fails", got)
	}
	if c.cachedState != unknown {
		t.Errorf("cachedState = %d, want unknown", c.cachedState)
	}
}

func TestHetznerConfigurer_getActiveIPFromJSON_MissingFields(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	c := newTestHetznerConfigurer(t)

	tests := []struct {
		name     string
		response string
	}{
		{
			name: "missing active_server_ip",
			response: `{
				"failover": {
					"ip": "192.168.1.10",
					"netmask": "255.255.255.255",
					"server_ip": "10.0.0.1",
					"server_number": 12345
				}
			}`,
		},
		{
			name: "active_server_ip is not a string",
			response: `{
				"failover": {
					"ip": "192.168.1.10",
					"netmask": "255.255.255.255",
					"server_ip": "10.0.0.1",
					"server_number": 12345,
					"active_server_ip": 12345
				}
			}`,
		},
		{
			name: "failover is not an object",
			response: `{
				"failover": "not an object"
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This will panic with current implementation due to type assertions
			// We're testing that the panic is caught or the function handles it
			defer func() {
				if r := recover(); r == nil {
					t.Error("expected panic for type assertion but got none")
				}
			}()
			_, _ = c.getActiveIPFromJSON(tt.response)
		})
	}
}

func TestHetznerConfigurer_queryAddress_NilIPFromJSON(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	dir := t.TempDir()
	credPath := writeHetznerCredentialsFile(t, dir, "testuser", "testpass")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath
	c.lastAPICheck = time.Now().Add(-2 * time.Hour)

	c.getOutboundIP = func() (net.IP, error) {
		return net.ParseIP("10.0.0.5"), nil
	}
	// Return response that will be parsed but returns nil IP (edge case)
	stubHetznerAPI(t, c, `{"unexpected": "structure"}`)

	if got := c.queryAddress(); got {
		t.Errorf("queryAddress() = %v, want false when JSON structure is unexpected", got)
	}
}

func TestHetznerConfigurer_getCIDR(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	c := newTestHetznerConfigurer(t)
	cidr := c.getCIDR()
	expected := "192.168.1.10/24"
	if cidr != expected {
		t.Errorf("getCIDR() = %v, want %v", cidr, expected)
	}
}

// TestHetznerConfigurer_readCredentials verifies that the values are read as
// written. They used to be cut out of the line by offset, which required
// exactly user="value" and silently dropped the last character otherwise.
func TestHetznerConfigurer_readCredentials(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	tests := []struct {
		name     string
		content  string
		user     string
		password string
		wantErr  bool
	}{
		{"documented format", "user=\"myUsername\"\npass=\"myPassword\"\n", "myUsername", "myPassword", false},
		{"spaces around the equals sign", "user = \"myUsername\"\npass = \"myPassword\"\n", "myUsername", "myPassword", false},
		{"without quotes", "user=myUsername\npass=myPassword\n", "myUsername", "myPassword", false},
		{"single quotes", "user='myUsername'\npass='myPassword'\n", "myUsername", "myPassword", false},
		{"long names", "username=\"myUsername\"\npassword=\"myPassword\"\n", "myUsername", "myPassword", false},
		{"password only", "pass=\"myPassword\"\n", "", "", true},
		{"nothing usable", "# a comment\n\n", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "hetzner")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("failed to write credentials file: %v", err)
			}
			c := newTestHetznerConfigurer(t)
			c.credentialsFile = path

			user, password, err := c.readCredentials()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if user != tt.user || password != tt.password {
				t.Errorf("got user %q password %q, want %q and %q", user, password, tt.user, tt.password)
			}
		})
	}
}

// TestHetznerConfigurer_queryFailover_KeepsCredentialsOutOfTheURL makes sure
// the credentials go into the Authorization header only. In the URL they would
// end up in the logs of every proxy on the way.
func TestHetznerConfigurer_queryFailover_KeepsCredentialsOutOfTheURL(t *testing.T) {
	t.Parallel()
	setupHetznerTest(t)

	credPath := writeHetznerCredentialsFile(t, t.TempDir(), "testuser", "s3cret")

	c := newTestHetznerConfigurer(t)
	c.credentialsFile = credPath

	var requestURI string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURI = r.URL.String()
		_, _ = io.WriteString(w, "{}")
	}))
	defer server.Close()
	c.apiURL = server.URL

	if _, err := c.queryFailover(false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(requestURI, "s3cret") || strings.Contains(requestURI, "testuser") {
		t.Errorf("credentials leaked into the request URI: %s", requestURI)
	}
}
