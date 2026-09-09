package server

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// Rejecting a registration (#1830).
//
// Before this there was no rejection at all: an admin could approve, and a request the owner did
// not want to grant sat at "pending" forever with the applicant never told anything. The tests
// below pin the four things the owner decided on 2026-09-08, plus the interaction that decision
// exposes:
//
//  1. an admin-initiated rejection emails the applicant, with an optional reason;
//  2. the audit row is written regardless -- not conditional on the send;
//  3. the AllowedEmailDomains rejection stays silent (untouched here, asserted by its own
//     pre-existing tests);
//  4. "reject silently" exists as an explicit choice;
//
// and the one that matters most: a rejected user must not be able to approve themselves by
// signing in through SSO, which auto-approves anything not already approved.
//
// Every audit assertion here goes through waitForAuditEntry or assertAuditRowCountStaysAt.
// writeAudit writes in a bare goroutine with no WaitGroup, so a single read races the write --
// it passes on a developer machine and fails on ubuntu CI, and an ABSENCE read that has simply
// not waited long enough passes for entirely the wrong reason (#1843).

// seedPendingRegistration creates the row an admin's emailed decision link acts on: verified,
// awaiting approval, still holding its approval token. That is what handleVerifyEmail leaves
// behind, and it is deliberately the state the reject handler requires.
func seedPendingRegistration(t *testing.T, srv *Server, email, token string) *db.User {
	t.Helper()
	u := &db.User{
		ID:            email,
		Email:         email,
		FirstName:     "Casey",
		LastName:      "Colleague",
		PreferredName: "Casey",
		Role:          "user",
		Status:        "pending",
		ApprovalToken: token,
		AuthMethod:    "registration",
	}
	if err := srv.db.CreateUser(u); err != nil {
		t.Fatalf("seeding pending registration %s: %v", email, err)
	}
	return u
}

// postRejection submits the confirmation form the way a browser does.
func postRejection(t *testing.T, srv *Server, email, token, reason string, silent bool) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{}
	form.Set("email", email)
	form.Set("token", token)
	if reason != "" {
		form.Set("reason", reason)
	}
	if silent {
		form.Set("silent", "1")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/reject", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleRejectUser(rec, req)
	return rec
}

func mustGetUser(t *testing.T, srv *Server, email string) *db.User {
	t.Helper()
	u, err := srv.db.GetUser(email)
	if err != nil {
		t.Fatalf("reading back %s: %v", email, err)
	}
	return u
}

// TestRejectionIsRecordedEvenWhenTheEmailFails is point 2 of the owner's decision asserted
// directly: "the audit entry is the record, the email is the courtesy" -- so the row must not be
// conditional on the send.
//
// Against the unfixed code this fails at the first assertion, because there is no
// handleRejectUser at all and nothing can reject anybody.
func TestRejectionIsRecordedEvenWhenTheEmailFails(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "declined@example.com"
	seedPendingRegistration(t, srv, email, "approval-token-1")
	failingSender(t, srv, errors.New("smtp: 550 mailbox unavailable"))

	rec := postRejection(t, srv, email, "approval-token-1", "duplicate of an existing account", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("rejection returned %d, want 200: %s", rec.Code, rec.Body.String())
	}

	if got := mustGetUser(t, srv, email).Status; got != statusRejected {
		t.Errorf("user status is %q after a rejection whose email failed, want %q. The rejection "+
			"is a decision the admin made; a failed courtesy email must not undo it", got, statusRejected)
	}

	entry := waitForAuditEntry(t, srv, ActionUserRejected)
	if entry.TargetID != email {
		t.Errorf("rejection row names target %q, want %q", entry.TargetID, email)
	}
	if !strings.Contains(entry.Details, "duplicate of an existing account") {
		t.Errorf("rejection row details %q do not carry the reason the admin typed; without it "+
			"the row records that somebody was rejected and not why", entry.Details)
	}

	// And the send failure is separately visible, through the funnel rather than a bespoke path.
	assertFailureRow(t, srv, email, "registration_rejected", "550 mailbox unavailable")
}

// TestSuccessfulRejectionEmailsTheApplicant is the other half of the pair. Without it, a handler
// that wrote the audit row and never sent anything satisfies the test above completely.
func TestSuccessfulRejectionEmailsTheApplicant(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "told@example.com"
	seedPendingRegistration(t, srv, email, "approval-token-2")
	m := installSender(t, srv, &mockMailSender{})

	rec := postRejection(t, srv, email, "approval-token-2", "", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("rejection returned %d, want 200", rec.Code)
	}

	waitForSentEmail(t, m)
	sent := m.getSentEmails()[0]
	if sent.To != email {
		t.Errorf("rejection email went to %q, want %q", sent.To, email)
	}
	// The next step is the whole reason for notifying rather than staying silent: a rejection
	// with nowhere to go leaves the person chasing the owner over Slack, which is what this
	// replaces. Asserted on the body an applicant actually reads, in both parts.
	both := sent.TextBody + sent.HtmlBody
	if !strings.Contains(both, "reply to this email") {
		t.Errorf("the rejection email offers no way back to a human: %q", both)
	}
	if !strings.Contains(strings.ToLower(both), "not been approved") {
		t.Errorf("the rejection email does not actually say the request was refused: %q", both)
	}

	// A successful send must not write a failure row. Bounded poll, not a single read: the row
	// would be written from a goroutine, so reading once observes not having waited.
	assertAuditRowCountStaysAt(t, srv, ActionNotifySendFailed, 0,
		"A rejection email that SENT wrote a send-failure row.")
	waitForAuditEntry(t, srv, ActionUserRejected)
}

// TestSilentRejectionSendsNothing is point 4: silence is available, as a deliberate choice.
//
// The assertion is on send ATTEMPTS rather than delivered emails. With a working sender the two
// agree, but attempts is the counter that cannot be satisfied by the wrong failure -- a mutant
// that always tried to send would be invisible to getSentEmails() the moment a test used a
// failing sender, which is exactly the gap #1732 documented.
func TestSilentRejectionSendsNothing(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "bad-actor@example.com"
	seedPendingRegistration(t, srv, email, "approval-token-3")
	m := installSender(t, srv, &mockMailSender{})

	rec := postRejection(t, srv, email, "approval-token-3", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("silent rejection returned %d, want 200", rec.Code)
	}

	if got := m.sendAttempts(); got != 0 {
		t.Errorf("%d send attempt(s) on a rejection the admin asked to make SILENTLY. The "+
			"opt-out exists for the rare bad actor who cleared the domain gate; emailing them "+
			"anyway is the one thing it must not do", got)
	}
	if got := mustGetUser(t, srv, email).Status; got != statusRejected {
		t.Errorf("user status is %q after a silent rejection, want %q", got, statusRejected)
	}

	// Silent to the applicant, never silent to the audit log.
	entry := waitForAuditEntry(t, srv, ActionUserRejected)
	if !strings.Contains(entry.Details, "no email was sent") {
		t.Errorf("audit row %q does not record that this rejection was deliberately silent; the "+
			"row is the only evidence a silent rejection leaves anywhere", entry.Details)
	}
}

// TestApprovalIsAlsoAudited pins the sibling gap this change closes.
//
// approveOnSSOSignIn has written "user.approved.sso" since #1824, on the grounds that pending ->
// approved is the most security-relevant transition a user can undergo and left no trace naming
// what did it. The admin link -- the path most registrations actually take -- wrote nothing at
// all. Adding the rejection row alone would have left the audit log able to answer "who was
// declined?" and not "who was let in?".
//
// Against the unfixed code this fails at waitForAuditEntry: no "user.approved" row is written.
func TestApprovalIsAlsoAudited(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "approved-by-link@example.com"
	seedPendingRegistration(t, srv, email, "approval-token-4")

	form := url.Values{}
	form.Set("email", email)
	form.Set("token", "approval-token-4")
	req := httptest.NewRequest(http.MethodPost, "/api/admin/approve", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleApproveUser(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("approval returned %d, want 200: %s", rec.Code, rec.Body.String())
	}

	entry := waitForAuditEntry(t, srv, ActionUserApproved)
	if entry.TargetID != email {
		t.Errorf("approval row names target %q, want %q", entry.TargetID, email)
	}
}

// TestRejectedUserCannotBeApprovedBySSO is the interaction that makes this change safe, and the
// reason the rejected row is kept rather than deleted.
//
// handleSSOCallback auto-approves any existing user who is not already approved -- deliberately,
// because the identity provider is the access decision. A rejected user reaching that branch
// would therefore have approved themselves by signing in, which is worse than having no rejection
// flow at all: the admin believes they declined somebody who now has access.
//
// This drives approveOnSSOSignIn, the function the callback actually calls; reaching the callback
// itself needs a full OIDC token exchange and a test that faked one would be asserting against
// the fake.
func TestRejectedUserCannotBeApprovedBySSO(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "rejected-then-sso@example.com"
	seedPendingRegistration(t, srv, email, "approval-token-5")
	postRejection(t, srv, email, "approval-token-5", "", true)

	user := mustGetUser(t, srv, email)
	if user.Status != statusRejected {
		t.Fatalf("fixture is wrong: user status is %q, want %q -- the SSO assertion below would "+
			"be exercising an ordinary pending user", user.Status, statusRejected)
	}

	err := srv.approveOnSSOSignIn(user, "liferay", nil)
	if !errors.Is(err, errSSORegistrationRejected) {
		t.Fatalf("approveOnSSOSignIn returned %v for a REJECTED user, want errSSORegistrationRejected. "+
			"The callback issues a portal session on a nil error, so this is the difference "+
			"between a rejection and a rejection the rejected person can undo by clicking "+
			"\"Sign in with Liferay\"", err)
	}

	// The status assertion is on the DATABASE, not the in-memory struct: a mutant that set
	// user.Status = "approved" and then returned the error would leave the caller holding an
	// approved user, and an in-memory check would still pass.
	if got := mustGetUser(t, srv, email).Status; got != statusRejected {
		t.Errorf("user status in the database is %q after an SSO sign-in attempt, want %q", got, statusRejected)
	}

	entry := waitForAuditEntry(t, srv, ActionSSODeniedRejected)
	if entry.TargetID != email {
		t.Errorf("SSO denial row names target %q, want %q", entry.TargetID, email)
	}
}

// TestSSOStillApprovesEveryOtherStatus is the discriminating half: a guard that refused everybody
// would satisfy the test above and quietly break the SSO auto-approval the owner confirmed is
// intended.
func TestSSOStillApprovesEveryOtherStatus(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "pending-then-sso@example.com"
	seedPendingRegistration(t, srv, email, "approval-token-6")

	user := mustGetUser(t, srv, email)
	if err := srv.approveOnSSOSignIn(user, "liferay", nil); err != nil {
		t.Fatalf("approveOnSSOSignIn returned %v for a PENDING user; SSO auto-approval is "+
			"deliberate and this change must not break it", err)
	}
	if got := mustGetUser(t, srv, email).Status; got != "approved" {
		t.Errorf("pending user is %q after SSO sign-in, want approved", got)
	}
}

// TestRejectedUserCannotBeApprovedByAStaleAdminLink covers the other live credential.
//
// The approval token is what authenticates the emailed link, and the SAME mail carries both
// decisions. If rejection left it armed, an admin who rejected and then scrolled back up in Slack
// could reverse their own decision with one click -- and so could anything else that reached that
// message. This is the stale-token defect #1824 found on the SSO path, in the place where
// following it silently overrides a decision.
func TestRejectedUserCannotBeApprovedByAStaleAdminLink(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "stale-link@example.com"
	const token = "approval-token-7"
	seedPendingRegistration(t, srv, email, token)
	installSender(t, srv, &mockMailSender{})

	postRejection(t, srv, email, token, "", true)

	form := url.Values{}
	form.Set("email", email)
	form.Set("token", token)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/approve", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleApproveUser(rec, req)

	if rec.Code != http.StatusGone {
		t.Errorf("the approve link returned %d after the registration was rejected, want 410 Gone", rec.Code)
	}
	if got := mustGetUser(t, srv, email).Status; got != statusRejected {
		t.Errorf("user status is %q after replaying the approve link, want %q", got, statusRejected)
	}
	// Asserted separately and on purpose. The status guard alone already refuses the replay
	// above, so that assertion would pass with the token still live -- it is satisfied by the
	// status change, not by the token being consumed. This is the assertion that fails if
	// rejection stops clearing it, which is what keeps the credential from outliving the
	// decision it authorised.
	if got := mustGetUser(t, srv, email).ApprovalToken; got != "" {
		t.Errorf("approval token is still %q after the rejection; the emailed link's entire "+
			"credential is meant to be consumed by the decision", got)
	}
}

// TestRejectedAddressCannotRegisterAgain states the consequence of keeping the row, so that it is
// a decision on the record rather than something a later reader discovers.
//
// Re-registration is refused exactly the way an existing account is refused -- a generic success
// response, because handleRegisterRequest must not confirm which addresses exist. The attempt is
// not lost: it gets its own audit action, which is where an admin sees a colleague trying again.
func TestRejectedAddressCannotRegisterAgain(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "tries-again@example.com"
	seedPendingRegistration(t, srv, email, "approval-token-8")
	installSender(t, srv, &mockMailSender{})
	postRejection(t, srv, email, "approval-token-8", "", true)

	body := fmt.Sprintf(`{"email":%q,"first_name":"Casey","last_name":"Colleague"}`, email)
	req := httptest.NewRequest(http.MethodPost, "/api/register-request", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.handleRegisterRequest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("re-registration returned %d, want the generic 200 that does not confirm the "+
			"address exists", rec.Code)
	}
	if got := mustGetUser(t, srv, email).Status; got != statusRejected {
		t.Errorf("user status is %q after re-registering, want %q -- a rejected row that is "+
			"reset by registering again is not a rejection", got, statusRejected)
	}
	entry := waitForAuditEntry(t, srv, "auth.registration_attempt_rejected")
	if entry.TargetID != email {
		t.Errorf("re-registration row names target %q, want %q", entry.TargetID, email)
	}
}

// TestRejectGetDoesNotReject is the #1143 property on the new link. These links are emailed to
// admin_notification_email, which on this deployment is a Slack email-to-channel bridge, so link
// previews, crawlers and prefetchers all fetch them. A GET that rejected would decline
// registrations nobody looked at.
func TestRejectGetDoesNotReject(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "prefetched@example.com"
	seedPendingRegistration(t, srv, email, "approval-token-9")

	req := httptest.NewRequest(http.MethodGet, "/api/admin/reject?email="+url.QueryEscape(email)+"&token=approval-token-9", nil)
	rec := httptest.NewRecorder()
	srv.handleRejectUser(rec, req)

	if got := mustGetUser(t, srv, email).Status; got != "pending" {
		t.Errorf("a GET changed the status to %q; only the POST may decide anything", got)
	}
	if !strings.Contains(rec.Body.String(), `<form method="POST"`) {
		t.Error("the GET should render a confirmation form that submits by POST")
	}
	// Bounded, for the same reason every absence assertion here is: reading the audit log once
	// immediately after the request observes not having waited, not the absence of a row.
	assertAuditRowCountStaysAt(t, srv, ActionUserRejected, 0,
		"A GET wrote a rejection row. Anything that merely fetched the URL would reject the user.")
}

// TestRejectionConfirmationEscapes -- the name and email come from an unverified registration and
// the token is the entire credential, so all three are attacker-influenced until the decision is
// made. Mirrors TestApprovalConfirmationEscapes on the approve half.
func TestRejectionConfirmationEscapes(t *testing.T) {
	s := &Server{}
	user := &db.User{
		FirstName: `<script>alert(1)</script>`,
		LastName:  `"onload="x`,
		Email:     `evil"@example.com`,
	}

	rec := httptest.NewRecorder()
	s.renderRejectionConfirmation(rec, user, user.Email, `tok"><script>`)
	body := rec.Body.String()

	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("registration name was rendered unescaped into the rejection confirmation")
	}
	if strings.Contains(body, `tok"><script>`) {
		t.Error("approval token was rendered unescaped, breaking out of the hidden field")
	}
	if strings.Contains(body, `action="/api/admin/reject?`) {
		t.Error("token must not be placed in the form action URL")
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Error("expected Referrer-Policy: no-referrer so the token is not leaked onward")
	}
	// The two admin choices #1830 asks for have to be reachable, or the flow silently only has
	// its default.
	if !strings.Contains(body, `name="reason"`) {
		t.Error("the confirmation offers no optional reason field")
	}
	if !strings.Contains(body, `name="silent"`) {
		t.Error("the confirmation offers no explicit \"reject silently\" choice, so silence " +
			"could only ever be the default -- the opposite of the decision")
	}
}

// TestRejectionReasonIsEscapedInTheEmail -- the reason is free text an admin types into a browser
// and it is interpolated into an HTML email.
func TestRejectionReasonIsEscapedInTheEmail(t *testing.T) {
	user := &db.User{Email: "x@example.com", FirstName: `<b>Name`}
	htmlBody, plainBody := rejectionEmailBodies(user, `<script>alert(1)</script>`)

	if strings.Contains(htmlBody, "<script>alert(1)</script>") {
		t.Errorf("the reason was interpolated unescaped into the HTML body: %q", htmlBody)
	}
	if strings.Contains(htmlBody, "<b>Name") {
		t.Errorf("the registration name was interpolated unescaped into the HTML body: %q", htmlBody)
	}
	if !strings.Contains(plainBody, "alert(1)") {
		t.Errorf("the plain-text body dropped the reason entirely: %q", plainBody)
	}
}

// TestAdminRegistrationEmailCarriesBothDecisions.
//
// The reject endpoint authenticates on the approval token and nothing else ever shows that token,
// so if the admin's registration email does not carry the link, the rejection flow exists in the
// code and is unreachable in practice.
//
// Both senders, because there are two: handleVerifyEmail and handleCompleteSetup each build this
// mail, from copies of the same code that had already drifted apart in every other respect. Fixing
// one and leaving the other is §5b's default failure mode, and the surviving one is the path a
// registration that completes its profile actually takes.
func TestAdminRegistrationEmailCarriesBothDecisions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		suffix  string
		trigger func(t *testing.T, srv *Server, verificationToken string)
	}{
		{
			name:   "handleVerifyEmail",
			suffix: "a",
			trigger: func(t *testing.T, srv *Server, verificationToken string) {
				req := httptest.NewRequest(http.MethodGet, "/api/verify-email?token="+verificationToken, nil)
				rec := httptest.NewRecorder()
				srv.handleVerifyEmail(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("verify-email returned %d, want 200: %s", rec.Code, rec.Body.String())
				}
			},
		},
		{
			name:   "handleCompleteSetup",
			suffix: "b",
			trigger: func(t *testing.T, srv *Server, verificationToken string) {
				body := fmt.Sprintf(`{"token":%q,"first_name":"Casey","last_name":"Colleague","policy_consent":true}`, verificationToken)
				req := httptest.NewRequest(http.MethodPost, "/api/complete-setup", strings.NewReader(body))
				rec := httptest.NewRecorder()
				srv.handleCompleteSetup(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("complete-setup returned %d, want 200: %s", rec.Code, rec.Body.String())
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := setupTestServerForAPI(t)
			srv.cfg.AdminNotificationEmail = "owner@example.com"
			email := "new-registration-" + tc.suffix + "@example.com"
			verificationToken := "verification-token-" + tc.suffix

			u := &db.User{
				ID:                email,
				Email:             email,
				FirstName:         "Casey",
				Role:              "user",
				Status:            "unverified",
				ApprovalToken:     "approval-token-" + tc.suffix,
				VerificationToken: verificationToken,
				AuthMethod:        "registration",
			}
			if err := srv.db.CreateUser(u); err != nil {
				t.Fatalf("seeding unverified registration: %v", err)
			}
			m := installSender(t, srv, &mockMailSender{})

			tc.trigger(t, srv, verificationToken)

			// Polled for the DECISION mail specifically, not for "an email was sent". The same
			// address also receives the routine "new registration" alert, and that one arrives
			// first: an earlier draft waited for one email, got the alert, and reported that the
			// approve link had been lost when it simply had not been sent yet. Both are async.
			adminMail := waitForAdminMailContaining(t, m, "owner@example.com", "/api/admin/approve?",
				"the admin registration email lost its approve link")

			// Both bodies, separately. Concatenating them first was a §5c miss caught by
			// mutation: removing the reject link from the HTML template left this test GREEN,
			// because the plain-text body still carried it. The two are built from different
			// sources -- one from templates/en/admin_registration_request.html, one from a
			// fmt.Sprintf in server.go -- so an assertion over their concatenation cannot tell
			// which of them dropped it.
			for _, part := range []struct{ name, body string }{
				{"first body (rendered HTML template)", adminMail.TextBody},
				{"second body (plain-text fallback)", adminMail.HtmlBody},
			} {
				if !strings.Contains(part.body, "/api/admin/reject?") {
					t.Errorf("the %s of the admin registration email carries no reject link, so "+
						"the rejection flow is unreachable from it: the endpoint authenticates "+
						"on the approval token and nothing else ever shows it. Body was: %q",
						part.name, part.body)
				}
			}
		})
	}
}

// waitForAdminMailContaining polls for a mail to one address whose bodies contain a marker.
// Bounded, so a mail that never arrives still fails, and fails saying which.
func waitForAdminMailContaining(t *testing.T, m *mockMailSender, to, marker, whyNot string) mockEmail {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var seen []string
		for _, e := range m.getSentEmails() {
			if e.To != to {
				continue
			}
			if strings.Contains(e.TextBody+e.HtmlBody, marker) {
				return e
			}
			seen = append(seen, e.TextBody+e.HtmlBody)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: no mail to %s contained %q within 5s. Mail that did arrive: %q",
				whyNot, to, marker, strings.Join(seen, " | "))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// --- the class guard ------------------------------------------------------------------------

// TestEveryUserStatusWriteIsAccountedFor answers the question §5b says belongs in the PR: how do
// I know there is not a second path by which a rejected user becomes approved?
//
// "I looked" is not an answer, so this is the answer: it parses every non-test Go file in
// pkg/server and enumerates every function that writes a user's Status field. Each one has to be
// listed below with the reason it cannot promote a rejected registration. A new path fails this
// test by name; so does a stale entry, which is what makes the list a ratchet rather than a
// suppression -- removing a status write without removing its entry goes red too.
//
// Deliberately matches ANY write to a user's Status, not just the literal "approved". The #1775
// lesson is that searching for the spelling the first instance happened to use is how the other
// instances survive: handleAdminUpdateUser assigns *req.Status, a variable, and a literal-only
// scan would have declared it absent.
func TestEveryUserStatusWriteIsAccountedFor(t *testing.T) {
	// Every entry is a function that writes db.User.Status, with why a rejected registration
	// cannot be promoted through it.
	accountedFor := map[string]string{
		"NewServer": "bootstraps the configured owner only, and only when that row does not exist; " +
			"a rejected applicant is not the owner",
		"handleRegisterRequest": "sets \"unverified\" on a NEW row, and returns early when any row " +
			"already exists -- which is exactly what a rejected row is",
		"handleCompleteSetup": "unverified -> pending, guarded on the current status being " +
			"\"unverified\"",
		"handleVerifyEmail": "unverified -> pending, guarded on the current status being " +
			"\"unverified\"",
		"handleApproveUser": "guarded on the current status being \"pending\" AND the approval " +
			"token matching; rejection clears that token and leaves the status \"rejected\"",
		"handleRejectUser": "the rejection itself; guarded on \"pending\"",
		"handleAdminVerify": "auto-creates the configured owner only, on redeeming a magic link " +
			"for an owner with no row; a rejected applicant is not the owner, and a rejected row " +
			"exists anyway",
		"handleAdminInviteUser": "creates an approved user only when no row exists for that " +
			"address -- a rejected row makes it 409",
		"handleAdminPatchUser": "the deliberate admin override (PATCH /api/admin/users), behind " +
			"an authenticated admin session. This is the intended way to reverse a rejection",
		"approveOnSSOSignIn": "refuses statusRejected outright and returns " +
			"errSSORegistrationRejected; see TestRejectedUserCannotBeApprovedBySSO",
		"handleSSOCallback": "creates an approved user only on db.ErrNotFound. This is precisely " +
			"why rejection KEEPS the row: deleting it would make a rejected person unknown here, " +
			"and SSO would create them approved on their next sign-in",
	}

	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving the package directory: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	found := map[string]bool{}
	var scanned int

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		scanned++

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.AssignStmt:
					for _, lhs := range node.Lhs {
						if writesUserStatusField(lhs) {
							found[fn.Name.Name] = true
						}
					}
				case *ast.CompositeLit:
					if !isUserCompositeLit(node) {
						return true
					}
					for _, elt := range node.Elts {
						kv, ok := elt.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Status" {
							found[fn.Name.Name] = true
						}
					}
				}
				return true
			})
		}
	}

	if scanned < 20 {
		t.Fatalf("only %d non-test Go files were parsed in %s; the scan is broken, so a green "+
			"result here means nothing was checked", scanned, dir)
	}
	// Anti-vacuity on the matcher itself, not just the walk. A matcher that recognised nothing
	// would report "no unaccounted paths" over an empty set, which is the §5c failure this whole
	// test exists to prevent.
	if len(found) < 8 {
		t.Fatalf("the scan found only %d function(s) writing a user's status (%v); the matcher is "+
			"broken -- there are at least 8 in this package", len(found), sortedKeys(found))
	}

	var unaccounted []string
	for fn := range found {
		if _, ok := accountedFor[fn]; !ok {
			unaccounted = append(unaccounted, fn)
		}
	}
	if len(unaccounted) > 0 {
		sort.Strings(unaccounted)
		t.Errorf("these functions write a user's status and are not accounted for: %s.\n\n"+
			"Each one is a candidate route by which a REJECTED registration could become "+
			"approved without an admin deciding so. Add it to accountedFor with the reason it "+
			"cannot, or guard it against statusRejected the way approveOnSSOSignIn is (#1830).",
			strings.Join(unaccounted, ", "))
	}

	var stale []string
	for fn := range accountedFor {
		if !found[fn] {
			stale = append(stale, fn)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("these entries name functions that no longer write a user's status: %s.\n\n"+
			"A list that tolerates its own staleness is a suppression wearing a ratchet's "+
			"clothes -- remove them, so the list can only shrink.", strings.Join(stale, ", "))
	}
}

// writesUserStatusField reports whether an assignment target is the Status field of something
// named like a user. AST alone has no types, so the receiver name is the discriminator; it is
// what keeps edge-host and lease Status writes out of the set.
func writesUserStatusField(lhs ast.Expr) bool {
	sel, ok := lhs.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Status" {
		return false
	}
	base, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	name := strings.ToLower(base.Name)
	return name == "u" || strings.Contains(name, "user")
}

// isUserCompositeLit reports whether a composite literal constructs a db.User.
func isUserCompositeLit(lit *ast.CompositeLit) bool {
	switch t := lit.Type.(type) {
	case *ast.SelectorExpr:
		return t.Sel.Name == "User"
	case *ast.Ident:
		return t.Name == "User"
	}
	return false
}
