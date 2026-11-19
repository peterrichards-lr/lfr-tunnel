package config

import (
	"strings"
	"testing"
)

// #2528: classic_portal_path is read from the YAML, normalised to "/<segment>", and refused
// when it is short or more than one segment.
func TestLoadServerConfigReadsClassicPortalPath(t *testing.T) {
	cases := []struct{ name, yaml, want string }{
		{"absent: V1 not served", "domains:\n  - example.com\n", ""},
		{"empty: V1 not served", "classic_portal_path: \"\"\n", ""},
		{"with a slash", "classic_portal_path: \"/classic-7f3k9q2m\"\n", "/classic-7f3k9q2m"},
		{"without a slash", "classic_portal_path: \"classic-7f3k9q2m/\"\n", "/classic-7f3k9q2m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadServerConfig(writeServerConfig(t, tc.yaml))
			if err != nil {
				t.Fatalf("LoadServerConfig: %v", err)
			}
			if cfg.ClassicPortalPath != tc.want {
				t.Errorf("ClassicPortalPath = %q, want %q", cfg.ClassicPortalPath, tc.want)
			}
		})
	}
}

func TestLoadServerConfigRefusesABadClassicPortalPath(t *testing.T) {
	cases := []struct{ name, yaml, wantInErr string }{
		{"too short", "classic_portal_path: \"/classic\"\n", "at least"},
		{"two segments", "classic_portal_path: \"/classic-7f3k9q2m/x\"\n", "one path segment"},
		{"query", "classic_portal_path: \"/classic-7f3k9q2m?x\"\n", "one path segment"},
		{"an existing route", "classic_portal_path: \"/portalv2\"\n", "at least"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadServerConfig(writeServerConfig(t, tc.yaml))
			if err == nil {
				t.Fatalf("LoadServerConfig accepted %q", tc.yaml)
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantInErr)
			}
		})
	}
}
