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
		Version string `json:"version"`
	}

	if err := json.Unmarshal(data, &whatsNew); err != nil {
		t.Fatalf("Failed to parse JSON from %s: %v", path, err)
	}

	if len(whatsNew) == 0 {
		t.Fatalf("whats-new.json is empty")
	}

	if Version != whatsNew[0].Version {
		t.Errorf("Version mismatch! config.Version = %q, but whats-new.json = %q. These must be kept in sync.", Version, whatsNew[0].Version)
	}
}
