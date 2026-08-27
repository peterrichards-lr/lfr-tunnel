package server

import (
	"lfr-tunnel/pkg/config"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newSectionRedirectServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.ServerConfig{
		Domains:                []string{"example.com"},
		DisableBackupScheduler: true,
	}
	cfg.DBPath = filepath.Join(t.TempDir(), "test.db")
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	t.Cleanup(func() {
		time.Sleep(50 * time.Millisecond)
		srv.Stop()
	})
	return srv
}

// A path under /portal/ matched no control plane handler and fell through to the
// data-plane ProxyHandler, which has no lease for a control-domain host. /portal/users
// therefore answered with the "Developer Environment Offline" page -- telling an admin
// their environment was down because they typed the URL shape Portal V2 taught them.
//
// V1 routes on the fragment, so the path form maps onto it directly (#1215).
func TestServer_PortalSectionPathRedirectsToFragment(t *testing.T) {
	srv := newSectionRedirectServer(t)

	tests := []struct {
		url      string
		expected string
	}{
		{"http://example.com/portal/users", "/portal#users"},
		{"http://example.com/portal/account", "/portal#account"},
		{"http://example.com/portal/network-health", "/portal#network-health"},
		{"http://example.com/admin/users", "/admin#users"},
		{"http://example.com/admin/blacklist", "/admin#blacklist"},
		// A query string is carried, and sits before the fragment as a URL requires.
		{"http://example.com/portal/users?token=123", "/portal?token=123#users"},
		// Trailing slashes on the section are not a distinct route.
		{"http://example.com/portal/users/", "/portal#users"},
		// Deeper paths still land somewhere real -- showTab falls back to the overview
		// for a section that does not exist.
		{"http://example.com/portal/users/42", "/portal#users/42"},
	}

	for _, tc := range tests {
		req := httptest.NewRequest("GET", tc.url, nil)
		req.Host = "example.com"
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		// Found, not MovedPermanently: browsers cache a 301 aggressively, and if V1 ever
		// gains real path routing a cached one would keep bouncing users to the fragment.
		if rec.Code != http.StatusFound {
			t.Errorf("for %s, expected status 302, got %d", tc.url, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != tc.expected {
			t.Errorf("for %s, expected location %q, got %q", tc.url, tc.expected, loc)
		}
		if body := rec.Body.String(); strings.Contains(body, "Environment Offline") {
			t.Errorf("for %s, still serving the offline page", tc.url)
		}
	}
}

// The redirect must not swallow the routes either side of it.
func TestServer_PortalSectionRedirectLeavesOtherRoutesAlone(t *testing.T) {
	srv := newSectionRedirectServer(t)

	t.Run("the V2 SPA is untouched", func(t *testing.T) {
		// "/portalv2/..." does not begin with "/portal/" -- the character after "portal"
		// is "v", not "/" -- but it is one edit away from doing so, and swallowing the V2
		// SPA would be a far worse bug than the one being fixed here.
		for _, path := range []string{"/portalv2", "/portalv2/", "/portalv2/admin/users"} {
			req := httptest.NewRequest("GET", "http://example.com"+path, nil)
			req.Host = "example.com"
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code == http.StatusFound {
				t.Errorf("%s was redirected to %q -- the V2 SPA must not be caught by the V1 rule",
					path, rec.Header().Get("Location"))
			}
		}
	})

	t.Run("the exact pages still render", func(t *testing.T) {
		for _, path := range []string{"/portal", "/admin", "/"} {
			req := httptest.NewRequest("GET", "http://example.com"+path, nil)
			req.Host = "example.com"
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("%s returned %d, expected 200", path, rec.Code)
			}
		}
	})

	t.Run("trailing-slash normalisation stays a permanent redirect", func(t *testing.T) {
		// The two rules overlap on "/portal/", and this one has to win: it is a genuine
		// canonicalisation, where the section redirect is provisional.
		req := httptest.NewRequest("GET", "http://example.com/portal/", nil)
		req.Host = "example.com"
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusMovedPermanently {
			t.Errorf("expected 301 for /portal/, got %d", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/portal" {
			t.Errorf("expected /portal, got %q", loc)
		}
	})

	t.Run("a tunnel host is not affected", func(t *testing.T) {
		// The control plane path chain is inside `if isControl`, so a tunnel host never
		// reaches it. That matters: a tunnelled application is free to serve its own
		// routes under /portal/, and redirecting those would break real traffic.
		req := httptest.NewRequest("GET", "http://mysite-se.example.com/portal/login", nil)
		req.Host = "mysite-se.example.com"
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code == http.StatusFound {
			t.Errorf("a tunnel host was redirected to %q -- the V1 rule must be control-plane only",
				rec.Header().Get("Location"))
		}
	})
}
