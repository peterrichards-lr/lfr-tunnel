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
		{"from the CLI", "&cli_login=1", "/portal/dashboard?cli_login=1"},
		// An older V2 page still sends portal=v2; it no longer changes anything (#2528).
		{"from the CLI, older V2 page", "&portal=v2&cli_login=1", "/portal/dashboard?cli_login=1"},
		{"ordinary sign-in", "", "/portal/dashboard"},
		{"an unrecognised value is ignored", "&cli_login=yes", "/portal/dashboard"},
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

// An abandoned CLI sign-in must not mark a later ordinary one in the same browser. The browser
// keeps the first oidc_cli cookie unless the second sign-in overwrites it, so /sso/login writes it
// every time -- empty when the request carries no marker.
func TestAnAbandonedCLISignInDoesNotMarkTheNextOne(t *testing.T) {
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st)
	abandoned, _ := startSignInWithQuery(t, srv, st, "&cli_login=1")
	ordinary, state := startSignInWithQuery(t, srv, st, "")

	// The browser's jar: the later Set-Cookie replaces the earlier one of the same name.
	jar := map[string]*http.Cookie{}
	for _, c := range append(abandoned, ordinary...) {
		jar[c.Name] = c
	}
	cookies := make([]*http.Cookie, 0, len(jar))
	for _, c := range jar {
		cookies = append(cookies, c)
	}
	// Fixture: the abandoned sign-in really did leave a marker that could go stale.
	marked := false
	for _, c := range abandoned {
		marked = marked || c.Name == ssoCLICookie && c.Value == "1"
	}
	if !marked {
		t.Fatalf("fixture: the abandoned sign-in set no %s=1 cookie to go stale", ssoCLICookie)
	}

	done := finishSignIn(srv, cookies, state)
	if got := done.Header().Get("Location"); done.Code != http.StatusFound || got != "/portal/dashboard" {
		t.Errorf("return %d %q, want 302 to %q -- a stale CLI marker carried over", done.Code, got, "/portal/dashboard")
	}
}

func TestMagicLinkEmailCarriesTheCLILoginMarker(t *testing.T) {
	for _, tc := range []struct {
		name, query, referer, want string
		carries                    bool
	}{
		{"from the CLI", "&cli_login=1", "http://example.com/portal/login", "/portal/login?token=", true},
		// Asked from V1 (an admin on the classic portal): the link still opens V2 (#2528).
		{"from V1", "&cli_login=1", "http://example.com/classic-7f3k9q2m", "/portal/login?token=", true},
		{"ordinary request", "", "http://example.com/portal/login", "/portal/login?token=", false},
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
