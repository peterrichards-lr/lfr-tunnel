package server

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"

	"lfr-tunnel/pkg/db"
)

// A test server that is never stopped keeps its SQLite file open.
//
// On Unix that is invisible: an open file can still be unlinked, so t.TempDir() cleans up and
// nobody notices. Windows refuses to delete a file another process holds, so the cleanup fails
// and the test is marked FAIL *after its body has passed* --
//
//	TempDir RemoveAll cleanup: unlinkat ...\api_test.db:
//	The process cannot access the file because it is being used by another process.
//
// Six tests failed that way on windows-latest while passing everywhere else. The platform-aware
// CI matrix (#1446) runs Windows only when the diff could behave differently there, so a Go-only
// change does not surface it -- it lands on master and shows up later on somebody else's PR.
//
// setupTestServerForAPI now stops the server itself, so the leak cannot be reintroduced by
// forgetting a defer. These are the two properties that fix depends on.

// TestServerStopClosesDatabase — Stop must actually close the handle, not just cancel the
// context. This is the property Windows was enforcing.
func TestServerStopClosesDatabase(t *testing.T) {
	srv := setupTestServerForAPI(t)

	if _, err := srv.db.GetUser("nobody@example.com"); err != nil && strings.Contains(err.Error(), "closed") {
		t.Fatalf("the database should still be open before Stop, got: %v", err)
	}

	srv.Stop()

	_, err := srv.db.GetUser("nobody@example.com")
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("after Stop the database handle must be closed, so the file can be removed on "+
			"Windows; got err=%v", err)
	}
}

// TestServerStopIsIdempotent — the helper registers a t.Cleanup Stop, and tests written before
// that still carry their own `defer srv.Stop()`. Both run. A second pass must not panic, and must
// not try to record a clean shutdown into a database it has already closed.
func TestServerStopIsIdempotent(t *testing.T) {
	srv := setupTestServerForAPI(t)

	srv.Stop()
	srv.Stop()
	srv.Stop()
}

// TestSetupHelperStopsTheServer — the fix itself: a caller that never calls Stop still gets a
// closed database, because the helper owns the lifecycle.
//
// Uses a subtest so the helper's cleanup has actually run by the time the assertion happens;
// t.Cleanup fires at the end of the test that registered it.
func TestSetupHelperStopsTheServer(t *testing.T) {
	var srv *Server
	t.Run("inner", func(t *testing.T) {
		srv = setupTestServerForAPI(t)
		// Deliberately no Stop -- this is the mistake 7 existing tests had made.
	})

	if _, err := srv.db.GetUser("nobody@example.com"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("the setup helper must stop the server even when the test never does, or the "+
			"SQLite file stays open and TempDir cleanup fails on Windows; got err=%v", err)
	}
}

// goroutinesInsideDatabase maps goroutine id -> stack, for every goroutine currently executing
// inside this repository's database layer.
//
// Frames from lfr-tunnel/pkg/db specifically, not database/sql: the pool runs a connectionOpener
// goroutine of its own that appears in every dump and belongs to nobody's leak. Filtering on
// database/sql caught that one, and would have made this test fail for a reason that has nothing
// to do with what it is named after.
func goroutinesInsideDatabase() map[string]string {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	out := map[string]string{}
	for _, g := range strings.Split(string(buf), "\n\n") {
		if !strings.Contains(g, "lfr-tunnel/pkg/db.") {
			continue
		}
		id, _, _ := strings.Cut(strings.TrimPrefix(strings.SplitN(g, "\n", 2)[0], "goroutine "), " ")
		out[id] = g
	}
	return out
}

// TestStopLeavesNothingInsideTheDatabase is the property #1833 needed and that
// TestServerStopClosesDatabase above could not give: not just that Stop closes the handle, but
// that nothing is still *using* it when it does.
//
// The failure it guards is not the assertion the symptom suggests. On windows-latest the visible
// failure was t.TempDir's RemoveAll refusing to unlink api_test.db -- but what held that file was
// a goroutine the handler had spawned and nobody was waiting for, and the unlink only fails on
// Windows. Asserting on the unlink would test the platform. Asserting that Stop returns with
// nothing inside pkg/db tests the cause, everywhere.
//
// Driven through the real handler rather than by calling writeAudit directly, because the two
// goroutines that leaked were spawned by production code, and a test that spawns its own
// goroutine is a test of itself (github-workflow §5c.4).
//
// Twenty requests, fired together rather than one after another, because both weaker shapes let
// the unfixed code pass: one request leaked 25 times in 30, and twenty sequential ones leaked
// *less* often, not more, because each request's background work drains while the next does its
// own reads. Fired together against a pool of one connection the backlog at Stop is the whole
// batch, and this fails 10 times in 10 against the code as it was.
//
// What it is NOT, measured rather than assumed: a per-site guard. Reverting only writeAudit to a
// bare `go` leaves this test green 10 times in 10 -- Stop now waits on the *other* half of the
// fix, and that wait is exactly the time the untracked write needed to finish. A guard whose
// sensitivity depends on how long Stop happens to take cannot be the thing standing between the
// next handler and this bug. That job belongs to the static gate in
// background_db_goroutines_test.go, which names the file and line and does not care about
// timing; this test is the proof that the property holds in a running server, and the two are
// only a guard together.
func TestStopLeavesNothingInsideTheDatabase(t *testing.T) {
	srv := setupTestServerForAPI(t)
	dbPath := srv.cfg.DBPath

	if err := srv.db.StartVanityDomainAttempt("retry-me.com", "user@example.com"); err != nil {
		t.Fatalf("seeding the tracked domain: %v", err)
	}
	sessionToken := newAdminSession(t, srv, "admin@example.com")

	// Anything already inside the database layer when this test starts belongs to somebody
	// else -- only goroutines that appear between here and Stop are this test's subject.
	before := goroutinesInsideDatabase()

	const retries = 20
	codes := make([]int, retries)
	var handlers sync.WaitGroup
	for i := 0; i < retries; i++ {
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			req := adminRequest(http.MethodPost,
				"http://example.com/api/admin/vanity-domain-status/retry-me.com/retry", nil, sessionToken)
			w := httptest.NewRecorder()
			srv.handleAdminRetryVanityDomain(w, req, "admin@example.com")
			codes[i] = w.Code
		}()
	}
	// Wait for the handlers themselves -- not for what they spawned, which is the subject.
	handlers.Wait()
	for i, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("retry %d: expected 200 OK, got %d", i, code)
		}
	}

	srv.Stop()

	for id, stack := range goroutinesInsideDatabase() {
		if _, existed := before[id]; existed {
			continue
		}
		t.Errorf("Stop returned while a goroutine was still inside the database, so the SQLite "+
			"file is still open -- on Windows t.TempDir then cannot remove it, which is #1833. "+
			"Bring it under bgWG (s.goTracked). Goroutine %s:\n%s", id, stack)
	}

	// The other half of the same fact, and the half that shows what the leak costs even where
	// the unlink succeeds: work issued before Stop must survive it. Read through a fresh handle
	// on the same file, the server's own being closed by now.
	reopened, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("reopening %s after Stop: %v", dbPath, err)
	}
	defer reopened.Close() //nolint:errcheck

	entries, err := reopened.ListAuditEntries(db.AuditFilter{Action: "vanity_domain.retry", Limit: 200})
	if err != nil {
		t.Fatalf("reading the audit log back: %v", err)
	}
	if len(entries) != retries {
		t.Errorf("%d of %d vanity_domain.retry audit rows survived Stop. writeAudit's goroutine "+
			"was still queued when the database closed, and it swallows \"database is closed\" -- "+
			"so the rows an operator would go looking for simply never appeared, silently.",
			len(entries), retries)
	}
}
