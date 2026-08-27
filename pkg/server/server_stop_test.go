package server

import (
	"strings"
	"testing"
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
