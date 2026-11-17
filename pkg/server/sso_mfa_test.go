package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// #2482: force_mfa is met per SESSION by a provider configured with satisfies_mfa.

// gatedStatus calls an API behind the force_mfa gate with the given session cookie and reports
// the status and whether the refusal was the MFA one.
func gatedStatus(t *testing.T, srv *Server, session string) (int, bool) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://tunnel.example.com/api/tokens", nil)
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: session})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		return w.Code, false // not JSON, so not the MFA refusal
	}
	return w.Code, body["mfa_required"] == true
}

func ssoSessionCookie(t *testing.T, satisfiesMFA bool) (*Server, string) {
	t.Helper()
	st := newOIDCStub(t)
	srv := ssoTestServer(t, st)
	srv.cfg.ForceMFA = true
	srv.cfg.SSOProviders[0].SatisfiesMFA = satisfiesMFA
	cookies, state := startSignIn(t, srv, st)
	rec := finishSignIn(srv, cookies, state)
	for _, c := range rec.Result().Cookies() {
		if c.Name == "lfr_session" && c.Value != "" {
			return srv, c.Value
		}
	}
	t.Fatalf("sign-in issued no session: %d %s", rec.Code, rec.Body.String())
	return nil, ""
}

func TestForceMFAIsMetByASatisfiesMFAProviderSession(t *testing.T) {
	srv, session := ssoSessionCookie(t, true)
	if code, mfa := gatedStatus(t, srv, session); mfa || code == http.StatusForbidden {
		t.Errorf("an SSO session from a satisfies_mfa provider was refused: %d (mfa_required %v)", code, mfa)
	}

	// Across a restart: the in-memory copy is gone and the session is reloaded from the table.
	srv.portalMap.Delete(portalSessionKeyPrefix + session)
	if code, mfa := gatedStatus(t, srv, session); mfa {
		t.Errorf("after reloading the session from the database it was refused: %d", code)
	}
}

func TestForceMFAStillAppliesToAProviderWithoutTheSetting(t *testing.T) {
	srv, session := ssoSessionCookie(t, false)
	if code, mfa := gatedStatus(t, srv, session); code != http.StatusForbidden || !mfa {
		t.Errorf("an SSO session from a provider without satisfies_mfa: %d (mfa_required %v), want 403 mfa_required", code, mfa)
	}
}

// The exemption follows how the session signed in, not the account: the same user arriving by
// magic link (a session with no provider) is held to force_mfa.
func TestForceMFAStillAppliesToTheSameAccountsMagicLinkSession(t *testing.T) {
	srv, _ := ssoSessionCookie(t, true) // creates the account through a satisfies_mfa provider
	token := "magic-link-session-2482"
	srv.sessionStore().storePortalSession(token, PortalSessionData{
		Email: "sso.person@example.com", ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now().UTC(),
	})
	if code, mfa := gatedStatus(t, srv, token); code != http.StatusForbidden || !mfa {
		t.Errorf("a magic-link session for an SSO-created account: %d (mfa_required %v), want 403 mfa_required", code, mfa)
	}
}

// A provider removed from the config satisfies nothing, even for a session it created.
func TestForceMFAAppliesWhenTheSessionsProviderIsGone(t *testing.T) {
	srv, session := ssoSessionCookie(t, true)
	srv.cfg.SSOProviders[0].ID = "renamed"
	if code, mfa := gatedStatus(t, srv, session); code != http.StatusForbidden || !mfa {
		t.Errorf("a session whose provider is no longer configured: %d (mfa_required %v), want 403 mfa_required", code, mfa)
	}
}
