package server

// goTracked runs fn on its own goroutine, counted by bgWG so Stop waits for it to return
// before closing the database.
//
// The defect this exists to prevent (#1833): a handler spawning `go s.doSomething()` where
// doSomething touches s.db. Stop cancels the context, waits on bgWG -- which knows nothing
// about that goroutine -- and closes the database while the goroutine is still inside
// database/sql. Two things follow from that, and the second is the one that got noticed:
//
//   - the write is lost. writeAudit's goroutine explicitly swallows "database is closed",
//     so an audit row simply never appears and nothing says so;
//   - the SQLite file stays open. The pool is capped at one connection (pkg/db.Open), and
//     sql.DB.Close does not wait for a connection that is checked out -- it closes it when
//     it is handed back. On Unix that is invisible, because an open file can still be
//     unlinked. Windows refuses, so `t.TempDir()` cleanup fails and the test is marked FAIL
//     after its body has already passed. That is how this surfaced: on windows-latest, in
//     TestServer_HandleAdminRetryVanityDomain, which passed on a re-run.
//
// Measured before the fix, driving that handler 30 times: 25 runs still had a goroutine
// inside modernc.org/sqlite when Stop returned, and 8 lost the audit row.
//
// After stop has begun, fn is dropped rather than started. Two reasons, in order: adding to
// a WaitGroup from zero while another goroutine is inside Wait is a documented panic, and
// the database is closing anyway, so the work could only fail. Every caller here is
// best-effort background work whose failure is already logged and never surfaced to a
// request.
func (s *Server) goTracked(fn func()) {
	s.bgMu.Lock()
	if s.bgStopping {
		s.bgMu.Unlock()
		return
	}
	s.bgWG.Add(1)
	s.bgMu.Unlock()

	go func() {
		defer s.bgWG.Done()
		fn()
	}()
}

// beginStopping closes goTracked to new work, so that bgWG's counter can only fall from
// here on and Wait cannot race an Add. Called by stop before it waits.
func (s *Server) beginStopping() {
	s.bgMu.Lock()
	s.bgStopping = true
	s.bgMu.Unlock()
}
