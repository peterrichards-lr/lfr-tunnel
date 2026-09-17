package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeAutoUpgradeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "client-config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the config file: %v", err)
	}
	return path
}

// auto_upgrade replaces a binary on somebody's machine, so the default and every ambiguous
// value must resolve to OFF (#2000). Asserted rather than assumed: the failure mode of an
// opt-in read too loosely is silent and irreversible.
func TestAutoUpgradeIsOffUnlessExplicitlyAskedFor(t *testing.T) {
	isolateTokenEnvironment(t)

	cases := []struct {
		name string
		body string
		env  string
		want bool
	}{
		{name: "absent from the config file", body: "server_url: \"https://x.example.com\"\n", want: false},
		{name: "explicitly false", body: "server_url: \"https://x.example.com\"\nauto_upgrade: false\n", want: false},
		{name: "explicitly true", body: "server_url: \"https://x.example.com\"\nauto_upgrade: true\n", want: true},

		{name: "env true turns it on", body: "server_url: \"https://x.example.com\"\n", env: "true", want: true},
		{name: "env 1 turns it on", body: "server_url: \"https://x.example.com\"\n", env: "1", want: true},
		{name: "env false turns it off again", body: "server_url: \"https://x.example.com\"\nauto_upgrade: true\n", env: "false", want: false},

		// The important one: anything that is not a recognised affirmative must not enable
		// it. A truthy-ish string read as "yes" is how an opt-in stops being one.
		{name: "env nonsense does not enable it", body: "server_url: \"https://x.example.com\"\n", env: "maybe", want: false},
		{name: "env nonsense does not disable an explicit opt-in", body: "server_url: \"https://x.example.com\"\nauto_upgrade: true\n", env: "maybe", want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("LFT_AUTO_UPGRADE", tc.env)
			} else {
				t.Setenv("LFT_AUTO_UPGRADE", "")
			}
			cfg, err := LoadClientConfig(writeAutoUpgradeConfig(t, tc.body))
			if err != nil {
				t.Fatalf("loading the config: %v", err)
			}
			if cfg.AutoUpgrade != tc.want {
				t.Errorf("AutoUpgrade = %v, want %v", cfg.AutoUpgrade, tc.want)
			}
		})
	}
}
