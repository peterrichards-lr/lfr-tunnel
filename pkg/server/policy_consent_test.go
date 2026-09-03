package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// setupConsentServer is setupTestServer with client auto-reservation on.
//
// These tests drive real registrations, and without it a named subdomain is refused for
// having no portal reservation -- a rule these tests are not about, and one that would
// otherwise mask the refusal they ARE about behind an identically-shaped 403.
func setupConsentServer(t *testing.T) (*Server, func()) {
	t.Helper()
	srv, _, cleanup := setupTestServer(t)
	srv.cfg.AllowClientAutoReservation = true
	return srv, cleanup
}

// seedConsentUser creates an approved user plus a PAT that handleRegister accepts.
func seedConsentUser(t *testing.T, srv *Server, email, token string) *db.User {
	t.Helper()
	user := &db.User{ID: email, Email: email, Role: "user", Status: "approved"}
	if err := srv.db.CreateUser(user); err != nil {
		t.Fatalf("creating user %s: %v", email, err)
	}
	sum := sha256.Sum256([]byte(token))
	if err := srv.db.CreatePAT(&db.PersonalAccessToken{
		UserID:    user.ID,
		TokenHash: hex.EncodeToString(sum[:]),
		Name:      "consent-test-pat",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("creating PAT for %s: %v", email, err)
	}
	return user
}

// firstSeenDaysAgo backdates the user's first sight of the current version, which is the
// only thing that moves them between phases.
func firstSeenDaysAgo(t *testing.T, srv *Server, userID string, days int) {
	t.Helper()
	when := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	if _, err := srv.db.RecordFirstSeen(userID, PolicyDocumentID, srv.cfg.PolicyVersion, when); err != nil {
		t.Fatalf("backdating first sight: %v", err)
	}
}

func registerTunnel(t *testing.T, srv *Server, token, subdomain string) (*httptest.ResponseRecorder, RegisterResponse) {
	t.Helper()
	payload, err := json.Marshal(RegisterRequest{
		SubdomainPrefix: subdomain,
		AuthToken:       token,
		Ports:           []PortMapping{{LocalPort: 8080}},
	})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/register", bytes.NewReader(payload))
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	srv.handleRegister(rec, req)

	var resp RegisterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding the register response: %v", err)
	}
	return rec, resp
}

// consentPhase is the whole enforcement decision, so it is tested directly rather than
// only through the handlers it feeds.
func TestConsentPhaseTransitions(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	const grace, warn = 14, 5

	cases := []struct {
		name          string
		firstSeenDays int
		neverSeen     bool
		wantPhase     string
	}{
		// Never shown the policy: the window has not started. Cutting somebody off for not
		// answering a question nobody put to them is the failure first-sight avoids.
		{name: "never seen", neverSeen: true, wantPhase: ConsentPhaseGrace},
		{name: "just seen", firstSeenDays: 0, wantPhase: ConsentPhaseGrace},
		{name: "mid grace", firstSeenDays: 5, wantPhase: ConsentPhaseGrace},
		// grace 14, warning 5 -> the warning starts on day 9.
		{name: "one day before the warning", firstSeenDays: 8, wantPhase: ConsentPhaseGrace},
		{name: "first day of the warning", firstSeenDays: 9, wantPhase: ConsentPhaseWarning},
		{name: "last day of the warning", firstSeenDays: 13, wantPhase: ConsentPhaseWarning},
		{name: "exactly at the deadline", firstSeenDays: 14, wantPhase: ConsentPhaseExpired},
		{name: "well past the deadline", firstSeenDays: 40, wantPhase: ConsentPhaseExpired},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var firstSeen time.Time
			if !tc.neverSeen {
				firstSeen = now.Add(-time.Duration(tc.firstSeenDays) * 24 * time.Hour)
			}
			phase, deadline := consentPhase(firstSeen, now, grace, warn)
			if phase != tc.wantPhase {
				t.Errorf("phase = %q, want %q", phase, tc.wantPhase)
			}
			if tc.neverSeen {
				if !deadline.IsZero() {
					t.Errorf("expected no deadline before the policy was ever shown, got %v", deadline)
				}
			} else if deadline.IsZero() {
				t.Error("expected a deadline once the policy has been seen")
			}
		})
	}
}

// The warning window may not start before the window it warns about, or it is on
// permanently and warns about nothing.
func TestConsentWarningWindowIsClampedToGrace(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()

	srv.cfg.PolicyConsentGraceDays = 3
	srv.cfg.PolicyConsentWarningDays = 10
	if got := srv.consentWarningDays(); got != 3 {
		t.Errorf("warning days = %d, want it clamped to the 3-day grace window", got)
	}

	srv.cfg.PolicyConsentGraceDays = 0
	srv.cfg.PolicyConsentWarningDays = 0
	if got := srv.consentGraceDays(); got != 14 {
		t.Errorf("grace days = %d, want the 14-day default when unset", got)
	}
	if got := srv.consentWarningDays(); got != 5 {
		t.Errorf("warning days = %d, want the 5-day default when unset", got)
	}
}

// With no policy_version configured nothing changes at all -- that is the upgrade path for
// every existing deployment.
func TestConsentInertWhenNoVersionConfigured(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()

	user := seedConsentUser(t, srv, "inert@example.com", "tok-inert")
	if state := srv.policyConsentState(user, true); state.Required {
		t.Error("consent reported as required with no policy_version set")
	}
	rec, _ := registerTunnel(t, srv, "tok-inert", "inert-sub")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected registration to succeed, got %d: %s", rec.Code, rec.Body.String())
	}
}

// A user inside the grace window keeps working, and the response carries the state the
// client needs to decide whether to say anything.
func TestConsentWithinGraceAllowsNewTunnels(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.PolicyVersion = "2"

	user := seedConsentUser(t, srv, "grace@example.com", "tok-grace")
	firstSeenDaysAgo(t, srv, user.ID, 2)

	rec, resp := registerTunnel(t, srv, "tok-grace", "grace-sub")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 within the grace window, got %d: %s", rec.Code, rec.Body.String())
	}
	if resp.PolicyConsent == nil || !resp.PolicyConsent.Required {
		t.Fatal("expected the outstanding consent state on the registration response")
	}
	if resp.PolicyConsent.Phase != ConsentPhaseGrace {
		t.Errorf("phase = %q, want %q", resp.PolicyConsent.Phase, ConsentPhaseGrace)
	}
	// The client stays quiet during grace: two weeks of noise is what stops the message
	// that matters from being read.
	if notice := PolicyConsentNoticeText(resp.PolicyConsent); notice != "" {
		t.Errorf("expected no client warning during grace, got %q", notice)
	}
}

func TestConsentWarningWindowWarnsButAllows(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.PolicyVersion = "2"
	srv.cfg.PortalURL = "https://portal.example.com"

	user := seedConsentUser(t, srv, "warn@example.com", "tok-warn")
	firstSeenDaysAgo(t, srv, user.ID, 11)

	rec, resp := registerTunnel(t, srv, "tok-warn", "warn-sub")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 inside the warning window, got %d: %s", rec.Code, rec.Body.String())
	}
	if resp.PolicyConsent == nil || resp.PolicyConsent.Phase != ConsentPhaseWarning {
		t.Fatalf("expected the warning phase, got %+v", resp.PolicyConsent)
	}
	notice := PolicyConsentNoticeText(resp.PolicyConsent)
	if notice == "" {
		t.Fatal("expected a client startup warning inside the warning window")
	}
	// The whole point of the client warning is telling a CLI-only user where to go.
	if !bytes.Contains([]byte(notice), []byte("https://portal.example.com")) {
		t.Errorf("the client warning does not say where to accept: %q", notice)
	}
}

// The enforcement test. A user past their deadline is refused a NEW tunnel, and the
// refusal has to be distinguishable from the quota 403 the client already knows about.
func TestConsentExpiredRefusesNewTunnel(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.PolicyVersion = "2"
	srv.cfg.PortalURL = "https://portal.example.com"

	user := seedConsentUser(t, srv, "expired@example.com", "tok-expired")
	firstSeenDaysAgo(t, srv, user.ID, 30)

	rec, resp := registerTunnel(t, srv, "tok-expired", "expired-sub")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 once the grace window has expired, got %d: %s", rec.Code, rec.Body.String())
	}
	if resp.PolicyConsent == nil || resp.PolicyConsent.Phase != ConsentPhaseExpired {
		t.Fatalf("the refusal must carry the consent state so the client can explain it, got %+v", resp.PolicyConsent)
	}
	if resp.Error == "" {
		t.Error("expected an operator-facing message on the refusal")
	}
	// No lease may have been created.
	if leases := srv.registry.ListLeases(); len(leases) != 0 {
		t.Errorf("expected no lease after a refused registration, got %d", len(leases))
	}
}

// The hard requirement from the issue: refusing the NEXT tunnel must not disturb one that
// is already established. This is a service used for live customer demos.
func TestConsentExpiryLeavesEstablishedTunnelRunning(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.PolicyVersion = "2"

	user := seedConsentUser(t, srv, "live@example.com", "tok-live")
	// Registered while still inside the window.
	firstSeenDaysAgo(t, srv, user.ID, 1)
	rec, _ := registerTunnel(t, srv, "tok-live", "live-sub")
	if rec.Code != http.StatusOK {
		t.Fatalf("setup registration failed: %d %s", rec.Code, rec.Body.String())
	}
	before := len(srv.registry.ListLeases())
	if before == 0 {
		t.Fatal("expected an established lease to exist before the deadline passes")
	}

	// Now the deadline passes underneath them. Shortening the grace window rather than
	// rewriting the stored first-sight: RecordFirstSeen is deliberately idempotent, so the
	// stamp cannot be moved, and the window it is measured against is configuration.
	srv.cfg.PolicyConsentGraceDays = 1

	// A new tunnel is refused...
	rec2, _ := registerTunnel(t, srv, "tok-live", "second-sub")
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("expected the NEW tunnel to be refused, got %d", rec2.Code)
	}
	// ...while the established one is untouched. The sweep only drops tunnels when the
	// operator has explicitly opted in.
	srv.runPolicyConsentSweep(nil)
	after := srv.registry.ListLeases()
	if len(after) != before {
		t.Fatalf("an established tunnel was torn down: %d leases before, %d after", before, len(after))
	}
	var found bool
	for _, l := range after {
		if l.SubdomainPrefix == "live-sub" {
			found = true
		}
	}
	if !found {
		t.Error("the established tunnel 'live-sub' was dropped at the expiry boundary")
	}
}

// The opt-in behaviour, so the config key is not a switch that does nothing.
func TestConsentExpiryDropsActiveTunnelsWhenConfigured(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.PolicyVersion = "2"

	user := seedConsentUser(t, srv, "drop@example.com", "tok-drop")
	firstSeenDaysAgo(t, srv, user.ID, 1)
	rec, _ := registerTunnel(t, srv, "tok-drop", "drop-sub")
	if rec.Code != http.StatusOK {
		t.Fatalf("setup registration failed: %d %s", rec.Code, rec.Body.String())
	}
	if len(srv.registry.ListLeases()) == 0 {
		t.Fatal("expected a lease before the sweep")
	}

	srv.cfg.PolicyConsentGraceDays = 1
	srv.cfg.PolicyConsentStopsActiveTunnels = true
	srv.runPolicyConsentSweep(nil)

	for _, l := range srv.registry.ListLeases() {
		if l.SubdomainPrefix == "drop-sub" {
			t.Fatal("policy_consent_stops_active_tunnels was set but the tunnel survived")
		}
	}
}

// Accepting clears the block, and the acceptance lands in the append-only history.
func TestAcceptingClearsTheBlock(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.PolicyVersion = "2"

	email := "accept@example.com"
	user := seedConsentUser(t, srv, email, "tok-accept")
	firstSeenDaysAgo(t, srv, user.ID, 30)

	token := "session-accept-1"
	srv.portalMap.Store("admin_session_"+token, PortalSessionData{
		Email: email, ExpiresAt: time.Now().Add(time.Hour), ClientIP: "127.0.0.1",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/me/policy-consent", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	srv.handlePolicyConsentAccept(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("accepting failed: %d %s", rec.Code, rec.Body.String())
	}

	if state := srv.policyConsentState(user, false); state.Required {
		t.Error("consent still reported as outstanding after acceptance")
	}
	history, err := srv.db.ListAcknowledgements(user.ID)
	if err != nil || len(history) != 1 {
		t.Fatalf("expected one history row, got %d (err %v)", len(history), err)
	}
	if history[0].Version != "2" {
		t.Errorf("recorded version = %q, want the current one", history[0].Version)
	}
	// And a previously-refused registration now succeeds.
	rec2, _ := registerTunnel(t, srv, "tok-accept", "accept-sub")
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected registration to succeed after acceptance, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

// "Remind me later" clears with the session, not permanently. Otherwise the banner applies
// no pressure and the grace window becomes the only mechanism.
func TestRemindLaterIsPerSession(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.PolicyVersion = "2"

	email := "later@example.com"
	user := seedConsentUser(t, srv, email, "tok-later")
	firstSeenDaysAgo(t, srv, user.ID, 2)

	tokenA := "session-later-A"
	srv.portalMap.Store("admin_session_"+tokenA, PortalSessionData{
		Email: email, ExpiresAt: time.Now().Add(time.Hour), ClientIP: "127.0.0.1",
	})

	reqA := httptest.NewRequest(http.MethodPost, "/api/me/policy-consent/remind-later", nil)
	reqA.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokenA})
	recA := httptest.NewRecorder()
	srv.handlePolicyConsentRemindLater(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Fatalf("remind-later failed: %d %s", recA.Code, recA.Body.String())
	}

	probeA := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	probeA.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokenA})
	if !srv.policyGateSuppressed(probeA) {
		t.Error("the gate was not suppressed for the session that dismissed it")
	}

	// A second session -- the next login -- must see the gate again.
	tokenB := "session-later-B"
	srv.portalMap.Store("admin_session_"+tokenB, PortalSessionData{
		Email: email, ExpiresAt: time.Now().Add(time.Hour), ClientIP: "127.0.0.1",
	})
	probeB := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	probeB.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokenB})
	if srv.policyGateSuppressed(probeB) {
		t.Error("a dismissal leaked into another session; it must clear at the next login")
	}

	// And it never suppresses the consent requirement itself -- only the gate's display.
	if !srv.policyConsentState(user, false).Required {
		t.Error("dismissing the gate cleared the outstanding consent")
	}
}

// The portal gate blocks the API once expired, and lets through exactly what a blocked
// user needs in order to become unblocked.
func TestPortalGateBlocksWhenExpired(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.PolicyVersion = "2"

	email := "gate@example.com"
	user := seedConsentUser(t, srv, email, "tok-gate")
	firstSeenDaysAgo(t, srv, user.ID, 30)

	token := "session-gate"
	srv.portalMap.Store("admin_session_"+token, PortalSessionData{
		Email: email, ExpiresAt: time.Now().Add(time.Hour), ClientIP: "127.0.0.1",
	})

	blocked := httptest.NewRequest(http.MethodGet, "/api/portal/reservations", nil)
	blocked.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	if !srv.enforcePolicyConsentGate(rec, blocked) {
		t.Fatal("expected the portal gate to answer the request once expired")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("gate status = %d, want 403", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the gate response: %v", err)
	}
	if body["policy_consent_required"] != true {
		t.Errorf("the 403 must be distinguishable from any other: %v", body)
	}

	for _, path := range []string{"/api/me", "/api/me/policy-consent", "/api/me/policy-consent/remind-later", "/api/auth/logout", "/api/register"} {
		allowed := httptest.NewRequest(http.MethodGet, path, nil)
		allowed.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		w := httptest.NewRecorder()
		if srv.enforcePolicyConsentGate(w, allowed) {
			t.Errorf("%s was blocked; a locked-out user could never become unblocked", path)
		}
	}

	// Once accepted, nothing is blocked.
	if err := srv.recordPolicyConsent(user, "127.0.0.1", "test"); err != nil {
		t.Fatalf("recording acceptance: %v", err)
	}
	after := httptest.NewRequest(http.MethodGet, "/api/portal/reservations", nil)
	after.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	if srv.enforcePolicyConsentGate(httptest.NewRecorder(), after) {
		t.Error("the portal is still gated after the user accepted")
	}
}

// /api/me is what both portals render from, so the shape it reports is part of the
// contract for V1 and V2 alike.
func TestGetMeReportsConsentState(t *testing.T) {
	srv, cleanup := setupConsentServer(t)
	defer cleanup()
	srv.cfg.PolicyVersion = "2"

	email := "me@example.com"
	user := seedConsentUser(t, srv, email, "tok-me")
	firstSeenDaysAgo(t, srv, user.ID, 11)

	token := "session-me"
	srv.portalMap.Store("admin_session_"+token, PortalSessionData{
		Email: email, ExpiresAt: time.Now().Add(time.Hour), ClientIP: "127.0.0.1",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	srv.handleGetMe(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/me failed: %d %s", rec.Code, rec.Body.String())
	}

	var body struct {
		PolicyConsent  ConsentState `json:"policy_consent"`
		GateSuppressed bool         `json:"policy_gate_suppressed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding /api/me: %v", err)
	}
	if !body.PolicyConsent.Required {
		t.Fatal("/api/me did not report the outstanding consent")
	}
	if body.PolicyConsent.Phase != ConsentPhaseWarning {
		t.Errorf("phase = %q, want %q", body.PolicyConsent.Phase, ConsentPhaseWarning)
	}
	if body.PolicyConsent.Deadline == "" {
		t.Error("no deadline reported; the portal has nothing to count down to")
	}
	if body.GateSuppressed {
		t.Error("the gate reported as suppressed before anyone dismissed it")
	}
}
