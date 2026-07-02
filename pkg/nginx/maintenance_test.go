package nginx

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMaintenanceManager(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "maintenance-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	triggerPath := filepath.Join(tempDir, "maintenance.enable")
	m := NewMaintenanceManager(triggerPath)

	if m.IsActive() {
		t.Errorf("Should not be active initially")
	}

	m.Enable("Upgrading", "DB schema update", 65, time.Now(), "<html>__ACTION__ __REASON__ __DURATION__</html>")

	if !m.IsActive() {
		t.Errorf("Should be active after Enable")
	}

	// Read html
	htmlPath := filepath.Join(tempDir, "maintenance.html")
	content, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Errorf("Failed to read html file: %v", err)
	}
	if string(content) != "<html>Upgrading DB schema update 2 hour(s)</html>" {
		t.Errorf("Unexpected html content: %s", string(content))
	}

	m.Disable()

	if m.IsActive() {
		t.Errorf("Should not be active after Disable")
	}
}

func TestMaintenanceManager_Empty(t *testing.T) {
	m := NewMaintenanceManager("")
	// This will fallback to /var/lib/lfr-tunneld if it exists, otherwise ""
	// We just ensure it doesn't panic
	m.getResolvedTriggerPath()
	m.IsActive()
	m.Disable()
	m.Enable("test", "test", 10, time.Now(), "test")
}
