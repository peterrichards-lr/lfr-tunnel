package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #2476: one portal host. Production's shape -- two domains, so the canonical host is
// tunnel.<first domain> and the second domain's names are the ones users must be moved off.
func canonicalTestServer(t *testing.T) *Server {
	t.Helper()
	srv, _, cleanup := setupTestServer(t)
	t.Cleanup(cleanup)
	srv.cfg.Domains = []string{"lfr-demo.se", "lfr-demo.online"}
	srv.cfg.ControlPlaneURL = ""
	return srv
}

func serve(srv *Server, method, url string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestPortalPageOnANonCanonicalControlHostGoesToTheCanonicalOne(t *testing.T) {
	srv := canonicalTestServer(t)
	for _, tc := range []struct{ url, want string }{
		{"https://lfr-demo.online/", "https://tunnel.lfr-demo.se/"},
		{"https://portal.lfr-demo.online/portal/tokens", "https://tunnel.lfr-demo.se/portal/tokens"},
		{"https://tunnel.lfr-demo.online/portalv2/dashboard?x=1", "https://tunnel.lfr-demo.se/portalv2/dashboard?x=1"},
		{"https://api.lfr-demo.online/admin", "https://tunnel.lfr-demo.se/admin"},
		// The first domain's other names too: the CLI opens portal.<domain>, and an SSO session
		// lives on tunnel., so a signed-in user was shown a sign-in page there.
		{"https://portal.lfr-demo.se/?cli_login=1", "https://tunnel.lfr-demo.se/?cli_login=1"},
		{"https://lfr-demo.se/admin?token=abc", "https://tunnel.lfr-demo.se/admin?token=abc"},
	} {
		rec := serve(srv, http.MethodGet, tc.url, map[string]string{"X-Forwarded-Proto": "https"})
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != tc.want {
			t.Errorf("GET %s = %d %q, want 302 to %q", tc.url, rec.Code, rec.Header().Get("Location"), tc.want)
		}
	}
	// HEAD is a page load too.
	if rec := serve(srv, http.MethodHead, "https://portal.lfr-demo.online/", map[string]string{"X-Forwarded-Proto": "https"}); rec.Code != http.StatusFound {
		t.Errorf("HEAD on a non-canonical host = %d, want 302", rec.Code)
	}
}

func TestTheCanonicalPortalHostIsServed(t *testing.T) {
	srv := canonicalTestServer(t)
	// The old entry points redirect to /portal (#2528), but on this host: a relative Location.
	for _, url := range []string{"https://tunnel.lfr-demo.se/", "http://tunnel.lfr-demo.se:80/admin"} {
		rec := serve(srv, http.MethodGet, url, map[string]string{"X-Forwarded-Proto": "https"})
		if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || loc != PortalV2Base {
			t.Errorf("GET %s = %d (Location %q): the canonical host must keep the visitor, at %s", url, rec.Code, loc, PortalV2Base)
		}
	}
	// The portal itself is not redirected. (Not asserted 200: unit tests do not embed the built V2
	// bundle, so the gateway answers "UI not built" here, which is not this change's concern.)
	for _, url := range []string{"https://tunnel.lfr-demo.se:443/portal", "https://tunnel.lfr-demo.se/portal/dashboard"} {
		if rec := serve(srv, http.MethodGet, url, map[string]string{"X-Forwarded-Proto": "https"}); rec.Header().Get("Location") != "" {
			t.Errorf("V2 on the canonical host redirected %s to %q", url, rec.Header().Get("Location"))
		}
	}
}

func TestWhatIsNeverRedirected(t *testing.T) {
	srv := canonicalTestServer(t)
	for _, tc := range []struct{ name, method, url string }{
		{"an API call from a client pinned to the second domain", http.MethodGet, "https://tunnel.lfr-demo.online/api/version"},
		{"a POST, which a redirect would strip of its body", http.MethodPost, "https://portal.lfr-demo.online/portal"},
		{"a download fetched by curl from the apex", http.MethodGet, "https://lfr-demo.online/static/downloads/x"},
		{"the install script", http.MethodGet, "https://lfr-demo.online/install.sh"},
		{"a portal asset", http.MethodGet, "https://portal.lfr-demo.online/static/dashboard.js"},
		{"a tunnel under the second domain", http.MethodGet, "https://acme.lfr-demo.online/"},
		{"localhost, as the e2e stacks use", http.MethodGet, "http://localhost:8000/"},
	} {
		rec := serve(srv, tc.method, tc.url, map[string]string{"X-Forwarded-Proto": "https"})
		if loc := rec.Header().Get("Location"); strings.Contains(loc, "tunnel.lfr-demo.se") {
			t.Errorf("%s: %s %s was redirected to %q", tc.name, tc.method, tc.url, loc)
		}
	}
}

// On an edge the canonical host is control_plane_url -- the same rule SSO uses. An edge answers on
// its own regional name, which its config lists as a domain (as tests/e2e/server-config-edge.yaml
// does), so that name is one of the four control names there.
func TestAnEdgeSendsPortalPagesToTheControlPlane(t *testing.T) {
	srv := canonicalTestServer(t)
	srv.cfg.Domains = []string{"us.lfr-demo.se", "lfr-demo.se"}
	srv.cfg.ControlPlaneURL = "https://tunnel.lfr-demo.se"
	rec := serve(srv, http.MethodGet, "https://us.lfr-demo.se/portal", nil)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://tunnel.lfr-demo.se/portal" {
		t.Errorf("edge GET = %d %q, want 302 to the control plane", rec.Code, rec.Header().Get("Location"))
	}
}
