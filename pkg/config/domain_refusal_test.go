package config

import "testing"

// #2471: domain_refusal is on unless switched off, and carries an optional message.
func TestLoadServerConfigReadsDomainRefusal(t *testing.T) {
	cases := []struct {
		name, yaml string
		shown      bool
		message    string
	}{
		{"absent: on, built-in text", "domains:\n  - example.com\n", true, ""},
		{"switched off", "domain_refusal:\n  enabled: false\n", false, ""},
		{"on with a message", "domain_refusal:\n  enabled: true\n  message: \"Liferay staff only\"\n", true, "Liferay staff only"},
		{"message only: still on", "domain_refusal:\n  message: \"x\"\n", true, "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadServerConfig(writeServerConfig(t, tc.yaml))
			if err != nil {
				t.Fatalf("LoadServerConfig: %v", err)
			}
			if got := cfg.DomainRefusal.Shown(); got != tc.shown {
				t.Errorf("Shown() = %v, want %v", got, tc.shown)
			}
			if cfg.DomainRefusal.Message != tc.message {
				t.Errorf("Message = %q, want %q", cfg.DomainRefusal.Message, tc.message)
			}
		})
	}
}
