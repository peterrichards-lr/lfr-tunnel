package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// seedDiagnosticsUser creates an approved user and a portal session for them, and returns
// the session token so a request can be made as that person.
func seedDiagnosticsUser(t *testing.T, srv *Server, email, role string) (*db.User, string) {
	t.Helper()
	user := &db.User{ID: email, Email: email, Role: role, Status: "approved"}
	if err := srv.db.CreateUser(user); err != nil {
		t.Fatalf("creating %s: %v", email, err)
	}
	token := generateToken(16)
	srv.sessionStore().storePortalSession(token, PortalSessionData{
		Email:     email,
		ExpiresAt: time.Now().Add(time.Hour),
	})
	return user, token
}

func postAs(t *testing.T, srv *Server, handler http.HandlerFunc, path, sessionToken string, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling %v: %v", body, err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://example.com"+path, bytes.NewReader(payload))
	req.RemoteAddr = "203.0.113.7:5555"
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// auditActions returns the actions recorded against one target, newest first.
func auditActions(t *testing.T, srv *Server, targetID string) []*db.AuditEntry {
	t.Helper()
	entries, err := srv.db.ListAuditEntries(db.AuditFilter{TargetID: targetID, Limit: 200})
	if err != nil {
		t.Fatalf("listing audit entries: %v", err)
	}
	return entries
}

func hasAuditAction(entries []*db.AuditEntry, action string) *db.AuditEntry {
	for _, e := range entries {
		if e.Action == action {
			return e
		}
	}
	return nil
}

// A user who has never been asked is not consenting, and neither is a nil one. This is
// the predicate every collection path has to pass, so it is tested on its own rather than
// only through a handler.
func TestDiagnosticsCollectionAllowed(t *testing.T) {
	past := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		user *db.User
		want bool
	}{
		{"nil user", nil, false},
		{"never asked", &db.User{ID: "a@example.com"}, false},
		{"consented", &db.User{ID: "a@example.com", DiagnosticsConsentAt: &past}, true},
		{
			"policy consent alone is not diagnostics consent",
			&db.User{ID: "a@example.com", PolicyConsentAt: &past},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := diagnosticsCollectionAllowed(tc.user); got != tc.want {
				t.Errorf("diagnosticsCollectionAllowed = %v, want %v", got, tc.want)
			}
		})
	}
}

// Every account that existed before this feature has a NULL column, and NULL must read as
// "not consented" all the way through the storage layer -- not just in the struct a test
// builds by hand.
func TestDiagnosticsConsentDefaultsOffForExistingUsers(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	existing, _ := seedDiagnosticsUser(t, srv, "existing@example.com", "user")

	stored, err := srv.db.GetUser(existing.ID)
	if err != nil {
		t.Fatalf("re-reading the user: %v", err)
	}
	if stored.DiagnosticsConsentAt != nil {
		t.Fatalf("a freshly created user has diagnostics consent stamped: %v", stored.DiagnosticsConsentAt)
	}
	if diagnosticsCollectionAllowed(stored) {
		t.Error("collection is allowed for a user who has never been asked")
	}
	if state := diagnosticsConsentState(stored); state.Enabled {
		t.Error("the reported state is enabled for a user who has never been asked")
	}

	// And through ListUsers, which scans its own rows rather than sharing the single-row
	// helper -- the two have drifted before.
	users, err := srv.db.ListUsers()
	if err != nil {
		t.Fatalf("listing users: %v", err)
	}
	for _, u := range users {
		if u.ID == existing.ID && u.DiagnosticsConsentAt != nil {
			t.Errorf("ListUsers reported consent for %s: %v", u.ID, u.DiagnosticsConsentAt)
		}
	}
}

// The column has to survive a write and a read, and a withdrawal has to actually clear it
// rather than leaving the old timestamp in place.
func TestDiagnosticsConsentRoundTrip(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	user, _ := seedDiagnosticsUser(t, srv, "roundtrip@example.com", "user")

	now := time.Now().UTC().Truncate(time.Second)
	user.DiagnosticsConsentAt = &now
	if err := srv.db.UpdateUser(user); err != nil {
		t.Fatalf("granting: %v", err)
	}
	stored, err := srv.db.GetUser(user.ID)
	if err != nil {
		t.Fatalf("re-reading after grant: %v", err)
	}
	if stored.DiagnosticsConsentAt == nil {
		t.Fatal("consent was not persisted")
	}
	if !stored.DiagnosticsConsentAt.UTC().Equal(now) {
		t.Errorf("stored consent time = %v, want %v", stored.DiagnosticsConsentAt.UTC(), now)
	}

	stored.DiagnosticsConsentAt = nil
	if err := srv.db.UpdateUser(stored); err != nil {
		t.Fatalf("withdrawing: %v", err)
	}
	cleared, err := srv.db.GetUser(user.ID)
	if err != nil {
		t.Fatalf("re-reading after withdrawal: %v", err)
	}
	if cleared.DiagnosticsConsentAt != nil {
		t.Errorf("withdrawal left a timestamp behind: %v", cleared.DiagnosticsConsentAt)
	}
}

// Granting and withdrawing through the endpoint the portals call, including the audit
// entries each leaves.
func TestHandleDiagnosticsConsentGrantAndWithdraw(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	user, session := seedDiagnosticsUser(t, srv, "grantor@example.com", "user")

	rec := postAs(t, srv, srv.handleDiagnosticsConsent, "/api/me/diagnostics-consent", session,
		map[string]bool{"enabled": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("granting returned %d: %s", rec.Code, rec.Body.String())
	}
	granted, err := srv.db.GetUser(user.ID)
	if err != nil {
		t.Fatalf("re-reading after grant: %v", err)
	}
	if granted.DiagnosticsConsentAt == nil {
		t.Fatal("the grant did not reach the database")
	}
	if entry := hasAuditAction(auditActions(t, srv, user.ID), diagnosticsAuditGranted); entry == nil {
		t.Error("granting consent wrote no audit entry")
	} else if entry.ActorID != user.Email {
		t.Errorf("audit actor = %q, want %q", entry.ActorID, user.Email)
	}

	rec = postAs(t, srv, srv.handleDiagnosticsConsent, "/api/me/diagnostics-consent", session,
		map[string]bool{"enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("withdrawing returned %d: %s", rec.Code, rec.Body.String())
	}
	withdrawn, err := srv.db.GetUser(user.ID)
	if err != nil {
		t.Fatalf("re-reading after withdrawal: %v", err)
	}
	if withdrawn.DiagnosticsConsentAt != nil {
		t.Errorf("the withdrawal did not reach the database: %v", withdrawn.DiagnosticsConsentAt)
	}
	if hasAuditAction(auditActions(t, srv, user.ID), diagnosticsAuditWithdrawn) == nil {
		t.Error("withdrawing consent wrote no audit entry")
	}
}

// An unauthenticated caller cannot set somebody's consent.
func TestHandleDiagnosticsConsentRequiresASession(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/me/diagnostics-consent",
		strings.NewReader(`{"enabled":true}`))
	rec := httptest.NewRecorder()
	srv.handleDiagnosticsConsent(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for an anonymous caller, got %d: %s", rec.Code, rec.Body.String())
	}
}

// THE enforcement test. An admin asking for the logs of somebody who has not consented is
// refused, and the refusal is recorded.
func TestAdminDiagnosticsCollectRefusedWithoutConsent(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	target, _ := seedDiagnosticsUser(t, srv, "target@example.com", "user")
	admin, adminSession := seedDiagnosticsUser(t, srv, "admin@example.com", "admin")

	rec := postAs(t, srv, srv.handleAdminDiagnosticsCollect, "/api/admin/diagnostics/collect",
		adminSession, map[string]string{"email": target.Email})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-consenting user, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the refusal: %v", err)
	}
	if consent, ok := body["diagnostics_consent"].(bool); !ok || consent {
		t.Errorf("refusal did not report diagnostics_consent=false: %v", body["diagnostics_consent"])
	}
	// The admin has to be told WHY, or they will keep asking.
	if msg, _ := body["error"].(string); !strings.Contains(msg, "has not enabled diagnostic log sharing") {
		t.Errorf("refusal message does not explain the cause: %q", msg)
	}

	entry := hasAuditAction(auditActions(t, srv, target.ID), diagnosticsAuditRefused)
	if entry == nil {
		t.Fatal("a refused collection left no audit entry")
	}
	if entry.ActorID != admin.Email {
		t.Errorf("audit actor = %q, want %q", entry.ActorID, admin.Email)
	}
	if entry.TargetID != target.ID {
		t.Errorf("audit target = %q, want %q", entry.TargetID, target.ID)
	}
}

// The permitted path: consent present, so the request is allowed through and audited.
//
// The 501 asserted here is the transport not existing yet, NOT the enforcement decision.
// When log collection is built this expectation changes and the audit assertion does not.
func TestAdminDiagnosticsCollectPermittedWithConsent(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	target, targetSession := seedDiagnosticsUser(t, srv, "willing@example.com", "user")
	admin, adminSession := seedDiagnosticsUser(t, srv, "admin2@example.com", "admin")

	if rec := postAs(t, srv, srv.handleDiagnosticsConsent, "/api/me/diagnostics-consent",
		targetSession, map[string]bool{"enabled": true}); rec.Code != http.StatusOK {
		t.Fatalf("granting consent returned %d: %s", rec.Code, rec.Body.String())
	}

	rec := postAs(t, srv, srv.handleAdminDiagnosticsCollect, "/api/admin/diagnostics/collect",
		adminSession, map[string]string{"email": target.Email})

	if rec.Code == http.StatusForbidden {
		t.Fatalf("a consenting user's logs were refused: %s", rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the response: %v", err)
	}
	if status, _ := body["status"].(string); status != "consent_granted" {
		t.Errorf("status = %q, want %q", status, "consent_granted")
	}

	entry := hasAuditAction(auditActions(t, srv, target.ID), diagnosticsAuditRequested)
	if entry == nil {
		t.Fatal("a permitted collection left no audit entry")
	}
	if entry.ActorID != admin.Email {
		t.Errorf("audit actor = %q, want %q", entry.ActorID, admin.Email)
	}
	if hasAuditAction(auditActions(t, srv, target.ID), diagnosticsAuditRefused) != nil {
		t.Error("a permitted collection also recorded a refusal")
	}
}

// Withdrawal is enforced when the request is made, not when the client last started. This
// is the property the whole server-side design exists for, so it is asserted end to end:
// permitted, then withdrawn, then refused, with no client involved at any point.
func TestAdminDiagnosticsCollectHonoursWithdrawalAtRequestTime(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	target, targetSession := seedDiagnosticsUser(t, srv, "changesmind@example.com", "user")
	_, adminSession := seedDiagnosticsUser(t, srv, "admin3@example.com", "admin")

	if rec := postAs(t, srv, srv.handleDiagnosticsConsent, "/api/me/diagnostics-consent",
		targetSession, map[string]bool{"enabled": true}); rec.Code != http.StatusOK {
		t.Fatalf("granting consent returned %d: %s", rec.Code, rec.Body.String())
	}
	if rec := postAs(t, srv, srv.handleAdminDiagnosticsCollect, "/api/admin/diagnostics/collect",
		adminSession, map[string]string{"email": target.Email}); rec.Code == http.StatusForbidden {
		t.Fatalf("the first request was refused while consent was in place: %s", rec.Body.String())
	}

	if rec := postAs(t, srv, srv.handleDiagnosticsConsent, "/api/me/diagnostics-consent",
		targetSession, map[string]bool{"enabled": false}); rec.Code != http.StatusOK {
		t.Fatalf("withdrawing consent returned %d: %s", rec.Code, rec.Body.String())
	}

	rec := postAs(t, srv, srv.handleAdminDiagnosticsCollect, "/api/admin/diagnostics/collect",
		adminSession, map[string]string{"email": target.Email})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 after withdrawal, got %d: %s", rec.Code, rec.Body.String())
	}

	entries := auditActions(t, srv, target.ID)
	if hasAuditAction(entries, diagnosticsAuditRequested) == nil {
		t.Error("the permitted request before the withdrawal was not recorded")
	}
	if hasAuditAction(entries, diagnosticsAuditRefused) == nil {
		t.Error("the refused request after the withdrawal was not recorded")
	}
}

// An ordinary user cannot collect anybody's logs, including from a consenting account.
//
// Asserted here rather than left to requireAdmin because requireAdmin's cookie path
// currently rewrites a stored role of "user" to "admin" (#1760); this handler makes its
// own check, and this test is what holds that in place.
func TestAdminDiagnosticsCollectRefusesNonAdmins(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	target, targetSession := seedDiagnosticsUser(t, srv, "victim@example.com", "user")
	_, snooperSession := seedDiagnosticsUser(t, srv, "snooper@example.com", "user")

	if rec := postAs(t, srv, srv.handleDiagnosticsConsent, "/api/me/diagnostics-consent",
		targetSession, map[string]bool{"enabled": true}); rec.Code != http.StatusOK {
		t.Fatalf("granting consent returned %d: %s", rec.Code, rec.Body.String())
	}

	rec := postAs(t, srv, srv.handleAdminDiagnosticsCollect, "/api/admin/diagnostics/collect",
		snooperSession, map[string]string{"email": target.Email})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-admin caller, got %d: %s", rec.Code, rec.Body.String())
	}
	if hasAuditAction(auditActions(t, srv, target.ID), diagnosticsAuditRequested) != nil {
		t.Error("a non-admin's rejected call was recorded as a permitted request")
	}
}

// Registration: the box is a separate, optional opt-in. Ticking policy consent must not
// grant it, and leaving it unticked must leave the column NULL.
func TestCompleteSetupDiagnosticsConsentIsSeparateAndOptIn(t *testing.T) {
	cases := []struct {
		name          string
		policy        bool
		diagnostics   bool
		wantConsented bool
	}{
		{"neither ticked", false, false, false},
		{"policy only", true, false, false},
		{"diagnostics only", false, true, true},
		{"both", true, true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := setupTestServerForAPI(t)
			defer srv.Stop()

			email := "setup@example.com"
			user := &db.User{
				ID:                email,
				Email:             email,
				Role:              "user",
				Status:            "unverified",
				VerificationToken: "verify-me",
				CreatedAt:         time.Now().UTC(),
			}
			if err := srv.db.CreateUser(user); err != nil {
				t.Fatalf("creating the pending user: %v", err)
			}

			payload, err := json.Marshal(map[string]any{
				"token":               "verify-me",
				"first_name":          "Ada",
				"last_name":           "Lovelace",
				"policy_consent":      tc.policy,
				"diagnostics_consent": tc.diagnostics,
			})
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "http://example.com/api/complete-setup",
				bytes.NewReader(payload))
			req.RemoteAddr = "203.0.113.9:5555"
			rec := httptest.NewRecorder()
			srv.handleCompleteSetup(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("complete-setup returned %d: %s", rec.Code, rec.Body.String())
			}

			stored, err := srv.db.GetUser(email)
			if err != nil {
				t.Fatalf("re-reading the user: %v", err)
			}
			if got := stored.DiagnosticsConsentAt != nil; got != tc.wantConsented {
				t.Errorf("diagnostics consented = %v, want %v (policy=%v diagnostics=%v)",
					got, tc.wantConsented, tc.policy, tc.diagnostics)
			}
			if got := stored.PolicyConsentAt != nil; got != tc.policy {
				t.Errorf("policy consented = %v, want %v", got, tc.policy)
			}
		})
	}
}

// A client that predates the field sends no diagnostics_consent key at all. Absent has to
// mean "not granted", not "granted by omission".
func TestCompleteSetupOmittedDiagnosticsConsentIsNotGranted(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	email := "legacy@example.com"
	if err := srv.db.CreateUser(&db.User{
		ID: email, Email: email, Role: "user", Status: "unverified",
		VerificationToken: "legacy-token", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("creating the pending user: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/complete-setup",
		strings.NewReader(`{"token":"legacy-token","first_name":"Grace","last_name":"Hopper","policy_consent":true}`))
	req.RemoteAddr = "203.0.113.9:5555"
	rec := httptest.NewRecorder()
	srv.handleCompleteSetup(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete-setup returned %d: %s", rec.Code, rec.Body.String())
	}

	stored, err := srv.db.GetUser(email)
	if err != nil {
		t.Fatalf("re-reading the user: %v", err)
	}
	if stored.DiagnosticsConsentAt != nil {
		t.Errorf("an omitted diagnostics_consent granted consent: %v", stored.DiagnosticsConsentAt)
	}
}
