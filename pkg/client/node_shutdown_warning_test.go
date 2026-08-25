package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseNodeShutdownWarning(t *testing.T) {
	// Valid warning payload
	validJSON, _ := json.Marshal(map[string]interface{}{
		"type":              "node_shutdown_warning",
		"node_id":           "edge-us",
		"action":            "shutdown_warning",
		"seconds_remaining": 300,
		"shutdown_at":       1787126400,
		"reason":            "Scheduled overnight maintenance",
	})

	warn, ok := ParseNodeShutdownWarning(validJSON)
	if !ok {
		t.Fatalf("expected valid parse to return true")
	}
	if warn.NodeID != "edge-us" {
		t.Errorf("expected NodeID 'edge-us', got %s", warn.NodeID)
	}
	if warn.SecondsRemaining != 300 {
		t.Errorf("expected SecondsRemaining 300, got %d", warn.SecondsRemaining)
	}

	// Invalid payload type
	invalidJSON := []byte(`{"type":"other_event"}`)
	_, okInvalid := ParseNodeShutdownWarning(invalidJSON)
	if okInvalid {
		t.Errorf("expected invalid payload to return false")
	}
}

func TestClearRegionCacheFile(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("skipping user home dir test")
	}
	cachePath := filepath.Join(home, ".lfr-tunnel", "region_cache.json")
	_ = os.MkdirAll(filepath.Dir(cachePath), 0700)
	// Checked, not discarded: if the fixture is not written the assertion below passes for the
	// wrong reason -- ClearRegionCacheFile would report success having removed nothing.
	if err := os.WriteFile(cachePath, []byte(`{"test":true}`), 0600); err != nil {
		t.Fatalf("failed to write the region cache fixture: %v", err)
	}

	if err := ClearRegionCacheFile(); err != nil {
		t.Fatalf("ClearRegionCacheFile failed: %v", err)
	}

	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Errorf("expected region_cache.json to be deleted")
	}
}
