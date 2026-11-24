package main

import (
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// #2586: a container names its gateway through the environment, so the non-pinning choice has to
// exist there too.
func TestResolveBootstrap(t *testing.T) {
	tests := []struct {
		name        string
		flag, pin   string
		env         map[string]string
		wantURL     string
		wantSource  string
		wantRefused string // a substring of the error, when refused
	}{
		{name: "nothing asked for", wantURL: ""},
		{name: "the flag", flag: "https://a", wantURL: "https://a", wantSource: "-bootstrap"},
		{name: "LFT_CLIENT_BOOTSTRAP", env: map[string]string{"LFT_CLIENT_BOOTSTRAP": "https://b"}, wantURL: "https://b", wantSource: "LFT_CLIENT_BOOTSTRAP"},
		{name: "the short alias", env: map[string]string{"LFT_BOOTSTRAP": "https://c"}, wantURL: "https://c", wantSource: "LFT_BOOTSTRAP"},
		{
			name:    "the long name outranks the alias",
			env:     map[string]string{"LFT_CLIENT_BOOTSTRAP": "https://b", "LFT_BOOTSTRAP": "https://c"},
			wantURL: "https://b", wantSource: "LFT_CLIENT_BOOTSTRAP",
		},
		{
			name: "the flag outranks the environment", flag: "https://a",
			env:     map[string]string{"LFT_CLIENT_BOOTSTRAP": "https://b"},
			wantURL: "https://a", wantSource: "-bootstrap",
		},
		{name: "-bootstrap with -pin", flag: "https://a", pin: "https://p", wantRefused: "-bootstrap and -pin contradict"},
		{
			name: "the variable with -pin", pin: "https://p",
			env:         map[string]string{"LFT_CLIENT_BOOTSTRAP": "https://b"},
			wantRefused: "LFT_CLIENT_BOOTSTRAP and -pin contradict",
		},
		{
			// The pre-#2586 hole: this was accepted and silently pinned to the bootstrap URL.
			name: "the flag with a pinning variable", flag: "https://a",
			env:         map[string]string{"LFT_CLIENT_SERVER": "https://p"},
			wantRefused: "-bootstrap and LFT_CLIENT_SERVER contradict",
		},
		{
			name:        "both as variables",
			env:         map[string]string{"LFT_CLIENT_BOOTSTRAP": "https://b", "LFT_SERVER_URL": "https://p"},
			wantRefused: "LFT_CLIENT_BOOTSTRAP and LFT_SERVER_URL contradict",
		},
		{name: "a pin alone is not this function's business", pin: "https://p", wantURL: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			url, source, err := resolveBootstrap(tc.flag, tc.pin, envOf(tc.env))
			if tc.wantRefused != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantRefused) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantRefused)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if url != tc.wantURL || (tc.wantURL != "" && source != tc.wantSource) {
				t.Errorf("got (%q, %q), want (%q, %q)", url, source, tc.wantURL, tc.wantSource)
			}
		})
	}
}
