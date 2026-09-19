package gui

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// The settings server used to bind a fixed 55556 and swallow the failure (#2055).
//
// Three consequences, all from one cause: a second tray instance killed the settings UI with
// `bind: address already in use`; the error vanished into a goroutine log while the object still
// reported itself started; and the menu went on linking to the port regardless. The Inspector had
// already solved this -- SetInspectorPort records what it ACTUALLY bound -- and this brings the
// tray's server into line.

// TestSettingsServerBindsAnotherPortWhenItsPreferredOneIsTaken is the regression.
//
// Occupying the preferred port is the whole point: against the old code the server logged a
// failure, left s.server non-nil, and there was no way to ask whether it was serving. Here the
// assertion is not "it started" but "something answers on the port it reports" -- a server that
// claims a port nothing serves is the exact defect, so believing Port() alone would reproduce it.
func TestSettingsServerBindsAnotherPortWhenItsPreferredOneIsTaken(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not occupy a port to test against: %v", err)
	}
	defer func() {
		// Handled rather than discarded: errcheck runs with check-blank: true here, so
		// `_ = x()` is flagged exactly like an unchecked call, and a //nolint would push the
		// suppression ratchet over its ceiling. Logged, not failed -- a close error on a
		// fixture listener says nothing about the subject under test.
		if err := blocker.Close(); err != nil {
			t.Logf("closing the blocker listener: %v", err)
		}
	}()
	taken := blocker.Addr().(*net.TCPAddr).Port

	s := NewTempSettingsServer(taken)
	if err := s.Start(); err != nil {
		t.Fatalf("Start returned %v; it should fall back to an OS-assigned port, not give up", err)
	}
	defer s.Stop()

	if !s.IsRunning() {
		t.Fatal("IsRunning() is false after a successful Start")
	}
	got := s.Port()
	if got == 0 {
		t.Fatal("Port() is 0 after a successful Start")
	}
	if got == taken {
		t.Fatalf("Port() reports %d, which is the port already held by the blocker -- "+
			"it cannot be serving there", got)
	}

	// The assertion that matters: the reported port actually serves the settings page. Against
	// the old code nothing was listening anywhere, so this is what goes red.
	resp, err := (&http.Client{Timeout: 3 * time.Second}).
		Get(fmt.Sprintf("http://127.0.0.1:%d/settings", got))
	if err != nil {
		t.Fatalf("nothing is serving on the port Port() reported (%d): %v", got, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Logf("closing the response body: %v", err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /settings on the reported port %d returned %d, want 200", got, resp.StatusCode)
	}
}

// TestSettingsServerReportsNotRunningBeforeStartAndAfterStop keeps the menu's gate honest: it
// disables "Settings..." on exactly this signal, so a stale true would put the dead link back.
func TestSettingsServerReportsNotRunningBeforeStartAndAfterStop(t *testing.T) {
	s := NewTempSettingsServer(0)

	if s.IsRunning() || s.Port() != 0 {
		t.Fatalf("before Start: IsRunning=%v Port=%d, want false/0", s.IsRunning(), s.Port())
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !s.IsRunning() {
		t.Fatal("after Start: IsRunning() is false")
	}

	s.Stop()
	if s.IsRunning() || s.Port() != 0 {
		t.Errorf("after Stop: IsRunning=%v Port=%d, want false/0", s.IsRunning(), s.Port())
	}
}
