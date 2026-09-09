package server

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

// A failed notification email used to leave no trace at all.
//
// Eleven call sites fired mail as `go func() { _ = s.notifications.Sender().Send(...) }()`, and
// four more logged the failure at INFO -- invisible to `journalctl -p err`, which is why an
// error-level search of the whole production deployment came back clean while approval emails had
// never once arrived (#1824). #1831 and #1834 fixed four of them by hand.
//
// This file asserts the two properties that make the remaining twelve, and the thirteenth nobody
// has written yet, impossible to get wrong:
//
//  1. Every notification send in the repository goes through one funnel
//     (TestEveryNotificationSendGoesThroughTheFunnel), so the behaviour is defined once.
//  2. The funnel records a failure where an owner can find it, and does NOT record one when the
//     mail actually went (#1732).
//
// Property 2 is asserted through the real call sites rather than by calling the funnel directly:
// a test of the funnel alone passes just as happily when a call site has quietly stopped using it.

// auditSettleWindow is how long an "and no more rows than this appeared" assertion watches for.
//
// Every assertion in this file that a row is ABSENT, or that there is exactly one of them, has to
// be a bounded poll rather than a single read. writeAudit deliberately writes in a bare goroutine
// with no WaitGroup or flush to await (server_audit.go), so reading once immediately after the
// call under test does not observe the absence of a row -- it observes not having waited for it.
//
// This is not theoretical: an earlier draft of TestAReachedAddressResetsItsStreak read once, and
// a mutant that never reset a streak -- so it escalated an address that had been successfully
// reached in between -- PASSED. The row it wrote was still in flight. A single read is the §5c
// mistake in its purest form: the assertion was satisfied by the harness being early rather than
// by the product being right.
//
// Two seconds because the write is a single local SQLite insert; the window only has to outlast
// scheduling, not the database.
const auditSettleWindow = 2 * time.Second

// assertAuditRowCountStaysAt polls for auditSettleWindow and requires that exactly want rows with
// this action exist for the whole of it -- failing the moment an extra one shows up, and again at
// the end if too few ever did.
func assertAuditRowCountStaysAt(t *testing.T, srv *Server, action string, want int, why string) {
	t.Helper()
	deadline := time.Now().Add(auditSettleWindow)
	for {
		entries, err := srv.db.ListAuditEntries(db.AuditFilter{Action: action})
		if err != nil {
			t.Fatalf("reading audit entries: %v", err)
		}
		if len(entries) > want {
			t.Fatalf("%d %q rows, want %d. %s\nFirst row: %q",
				len(entries), action, want, why, entries[0].Details)
		}
		if time.Now().After(deadline) {
			if len(entries) != want {
				t.Fatalf("%d %q rows after %s, want %d. %s", len(entries), action, auditSettleWindow, want, why)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitForSendThenAssertNoFailureRow is the discriminating half of every pair below.
//
// Without it, a funnel that wrote the failure row unconditionally -- or one that had stopped
// sending altogether and only ever wrote failure rows -- satisfies the failure assertions
// completely. It waits for evidence the send actually happened before concluding that no failure
// row exists, so it cannot pass merely by reading before the send ran either.
func waitForSendThenAssertNoFailureRow(t *testing.T, srv *Server, m *mockMailSender) {
	t.Helper()
	waitForSentEmail(t, m)

	assertAuditRowCountStaysAt(t, srv, ActionNotifySendFailed, 0,
		"A SUCCESSFUL send wrote a failure row. The row has to mean the mail failed, or it means "+
			"nothing and an owner reading these learns nothing.")
}

// installSender points the server's notifications at m and fails loudly if that did not take --
// a nil sender makes every call site skip its send at the nil check, and every assertion about a
// missing failure row would then pass for entirely the wrong reason.
func installSender(t *testing.T, srv *Server, m *mockMailSender) *mockMailSender {
	t.Helper()
	srv.notifications = NewNotificationService(m, srv.db, srv.cfg)
	if srv.notifications.Sender() == nil {
		t.Fatal("fixture did not install a sender -- every call site's nil check would skip the " +
			"send and the assertions below would pass without exercising anything")
	}
	return m
}

func failingSender(t *testing.T, srv *Server, err error) *mockMailSender {
	t.Helper()
	m := &mockMailSender{}
	m.failSends(err)
	return installSender(t, srv, m)
}

func seedUser(t *testing.T, srv *Server, email string) *db.User {
	t.Helper()
	u := &db.User{ID: email, Email: email, FirstName: "Test", Role: "user", Status: "approved"}
	if err := srv.db.CreateUser(u); err != nil {
		t.Fatalf("seeding user %s: %v", email, err)
	}
	return u
}

// assertFailureRow waits for the single canonical failure row and checks it names the three
// things an operator needs: who could not be reached, which notification it was, and why.
//
// Asserting all three rather than "a row exists" is deliberate. "A row exists" is satisfied by
// any row the funnel might write for any reason, including one written on the success path.
func assertFailureRow(t *testing.T, srv *Server, recipient, kind, causeFragment string) *db.AuditEntry {
	t.Helper()
	entry := waitForAuditEntry(t, srv, ActionNotifySendFailed)
	if entry.TargetID != recipient {
		t.Errorf("failure row names target %q, want %q -- an owner filters these by address",
			entry.TargetID, recipient)
	}
	if !strings.Contains(entry.Details, kind) {
		t.Errorf("failure row details %q do not say which notification it was (want %q); with one "+
			"canonical action, the kind is the only thing distinguishing a lost magic link from a "+
			"lost marketing-ish notice", entry.Details, kind)
	}
	if !strings.Contains(entry.Details, causeFragment) {
		t.Errorf("failure row details %q do not name the underlying cause (want %q); a row saying "+
			"only \"it failed\" is satisfied by every failure mode there is",
			entry.Details, causeFragment)
	}
	return entry
}

// --- api.go: the subdomain notices (three sites, one shape) ------------------------------------

// TestSubdomainNoticeSendFailureIsRecorded covers pkg/server/api.go, where three of the seven
// discarded sends lived. Against the unfixed code this fails at waitForAuditEntry: the send was
// `go func() { _ = ...Send(...) }()` and produced nothing at all.
func TestSubdomainNoticeSendFailureIsRecorded(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "reserver@example.com"
	user := seedUser(t, srv, email)
	failingSender(t, srv, errors.New("smtp: 550 mailbox unavailable"))

	srv.sendSubdomainReservedEmail(user, "demo", "example.com", nil, nil)

	assertFailureRow(t, srv, email, "subdomain_reserved", "550 mailbox unavailable")
}

func TestSubdomainNoticeSuccessWritesNoFailureRow(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "reserver-ok@example.com"
	user := seedUser(t, srv, email)
	m := installSender(t, srv, &mockMailSender{})

	srv.sendSubdomainReservedEmail(user, "demo", "example.com", nil, nil)

	waitForSendThenAssertNoFailureRow(t, srv, m)
}

// --- server_domain.go: the custom-domain hook failure notice ------------------------------------

// TestVanityDomainNoticeSendFailureIsRecorded covers pkg/server/server_domain.go. This one is the
// sharpest of the set: the email exists to tell a user their custom domain may have no
// certificate, so losing it silently leaves them with a broken domain and no explanation, and the
// admin alert that accompanies it goes out over the same SMTP connection that just failed.
func TestVanityDomainNoticeSendFailureIsRecorded(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "domain-owner@example.com"
	seedUser(t, srv, email)
	failingSender(t, srv, errors.New("smtp: connection refused"))

	srv.alertVanityDomainHookFailure("add", "custom.example.net", email, errors.New("dns hook exploded"))

	assertFailureRow(t, srv, email, "vanity_domain_hook_failed", "connection refused")
}

// --- policy_consent_sweep.go: the subject of #1732 itself ---------------------------------------

// TestPolicyReminderSendFailureIsRecorded is the notification #1732 was actually raised about.
//
// #1724 made a failed reminder release its claim so the next sweep retries it, which fixed the
// "recorded as warned having never been warned" lie. What it left was an hourly retry that fails
// hourly and silently: the only trace was one gateway log line per user per hour, and an operator
// had no way to answer "is anybody about to be cut off without notice?" short of grepping.
//
// Against the unfixed code this fails at waitForAuditEntry -- the send returned its error to a
// caller that logged it and wrote nothing durable anywhere.
func TestPolicyReminderSendFailureIsRecorded(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "cli-only@example.com"
	user := seedUser(t, srv, email)
	failingSender(t, srv, errors.New("smtp: 421 service not available"))

	state := ConsentState{Required: true, Deadline: "2026-10-01", SecondsRemaining: 3 * 24 * 3600}
	if err := srv.sendPolicyConsentWarningEmail(user, state, nil); err == nil {
		t.Fatal("sendPolicyConsentWarningEmail returned nil for a failing sender. The caller " +
			"releases its warning claim on this error, so swallowing it re-creates #1724: the " +
			"user is recorded as warned and no later sweep retries them")
	}

	assertFailureRow(t, srv, email, "policy_reminder", "421 service not available")
}

func TestPolicyReminderSuccessWritesNoFailureRow(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "cli-only-ok@example.com"
	user := seedUser(t, srv, email)
	m := installSender(t, srv, &mockMailSender{})

	state := ConsentState{Required: true, Deadline: "2026-10-01", SecondsRemaining: 3 * 24 * 3600}
	if err := srv.sendPolicyConsentWarningEmail(user, state, nil); err != nil {
		t.Fatalf("sendPolicyConsentWarningEmail returned %v for a sender that succeeds; the "+
			"caller would release a claim it should have kept", err)
	}

	waitForSendThenAssertNoFailureRow(t, srv, m)
}

// --- the "repeated" half of #1732 ---------------------------------------------------------------

// TestRepeatedFailuresToOneAddressEscalateOnce is what makes a permanently-unreachable address
// visible as such rather than as a stream of indistinguishable rows.
//
// #1732 rejected the cheap answer -- fire an admin alert per failure -- for two reasons, and both
// are asserted here rather than described: the escalation writes a row and sends no mail (the
// broken channel cannot report on itself), and it fires once per streak rather than once per
// attempt (a gateway-wide outage during a policy rollout would otherwise produce a row per user
// per hour, indefinitely).
func TestRepeatedFailuresToOneAddressEscalateOnce(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "departed@example.com"
	user := seedUser(t, srv, email)
	failingSender(t, srv, errors.New("smtp: 550 no such user here"))

	// Below the threshold there must be no escalation: a single deferral is routine, and an
	// owner paged for those stops reading them.
	for i := 0; i < notifyRepeatThreshold-1; i++ {
		if err := srv.sendNotification(notifyPolicyReminder, user.Email, "s", "b", "p"); err == nil {
			t.Fatal("sendNotification returned nil from a sender that always fails")
		}
	}
	assertAuditRowCountStaysAt(t, srv, ActionNotifyRepeatedFailures, 0, fmt.Sprintf(
		"Escalated after %d failures, below the threshold of %d. An owner who is told about every "+
			"transient deferral stops reading the ones that matter.",
		notifyRepeatThreshold-1, notifyRepeatThreshold))

	// Crossing the threshold escalates, once...
	for i := 0; i < 4; i++ {
		_ = srv.sendNotification(notifyPolicyReminder, user.Email, "s", "b", "p") //nolint:errcheck
	}

	entry := waitForAuditEntry(t, srv, ActionNotifyRepeatedFailures)
	if entry.TargetID != email {
		t.Errorf("escalation row names %q, want %q", entry.TargetID, email)
	}
	if !strings.Contains(entry.Details, fmt.Sprintf("%d consecutive", notifyRepeatThreshold)) {
		t.Errorf("escalation row details %q do not say how many times this address has failed; "+
			"\"N times\" is the whole difference between this row and the per-send ones",
			entry.Details)
	}

	// ...and only once, however long the address stays broken.
	assertAuditRowCountStaysAt(t, srv, ActionNotifyRepeatedFailures, 1, fmt.Sprintf(
		"After %d consecutive failures there must be exactly one escalation row. One row per "+
			"attempt is the un-deduplicated shape #1732 rejected by name -- an SMTP outage during "+
			"a policy rollout would bury the owner in them.", notifyRepeatThreshold+3))
}

// TestAReachedAddressResetsItsStreak pins that the count is CONSECUTIVE. Cumulative counting
// would eventually escalate every address that has ever seen a transient deferral, which is the
// same as escalating none of them.
func TestAReachedAddressResetsItsStreak(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "flaky@example.com"
	user := seedUser(t, srv, email)
	m := failingSender(t, srv, errors.New("smtp: 451 try again"))

	for i := 0; i < notifyRepeatThreshold-1; i++ {
		_ = srv.sendNotification(notifyPolicyReminder, user.Email, "s", "b", "p") //nolint:errcheck
	}

	m.failSends(nil) // the address is reachable again
	if err := srv.sendNotification(notifyPolicyReminder, user.Email, "s", "b", "p"); err != nil {
		t.Fatalf("send failed after the sender recovered: %v", err)
	}

	m.failSends(errors.New("smtp: 451 try again"))
	for i := 0; i < notifyRepeatThreshold-1; i++ {
		_ = srv.sendNotification(notifyPolicyReminder, user.Email, "s", "b", "p") //nolint:errcheck
	}

	assertAuditRowCountStaysAt(t, srv, ActionNotifyRepeatedFailures, 0, fmt.Sprintf(
		"Escalated for an address that was successfully reached in between. %d failures either "+
			"side of a success is not %d consecutive failures, and counting it that way escalates "+
			"every address on the system eventually.",
		notifyRepeatThreshold-1, notifyRepeatThreshold))
}

// TestEscalationSendsNoMail is reason 1 from #1732 asserted rather than commented.
//
// "The channel being reported as broken is the channel doing the reporting": when SMTP is down
// globally, every alert about a failed send is itself a failed send, so the case where an owner
// most needs to hear about it is precisely the case where email cannot tell them.
func TestEscalationSendsNoMail(t *testing.T) {
	srv := setupTestServerForAPI(t)
	const email = "unreachable@example.com"
	seedUser(t, srv, email)
	m := failingSender(t, srv, errors.New("smtp: connection refused"))

	const sends = notifyRepeatThreshold + 2
	for i := 0; i < sends; i++ {
		_ = srv.sendNotification(notifyPolicyReminder, email, "s", "b", "p") //nolint:errcheck
	}
	waitForAuditEntry(t, srv, ActionNotifyRepeatedFailures)

	// ATTEMPTS, not delivered emails. Every send in this test fails, so getSentEmails() is empty
	// whether or not the escalation tried to send -- an assertion on it passes for a mutant that
	// mails the owner on every crossing. Verified: adding such a send to recordNotificationFailure
	// left the getSentEmails() version of this test green.
	if got := m.sendAttempts(); got != sends {
		t.Errorf("%d Send attempts for %d notifications: the escalation path tried to send %d "+
			"email(s) of its own. Reporting a broken mail channel over that same channel is the "+
			"mechanism #1732 rejected by name -- when SMTP is down globally, the alert about the "+
			"failed send is itself a failed send", got, sends, got-sends)
	}
}

// --- the class guard ----------------------------------------------------------------------------

// TestEveryNotificationSendGoesThroughTheFunnel is the durable half of this change.
//
// The behaviour tests above cover the sites that exist today. This one covers the site nobody has
// written yet: it parses every Go file in the repository and fails if a four-argument Send call --
// the signature of mail.Sender.Send -- appears anywhere except the funnel.
//
// Parsed, not grepped, for two §5c reasons. A grep for `Sender().Send(` self-matches its own
// source and the prose in notify.go, and it matched a commented-out copy of the defect in api.go
// that had been sitting there dead. The AST sees neither comments nor strings, and it also
// catches the spelling the grep would have missed entirely: notifications.go called `n.sender.Send`,
// not `Sender().Send`, and it was the last INFO-only failure path in the package.
func TestEveryNotificationSendGoesThroughTheFunnel(t *testing.T) {
	const funnel = "pkg/server/notify.go"

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("%s is not the repository root (no go.mod): %v -- the walk below would scan "+
			"nothing and report a clean pass over it", root, err)
	}

	// pkg/mail is where mail.Sender is declared and implemented, so its own Send calls are the
	// thing being funnelled, not a bypass of it. Stated here as an exclusion of exactly one
	// directory rather than as prose about what this test "does not look at": a gate whose scope
	// is only described in a comment cannot fail when the scope is wrong.
	excludedDirs := map[string]bool{
		filepath.Join(root, "pkg", "mail"): true,
	}

	var scanned int
	var offenders []string

	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := info.Name()
			if base == ".git" || base == "node_modules" || base == "vendor" || excludedDirs[path] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		scanned++

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Send" || len(call.Args) != 4 {
				return true
			}
			if rel == funnel {
				return true
			}
			offenders = append(offenders, rel)
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking the repository: %v", walkErr)
	}

	// A derivation that silently produced nothing would report a clean pass over an empty set,
	// which is the failure mode this whole file exists to prevent. 50 is well under the real
	// count and well over anything a broken walk would reach.
	if scanned < 50 {
		t.Fatalf("only %d Go files were parsed; the walk is broken, so a green result here means "+
			"nothing was checked", scanned)
	}
	if len(offenders) != 0 {
		t.Errorf("notification email is sent outside %s, at: %s.\n\n"+
			"Every send has to go through Server.sendNotification / sendNotificationAsync. The "+
			"behaviour a bypass loses is not cosmetic: eleven sites discarded the error outright "+
			"and four logged it at INFO, so a failed send left nothing in `journalctl -p err` and "+
			"nothing in the audit log, and registration-approval emails went missing for weeks "+
			"before anyone noticed (#1824, #1732).",
			funnel, strings.Join(offenders, ", "))
	}
}
