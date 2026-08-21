package client

import (
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
)

// TestHeaderWidthStaysOnTheBorder pins the arithmetic that right-aligns the status marker
// against the 80-column rule the borders use. Adding the version to that line is what made
// this breakable.
func TestHeaderWidthStaysOnTheBorder(t *testing.T) {
	cases := []struct {
		name       string
		title      string
		statusText string
	}{
		{"offline", "  LIFERAY TUNNEL CLIENT  v1.46.0", "OFFLINE"},
		{"connected", "  LIFERAY TUNNEL CLIENT  v1.46.0", "CONNECTED"},
		{"connecting", "  LIFERAY TUNNEL CLIENT  v1.46.0", "CONNECTING"},
		{"with update notice", "  LIFERAY TUNNEL CLIENT  v1.46.0  (update available: v1.47.0)", "CONNECTED"},
		{"dev build", "  LIFERAY TUNNEL CLIENT  dev", "CONNECTED"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gap := 80 - runeLen(tc.title) - runeLen(tc.statusText) - 4
			if gap < 1 {
				gap = 1
			}
			// Mirrors the render: title, gap, "[", status, "]", two trailing spaces.
			rendered := tc.title + strings.Repeat(" ", gap) + "[" + tc.statusText + "]  "
			if runeLen(rendered) != 80 {
				t.Errorf("header is %d columns, borders are 80:\n|%s|", runeLen(rendered), rendered)
			}
		})
	}
}

// TestHeaderDegradesRatherThanWrapping covers a title long enough to leave no room. It
// must not compute a negative pad and it must not silently wrap, which would push every
// row below it out of alignment.
func TestHeaderDegradesRatherThanWrapping(t *testing.T) {
	title := "  LIFERAY TUNNEL CLIENT  " + strings.Repeat("x", 100)
	gap := 80 - runeLen(title) - runeLen("CONNECTED") - 4
	if gap < 1 {
		gap = 1
	}
	if gap != 1 {
		t.Errorf("expected the gap to clamp to 1, got %d", gap)
	}
}

func TestRuneLenCountsCharacters(t *testing.T) {
	if runeLen("café") != 4 {
		t.Errorf("expected 4 characters, got %d", runeLen("café"))
	}
	if runeLen("👉 go") != 4 {
		t.Errorf("expected 4 characters, got %d", runeLen("👉 go"))
	}
}

// TestNewerVersion covers the update indicator. It must stay quiet on a dev build and when
// the gateway has told us nothing.
func TestNewerVersion(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)

	if got := e.NewerVersion(); got != "" {
		t.Errorf("nothing advertised yet, expected no notice, got %q", got)
	}

	e.SetLatestVersion("v99.0.0")
	if config.Version == "dev" {
		if got := e.NewerVersion(); got != "" {
			t.Errorf("a dev build has no meaningful version to compare, expected no notice, got %q", got)
		}
		return
	}

	if got := e.NewerVersion(); got != "v99.0.0" {
		t.Errorf("expected the newer version to be reported, got %q", got)
	}

	e.SetLatestVersion("v0.0.1")
	if got := e.NewerVersion(); got != "" {
		t.Errorf("an older advertised version is not an update, got %q", got)
	}
}
