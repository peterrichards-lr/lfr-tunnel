package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
)

// An SSO sign-in returns to V2, the portal (#2528), carrying `lfr-tunnel login`'s marker when the
// CLI started it (#2459).

// stubIssuer serves just enough OIDC discovery for oidc.NewProvider; nothing here exchanges a
// code, so the endpoints only have to exist as URLs.
func stubIssuer(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"issuer":                                srv.URL,
			"authorization_endpoint":                srv.URL + "/auth",
			"token_endpoint":                        srv.URL + "/token",
			"jwks_uri":                              srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func ssoLoginCookie(t *testing.T, query string) (*http.Cookie, *httptest.ResponseRecorder) {
	t.Helper()
	srv := setupTestServerForAPI(t)
	srv.cfg.SSOProviders = []config.SSOProviderConfig{{
		ID: "liferay", Name: "Liferay", ClientID: "c", ClientSecret: "s", IssuerURL: stubIssuer(t).URL,
	}}
	rec := httptest.NewRecorder()
	srv.handleSSOLogin(rec, httptest.NewRequest(http.MethodGet, "http://tunnel.example.com/api/auth/login?"+query, nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("login: want 302 to the provider, got %d (%s)", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == ssoCLICookie {
			return c, rec
		}
	}
	return nil, rec
}

func TestSSOLoginRemembersACLIStart(t *testing.T) {
	c, rec := ssoLoginCookie(t, "provider=liferay&cli_login=1")
	if c == nil || c.Value != "1" {
		t.Fatalf("a CLI sign-in must record the marker; got cookie %+v", c)
	}
	if !c.HttpOnly {
		t.Error("the CLI cookie must be HttpOnly")
	}
	// It still goes to the provider: remembering the marker must not replace the sign-in.
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "/auth?") {
		t.Errorf("want a redirect to the provider's authorization endpoint, got %q", loc)
	}
}

func TestSSOLoginClearsAnEarlierCLIStart(t *testing.T) {
	// Written empty, not omitted: an abandoned CLI sign-in in the same browser must not mark a
	// later ordinary one.
	c, _ := ssoLoginCookie(t, "provider=liferay")
	if c == nil {
		t.Fatal("an ordinary sign-in must overwrite the CLI cookie, not leave an earlier one in place")
	}
	if c.Value != "" {
		t.Fatalf("an ordinary sign-in recorded CLI marker %q", c.Value)
	}
}

func TestSSOReturnPath(t *testing.T) {
	for _, tc := range []struct {
		cli  string
		want string
	}{
		{"", "/portal/dashboard"},
		{"1", "/portal/dashboard?cli_login=1"},
		// Only the fixed value is honoured: the cookie can never become an open redirect.
		{"https://evil.example", "/portal/dashboard"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/auth/callback", nil)
		if tc.cli != "" {
			r.AddCookie(&http.Cookie{Name: ssoCLICookie, Value: tc.cli})
		}
		if got := ssoReturnPath(r); got != tc.want {
			t.Errorf("cli cookie %q: return path %q, want %q", tc.cli, got, tc.want)
		}
	}
	// A V1 sign-in from before the change still has its old portal cookie; it lands in V2 too.
	r := httptest.NewRequest(http.MethodGet, "/api/auth/callback", nil)
	r.AddCookie(&http.Cookie{Name: "oidc_portal", Value: ""})
	if got := ssoReturnPath(r); got != "/portal/dashboard" {
		t.Errorf("old V1 portal cookie: return path %q, want /portal/dashboard", got)
	}
}
