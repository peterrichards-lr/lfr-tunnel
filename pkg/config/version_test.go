package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestVersionSync(t *testing.T) {
	// Path to whats-new.json relative to pkg/config
	path := filepath.Join("..", "server", "static", "whats-new.json")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", path, err)
	}

	var whatsNew []struct {
		Version     string   `json:"version"`
		ReleaseDate string   `json:"release_date"`
		Features    []string `json:"features"`
	}

	if err := json.Unmarshal(data, &whatsNew); err != nil {
		t.Fatalf("Failed to parse JSON from %s: %v", path, err)
	}

	if len(whatsNew) == 0 {
		t.Fatalf("whats-new.json is empty")
	}

	if len(whatsNew) > 5 {
		t.Errorf("whats-new.json contains %d releases; maximum allowed is 5 to prevent unbounded growth. Run 'python3 scripts/trim-whatsnew.py' to fix.", len(whatsNew))
	}

	if Version != whatsNew[0].Version {
		t.Errorf("Version mismatch! config.Version = %q, but whats-new.json = %q. These must be kept in sync.", Version, whatsNew[0].Version)
	}

	for i, entry := range whatsNew {
		if entry.Version == "" {
			t.Errorf("Entry %d in whats-new.json is missing 'version'", i)
		}
		if entry.ReleaseDate == "" {
			t.Errorf("Entry %d (%s) in whats-new.json is missing 'release_date'", i, entry.Version)
		}
		if len(entry.Features) == 0 {
			t.Errorf("Entry %d (%s) in whats-new.json has an empty 'features' array", i, entry.Version)
		}
	}
}
