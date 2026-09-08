package server

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// waitForSentEmail waits for the asynchronous send to actually happen. Both sends are fired in a
// goroutine on purpose -- a blocking send would turn SMTP latency into a timing oracle for
// whether an address exists -- so reading immediately races the write rather than testing it.
func waitForSentEmail(t *testing.T, m *mockMailSender) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if len(m.getSentEmails()) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no email was sent within 5s -- the success path did not send, so the " +
				"absence of a failure row below proves nothing")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A magic-link or invite email that fails is a lockout, not an inconvenience: the magic link is
// the primary way anyone signs into the portal, and an invite is the only thing that tells a
// newly-created user their account exists.
//
// Both sends used to discard the error -- the magic link as a bare
// `go s.notifications.Sender().Send(...)`, not even assigned to _ -- so a failure produced no log
// line, no audit row, and the endpoint had already answered {"status":"ok"}. Nothing anywhere
// distinguished "we emailed you" from "we tried and it failed" (#1832).
//
// This is the same class as #1824, on the recovery route from it: the two users identified there
// were told to sign in with a magic link, which is this path.

// withFailingSender points the server's notifications at a sender that always errors, and returns
// it so a test can assert on it.
func withFailingSender(t *testing.T, srv *Server, err error) *mockMailSender {
	t.Helper()
	m := &mockMailSender{}
	m.failSends(err)
	srv.notifications = NewNotificationService(m, srv.db, srv.cfg)
	if srv.notifications.Sender() == nil {
		t.Fatal("fixture did not install a sender -- the handler's nil check would skip the " +
			"send entirely and every assertion below would pass for the wrong reason")
	}
	return m
}

// TestMagicLinkSendFailureIsRecorded is the half that matters most: a user who cannot log in must
// leave a trace somewhere.
func TestMagicLinkSendFailureIsRecorded(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "locked-out@example.com"
	if err := srv.db.CreateUser(&db.User{
		ID: email, Email: email, Role: "user", Status: "approved", AuthMethod: "registration",
	}); err != nil {
		t.Fatalf("seeding approved user: %v", err)
	}
	withFailingSender(t, srv, errors.New("smtp: connection refused"))

	req := httptest.NewRequest("POST", "http://example.com/api/admin/magic-link",
		strings.NewReader(`{"email":"`+email+`"}`))
	req.RemoteAddr = "127.0.0.1:5555"
	w := httptest.NewRecorder()

	srv.handleAdminMagicLink(w, req)

	// The response must NOT change. handleAdminMagicLink answers identically for an unknown
	// address, an out-of-domain address and a real one -- that is what stops it enumerating
	// accounts. Reporting the failure to an anonymous caller would undo it. This is the
	// deliberate asymmetry with #1824, where telling the ADMIN was the fix.
	if w.Code != 200 {
		t.Errorf("status %d, want 200: a send failure must not change what an anonymous caller "+
			"sees, or the endpoint becomes an account oracle", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "ok") {
		t.Errorf("body %q no longer reports ok; the response must be indistinguishable from the "+
			"success case", body)
	}

	// ...but it must be recorded where an operator can find it.
	entry := waitForAuditEntry(t, srv, "user.magic_link.notify_failed")
	if entry.TargetID != email {
		t.Errorf("audit row names %q, want %q", entry.TargetID, email)
	}
	if !strings.Contains(entry.Details, "connection refused") {
		t.Errorf("audit row details %q do not name the underlying cause; a row that says only "+
			"\"it failed\" is satisfied by every failure mode there is", entry.Details)
	}
}

// TestMagicLinkSuccessWritesNoFailureRow is the discriminating half. Without it, a handler that
// wrote the failure row unconditionally -- or one that had stopped sending altogether -- would
// satisfy the test above.
func TestMagicLinkSuccessWritesNoFailureRow(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "can-log-in@example.com"
	if err := srv.db.CreateUser(&db.User{
		ID: email, Email: email, Role: "user", Status: "approved", AuthMethod: "registration",
	}); err != nil {
		t.Fatalf("seeding approved user: %v", err)
	}
	m := &mockMailSender{} // succeeds
	srv.notifications = NewNotificationService(m, srv.db, srv.cfg)

	req := httptest.NewRequest("POST", "http://example.com/api/admin/magic-link",
		strings.NewReader(`{"email":"`+email+`"}`))
	req.RemoteAddr = "127.0.0.1:5555"
	srv.handleAdminMagicLink(httptest.NewRecorder(), req)

	// The send is asynchronous, so wait for evidence it happened at all before concluding that
	// no failure row exists -- otherwise this passes simply by reading too early.
	waitForSentEmail(t, m)

	entries, err := srv.db.ListAuditEntries(db.AuditFilter{Action: "user.magic_link.notify_failed"})
	if err != nil {
		t.Fatalf("reading audit entries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a successful send still wrote %d user.magic_link.notify_failed row(s); the row "+
			"must mean the mail failed, or it means nothing", len(entries))
	}
}
