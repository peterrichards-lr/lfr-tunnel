package provisioner

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "edge-provisioner.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing test config: %v", err)
	}
	return path
}

func TestLoadConfig_ValidMinimal(t *testing.T) {
	path := writeTestConfig(t, `
listen_addr: "127.0.0.1:8091"
token_file: "/tmp/token"
nodes:
  edge-sa:
    instance_id: i-abc123
    region: sa-east-1
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ScheduleGroup != "lfr-tunnel-edge-nodes" {
		t.Errorf("expected default schedule group, got %q", cfg.ScheduleGroup)
	}
	if cfg.Nodes["edge-sa"].InstanceID != "i-abc123" {
		t.Errorf("node not parsed correctly: %+v", cfg.Nodes["edge-sa"])
	}
}

func TestLoadConfig_RejectsMissingListenAddr(t *testing.T) {
	path := writeTestConfig(t, `
token_file: "/tmp/token"
`)
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for missing listen_addr")
	}
}

func TestLoadConfig_RejectsNonLoopbackAddr(t *testing.T) {
	cases := []string{
		"0.0.0.0:8091",
		":8091",
		"10.0.0.5:8091",
		"example.com:8091",
	}
	for _, addr := range cases {
		path := writeTestConfig(t, `
listen_addr: "`+addr+`"
token_file: "/tmp/token"
`)
		if _, err := LoadConfig(path); err == nil {
			t.Errorf("listen_addr %q: expected rejection as non-loopback, got no error", addr)
		}
	}
}

func TestLoadConfig_AcceptsLoopbackVariants(t *testing.T) {
	cases := []string{"127.0.0.1:8091", "localhost:8091", "[::1]:8091"}
	for _, addr := range cases {
		path := writeTestConfig(t, `
listen_addr: "`+addr+`"
token_file: "/tmp/token"
`)
		if _, err := LoadConfig(path); err != nil {
			t.Errorf("listen_addr %q: expected acceptance, got error: %v", addr, err)
		}
	}
}

func TestLoadConfig_RejectsIncompleteNode(t *testing.T) {
	path := writeTestConfig(t, `
listen_addr: "127.0.0.1:8091"
token_file: "/tmp/token"
nodes:
  edge-sa:
    instance_id: i-abc123
`)
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for node missing region")
	}
}
