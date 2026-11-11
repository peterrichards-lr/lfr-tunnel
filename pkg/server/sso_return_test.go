package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
)

// An SSO sign-in returns to the portal that started it (#2447). The callback used to redirect to
// "/" whatever the starting portal, so Portal V2's users were handed to V1.

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
	srv.handleSSOLogin(rec, httptest.NewRequest(http.MethodGet, "/api/auth/login?"+query, nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("login: want 302 to the provider, got %d (%s)", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == ssoPortalCookie {
			return c, rec
		}
	}
	return nil, rec
}

func TestSSOLoginRemembersAV2Start(t *testing.T) {
	c, rec := ssoLoginCookie(t, "provider=liferay&portal=v2")
	if c == nil || c.Value != ssoPortalV2 {
		t.Fatalf("a V2 sign-in must record its portal; got cookie %+v", c)
	}
	if !c.HttpOnly {
		t.Error("the portal cookie must be HttpOnly")
	}
	// It still goes to the provider: remembering the portal must not replace the sign-in.
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "/auth?") {
		t.Errorf("want a redirect to the provider's authorization endpoint, got %q", loc)
	}
}

func TestSSOLoginFromV1ClearsAnEarlierV2Start(t *testing.T) {
	// Written empty, not omitted: an abandoned V2 sign-in in the same browser must not send a
	// later V1 sign-in to V2.
	c, _ := ssoLoginCookie(t, "provider=liferay")
	if c == nil {
		t.Fatal("a V1 sign-in must overwrite the portal cookie, not leave an earlier one in place")
	}
	if c.Value != "" {
		t.Fatalf("a V1 sign-in recorded portal %q", c.Value)
	}
}

func TestSSOLoginIgnoresAnUnknownPortal(t *testing.T) {
	c, _ := ssoLoginCookie(t, "provider=liferay&portal=https://evil.example")
	if c == nil || c.Value != "" {
		t.Fatalf("an unrecognised portal value must be dropped, got %+v", c)
	}
}

func TestSSOReturnPath(t *testing.T) {
	for _, tc := range []struct {
		cookie string
		want   string
	}{
		{"", "/"},
		{ssoPortalV2, "/portalv2/dashboard"},
		// Only the fixed value is honoured: the cookie can never become an open redirect.
		{"https://evil.example", "/"},
		{"//evil.example", "/"},
		{"V2", "/"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/auth/callback", nil)
		if tc.cookie != "" {
			r.AddCookie(&http.Cookie{Name: ssoPortalCookie, Value: tc.cookie})
		}
		if got := ssoReturnPath(r); got != tc.want {
			t.Errorf("cookie %q: return path %q, want %q", tc.cookie, got, tc.want)
		}
	}
	// No cookie at all is the old behaviour.
	if got := ssoReturnPath(httptest.NewRequest(http.MethodGet, "/api/auth/callback", nil)); got != "/" {
		t.Errorf("no cookie: return path %q, want /", got)
	}
}
