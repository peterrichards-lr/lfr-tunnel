package main

import (
	"os"
	"path/filepath"
	"testing"

	"lfr-tunnel/pkg/config"
)

// A config file saved by the tray or the Inspector before #2560 holds `preserve_host: false`
// that nobody chose. Loaded through the real loader, it must still get detection for the
// Liferay port: the file's false means "not set", not "off".
func TestLegacyPreserveHostFalseStillGetsDetection(t *testing.T) {
	resetFacts(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("subdomain: demo\npreserve_host: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadClientConfig(path)
	if err != nil {
		t.Fatalf("LoadClientConfig: %v", err)
	}
	facts.liferayPort = 8080

	plan := currentPreserveHostPlan(cfg)
	if plan.all || len(plan.ports) != 1 || plan.ports[0] != 8080 {
		t.Errorf("legacy preserve_host: false gave all=%t ports=%v (%q); want detection on for 8080 only",
			plan.all, plan.ports, plan.summary)
	}
	if plan.summary != "on for 8080 (Liferay detected)" {
		t.Errorf("summary = %q", plan.summary)
	}
}
