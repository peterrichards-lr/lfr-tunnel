package ops

import (
	"strings"
	"testing"
)

// TestBuildNginxConfig_SingleDomain verifies the generated config matches what
// setup-central-vps.sh writes for a single `-d <domain>` -- the map block once, and the
// domain's three server blocks with the domain and port substituted in every place the
// original template uses them.
func TestBuildNginxConfig_SingleDomain(t *testing.T) {
	cfg := buildNginxConfig([]string{"lfr-demo.se"}, "8080")

	if strings.Count(cfg, "map $http_upgrade $connection_upgrade") != 1 {
		t.Errorf("expected exactly one upgrade map block, got config:\n%s", cfg)
	}
	if strings.Count(cfg, "server_name lfr-demo.se *.lfr-demo.se;") != 1 {
		t.Errorf("expected exactly one HTTP redirect server_name line for lfr-demo.se, got:\n%s", cfg)
	}
	if strings.Count(cfg, "server_name lfr-demo.se;") != 1 {
		t.Errorf("expected exactly one control-plane server_name line for lfr-demo.se, got:\n%s", cfg)
	}
	if strings.Count(cfg, "server_name *.lfr-demo.se;") != 1 {
		t.Errorf("expected exactly one wildcard data-plane server_name line for lfr-demo.se, got:\n%s", cfg)
	}
	if !strings.Contains(cfg, "ssl_certificate /etc/letsencrypt/live/lfr-demo.se/fullchain.pem;") {
		t.Error("expected the cert path to reference lfr-demo.se's own live/ directory")
	}
	if !strings.Contains(cfg, "proxy_pass http://127.0.0.1:8080;") {
		t.Error("expected proxy_pass to use the configured port")
	}
	// The ACME fallback fix (#979) must be present in both the port-80 and port-443
	// control-plane blocks -- this is the exact regression #997 exists to prevent.
	if strings.Count(cfg, "location /.well-known/acme-challenge/ {") != 2 {
		t.Errorf("expected the ACME challenge fallback location in both the port-80 and control-plane blocks, got:\n%s", cfg)
	}
}

// TestBuildNginxConfig_MultipleDomains verifies the live topology this was actually written
// for: a single lfr-tunneld instance serving two independent domain groups (lfr-demo.se and
// lfr-demo.online) needs both fully represented, with the shared upgrade map block written
// only once (nginx errors on a duplicate `map` directive).
func TestBuildNginxConfig_MultipleDomains(t *testing.T) {
	cfg := buildNginxConfig([]string{"lfr-demo.se", "lfr-demo.online"}, "8080")

	if strings.Count(cfg, "map $http_upgrade $connection_upgrade") != 1 {
		t.Errorf("expected exactly one upgrade map block shared across both domains, got config:\n%s", cfg)
	}
	for _, d := range []string{"lfr-demo.se", "lfr-demo.online"} {
		if strings.Count(cfg, "server_name "+d+";") != 1 {
			t.Errorf("expected exactly one control-plane server_name line for %s, got:\n%s", d, cfg)
		}
		if !strings.Contains(cfg, "ssl_certificate /etc/letsencrypt/live/"+d+"/fullchain.pem;") {
			t.Errorf("expected a cert path referencing %s's own live/ directory", d)
		}
	}
	// Each domain's three server blocks are independent -- one domain's cert path must never
	// leak into the other's blocks.
	seIdx := strings.Index(cfg, "server_name lfr-demo.se;")
	onlineIdx := strings.Index(cfg, "server_name lfr-demo.online;")
	if seIdx < 0 || onlineIdx < 0 {
		t.Fatalf("expected both domains' control-plane blocks to be present, got:\n%s", cfg)
	}
}

// ~/ expansion for identity files is now covered by TestResolveDeployTarget_ExpandsHomeDir
// in target_test.go -- reconcile-nginx resolves its identity file through
// ResolveDeployTarget like every other command now (#1019), rather than its own helper.
