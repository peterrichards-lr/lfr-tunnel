package client

import (
	"testing"

	"lfr-tunnel/pkg/config"
)

// resolvePortalURL used to fall back to one organisation's production portal, so a
// self-hoster whose gateway wasn't named tunnel.* was sent to a stranger's login page to
// authenticate (#1188).
func TestResolvePortalURL(t *testing.T) {
	tests := []struct {
		name          string
		serverURL     string
		defaultPortal string
		want          string
	}{
		{
			name:      "the tunnel/portal convention is honoured for anyone, not just one deployment",
			serverURL: "https://tunnel.example.com",
			want:      "https://portal.example.com",
		},
		{
			name:      "a self-hosted deployment following the convention resolves to its own portal",
			serverURL: "https://tunnel.mycorp.internal",
			want:      "https://portal.mycorp.internal",
		},
		{
			// The important case: no convention to follow and no configured portal. The
			// gateway serves the portal too, so sending them there is both correct and the
			// safe direction to be wrong in -- unlike sending them to a third party.
			name:      "a gateway named something else falls back to the gateway itself",
			serverURL: "https://gateway.mycorp.internal",
			want:      "https://gateway.mycorp.internal",
		},
		{
			name:          "a build with a configured portal uses it when the convention does not apply",
			serverURL:     "https://gateway.mycorp.internal",
			defaultPortal: "https://portal.mycorp.internal",
			want:          "https://portal.mycorp.internal",
		},
		{
			// The convention wins over the build-time default, so one binary still works
			// against several gateways.
			name:          "the derived portal wins over the configured default",
			serverURL:     "https://tunnel.other.example",
			defaultPortal: "https://portal.mycorp.internal",
			want:          "https://portal.other.example",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := config.DefaultPortalURL
			config.DefaultPortalURL = tc.defaultPortal
			t.Cleanup(func() { config.DefaultPortalURL = original })

			if got := resolvePortalURL(tc.serverURL); got != tc.want {
				t.Errorf("resolvePortalURL(%q) = %q, want %q", tc.serverURL, got, tc.want)
			}
		})
	}
}

// No deployment hostname may be baked into the source tree. They are injected at build
// time instead (#1188, same class as #1124).
func TestDeploymentURLsAreNotHardcoded(t *testing.T) {
	cases := map[string]string{
		"DefaultServerURL":     config.DefaultServerURL,
		"DefaultStatusPageURL": config.DefaultStatusPageURL,
		"DefaultPortalURL":     config.DefaultPortalURL,
	}
	for name, value := range cases {
		if value != "" {
			t.Errorf("%s is %q in an unconfigured build -- deployment hostnames must come "+
				"from -ldflags, not from source", name, value)
		}
	}
}
