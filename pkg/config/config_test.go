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
	defer os.Remove(tmpFile.Name())

	content := []byte(`
domain1: "example.com"
domain2: "example.org"
bind_addr: ":8443"
http_bind_addr: ":8080"
chisel_bind_addr: ":8082"
auth_token: "file-secret"
ssl_cert_file: "/path/to/cert"
ssl_key_file: "/path/to/key"
`)
	if _, err := tmpFile.Write(content); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	tmpFile.Close()

	// 2. Load config from file
	cfg, err := LoadServerConfig(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to load server config: %v", err)
	}

	if cfg.Domain1 != "example.com" {
		t.Errorf("expected Domain1 to be example.com, got %s", cfg.Domain1)
	}
	if cfg.BindAddr != ":8443" {
		t.Errorf("expected BindAddr to be :8443, got %s", cfg.BindAddr)
	}

	// 3. Set environment variables to override
	os.Setenv("LFT_DOMAIN1", "env.com")
	os.Setenv("LFT_BIND_ADDR", ":9443")
	defer func() {
		os.Unsetenv("LFT_DOMAIN1")
		os.Unsetenv("LFT_BIND_ADDR")
	}()

	cfgEnv, err := LoadServerConfig(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to reload server config: %v", err)
	}

	if cfgEnv.Domain1 != "env.com" {
		t.Errorf("expected Domain1 override to be env.com, got %s", cfgEnv.Domain1)
	}
	if cfgEnv.BindAddr != ":9443" {
		t.Errorf("expected BindAddr override to be :9443, got %s", cfgEnv.BindAddr)
	}
}

func TestLoadClientConfig(t *testing.T) {
	// 1. Create a temporary YAML config file
	tmpFile, err := os.CreateTemp("", "client-config-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

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
	tmpFile.Close()

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
	os.Setenv("LFT_CLIENT_SERVER", "https://env-tunnel.com")
	os.Setenv("LFT_CLIENT_PORTS", "8080,9000")
	defer func() {
		os.Unsetenv("LFT_CLIENT_SERVER")
		os.Unsetenv("LFT_CLIENT_PORTS")
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
