package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeServerConfig writes a minimal server config and returns its path.
func writeServerConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server-config.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	return path
}

// TestAdminNotificationEmailFallsBackToOwner covers the gap that left a gateway with a
// configured owner sending admin alerts to nobody, silently: every alert site returns
// early when AdminNotificationEmail is empty.
func TestAdminNotificationEmailFallsBackToOwner(t *testing.T) {
	t.Setenv("LFT_ADMIN_EMAIL", "")
	path := writeServerConfig(t, "owner:\n  user_id: owner@example.com\n")

	cfg, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig failed: %v", err)
	}
	if cfg.AdminNotificationEmail != "owner@example.com" {
		t.Errorf("expected admin alerts to fall back to the owner, got %q", cfg.AdminNotificationEmail)
	}
}

// TestAdminNotificationEmailNotOverridden makes sure an explicitly configured address
// still wins over the owner.
func TestAdminNotificationEmailNotOverridden(t *testing.T) {
	t.Setenv("LFT_ADMIN_EMAIL", "")
	path := writeServerConfig(t, "owner:\n  user_id: owner@example.com\nadmin_notification_email: alerts@example.com\n")

	cfg, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig failed: %v", err)
	}
	if cfg.AdminNotificationEmail != "alerts@example.com" {
		t.Errorf("an explicit admin_notification_email must win, got %q", cfg.AdminNotificationEmail)
	}
}

// TestAdminNotificationEmailEnvWins covers LFT_ADMIN_EMAIL taking precedence, since the
// fallback runs after the environment overrides.
func TestAdminNotificationEmailEnvWins(t *testing.T) {
	t.Setenv("LFT_ADMIN_EMAIL", "env@example.com")
	path := writeServerConfig(t, "owner:\n  user_id: owner@example.com\n")

	cfg, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig failed: %v", err)
	}
	if cfg.AdminNotificationEmail != "env@example.com" {
		t.Errorf("LFT_ADMIN_EMAIL must win over the owner fallback, got %q", cfg.AdminNotificationEmail)
	}
}

// TestAdminNotificationEmailStaysEmptyWithoutOwner documents that with neither set, the
// value remains empty -- alerts are disabled, and the loader warns about it.
func TestAdminNotificationEmailStaysEmptyWithoutOwner(t *testing.T) {
	t.Setenv("LFT_ADMIN_EMAIL", "")
	t.Setenv("LFT_OWNER_USER_ID", "")
	path := writeServerConfig(t, "domains:\n  - example.com\n")

	cfg, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig failed: %v", err)
	}
	if cfg.AdminNotificationEmail != "" {
		t.Errorf("expected no admin email when neither is configured, got %q", cfg.AdminNotificationEmail)
	}
}
