package server

import (
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// waitForAuditEntry polls for an audit row rather than reading once.
//
// writeAudit deliberately writes in a bare goroutine -- "so it doesn't block the HTTP response"
// (server_audit.go) -- and there is no WaitGroup or flush to await. Asserting immediately after
// the call is therefore a race with the write, not a test of it. That race won on a developer
// machine and lost on ubuntu CI, which is exactly how this test first went red: the product was
// correct and the assertion was not.
//
// Bounded rather than unbounded so a genuinely missing row still fails, and fails saying which.
func waitForAuditEntry(t *testing.T, srv *Server, action string) *db.AuditEntry {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, err := srv.db.ListAuditEntries(db.AuditFilter{Action: action})
		if err != nil {
			t.Fatalf("reading audit entries: %v", err)
		}
		if len(entries) > 0 {
			return entries[0]
		}
		if time.Now().After(deadline) {
			// Generic on purpose: this helper is shared, so a message describing one caller's
			// subject would misattribute every other caller's failure. The action name is the
			// specific part, and it is already in the message.
			t.Fatalf("no %q audit row appeared within 5s -- the operation completed without "+
				"leaving the trace an operator would need to find it.", action)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// SSO sign-in auto-approving is the intended behaviour, confirmed by the repo owner on
// 2026-09-08: once Liferay SSO is configured, the Liferay server controls who may authenticate,
// so reaching the callback IS the authorisation.
//
// The defect in #1824 was that it approved PARTIALLY -- status only. These tests pin the three
// things it left undone, each of which was observable in production:
//
//   - approval_token stayed live on all five self-registered users, so the admin's emailed
//     approve link was still armed against an already-approved account;
//   - no audit row, so a pending -> approved transition left no trace of what did it;
//   - no last_login_at, which made those users read as "never logged in" and very nearly sent
//     this investigation down the wrong path.
//
// approveOnSSOSignIn is the function handleSSOCallback actually calls. Reaching the callback
// itself needs a full OIDC token exchange, and a test that faked one would be asserting against
// the fake -- so this drives the real function instead.

func pendingSSOUser(t *testing.T, srv *Server, email string) *db.User {
	t.Helper()
	u := &db.User{
		ID:            email,
		Email:         email,
		Role:          "user",
		Status:        "pending",
		ApprovalToken: "approval-token-still-armed",
		AuthMethod:    "registration",
	}
	if err := srv.db.CreateUser(u); err != nil {
		t.Fatalf("seeding pending user: %v", err)
	}
	loaded, err := srv.db.GetUserByEmail(email)
	if err != nil || loaded == nil {
		t.Fatalf("reading back seeded user: %v", err)
	}
	// The fixture must actually be in the state the defect needs, or every assertion below
	// passes for the wrong reason.
	if loaded.Status != "pending" || loaded.ApprovalToken == "" {
		t.Fatalf("fixture is not a pending user with a live approval token (status %q, token %q)",
			loaded.Status, loaded.ApprovalToken)
	}
	return loaded
}

// TestSSOSignInCompletesTheApproval is the whole of #1824's server-side half.
func TestSSOSignInCompletesTheApproval(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "sso-approve@example.com"
	user := pendingSSOUser(t, srv, email)

	req := httptest.NewRequest("GET", "http://example.com/api/auth/callback?provider=liferay", nil)
	req.RemoteAddr = "127.0.0.1:5555"

	if err := srv.approveOnSSOSignIn(user, "liferay", req); err != nil {
		t.Fatalf("approveOnSSOSignIn refused a pending user: %v", err)
	}

	got, err := srv.db.GetUserByEmail(email)
	if err != nil || got == nil {
		t.Fatalf("reading user back: %v", err)
	}

	if got.Status != "approved" {
		t.Errorf("status = %q, want \"approved\" -- SSO sign-in is the access decision", got.Status)
	}

	// The one that was live in production on all five users.
	if got.ApprovalToken != "" {
		t.Errorf("approval_token is still %q. The admin's emailed approve link remains armed "+
			"against an already-approved account; following it later mints a claim token and "+
			"sends a \"Registration Approved!\" mail for something that happened days earlier.",
			got.ApprovalToken)
	}

	// Why this matters beyond tidiness: with it unset, these users read as "never logged in",
	// which is exactly what made the production data look as though nobody had signed in.
	if got.LastLoginAt == nil {
		t.Error("last_login_at was not recorded, so an SSO-only user still reads as having " +
			"never signed in")
	}

	entry := waitForAuditEntry(t, srv, "user.approved.sso")
	if entry.TargetID != email {
		t.Errorf("audit row names target %q, want %q", entry.TargetID, email)
	}
}

// TestSSOApprovalDoesNotEmailTheUser pins a deliberate omission, so that a later reader does not
// "fix" it by adding a notification. The user is completing a sign-in as this runs and is about
// to be looking at the dashboard; the mail in the ADMIN approval path exists to reach somebody
// who is not present.
//
// Asserted through the audit log rather than a mail spy: setupTestServerForAPI builds no sender,
// so a send attempt here would nil-panic -- which is itself the assertion. This test records the
// intent and fails loudly if a send is ever added without one.
func TestSSOApprovalDoesNotEmailTheUser(t *testing.T) {
	srv := setupTestServerForAPI(t)
	if srv.notifications != nil && srv.notifications.Sender() != nil {
		t.Skip("fixture unexpectedly has a mail sender; this test relies on there being none")
	}

	const email = "sso-nomail@example.com"
	user := pendingSSOUser(t, srv, email)
	req := httptest.NewRequest("GET", "http://example.com/api/auth/callback?provider=liferay", nil)

	// If approveOnSSOSignIn ever starts sending mail, this call panics on the nil sender rather
	// than returning -- so reaching the assertion below is the property.
	if err := srv.approveOnSSOSignIn(user, "liferay", req); err != nil {
		t.Fatalf("approveOnSSOSignIn refused a pending user: %v", err)
	}

	got, err := srv.db.GetUserByEmail(email)
	if err != nil || got == nil || got.Status != "approved" {
		t.Fatalf("approval did not complete: %v", err)
	}
}

// TestSSOApprovalSurvivesAnAlreadyApprovedUser guards the caller's condition rather than the
// function: handleSSOCallback only calls this when status != "approved". If that guard is ever
// dropped, a returning user's last_login_at should still update and nothing should break -- but
// a second audit row per sign-in would be noise, so this documents which side owns the check.
func TestSSOApprovalSurvivesAnAlreadyApprovedUser(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "sso-repeat@example.com"
	if err := srv.db.CreateUser(&db.User{
		ID: email, Email: email, Role: "user", Status: "approved", AuthMethod: "sso - liferay",
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	u, err := srv.db.GetUserByEmail(email)
	if err != nil || u == nil {
		t.Fatalf("reading back: %v", err)
	}

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	if err := srv.approveOnSSOSignIn(u, "liferay", req); err != nil {
		t.Fatalf("approveOnSSOSignIn refused an already-approved user: %v", err)
	}

	got, err := srv.db.GetUserByEmail(email)
	if err != nil || got == nil {
		t.Fatalf("reading user back: %v", err)
	}
	if got.Status != "approved" {
		t.Errorf("status = %q, want it left approved", got.Status)
	}
}
