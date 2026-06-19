package config

import (
	"os"
	"reflect"
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
client_platforms:
  macos_arm64:
    url: "http://example.com/darwin-arm64"
    cmd: "brew install test"
    cmd_fallback: "curl"
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
	if cfg.ClientPlatforms == nil || cfg.ClientPlatforms["macos_arm64"].URL != "http://example.com/darwin-arm64" {
		t.Errorf("expected ClientPlatforms macos_arm64 URL to be http://example.com/darwin-arm64, got %v", cfg.ClientPlatforms)
	}
	if cfg.ClientPlatforms["macos_arm64"].Cmd != "brew install test" {
		t.Errorf("expected ClientPlatforms macos_arm64 Cmd to be brew install test, got %s", cfg.ClientPlatforms["macos_arm64"].Cmd)
	}
	if cfg.ClientPlatforms["macos_arm64"].CmdFallback != "curl" {
		t.Errorf("expected ClientPlatforms macos_arm64 CmdFallback to be curl, got %s", cfg.ClientPlatforms["macos_arm64"].CmdFallback)
	}

	// 3. Set environment variables to override
	os.Setenv("LFT_DOMAINS", "env.com")                    //nolint:errcheck
	os.Setenv("LFT_BIND_ADDR", ":9443")                    //nolint:errcheck
	os.Setenv("LFT_DOCKER_IMAGE", "override/image:latest") //nolint:errcheck
	defer func() {
		os.Unsetenv("LFT_DOMAINS")      //nolint:errcheck
		os.Unsetenv("LFT_BIND_ADDR")    //nolint:errcheck
		os.Unsetenv("LFT_DOCKER_IMAGE") //nolint:errcheck
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

	// 3. Set environment variables to override
	os.Setenv("LFT_CLIENT_SERVER", "https://env-tunnel.com") //nolint:errcheck
	os.Setenv("LFT_CLIENT_PORTS", "8080,9000")               //nolint:errcheck
	defer func() {
		os.Unsetenv("LFT_CLIENT_SERVER") //nolint:errcheck
		os.Unsetenv("LFT_CLIENT_PORTS")  //nolint:errcheck
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
