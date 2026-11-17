package config

import (
	"reflect"
	"testing"
)

// #2466: owner.user_id and allowed_email_domains from the YAML were used exactly as written, while
// every request's email is lowercased -- so a mixed-case owner was not recognised as the owner,
// and a mixed-case domain admitted nobody. The environment overrides were already normalised.
func TestLoadServerConfigNormalisesIdentityFieldsFromYAML(t *testing.T) {
	t.Setenv("LFT_OWNER_USER_ID", "")
	t.Setenv("LFT_ALLOWED_DOMAINS", "")
	t.Setenv("LFT_ADMIN_EMAIL", "")
	path := writeServerConfig(t, "owner:\n  user_id: \" Peter.Richards@Example.COM \"\n"+
		"allowed_email_domains:\n  - Liferay.COM\n  - \" example.org \"\n")

	cfg, err := LoadServerConfig(path)
	if err != nil {
		t.Fatalf("LoadServerConfig: %v", err)
	}
	if cfg.Owner.UserID != "peter.richards@example.com" {
		t.Errorf("owner.user_id = %q, want it trimmed and lowercased", cfg.Owner.UserID)
	}
	if want := []string{"liferay.com", "example.org"}; !reflect.DeepEqual(cfg.AllowedEmailDomains, want) {
		t.Errorf("allowed_email_domains = %q, want %q", cfg.AllowedEmailDomains, want)
	}
	// Alerts default to the owner; they go to the normalised address too.
	if cfg.AdminNotificationEmail != "peter.richards@example.com" {
		t.Errorf("admin_notification_email defaulted to %q", cfg.AdminNotificationEmail)
	}
}

func TestNormalizeEmail(t *testing.T) {
	for in, want := range map[string]string{
		" Jane.Doe@Example.COM ": "jane.doe@example.com",
		"already@lower.case":     "already@lower.case",
		"":                       "",
	} {
		if got := NormalizeEmail(in); got != want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", in, got, want)
		}
	}
}
