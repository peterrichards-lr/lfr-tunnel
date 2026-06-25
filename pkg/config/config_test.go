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
	os.Setenv("LFT_PORTAL_URL", "https://env-portal.com")  //nolint:errcheck
	defer func() {
		os.Unsetenv("LFT_DOMAINS")                  //nolint:errcheck
		os.Unsetenv("LFT_BIND_ADDR")                //nolint:errcheck
		os.Unsetenv("LFT_DOCKER_IMAGE")             //nolint:errcheck
		os.Unsetenv("LFT_MIN_CLIENT_VERSION")       //nolint:errcheck
		os.Unsetenv("LFT_LATEST_CLIENT_VERSION")    //nolint:errcheck
		os.Unsetenv("LFT_ENABLE_WAF")               //nolint:errcheck
		os.Unsetenv("LFT_DISABLE_CLIENT_DOWNLOADS") //nolint:errcheck
		os.Unsetenv("LFT_PORTAL_URL")               //nolint:errcheck
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
	if cfgEnv.PortalURL != "https://env-portal.com" {
		t.Errorf("expected PortalURL override to be https://env-portal.com, got %s", cfgEnv.PortalURL)
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

	// 1. Point LFT_TOKEN_FILE to it
	os.Setenv("LFT_TOKEN_FILE", tmpTokenFile.Name()) //nolint:errcheck
	defer os.Unsetenv("LFT_TOKEN_FILE")              //nolint:errcheck

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
	os.Setenv("LFT_CLIENT_TOKEN", "env-token-override") //nolint:errcheck
	defer os.Unsetenv("LFT_CLIENT_TOKEN")               //nolint:errcheck

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
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	if runtime.GOOS != "windows" {
		if !strings.Contains(output, "Warning: Token file") {
			t.Errorf("expected warning output about insecure permissions, got: %q", output)
		}
	}
}
