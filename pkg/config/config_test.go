package config

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLoadServerConfig(t *testing.T) {
	// 1. Create a temporary YAML config file
	tmpFile, err := os.CreateTemp("", "server-config-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name()) //nolint:errcheck

	content := []byte(`
domains:
  - "example.com"
  - "example.org"
bind_addr: ":8443"
http_bind_addr: ":8080"
chisel_bind_addr: ":8082"
ssl_cert_file: "/path/to/cert"
ssl_key_file: "/path/to/key"
docker_image: "peterjrichards/lfr-tunnel:latest"
min_client_version: "v1.0.1"
latest_client_version: "v1.2.0"
client_platforms:
  macos_arm64:
    url: "http://example.com/darwin-arm64"
    cmd: "brew install test"
    cmd_fallback: "curl"
portal_url: "https://portal.example.com"
force_mfa: true
proxy_headers:
  X-Custom-Header: "my-custom-val"
  X-Client-IP: "$client_ip"
`)
	if _, err := tmpFile.Write(content); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	tmpFile.Close() //nolint:errcheck

	// 2. Load config from file
	cfg, err := LoadServerConfig(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to load server config: %v", err)
	}

	if len(cfg.Domains) == 0 || cfg.Domains[0] != "example.com" {
		t.Errorf("expected Domains[0] to be example.com, got %v", cfg.Domains)
	}
	if cfg.BindAddr != ":8443" {
		t.Errorf("expected BindAddr to be :8443, got %s", cfg.BindAddr)
	}
	if cfg.DockerImage != "peterjrichards/lfr-tunnel:latest" {
		t.Errorf("expected DockerImage to be peterjrichards/lfr-tunnel:latest, got %s", cfg.DockerImage)
	}
	if cfg.MinClientVersion != "v1.0.1" {
		t.Errorf("expected MinClientVersion to be v1.0.1, got %s", cfg.MinClientVersion)
	}
	if cfg.LatestClientVersion != "v1.2.0" {
		t.Errorf("expected LatestClientVersion to be v1.2.0, got %s", cfg.LatestClientVersion)
	}
	if cfg.PortalURL != "https://portal.example.com" {
		t.Errorf("expected PortalURL to be https://portal.example.com, got %s", cfg.PortalURL)
	}
	if !cfg.ForceMFA {
		t.Errorf("expected ForceMFA to be true, got false")
	}
	if cfg.ProxyHeaders == nil || cfg.ProxyHeaders["X-Custom-Header"] != "my-custom-val" || cfg.ProxyHeaders["X-Client-IP"] != "$client_ip" {
		t.Errorf("expected ProxyHeaders to be parsed correctly, got %v", cfg.ProxyHeaders)
	}
	if cfg.ClientPlatforms == nil || cfg.ClientPlatforms["macos_arm64"].URL != "http://example.com/darwin-arm64" {
		t.Errorf("expected ClientPlatforms macos_arm64 URL to be http://example.com/darwin-arm64, got %v", cfg.ClientPlatforms)
	}
	if cfg.ClientPlatforms["macos_arm64"].Cmd != "brew install test" {
		t.Errorf("expected ClientPlatforms macos_arm64 Cmd to be brew install test, got %s", cfg.ClientPlatforms["macos_arm64"].Cmd)
	}
	if cfg.ClientPlatforms["macos_arm64"].CmdFallback != "curl" {
		t.Errorf("expected ClientPlatforms macos_arm64 CmdFallback to be curl, got %s", cfg.ClientPlatforms["macos_arm64"].CmdFallback)
	}
	if !cfg.EnableWAF {
		t.Errorf("expected default EnableWAF to be true, got false")
	}

	// 3. Set environment variables to override
	os.Setenv("LFT_DOMAINS", "env.com")                    //nolint:errcheck
	os.Setenv("LFT_BIND_ADDR", ":9443")                    //nolint:errcheck
	os.Setenv("LFT_DOCKER_IMAGE", "override/image:latest") //nolint:errcheck
	os.Setenv("LFT_MIN_CLIENT_VERSION", "v2.0.0")          //nolint:errcheck
	os.Setenv("LFT_LATEST_CLIENT_VERSION", "v2.1.0")       //nolint:errcheck
	os.Setenv("LFT_ENABLE_WAF", "false")                   //nolint:errcheck
	os.Setenv("LFT_DISABLE_CLIENT_DOWNLOADS", "true")      //nolint:errcheck
	os.Setenv("LFT_DISABLE_BREW", "true")                  //nolint:errcheck
	os.Setenv("LFT_DISABLE_SCOOP", "true")                 //nolint:errcheck
	os.Setenv("LFT_PORTAL_URL", "https://env-portal.com")  //nolint:errcheck
	os.Setenv("LFT_FORCE_MFA", "false")                    //nolint:errcheck
	defer func() {
		os.Unsetenv("LFT_DOMAINS")                  //nolint:errcheck
		os.Unsetenv("LFT_BIND_ADDR")                //nolint:errcheck
		os.Unsetenv("LFT_DOCKER_IMAGE")             //nolint:errcheck
		os.Unsetenv("LFT_MIN_CLIENT_VERSION")       //nolint:errcheck
		os.Unsetenv("LFT_LATEST_CLIENT_VERSION")    //nolint:errcheck
		os.Unsetenv("LFT_ENABLE_WAF")               //nolint:errcheck
		os.Unsetenv("LFT_DISABLE_CLIENT_DOWNLOADS") //nolint:errcheck
		os.Unsetenv("LFT_DISABLE_BREW")             //nolint:errcheck
		os.Unsetenv("LFT_DISABLE_SCOOP")            //nolint:errcheck
		os.Unsetenv("LFT_PORTAL_URL")               //nolint:errcheck
		os.Unsetenv("LFT_FORCE_MFA")                //nolint:errcheck
	}()

	cfgEnv, err := LoadServerConfig(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to reload server config: %v", err)
	}

	if len(cfgEnv.Domains) == 0 || cfgEnv.Domains[0] != "env.com" {
		t.Errorf("expected Domains override to be [env.com], got %v", cfgEnv.Domains)
	}
	if cfgEnv.BindAddr != ":9443" {
		t.Errorf("expected BindAddr override to be :9443, got %s", cfgEnv.BindAddr)
	}
	if cfgEnv.DockerImage != "override/image:latest" {
		t.Errorf("expected DockerImage override to be override/image:latest, got %s", cfgEnv.DockerImage)
	}
	if cfgEnv.MinClientVersion != "v2.0.0" {
		t.Errorf("expected MinClientVersion override to be v2.0.0, got %s", cfgEnv.MinClientVersion)
	}
	if cfgEnv.LatestClientVersion != "v2.1.0" {
		t.Errorf("expected LatestClientVersion override to be v2.1.0, got %s", cfgEnv.LatestClientVersion)
	}
	if cfgEnv.EnableWAF {
		t.Errorf("expected EnableWAF override to be false, got true")
	}
	if !cfgEnv.DisableClientDownloads {
		t.Errorf("expected DisableClientDownloads override to be true, got false")
	}
	if !cfgEnv.DisableBrew {
		t.Errorf("expected DisableBrew override to be true, got false")
	}
	if !cfgEnv.DisableScoop {
		t.Errorf("expected DisableScoop override to be true, got false")
	}
	if cfgEnv.PortalURL != "https://env-portal.com" {
		t.Errorf("expected PortalURL override to be https://env-portal.com, got %s", cfgEnv.PortalURL)
	}
	if cfgEnv.ForceMFA {
		t.Errorf("expected ForceMFA override to be false, got true")
	}
}

func TestLoadServerConfig_ProxyHeadersEnv(t *testing.T) {
	// A. JSON format
	t.Setenv("LFT_PROXY_HEADERS", `{"X-Custom-Env": "$host", "X-Another": "static-val"}`)
	cfg, err := LoadServerConfig("")
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}
	if cfg.ProxyHeaders == nil || cfg.ProxyHeaders["X-Custom-Env"] != "$host" || cfg.ProxyHeaders["X-Another"] != "static-val" {
		t.Errorf("expected JSON proxy headers to be parsed, got %v", cfg.ProxyHeaders)
	}

	// B. Comma-separated format
	t.Setenv("LFT_PROXY_HEADERS", "X-Header-One=$client_ip,X-Header-Two=$proto")
	cfg, err = LoadServerConfig("")
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}
	if cfg.ProxyHeaders == nil || cfg.ProxyHeaders["X-Header-One"] != "$client_ip" || cfg.ProxyHeaders["X-Header-Two"] != "$proto" {
		t.Errorf("expected comma-separated proxy headers to be parsed, got %v", cfg.ProxyHeaders)
	}
}

func TestLoadClientConfig(t *testing.T) {
	// 1. Create a temporary YAML config file
	tmpFile, err := os.CreateTemp("", "client-config-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name()) //nolint:errcheck

	content := []byte(`
server_url: "https://my-tunnel.com"
auth_token: "client-secret"
subdomain: "test-sub"
ports:
  - 80
  - 443
passcode: "mypass"
whitelist_ips: "10.0.0.1,10.0.0.2"
`)
	if _, err := tmpFile.Write(content); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	tmpFile.Close() //nolint:errcheck

	// 2. Load config from file
	cfg, err := LoadClientConfig(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to load client config: %v", err)
	}

	if cfg.ServerURL != "https://my-tunnel.com" {
		t.Errorf("expected ServerURL to be https://my-tunnel.com, got %s", cfg.ServerURL)
	}
	if !reflect.DeepEqual(cfg.Ports, []int{80, 443}) {
		t.Errorf("expected Ports to be [80, 443], got %v", cfg.Ports)
	}
	if cfg.Passcode != "mypass" {
		t.Errorf("expected Passcode to be mypass, got %s", cfg.Passcode)
	}
	if cfg.WhitelistIPs != "10.0.0.1,10.0.0.2" {
		t.Errorf("expected WhitelistIPs to be 10.0.0.1,10.0.0.2, got %s", cfg.WhitelistIPs)
	}

	// 3. Set environment variables to override
	os.Setenv("LFT_CLIENT_SERVER", "https://env-tunnel.com") //nolint:errcheck
	os.Setenv("LFT_CLIENT_PORTS", "8080,9000")               //nolint:errcheck
	os.Setenv("LFT_PASSCODE", "envpass")                     //nolint:errcheck
	os.Setenv("LFT_WHITELIST_IPS", "192.168.1.1")            //nolint:errcheck
	defer func() {
		os.Unsetenv("LFT_CLIENT_SERVER") //nolint:errcheck
		os.Unsetenv("LFT_CLIENT_PORTS")  //nolint:errcheck
		os.Unsetenv("LFT_PASSCODE")      //nolint:errcheck
		os.Unsetenv("LFT_WHITELIST_IPS") //nolint:errcheck
	}()

	cfgEnv, err := LoadClientConfig(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to reload client config: %v", err)
	}

	if cfgEnv.ServerURL != "https://env-tunnel.com" {
		t.Errorf("expected ServerURL override to be https://env-tunnel.com, got %s", cfgEnv.ServerURL)
	}
	if !reflect.DeepEqual(cfgEnv.Ports, []int{8080, 9000}) {
		t.Errorf("expected Ports override to be [8080, 9000], got %v", cfgEnv.Ports)
	}
	if cfgEnv.Passcode != "envpass" {
		t.Errorf("expected Passcode override to be envpass, got %s", cfgEnv.Passcode)
	}
	if cfgEnv.WhitelistIPs != "192.168.1.1" {
		t.Errorf("expected WhitelistIPs override to be 192.168.1.1, got %s", cfgEnv.WhitelistIPs)
	}
}

func TestLoadClientConfig_TokenFile(t *testing.T) {
	// Create a temporary token file
	tmpTokenFile, err := os.CreateTemp("", "lfr-token-*")
	if err != nil {
		t.Fatalf("failed to create temp token file: %v", err)
	}
	defer os.Remove(tmpTokenFile.Name()) //nolint:errcheck

	tokenVal := "  my-secret-token-from-file\n "
	if _, err := tmpTokenFile.Write([]byte(tokenVal)); err != nil {
		t.Fatalf("failed to write token file: %v", err)
	}
	tmpTokenFile.Close() //nolint:errcheck

	// 1. Point LFT_TOKEN_FILE to it and clear potential other token env variables
	t.Setenv("LFT_TOKEN_FILE", tmpTokenFile.Name())
	t.Setenv("LFT_CLIENT_TOKEN", "")
	t.Setenv("LFT_TOKEN", "")

	// 2. Load client config (without path to config yaml, so it uses default)
	cfg, err := LoadClientConfig("")
	if err != nil {
		t.Fatalf("failed to load client config: %v", err)
	}

	expectedToken := "my-secret-token-from-file"
	if cfg.AuthToken != expectedToken {
		t.Errorf("expected AuthToken to be %q, got %q", expectedToken, cfg.AuthToken)
	}

	// 3. Environment variable LFT_CLIENT_TOKEN should override the token file
	t.Setenv("LFT_CLIENT_TOKEN", "env-token-override")

	cfgEnv, err := LoadClientConfig("")
	if err != nil {
		t.Fatalf("failed to reload client config: %v", err)
	}

	if cfgEnv.AuthToken != "env-token-override" {
		t.Errorf("expected LFT_CLIENT_TOKEN to override token file, got %q", cfgEnv.AuthToken)
	}
}

func TestLoadClientConfig_LDMOverridesAndTargetHost(t *testing.T) {
	// Set environment variables for fallback LDM contract and URL cleaning
	t.Setenv("LFT_SERVER_URL", "https://ldm-server-url.com")
	t.Setenv("LFT_CLIENT_TOKEN", "")
	t.Setenv("LFT_TOKEN", "ldm-token-override")
	t.Setenv("LFT_SUBDOMAIN", "ldm-subdomain-override")
	t.Setenv("LFT_TARGET_HOST", "http://liferay:8080")

	cfg, err := LoadClientConfig("")
	if err != nil {
		t.Fatalf("failed to load client config: %v", err)
	}

	if cfg.ServerURL != "https://ldm-server-url.com" {
		t.Errorf("expected ServerURL override to be https://ldm-server-url.com, got %s", cfg.ServerURL)
	}
	if cfg.AuthToken != "ldm-token-override" {
		t.Errorf("expected AuthToken override to be ldm-token-override, got %s", cfg.AuthToken)
	}
	if cfg.Subdomain != "ldm-subdomain-override" {
		t.Errorf("expected Subdomain override to be ldm-subdomain-override, got %s", cfg.Subdomain)
	}
	if cfg.TargetHost != "liferay" {
		t.Errorf("expected TargetHost override to be cleaned to liferay, got %s", cfg.TargetHost)
	}
}

func TestParseSecretsFile_Unix(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "secrets")
	content := "# Comment\nexport LFT_CLIENT_TOKEN=\"unix-secret-token\"\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	val, err := parseSecretsFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "unix-secret-token" {
		t.Errorf("expected 'unix-secret-token', got %q", val)
	}
}

func TestParseSecretsFile_PowerShell(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "secrets.ps1")
	content := "$env:LFT_TOKEN = 'ps-secret-token'\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	val, err := parseSecretsFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "ps-secret-token" {
		t.Errorf("expected 'ps-secret-token', got %q", val)
	}
}

func TestLoadClientConfig_SecretsFallback(t *testing.T) {
	tmpDir := t.TempDir()

	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)
	t.Setenv("LFT_TOKEN_FILE", "")
	t.Setenv("LFT_CLIENT_TOKEN", "")
	t.Setenv("LFT_TOKEN", "")

	secretsDir := filepath.Join(tmpDir, ".config", "lfr")
	if err := os.MkdirAll(secretsDir, 0700); err != nil {
		t.Fatalf("failed to create secrets dir: %v", err)
	}

	secretsFile := filepath.Join(secretsDir, "secrets")
	secretsContent := "export LFT_CLIENT_TOKEN=\"fallback-token-val\"\n"
	if err := os.WriteFile(secretsFile, []byte(secretsContent), 0600); err != nil {
		t.Fatalf("failed to write secrets file: %v", err)
	}

	cfg, err := LoadClientConfig("")
	if err != nil {
		t.Fatalf("failed to load client config: %v", err)
	}

	if cfg.AuthToken != "fallback-token-val" {
		t.Errorf("expected AuthToken to be fallback-token-val, got %q", cfg.AuthToken)
	}
}

func TestInsecurePermissionWarning(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "insecure-token-*")
	if err != nil {
		t.Fatalf("failed to create temp token: %v", err)
	}
	defer os.Remove(tmpFile.Name()) //nolint:errcheck

	if _, err := tmpFile.Write([]byte("some-token")); err != nil {
		t.Fatalf("failed to write: %v", err)
	}
	tmpFile.Close() //nolint:errcheck

	if err := os.Chmod(tmpFile.Name(), 0644); err != nil {
		t.Fatalf("failed to chmod: %v", err)
	}

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	t.Setenv("LFT_CLIENT_TOKEN", "")
	t.Setenv("LFT_TOKEN", "")
	t.Setenv("LFT_TOKEN_FILE", tmpFile.Name())

	_, err = LoadClientConfig("")
	if err != nil {
		w.Close() //nolint:errcheck
		os.Stderr = oldStderr
		t.Fatalf("failed to load config: %v", err)
	}

	w.Close() //nolint:errcheck
	os.Stderr = oldStderr

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r) //nolint:errcheck
	output := buf.String()

	if runtime.GOOS != "windows" {
		if !strings.Contains(output, "Warning: Token file") {
			t.Errorf("expected warning output about insecure permissions, got: %q", output)
		}
	}
}

// TestDefaultClientConfigLeavesPortsUnset pins the fix for #1710. Seeding Ports with
// []int{8080} here made "the user asked for 8080" and "the user said nothing" the same
// value, and the client only runs workspace/host discovery when the list is empty -- so
// the zero-config workspace scan was unreachable. 8080 is now applied at the point of use.
func TestDefaultClientConfigLeavesPortsUnset(t *testing.T) {
	if ports := DefaultClientConfig().Ports; len(ports) != 0 {
		t.Fatalf("DefaultClientConfig must leave Ports unset so discovery is reachable, got %v", ports)
	}
}

// TestLoadClientConfigDistinguishesUnsetPortsFromExplicitPorts is the property the fix
// depends on: a config file that never mentions `ports` must load as unset, while one that
// does must be honoured verbatim -- including an explicit 8080, which must not be confused
// with the absence of a setting.
func TestLoadClientConfigDistinguishesUnsetPortsFromExplicitPorts(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want []int
	}{
		{name: "no ports key", yaml: "server_url: \"https://tunnel.example.com\"\n", want: nil},
		{name: "explicit 8080", yaml: "ports:\n  - 8080\n", want: []int{8080}},
		{name: "explicit other ports", yaml: "ports:\n  - 3000\n  - 4001\n", want: []int{3000, 4001}},
		{name: "explicit empty list", yaml: "ports: []\n", want: []int{}},
	}
	// An LFT_CLIENT_PORTS in the developer's own environment would override the file.
	t.Setenv("LFT_CLIENT_PORTS", "")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
				t.Fatalf("failed to write config fixture: %v", err)
			}
			cfg, err := LoadClientConfig(path)
			if err != nil {
				t.Fatalf("failed to load client config: %v", err)
			}
			if len(cfg.Ports) != len(tc.want) {
				t.Fatalf("expected Ports %v, got %v", tc.want, cfg.Ports)
			}
			for i := range tc.want {
				if cfg.Ports[i] != tc.want[i] {
					t.Fatalf("expected Ports %v, got %v", tc.want, cfg.Ports)
				}
			}
		})
	}
}

// TestLoadClientConfig_IgnoresKeysRemovedIn1709 is the compatibility guarantee for #1709.
//
// token_file and bypass_proxy were parsed by ClientConfig for months and were never read by
// anything, so they were removed. People who copied them out of the example file still have them
// in ~/.lfr-tunnel/config.yaml, and their client must keep starting. LoadClientConfig decodes
// with a plain yaml.Decoder and deliberately does NOT call dec.KnownFields(true), so an
// unrecognised key is skipped rather than rejected.
//
// The assertion that matters is the absence of an error. The rest of the config still being
// applied is what proves the decode did not stop at the unknown key.
//
// Mutation-checked: adding dec.KnownFields(true) to LoadClientConfig makes this fail with
// `field token_file not found in type config.ClientConfig`.
func TestLoadClientConfig_IgnoresKeysRemovedIn1709(t *testing.T) {
	removed := []string{"token_file", "bypass_proxy"}

	// Guard against this test quietly becoming vacuous: if either key is ever re-added to
	// ClientConfig it would be a known field again, and the test would pass without testing
	// anything.
	typ := reflect.TypeOf(ClientConfig{})
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		for _, r := range removed {
			if name == r {
				t.Fatalf("ClientConfig has re-acquired the %q field that #1709 removed -- this "+
					"test no longer proves anything about unknown keys", r)
			}
		}
	}

	// Env overrides would mask a decode that silently produced nothing.
	for _, k := range []string{
		"LFT_CLIENT_SERVER", "LFT_SERVER_URL", "LFT_SERVER",
		"LFT_CLIENT_SUBDOMAIN", "LFT_SUBDOMAIN",
	} {
		t.Setenv(k, "")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// A config file as it stood before #1709: the two removed keys, set to non-zero values,
	// surrounded by settings that still exist.
	content := `
server_url: "https://legacy.example.com"
subdomain: "legacy-sub"
token_file: "/home/someone/.lfr-tunnel/token"
bypass_proxy: true
rate_limit: 42
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadClientConfig(path)
	if err != nil {
		t.Fatalf("a config file carrying the keys removed in #1709 must still load, got: %v", err)
	}

	if cfg.ServerURL != "https://legacy.example.com" {
		t.Errorf("expected ServerURL https://legacy.example.com, got %q", cfg.ServerURL)
	}
	if cfg.Subdomain != "legacy-sub" {
		t.Errorf("expected Subdomain legacy-sub, got %q", cfg.Subdomain)
	}
	// Declared after both removed keys in the file, so this is what proves decoding continued
	// past them rather than stopping at the first one.
	if cfg.RateLimit != 42 {
		t.Errorf("expected RateLimit 42 -- a key declared after the removed ones -- got %d", cfg.RateLimit)
	}
}
