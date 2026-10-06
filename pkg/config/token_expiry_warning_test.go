package config

import "testing"

// token_expiry_warning_days is set in the server config file (#2344), the way every other
// operator setting is. Asserted through LoadServerConfig rather than by constructing a struct, so
// a wrong yaml tag -- the one mistake that would make the key silently do nothing -- fails here.
func TestTokenExpiryWarningDaysComesFromTheConfigFile(t *testing.T) {
	cfg, err := LoadServerConfig(writeServerConfig(t, "token_expiry_warning_days: 12\n"))
	if err != nil {
		t.Fatalf("LoadServerConfig failed: %v", err)
	}
	if cfg.TokenExpiryWarningDays != 12 {
		t.Errorf("token_expiry_warning_days: 12 in the config file loaded as %d", cfg.TokenExpiryWarningDays)
	}

	unset, err := LoadServerConfig(writeServerConfig(t, "domains: [example.com]\n"))
	if err != nil {
		t.Fatalf("LoadServerConfig failed: %v", err)
	}
	if unset.TokenExpiryWarningDays != DefaultTokenExpiryWarningDays {
		t.Errorf("an unset token_expiry_warning_days loaded as %d, want the default %d",
			unset.TokenExpiryWarningDays, DefaultTokenExpiryWarningDays)
	}
}
