package config

import (
	"strings"
	"testing"
)

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

// TestSSOEmailVerificationMode covers email_verification (#2472): an explicit mode wins, an unset
// one keeps what skip_email_verified_check meant, and an unknown one is refused at load.
func TestSSOEmailVerificationMode(t *testing.T) {
	cases := []struct {
		name, lines, want string
	}{
		{"unset is provider", "", EmailVerificationProvider},
		{"unset with the skip flag is trust", "    skip_email_verified_check: true\n", EmailVerificationTrust},
		{"gateway", "    email_verification: gateway\n", EmailVerificationGateway},
		{"explicit mode wins over the skip flag", "    skip_email_verified_check: true\n    email_verification: gateway\n", EmailVerificationGateway},
		{"provider", "    email_verification: provider\n", EmailVerificationProvider},
		{"trust", "    email_verification: trust\n", EmailVerificationTrust},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadServerConfig(writeServerConfig(t, ssoProviderYAML+tc.lines))
			if err != nil {
				t.Fatalf("LoadServerConfig failed: %v", err)
			}
			if got := cfg.SSOProviders[0].EmailVerificationMode(); got != tc.want {
				t.Errorf("EmailVerificationMode() = %q, want %q", got, tc.want)
			}
		})
	}
	if _, err := LoadServerConfig(writeServerConfig(t, ssoProviderYAML+"    email_verification: gatway\n")); err == nil ||
		!strings.Contains(err.Error(), "gatway") {
		t.Errorf("an unknown email_verification loaded (err %v)", err)
	}
}

const ssoProviderYAML = "sso_providers:\n  - id: okta\n    name: Okta\n    client_id: c\n    client_secret: s\n" +
	"    issuer_url: https://login.example.com\n"
