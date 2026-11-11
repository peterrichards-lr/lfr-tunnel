package config

import "testing"

// TestSSOEmailVerifiedCheckIsOnByDefault covers skip_email_verified_check (#2451): a provider that
// does not mention it keeps the check, so no existing gateway starts accepting unverified
// addresses, and setting it is the only way to turn the check off.
func TestSSOEmailVerifiedCheckIsOnByDefault(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"unset keeps the check", "", false},
		{"explicit false keeps the check", "    skip_email_verified_check: false\n", false},
		{"true skips it", "    skip_email_verified_check: true\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "sso_providers:\n  - id: entra\n    name: Entra\n    client_id: c\n    client_secret: s\n" +
				"    issuer_url: https://login.microsoftonline.com/t/v2.0\n" + tc.line
			cfg, err := LoadServerConfig(writeServerConfig(t, body))
			if err != nil {
				t.Fatalf("LoadServerConfig failed: %v", err)
			}
			if len(cfg.SSOProviders) != 1 {
				t.Fatalf("want 1 provider, got %d", len(cfg.SSOProviders))
			}
			if got := cfg.SSOProviders[0].SkipEmailVerifiedCheck; got != tc.want {
				t.Errorf("SkipEmailVerifiedCheck = %v, want %v", got, tc.want)
			}
		})
	}
}
