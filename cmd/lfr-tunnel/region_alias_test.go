package main

import "testing"

// TestGatewayHostKeyCollapsesAliases is the regression test for #1166. The gateway
// advertises each region under two names, so anything keyed on the name treats one
// gateway as several places.
func TestGatewayHostKeyCollapsesAliases(t *testing.T) {
	// The live advertised map, which is where this was found.
	central := "https://tunnel.lfr-demo.se"
	eu := "https://tunnel.lfr-demo.se"
	in := "https://aws-edge-in.lfr-demo.se"

	if gatewayHostKey(central) != gatewayHostKey(eu) {
		t.Error("'central' and 'eu' are the same gateway and must share a key")
	}
	if gatewayHostKey(central) == gatewayHostKey(in) {
		t.Error("different gateways must not share a key")
	}

	// Spelling differences that must not create a second identity.
	if gatewayHostKey("https://tunnel.lfr-demo.se/") != gatewayHostKey(central) {
		t.Error("a trailing slash must not change the key")
	}
	if gatewayHostKey("https://TUNNEL.LFR-DEMO.se") != gatewayHostKey(central) {
		t.Error("case must not change the key")
	}
	if gatewayHostKey("https://tunnel.lfr-demo.se:443") != gatewayHostKey(central) {
		t.Error("an explicit default port must not change the key")
	}

	// But a real port difference is a real difference -- local and test deployments
	// routinely share a hostname and differ only by port.
	if gatewayHostKey("http://127.0.0.1:8080") == gatewayHostKey("http://127.0.0.1:9090") {
		t.Error("gateways differing only by port must not collapse")
	}
}

// TestCooldownCoversEveryAlias is the behaviour that failed live: excluding 'in' left
// 'edge-in' electable, because they were treated as different regions.
func TestCooldownCoversEveryAlias(t *testing.T) {
	resetCooldowns(t)

	regions := map[string]string{
		"in":      "https://aws-edge-in.lfr-demo.se",
		"edge-in": "https://aws-edge-in.lfr-demo.se",
		"central": "https://tunnel.lfr-demo.se",
		"eu":      "https://tunnel.lfr-demo.se",
	}

	cooldowns.exclude("https://aws-edge-in.lfr-demo.se", regionFailoverCooldown)
	got := cooldowns.filter(regions)

	if _, present := got["in"]; present {
		t.Error("'in' should be excluded")
	}
	if _, present := got["edge-in"]; present {
		t.Error("'edge-in' is the same gateway as 'in' and must be excluded with it")
	}
	if _, present := got["central"]; !present {
		t.Error("an unrelated gateway must remain a candidate")
	}
}

// TestDedupeRegionsByHostPrefersTheFamiliarName pins the name selection, which is
// user-visible: it becomes the region shown in the TUI and written to the state file.
func TestDedupeRegionsByHostPrefersTheFamiliarName(t *testing.T) {
	regions := map[string]string{
		"in":      "https://aws-edge-in.lfr-demo.se",
		"edge-in": "https://aws-edge-in.lfr-demo.se",
		"central": "https://tunnel.lfr-demo.se",
		"eu":      "https://tunnel.lfr-demo.se",
		"sa":      "https://aws-edge-sa.lfr-demo.se",
		"edge-sa": "https://aws-edge-sa.lfr-demo.se",
	}

	// Run repeatedly: map iteration order is random, and the point is that the choice
	// does not depend on it.
	for i := 0; i < 25; i++ {
		got := dedupeRegionsByHost(regions)

		if len(got) != 3 {
			t.Fatalf("expected one name per gateway, got %d: %v", len(got), got)
		}
		if _, ok := got["in"]; !ok {
			t.Errorf("expected the shorter 'in' to survive over 'edge-in', got %v", got)
		}
		if _, ok := got["sa"]; !ok {
			t.Errorf("expected 'sa' to survive over 'edge-sa', got %v", got)
		}
		// 'eu' and 'central' are the same length is false -- 'eu' is shorter, so it wins.
		if _, ok := got["eu"]; !ok {
			t.Errorf("expected the shorter 'eu' to survive over 'central', got %v", got)
		}
	}
}

// TestDedupePassesThroughSmallMaps guards the early return.
func TestDedupePassesThroughSmallMaps(t *testing.T) {
	single := map[string]string{"eu": "https://tunnel.example"}
	if got := dedupeRegionsByHost(single); len(got) != 1 {
		t.Errorf("a single region must pass through unchanged, got %v", got)
	}
	if got := dedupeRegionsByHost(nil); got != nil {
		t.Errorf("nil must pass through, got %v", got)
	}
}
