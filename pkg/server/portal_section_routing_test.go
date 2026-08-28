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

// Portal V1 routes on the PATH (#1513), so /portal/users is a real route that serves the
// portal document and the address bar keeps that path -- where V2 gives
// /portalv2/admin/users. #1477 answered the same URLs with a 302 to /portal#users, which
// stopped them falling through to the data-plane ProxyHandler and answering with the
// "Developer Environment Offline" page (#1215), but left V1 handing out a different kind
// of link from V2 for the same section.
//
// What is asserted is that the request is SERVED, not where it is sent: the server has no
// list of valid sections, deliberately. showTab reads the valid set from the DOM, so an
// unknown one is normalised client-side and a section added to the markup cannot drift out
// of sync with a list kept here.
func TestServer_PortalSectionPathIsServed(t *testing.T) {
	srv := newSectionRedirectServer(t)

	paths := []string{
		"/portal/users",
		"/portal/account",
		"/portal/network-health",
		"/admin/users",
		"/admin/blacklist",
		// A query string does not make it a different route.
		"/portal/users?token=123",
		// Trailing slashes on the section are not a distinct route.
		"/portal/users/",
		// A deeper path is still served; the client normalises what it cannot match, the
		// same way it always has for an unknown fragment.
		"/portal/users/42",
		// The section the client will bounce to the overview. It must still be a 200 --
		// answering 404 would put the "environment is down" reading back in play.
		"/portal/nonsense",
	}

	for _, path := range paths {
		req := httptest.NewRequest("GET", "http://example.com"+path, nil)
		req.Host = "example.com"
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("for %s, expected status 200, got %d", path, rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, "Environment Offline") {
			t.Errorf("for %s, still serving the offline page", path)
		}
		// The portal document, not merely some 200.
		if !strings.Contains(body, "/static/dashboard.js") {
			t.Errorf("for %s, the response is not the portal document", path)
		}
	}
}

// The script tag has to be an absolute path.
//
// It was "static/dashboard.js" -- relative, which is harmless while the portal is only ever
// served at /portal, and fatal the moment it is also served at /portal/users, where it
// resolves to /portal/static/dashboard.js and 404s. The portal would render as an unstyled
// shell with no behaviour at all, and every server-side test would still pass.
func TestServer_PortalDocumentUsesAbsoluteAssetPaths(t *testing.T) {
	srv := newSectionRedirectServer(t)

	req := httptest.NewRequest("GET", "http://example.com/portal/users", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, `src="static/dashboard.js`) {
		t.Error(`dashboard.js is referenced relatively; at /portal/users that resolves to ` +
			`/portal/static/dashboard.js and 404s, leaving a shell with no JavaScript`)
	}
	if !strings.Contains(body, `src="/static/dashboard.js`) {
		t.Error("the absolute dashboard.js script tag is missing")
	}
	// The cache-busting rewrite keys off the same string, so a change to one silently
	// disarms the other -- every user then keeps a stale bundle after a release.
	if !strings.Contains(body, "/static/dashboard.js?v=") {
		t.Error("the cache-busting query is missing, so a release would serve a stale bundle")
	}
}

// The V1 path rule must not swallow the routes either side of it.
func TestServer_PortalSectionRoutingLeavesOtherRoutesAlone(t *testing.T) {
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
			// The V1 document would be a 200 too, so the status cannot tell them apart --
			// what distinguishes them is which document came back.
			if strings.Contains(rec.Body.String(), "/static/dashboard.js") {
				t.Errorf("%s was served the V1 portal document -- the V2 SPA must not be caught "+
					"by the V1 rule", path)
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
		// The two rules overlap on "/portal/", and this one has to win. "/portal/" names no
		// section, so serving the document there would leave a URL with a dangling slash
		// that /portal already canonicalises away.
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
		// reaches it. That matters more now than it did with a redirect: a tunnelled
		// application is free to serve its own routes under /portal/, and answering those
		// with the portal document would break real traffic.
		req := httptest.NewRequest("GET", "http://mysite-se.example.com/portal/login", nil)
		req.Host = "mysite-se.example.com"
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if strings.Contains(rec.Body.String(), "/static/dashboard.js") {
			t.Error("a tunnel host was served the V1 portal document -- the rule must be " +
				"control-plane only, or a tunnelled app cannot serve its own /portal/ routes")
		}
	})
}
