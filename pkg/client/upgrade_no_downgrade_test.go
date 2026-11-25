package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// #2606: -upgrade compared "different", not "newer", so a gateway advertising an OLDER client
// (one lagging the fleet) downgraded anyone pinned to it through the full verified-replace path.

// noTouch fails the test if the upgrade gets as far as stopping anything.
func noTouch(t *testing.T) {
	t.Helper()
	orig := stopForUpgrade
	t.Cleanup(func() { stopForUpgrade = orig })
	stopForUpgrade = func() ([]string, bool, [][]string) {
		t.Error("stopForUpgrade called: an older version was about to be installed")
		return nil, false, nil
	}
}

func TestSelfUpgradeRefusesAnOlderGatewayVersion(t *testing.T) {
	noTouch(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			t.Errorf("fetched %s: an older version was being downloaded", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"latest_version": "v1.52.10",
			"client_platforms": map[string]any{gatewayPlatformKey(): map[string]string{
				"url": "/static/downloads/lfr-tunnel-old", "binary_name": "lfr-tunnel-old",
			}},
		}); err != nil {
			t.Errorf("encoding /api/version: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	if err := SelfUpgrade("v1.52.12", srv.URL); err != nil {
		t.Fatalf("SelfUpgrade against an older gateway: %v; want nil (nothing to do)", err)
	}
}

func TestSelfUpgradeRefusesAnOlderGitHubRelease(t *testing.T) {
	noTouch(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/peterrichards-lr/lfr-tunnel/releases/latest" {
			t.Errorf("fetched %s: an older version was being downloaded", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(Release{TagName: "v1.52.10"}); err != nil {
			t.Errorf("encoding release: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { githubAPIBase = oldBase })

	if err := SelfUpgrade("v1.52.12", ""); err != nil {
		t.Fatalf("SelfUpgrade against an older GitHub release: %v; want nil (nothing to do)", err)
	}
}

func TestCheckForUpdateReportsNoUpdateForAnOlderRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(Release{TagName: "v1.52.10"}); err != nil {
			t.Errorf("encoding release: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { githubAPIBase = oldBase })

	latest, err := CheckForUpdate("v1.52.12")
	if err != nil {
		t.Fatal(err)
	}
	if latest != "" {
		t.Errorf("CheckForUpdate(v1.52.12) offered %s, an older release", latest)
	}
}

func TestIsNewerRelease(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v1.52.12", "v1.52.11", true},
		{"v1.52.12", "v1.52.12", false},
		{"v1.52.10", "v1.52.12", false},
		{"v1.53.0", "v1.52.12", true},
		{"v1.52.12", "dev", true}, // a development build still upgrades to any release
		{"", "v1.52.12", false},
	}
	for _, c := range cases {
		if got := isNewerRelease(c.latest, c.current); got != c.want {
			t.Errorf("isNewerRelease(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}
