package server

import (
	"encoding/json"
	"lfr-tunnel/pkg/config"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// V2 is the portal at /portal; V1 is served to admins only, at classic_portal_path; every older
// entry point redirects (#2528).

const testClassicPortalPath = "/classic-7f3k9q2m"

func newPortalRoutesServer(t *testing.T, classic string) *Server {
	t.Helper()
	cfg := &config.ServerConfig{
		Domains:                []string{"example.com"},
		DisableBackupScheduler: true,
		ClassicPortalPath:      classic,
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

func getPortalPage(srv *Server, path, session string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://tunnel.example.com"+path, nil)
	req.Host = "tunnel.example.com"
	if session != "" {
		req.AddCookie(&http.Cookie{Name: "lfr_session", Value: session})
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// isV1Document reports whether rec is Portal V1's page.
func isV1Document(rec *httptest.ResponseRecorder) bool {
	return rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "/static/dashboard.js")
}

// isV2Document reports whether rec is V2's route answering: its app shell, served in place rather
// than redirected, and not V1's page. The shell's content depends on the build -- a real ui-dist,
// CI's empty stub index.html, or none at all, when the route answers "UI not built" -- so it is
// not what is checked.
func isV2Document(rec *httptest.ResponseRecorder) bool {
	body := rec.Body.String()
	if rec.Code == http.StatusInternalServerError && strings.Contains(body, "UI not built") {
		return true
	}
	return rec.Code == http.StatusOK && rec.Header().Get("Location") == "" &&
		!strings.Contains(body, "/static/dashboard.js")
}

func TestPortalEntryPointsRedirectToV2KeepingTheQuery(t *testing.T) {
	srv := newPortalRoutesServer(t, testClassicPortalPath)
	for _, tc := range []struct{ path, want string }{
		{"/", "/portal"},
		{"/?lang=fr", "/portal?lang=fr"},
		{"/admin", "/portal"},
		{"/admin?cli_login=1", "/portal?cli_login=1"},
		// Sign-ins in flight across the move: V1's emailed magic links and SSO's old code prompt
		// go to V2's sign-in page, which is the only place V2 reads them.
		{"/portal?token=abc&lang=fr", "/portal/login?token=abc&lang=fr"},
		{"/?token=abc&lang=fr", "/portal/login?token=abc&lang=fr"},
		{"/admin?token=abc", "/portal/login?token=abc"},
		{"/admin?sso_mfa=1&cli_login=1", "/portal/login?sso_mfa=1&cli_login=1"},
		// /admin/<section> is V1's admin alias for a section: the V2 page for that section.
		{"/admin/users", "/portal/admin/users"},
		{"/admin/users/", "/portal/admin/users"},
		{"/admin/tokens?x=1", "/portal/dashboard/tokens?x=1"},
		{"/admin/system", "/portal/admin/settings"},
		{"/admin/nonsense", "/portal"},
		// V1's /portal/<section> links, from old emails and bookmarks.
		{"/portal/users", "/portal/admin/users"},
		{"/portal/overview", "/portal/dashboard"},
		{"/portal/tokens?cli_login=1", "/portal/dashboard/tokens?cli_login=1"},
		{"/portal/custom-domains", "/portal/admin/vanity-domain-status"},
		// A trailing slash, which V1 accepted, keeps the section.
		{"/portal/users/", "/portal/admin/users"},
		// V2's old base, for magic links already sent, bookmarks and SSO round-trips in flight.
		{"/portalv2", "/portal"},
		{"/portalv2/", "/portal/"},
		{"/portalv2/admin/users", "/portal/admin/users"},
		{"/portalv2/x?y", "/portal/x?y"},
		{"/portalv2/login?token=t&lang=en&cli_login=1", "/portal/login?token=t&lang=en&cli_login=1"},
		{"/portalv2/login?sso_mfa=1", "/portal/login?sso_mfa=1"},
	} {
		rec := getPortalPage(srv, tc.path, "")
		// 302, not 301: a browser keeps a 301 indefinitely, so this could not be undone.
		if rec.Code != http.StatusFound {
			t.Errorf("%s: status %d, want 302", tc.path, rec.Code)
			continue
		}
		if got := rec.Header().Get("Location"); got != tc.want {
			t.Errorf("%s: redirected to %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestV2IsServedAtPortal(t *testing.T) {
	srv := newPortalRoutesServer(t, testClassicPortalPath)
	for _, path := range []string{
		"/portal",
		"/portal/dashboard",
		"/portal/dashboard/tokens",
		"/portal/admin/users",
		"/portal/login?token=abc&lang=en",
		// V1 section names whose V2 page has the same path: served, not redirected to themselves.
		"/portal/account",
		"/portal/analytics",
		// Not a V1 section: V2's router decides what to show.
		"/portal/nonsense",
	} {
		rec := getPortalPage(srv, path, "")
		if !isV2Document(rec) {
			t.Errorf("%s: status %d, want V2's app; Location %q", path, rec.Code, rec.Header().Get("Location"))
		}
	}
}

// The reviewer's self-redirect risk (#2537): with V2 at /portal, a V1 section whose V2 path is
// /<same name> would redirect to itself forever.
func TestNoV1SectionLinkRedirectsToItself(t *testing.T) {
	srv := newPortalRoutesServer(t, "")
	for section := range PortalV1SectionToV2 {
		path := PortalV2Base + "/" + section
		rec := getPortalPage(srv, path, "")
		if rec.Code == http.StatusFound && rec.Header().Get("Location") == path {
			t.Errorf("%s redirects to itself", path)
		}
	}
	// The fixture has to contain the case: at least one section maps to its own name.
	if v1SectionRedirect("account") != PortalV2Base+"/account" {
		t.Fatal("fixture: no V1 section maps to the same path in V2, so this test proves nothing")
	}
}

func TestClassicPortalIsServedOnlyToAdmins(t *testing.T) {
	srv := newPortalRoutesServer(t, testClassicPortalPath)
	_, admin := userWithSession(t, srv, "admin@example.com", "admin")
	_, owner := userWithSession(t, srv, "owner@example.com", "owner")
	_, user := userWithSession(t, srv, "user@example.com", "user")

	for _, path := range []string{testClassicPortalPath, testClassicPortalPath + "/users", testClassicPortalPath + "/users?lang=fr"} {
		for _, who := range []struct {
			name, session string
		}{{"admin", admin}, {"owner", owner}} {
			if rec := getPortalPage(srv, path, who.session); !isV1Document(rec) {
				t.Errorf("%s as %s: status %d, want V1's page", path, who.name, rec.Code)
			}
		}
		// Obscurity is not the control: a user who knows the path, or nobody, is sent to V2.
		for _, who := range []struct {
			name, session string
		}{{"a user", user}, {"no session", ""}, {"an unknown session", "not-a-session"}} {
			rec := getPortalPage(srv, path, who.session)
			if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), PortalV2Base) {
				t.Errorf("%s as %s: status %d Location %q, want 302 to V2", path, who.name, rec.Code, rec.Header().Get("Location"))
			}
			if isV1Document(rec) {
				t.Errorf("%s as %s: served V1", path, who.name)
			}
		}
	}

	// V1 is served beneath the path too, so its script has to be absolute: relative, it would
	// resolve under the section and 404, leaving a shell with no behaviour.
	rec := getPortalPage(srv, testClassicPortalPath+"/users", admin)
	if body := rec.Body.String(); !strings.Contains(body, `src="/static/dashboard.js?v=`) {
		t.Error("V1's script tag is not the absolute, cache-busted /static/dashboard.js")
	}
}

func TestClassicPortalIsNotServedWhenThePathIsUnset(t *testing.T) {
	srv := newPortalRoutesServer(t, "")
	_, admin := userWithSession(t, srv, "admin@example.com", "admin")
	// Control: the same request is served V1 once the path is configured.
	configured := newPortalRoutesServer(t, testClassicPortalPath)
	_, configuredAdmin := userWithSession(t, configured, "admin@example.com", "admin")
	if rec := getPortalPage(configured, testClassicPortalPath, configuredAdmin); !isV1Document(rec) {
		t.Fatal("control: the configured path did not serve V1")
	}
	for _, path := range []string{testClassicPortalPath, "/", "/portal", "/admin"} {
		if rec := getPortalPage(srv, path, admin); isV1Document(rec) {
			t.Errorf("%s: served V1 with classic_portal_path unset", path)
		}
	}
}

func TestMeCarriesTheClassicPortalPathForAdminsOnly(t *testing.T) {
	me := func(srv *Server, session string) map[string]interface{} {
		req := httptest.NewRequest(http.MethodGet, "http://tunnel.example.com/api/me", nil)
		req.AddCookie(&http.Cookie{Name: "lfr_session", Value: session})
		rec := httptest.NewRecorder()
		srv.handleGetMe(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("/api/me: %d %s", rec.Code, rec.Body.String())
		}
		var body map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("/api/me: %v", err)
		}
		return body
	}

	srv := newPortalRoutesServer(t, testClassicPortalPath)
	_, admin := userWithSession(t, srv, "admin@example.com", "admin")
	_, user := userWithSession(t, srv, "user@example.com", "user")
	if got := me(srv, admin)["classic_portal_path"]; got != testClassicPortalPath {
		t.Errorf("admin: classic_portal_path = %v, want %q", got, testClassicPortalPath)
	}
	if got, present := me(srv, user)["classic_portal_path"]; present {
		t.Errorf("user: classic_portal_path = %v, want it absent", got)
	}

	unset := newPortalRoutesServer(t, "")
	_, admin2 := userWithSession(t, unset, "admin@example.com", "admin")
	if got, present := me(unset, admin2)["classic_portal_path"]; present {
		t.Errorf("path unset: classic_portal_path = %v, want it absent", got)
	}
}

func TestPortalRoutingLeavesTunnelHostsAlone(t *testing.T) {
	// The control plane path chain is inside `if isControl`, so a tunnel host never reaches it:
	// a tunnelled application is free to serve its own /portal/ and /admin/ routes.
	srv := newPortalRoutesServer(t, testClassicPortalPath)
	for _, path := range []string{"/", "/portal/login", "/admin/users", "/portalv2/x"} {
		req := httptest.NewRequest(http.MethodGet, "http://mysite-se.example.com"+path, nil)
		req.Host = "mysite-se.example.com"
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code == http.StatusFound && strings.HasPrefix(rec.Header().Get("Location"), PortalV2Base) {
			t.Errorf("%s on a tunnel host was redirected to the portal", path)
		}
		if isV1Document(rec) {
			t.Errorf("%s on a tunnel host was served the V1 portal", path)
		}
	}
}
