package server

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

// The callback's defences, exercised through a real sign-in (#2451): handleSSOLogin's redirect is
// parsed, a provider issues a signed ID token, and handleSSOCallback verifies it with the real
// go-oidc verifier. The stub provider behaves as one that requires PKCE does, so a login that
// sends no challenge, or an exchange that sends the wrong verifier, fails here as it would in
// production.

// stubDetail is planted in every error the stub returns. A response body containing it means
// provider error text reached the browser.
const stubDetail = "STUB-PROVIDER-INTERNAL-DETAIL"

type oidcStub struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey

	// Captured from the authorization request, as a provider would record them against the code.
	challenge string
	nonce     string

	// What the next ID token asserts.
	emailVerified *bool // nil omits the claim, as Microsoft Entra ID does
	nonceOverride string
}

func newOIDCStub(t *testing.T) *oidcStub {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	st := &oidcStub{t: t, key: key}
	verified := true
	st.emailVerified = &verified
	st.srv = httptest.NewServer(http.HandlerFunc(st.serve))
	t.Cleanup(st.srv.Close)
	return st
}

// writeJSON and marshal report through t.Errorf, which unlike t.Fatal is safe from the handler's
// goroutine.
func (st *oidcStub) writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		st.t.Errorf("stub provider: writing response: %v", err)
	}
}

func (st *oidcStub) marshal(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		st.t.Errorf("stub provider: marshalling: %v", err)
	}
	return b
}

func (st *oidcStub) serve(w http.ResponseWriter, r *http.Request) {
	b64 := base64.RawURLEncoding.EncodeToString
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		st.writeJSON(w, map[string]interface{}{
			"issuer":                                st.srv.URL,
			"authorization_endpoint":                st.srv.URL + "/auth",
			"token_endpoint":                        st.srv.URL + "/token",
			"jwks_uri":                              st.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	case "/jwks":
		st.writeJSON(w, map[string]interface{}{
			"keys": []map[string]string{{
				"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
				"n": b64(st.key.N.Bytes()),
				"e": b64(big.NewInt(int64(st.key.E)).Bytes()),
			}},
		})
	case "/token":
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if st.challenge == "" || b64(sum[:]) != st.challenge {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			st.writeJSON(w, map[string]string{
				"error": "invalid_grant", "error_description": "PKCE verification failed " + stubDetail,
			})
			return
		}
		nonce := st.nonce
		if st.nonceOverride != "" {
			nonce = st.nonceOverride
		}
		claims := map[string]interface{}{
			"iss": st.srv.URL, "aud": "c", "sub": "subject-1",
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
			"nonce": nonce, "email": "sso.person@example.com",
			"given_name": "Sso", "family_name": "Person",
		}
		if st.emailVerified != nil {
			claims["email_verified"] = *st.emailVerified
		}
		header := st.marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
		payload := st.marshal(claims)
		signing := b64(header) + "." + b64(payload)
		digest := sha256.Sum256([]byte(signing))
		sig, err := rsa.SignPKCS1v15(rand.Reader, st.key, crypto.SHA256, digest[:])
		if err != nil {
			st.t.Errorf("stub provider: signing: %v", err)
		}
		st.writeJSON(w, map[string]interface{}{
			"access_token": "at", "token_type": "Bearer", "expires_in": 3600,
			"id_token": signing + "." + b64(sig),
		})
	default:
		http.NotFound(w, r)
	}
}

func ssoTestServer(t *testing.T, st *oidcStub) *Server {
	t.Helper()
	srv := setupTestServerForAPI(t)
	srv.cfg.SSOProviders = []config.SSOProviderConfig{{
		ID: "stub", Name: "Stub", ClientID: "c", ClientSecret: "s", IssuerURL: st.srv.URL,
	}}
	return srv
}

// startSignIn runs handleSSOLogin and records what the provider would see: the PKCE challenge
// and the nonce. It returns the cookies the browser would carry back and the state.
func startSignIn(t *testing.T, srv *Server, st *oidcStub) ([]*http.Cookie, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleSSOLogin(rec, httptest.NewRequest(http.MethodGet, "/api/auth/login?provider=stub", nil))
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
	if q.Get("code_challenge_method") != "S256" && st.challenge != "" {
		t.Errorf("code_challenge_method = %q, want S256", q.Get("code_challenge_method"))
	}
	return rec.Result().Cookies(), q.Get("state")
}

func finishSignIn(srv *Server, cookies []*http.Cookie, state string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?provider=stub&code=the-code&state="+url.QueryEscape(state), nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.handleSSOCallback(rec, req)
	return rec
}

func hasSession(rec *httptest.ResponseRecorder) bool {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "lfr_session" && c.Value != "" {
			return true
		}
	}
	return false
}

func assertNoProviderDetail(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if strings.Contains(rec.Body.String(), stubDetail) {
		t.Errorf("provider error text reached the browser: %q", rec.Body.String())
	}
}

// The control for every refusal below: the same flow, unaltered, signs in.
func TestSSOSignInSucceedsWithPKCENonceAndVerifiedEmail(t *testing.T) {
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st)
	cookies, state := startSignIn(t, srv, st)
	if st.challenge == "" {
		t.Fatal("the login redirect carried no PKCE code_challenge")
	}
	if st.nonce == "" {
		t.Fatal("the login redirect carried no nonce")
	}

	rec := finishSignIn(srv, cookies, state)
	if rec.Code != http.StatusFound || !hasSession(rec) {
		t.Fatalf("a valid sign-in: want 302 with a session, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestSSOCallbackRefusesAnExchangeWithoutTheVerifier(t *testing.T) {
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st)
	cookies, state := startSignIn(t, srv, st)

	// The browser that started the sign-in is the only holder of the verifier; a code replayed
	// from anywhere else arrives with the state but not it.
	var kept []*http.Cookie
	for _, c := range cookies {
		if c.Name != ssoVerifierCookie {
			kept = append(kept, c)
		}
	}
	rec := finishSignIn(srv, kept, state)
	if rec.Code == http.StatusFound || hasSession(rec) {
		t.Fatalf("a callback without the PKCE verifier signed in: %d", rec.Code)
	}
	assertNoProviderDetail(t, rec)

	// And with a wrong verifier, the provider refuses the exchange; its error stays in the log.
	kept = append(kept, &http.Cookie{Name: ssoVerifierCookie, Value: "not-the-verifier-not-the-verifier-not-the-verifier"})
	rec = finishSignIn(srv, kept, state)
	if rec.Code == http.StatusFound || hasSession(rec) {
		t.Fatalf("a callback with the wrong PKCE verifier signed in: %d", rec.Code)
	}
	assertNoProviderDetail(t, rec)
}

func TestSSOCallbackRefusesATokenForAnotherSignIn(t *testing.T) {
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st)
	cookies, state := startSignIn(t, srv, st)
	st.nonceOverride = "nonce-from-a-different-sign-in"

	rec := finishSignIn(srv, cookies, state)
	if rec.Code != http.StatusUnauthorized || hasSession(rec) {
		t.Fatalf("an ID token with another sign-in's nonce: want 401 and no session, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestSSOCallbackRefusesAnUnverifiedEmail(t *testing.T) {
	for _, tc := range []struct {
		name     string
		verified *bool
	}{
		{"email_verified false", new(bool)},
		{"email_verified absent", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newOIDCStub(t)
			st.emailVerified = tc.verified
			srv := ssoTestServer(t, st)
			cookies, state := startSignIn(t, srv, st)

			rec := finishSignIn(srv, cookies, state)
			if rec.Code != http.StatusForbidden || hasSession(rec) {
				t.Fatalf("want 403 and no session, got %d (%s)", rec.Code, rec.Body.String())
			}
			if _, err := srv.db.GetUserByEmail("sso.person@example.com"); err == nil {
				t.Error("a refused sign-in still created the account")
			}
			waitForAuditEntry(t, srv, ActionSSODeniedUnverified)
		})
	}
}

func TestSSOCallbackAcceptsAnAbsentClaimWhenTheProviderOptsOut(t *testing.T) {
	st := newOIDCStub(t)
	st.emailVerified = nil
	srv := ssoTestServer(t, st)
	srv.cfg.SSOProviders[0].SkipEmailVerifiedCheck = true
	cookies, state := startSignIn(t, srv, st)

	rec := finishSignIn(srv, cookies, state)
	if rec.Code != http.StatusFound || !hasSession(rec) {
		t.Fatalf("skip_email_verified_check: want a session, got %d (%s)", rec.Code, rec.Body.String())
	}
}
