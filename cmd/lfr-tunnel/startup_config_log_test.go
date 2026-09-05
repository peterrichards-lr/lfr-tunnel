package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
)

// captureStartupLog runs logStartupConfiguration against a captured slog default and returns
// everything it emitted.
//
// Asserted on the rendered output rather than on the inputs, because the property that matters
// -- that nothing secret reaches the log -- is a property of what came out.
func captureStartupLog(t *testing.T, cfg *config.ClientConfig, mappings []client.PortMapping) string {
	t.Helper()

	var buf bytes.Buffer
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	logStartupConfiguration(cfg, mappings)
	return buf.String()
}

// resetFacts clears the package-level record between cases, so one test's election path
// cannot leak into another's expectations.
func resetFacts(t *testing.T) {
	t.Helper()
	original := facts
	t.Cleanup(func() { facts = original })
	facts = startupFacts{}
}

// The block exists to be pasted into a support conversation, so it must contain nothing
// secret. The client config file holds a PAT (#1693).
//
// Asserted against the rendered lines rather than by reviewing the format string, because the
// leak this guards against would arrive by someone adding a field later.
func TestStartupConfigurationNeverLogsCredentials(t *testing.T) {
	resetFacts(t)

	const token = "lft_pat_SUPERSECRET_shouldneverappear"
	cfg := &config.ClientConfig{
		ServerURL: "https://us.lfr-demo.se",
		Region:    "edge-us",
		Subdomain: "dxplive",
		AuthToken: token,
	}

	lines := captureStartupLog(t, cfg, []client.PortMapping{{LocalPort: 8080}})

	if strings.Contains(lines, token) {
		t.Fatal("the startup block logged the auth token -- it is pasted into Slack by design")
	}
	// Nor any fragment of it: a truncated or masked token is still a token in a support channel.
	if strings.Contains(lines, "SUPERSECRET") || strings.Contains(lines, token[:12]) {
		t.Error("the startup block logged part of the auth token")
	}
	for _, forbidden := range []string{"AuthToken", "auth_token", "password", "secret"} {
		if strings.Contains(strings.ToLower(lines), strings.ToLower(forbidden)) {
			t.Errorf("the startup block mentions %q, which invites a credential into it later", forbidden)
		}
	}
}

// Reporting the value without its source is the defect this closes. "gateway
// https://tunnel.lfr-demo.se" reads identically whether the user chose it, a stale cached
// election chose it, or it is a fallback nobody intended -- and telling those apart is what
// cost several rounds of questions.
func TestStartupConfigurationReportsHowTheGatewayWasChosen(t *testing.T) {
	cases := []struct {
		name       string
		setup      func()
		wantSubstr string
	}{
		{
			name:       "a cached election says so, and says how to re-probe",
			setup:      func() { facts.regionSource = "cached election (valid for 1h0m0s, -refresh-region re-probes)" },
			wantSubstr: "cached election",
		},
		{
			name:       "a fresh probe is distinguishable from a cached one",
			setup:      func() { facts.regionSource = "fresh latency probe" },
			wantSubstr: "fresh latency probe",
		},
		{
			name:       "no election at all is stated rather than left blank",
			setup:      func() {},
			wantSubstr: "no election",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetFacts(t)
			tc.setup()
			lines := captureStartupLog(t, &config.ClientConfig{ServerURL: "https://x"}, nil)
			if !strings.Contains(lines, tc.wantSubstr) {
				t.Errorf("expected the region source to mention %q, got:\n%s", tc.wantSubstr, lines)
			}
		})
	}
}

// Pinning is the single most consequential thing about a client's configuration and the least
// visible (#1691). A boolean would not have been enough: the mechanism has to be named, because
// -server and the env vars pin while server_url: in the config file does not.
func TestStartupConfigurationNamesWhatPinnedTheClient(t *testing.T) {
	resetFacts(t)
	facts.pinnedBy = "-server flag"

	lines := captureStartupLog(t, &config.ClientConfig{ServerURL: "https://tunnel.lfr-demo.se"}, nil)

	if !strings.Contains(lines, "-server flag") {
		t.Error("the block does not name the mechanism that pinned the client")
	}
	if !strings.Contains(lines, "failover are OFF") {
		t.Error("the block does not say that pinning disables failover, which is the consequence people miss")
	}
}

func TestStartupConfigurationSaysWhenNoGatewayWasCompiledIn(t *testing.T) {
	// The #1692 condition. An absence is invisible unless it is stated: a client with no
	// compiled-in gateway looks identical to one that has it until the user is asked to
	// pass -server, at which point they are silently pinned.
	resetFacts(t)

	original := config.DefaultServerURL
	t.Cleanup(func() { config.DefaultServerURL = original })
	config.DefaultServerURL = ""

	lines := captureStartupLog(t, &config.ClientConfig{ServerURL: "https://given"}, nil)

	if !strings.Contains(lines, "none compiled in") {
		t.Errorf("a build with no default gateway must say so, got:\n%s", lines)
	}
}

func TestStartupConfigurationReportsUnavailableRegions(t *testing.T) {
	// The gateway now reports edges it knows about but cannot reach (#1690). Naming them
	// explains a worse-than-expected route without anyone having to ask.
	resetFacts(t)
	facts.advertised = 4
	facts.unavailable = []string{"edge-us", "us"}

	lines := captureStartupLog(t, &config.ClientConfig{ServerURL: "https://x", Region: "eu"}, nil)

	if !strings.Contains(lines, "edge-us") {
		t.Error("regions the gateway reported as down are not named")
	}
	if !strings.Contains(lines, "4 advertised") {
		t.Error("the advertised count is missing, so 3-of-3 cannot be told from 3-of-4")
	}
}

func TestPinnedBy(t *testing.T) {
	tests := []struct {
		name       string
		serverFlag string
		env        map[string]string
		want       string
	}{
		{name: "nothing pins", want: ""},
		{name: "the flag pins", serverFlag: "https://x", want: "-server flag"},
		{name: "LFT_SERVER_URL pins", env: map[string]string{"LFT_SERVER_URL": "https://x"}, want: "LFT_SERVER_URL environment variable"},
		{name: "LFT_CLIENT_SERVER pins", env: map[string]string{"LFT_CLIENT_SERVER": "https://x"}, want: "LFT_CLIENT_SERVER environment variable"},
		{name: "LFT_SERVER pins", env: map[string]string{"LFT_SERVER": "https://x"}, want: "LFT_SERVER environment variable"},
		{
			// The flag is checked first because it is what the user most recently typed.
			name:       "the flag wins over the environment",
			serverFlag: "https://x",
			env:        map[string]string{"LFT_SERVER_URL": "https://y"},
			want:       "-server flag",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"LFT_CLIENT_SERVER", "LFT_SERVER_URL", "LFT_SERVER"} {
				t.Setenv(k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := pinnedBy(tc.serverFlag); got != tc.want {
				t.Errorf("pinnedBy() = %q, want %q", got, tc.want)
			}
		})
	}
}
