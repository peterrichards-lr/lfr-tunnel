package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// #2459: `lfr-tunnel login` opens the portal with ?cli_login=1 so the portal can ask the user to
// create a token -- the only way a token reaches the waiting CLI. A signed-out user must still
// get there after signing in, so both sign-in paths carry the marker through.

func TestSSOReturnCarriesTheCLILoginMarker(t *testing.T) {
	cases := []struct {
		name, query, want string
	}{
		{"V1 from the CLI", "&cli_login=1", "/?cli_login=1"},
		{"V2 from the CLI", "&portal=v2&cli_login=1", "/portalv2/dashboard?cli_login=1"},
		{"V1 ordinary sign-in", "", "/"},
		{"V2 ordinary sign-in", "&portal=v2", "/portalv2/dashboard"},
		{"an unrecognised value is ignored", "&cli_login=yes", "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newOIDCStub(t)
			srv := ssoTestServer(t, st)
			cookies, state := startSignInWithQuery(t, srv, st, tc.query)
			done := finishSignIn(srv, cookies, state)
			if got := done.Header().Get("Location"); done.Code != http.StatusFound || got != tc.want {
				t.Errorf("return %d %q, want 302 to %q", done.Code, got, tc.want)
			}
		})
	}
}

func TestMagicLinkEmailCarriesTheCLILoginMarker(t *testing.T) {
	for _, tc := range []struct {
		name, query, referer, want string
		carries                    bool
	}{
		{"V1 from the CLI", "&cli_login=1", "http://example.com/portal", "/portal?token=", true},
		{"V2 from the CLI", "&cli_login=1", "http://example.com/portalv2/login", "/portalv2/login?token=", true},
		{"ordinary request", "", "http://example.com/portal", "/portal?token=", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, mail, cleanup := setupTestServer(t)
			defer cleanup()
			// Only an approved account is sent a link (an unknown address gets the same "ok" and no
			// email, so the endpoint cannot be used to enumerate accounts).
			if err := srv.db.CreateUser(&db.User{ID: "cli.user@example.com", Email: "cli.user@example.com",
				Role: "user", Status: db.UserStatusApproved}); err != nil {
				t.Fatalf("seeding: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "http://example.com/api/auth/magic-link?lang=en"+tc.query,
				strings.NewReader(`{"email":"cli.user@example.com"}`))
			req.Header.Set("Referer", tc.referer)
			srv.ServeHTTP(httptest.NewRecorder(), req)

			var body string
			for deadline := time.Now().Add(3 * time.Second); body == "" && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
				for _, e := range mail.getSentEmails() {
					if e.To == "cli.user@example.com" {
						body = e.HtmlBody + e.TextBody
					}
				}
			}
			if body == "" {
				t.Fatal("no magic-link email was sent")
			}
			if !strings.Contains(body, tc.want) {
				t.Fatalf("the link does not go to %q:\n%s", tc.want, body)
			}
			if got := strings.Contains(body, "cli_login=1"); got != tc.carries {
				t.Errorf("link carries cli_login=1: %v, want %v", got, tc.carries)
			}
		})
	}
}

// startSignInWithQuery is startSignIn with extra login parameters, e.g. "&cli_login=1".
func startSignInWithQuery(t *testing.T, srv *Server, st *oidcStub, extra string) ([]*http.Cookie, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleSSOLogin(rec, httptest.NewRequest(http.MethodGet, "http://tunnel.example.com/api/auth/login?provider=stub"+extra, nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("login: want 302, got %d (%s)", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := loc.Query()
	st.challenge = q.Get("code_challenge")
	st.nonce = q.Get("nonce")
	return rec.Result().Cookies(), q.Get("state")
}
