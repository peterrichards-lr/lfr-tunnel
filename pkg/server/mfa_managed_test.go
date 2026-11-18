package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

// #2514: once nothing would ever ask for the gateway's own TOTP -- email sign-in is off, every SSO
// provider performs MFA itself, and this session came through one -- Account Settings says who
// handles MFA instead of offering to enrol it, and the server refuses enrolment.

func meBody(t *testing.T, srv *Server, session string) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://tunnel.example.com/api/me", nil)
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: session})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("/api/me: %d %s", w.Code, w.Body.String())
	}
	return body
}

func TestMFAManagedByProviderOnlyWhenNothingWouldAskForTheGatewaysTOTP(t *testing.T) {
	for _, tc := range []struct {
		name               string
		emailLoginOff      bool
		satisfies, second  bool
		withSecondProvider bool
		magicLinkSession   bool
		want               bool
	}{
		{name: "SSO-only, provider performs MFA", emailLoginOff: true, satisfies: true, want: true},
		{name: "email sign-in still on", emailLoginOff: false, satisfies: true, want: false},
		{name: "provider does not perform MFA", emailLoginOff: true, satisfies: false, want: false},
		{name: "a second provider does not perform MFA", emailLoginOff: true, satisfies: true, withSecondProvider: true, second: false, want: false},
		{name: "two providers, both perform MFA", emailLoginOff: true, satisfies: true, withSecondProvider: true, second: true, want: true},
		{name: "a magic-link session from before the switch", emailLoginOff: true, satisfies: true, magicLinkSession: true, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, session := ssoSessionCookie(t, tc.satisfies)
			srv.cfg.ForceMFA = false // the gate is #2482's concern; this is the panel
			srv.cfg.DisableEmailLogin = tc.emailLoginOff
			if tc.withSecondProvider {
				srv.cfg.SSOProviders = append(srv.cfg.SSOProviders, config.SSOProviderConfig{
					ID: "other", Name: "Other", ClientID: "c", ClientSecret: "s", IssuerURL: "https://other.example", SatisfiesMFA: tc.second,
				})
			}
			if tc.magicLinkSession {
				session = "magic-link-session-2514"
				srv.sessionStore().storePortalSession(session, PortalSessionData{
					Email: "sso.person@example.com", ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now().UTC(),
				})
			}
			body := meBody(t, srv, session)
			if got := body["mfa_managed_by_provider"] == true; got != tc.want {
				t.Errorf("mfa_managed_by_provider = %v, want %v", body["mfa_managed_by_provider"], tc.want)
			}
			if tc.want && body["mfa_provider_name"] != "Stub" {
				t.Errorf("mfa_provider_name = %v, want the session's provider, Stub", body["mfa_provider_name"])
			}
		})
	}
}

func mfaCall(t *testing.T, srv *Server, session, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://tunnel.example.com"+path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: session})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestEnrolmentIsRefusedWhenTheProviderHandlesMFA(t *testing.T) {
	srv, session := ssoSessionCookie(t, true)
	srv.cfg.ForceMFA = false
	srv.cfg.DisableEmailLogin = true

	if w := mfaCall(t, srv, session, http.MethodGet, "/api/mfa/setup", ""); w.Code != http.StatusConflict {
		t.Errorf("GET /api/mfa/setup = %d, want 409: nothing would ever ask for the code", w.Code)
	}
	if w := mfaCall(t, srv, session, http.MethodPost, "/api/mfa/enable", `{"secret":"JBSWY3DPEHPK3PXP","code":"000000"}`); w.Code != http.StatusConflict {
		t.Errorf("POST /api/mfa/enable = %d, want 409", w.Code)
	}

	// CONTROL: with email sign-in back on (the break-glass route), enrolment works again.
	srv.cfg.DisableEmailLogin = false
	if w := mfaCall(t, srv, session, http.MethodGet, "/api/mfa/setup", ""); w.Code != http.StatusOK {
		t.Errorf("with email sign-in on, GET /api/mfa/setup = %d %s, want 200", w.Code, w.Body.String())
	}
}

// Someone who enrolled before the switch can still remove it: disable is not refused.
func TestRemovingAnEarlierEnrolmentStaysAllowed(t *testing.T) {
	srv, session := ssoSessionCookie(t, true)
	srv.cfg.ForceMFA = false
	srv.cfg.DisableEmailLogin = true
	w := mfaCall(t, srv, session, http.MethodPost, "/api/mfa/disable", `{"code":"000000"}`)
	if w.Code == http.StatusConflict {
		t.Errorf("POST /api/mfa/disable was refused as managed (%d): an earlier enrolment must stay removable", w.Code)
	}
}
