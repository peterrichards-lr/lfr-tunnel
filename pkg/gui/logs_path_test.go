package gui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The logs view must serve the CURRENT log, not the legacy one (#2071).
//
// handleApiLogs built `~/.lfr-tunnel/client-<sub>.log` itself. The client writes to
// `~/.lfr-tunnel/logs/` now, so the view served a file frozen at whenever the layout changed --
// observed on a live machine as three lines from 16 July, including a bind error that had since
// been fixed and could not recur. It read as a regression for weeks.
//
// The Inspector serves the same page and resolves through client.ResolveClientLogPath, which
// prefers the current path and falls back to legacy only when the current file is absent. Two
// handlers, one page: they must agree.

func writeLogsFixture(t *testing.T, sub, current, legacy string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dir := filepath.Join(home, ".lfr-tunnel")
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o700); err != nil {
		t.Fatalf("creating dirs: %v", err)
	}
	// A config naming the subdomain, so the handler gets past its own lookup.
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"),
		// Unquoted scalar on purpose: concatenating a value into a DOUBLE-quoted YAML scalar
		// is what yaml_guard_test.go forbids, because a Windows temp path turns \U into an
		// invalid escape and the file fails to parse before the key under test is reached
		// (#1775, #1773, #2029). A subdomain would not trip it, but the shape is the hazard.
		[]byte("subdomain: "+sub+"\n"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	if legacy != "" {
		if err := os.WriteFile(filepath.Join(dir, "client-"+sub+".log"), []byte(legacy), 0o600); err != nil {
			t.Fatalf("writing legacy log: %v", err)
		}
	}
	if current != "" {
		if err := os.WriteFile(filepath.Join(dir, "logs", "client-"+sub+".log"), []byte(current), 0o600); err != nil {
			t.Fatalf("writing current log: %v", err)
		}
	}
}

// TestTheLogsViewServesTheCurrentLogNotTheLegacyOne is the reported defect.
func TestTheLogsViewServesTheCurrentLogNotTheLegacyOne(t *testing.T) {
	writeLogsFixture(t, "demo",
		"CURRENT: written by the running client",
		"LEGACY: frozen when the log layout changed")

	s := NewTempSettingsServer(0)
	rec := httptest.NewRecorder()
	s.handleApiLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/logs returned %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	if strings.Contains(body, "LEGACY") {
		t.Errorf("the logs view served the LEGACY file while a current one exists -- this is how "+
			"a July log kept being shown through several releases that fixed what it reported.\nGot: %s", body)
	}
	if !strings.Contains(body, "CURRENT") {
		t.Errorf("the logs view did not serve the current log.\nGot: %s", body)
	}
}

// An install that predates the move has only the legacy file, and must still show its history --
// otherwise the fix trades one empty view for another.
func TestTheLogsViewFallsBackToLegacyWhenThereIsNoCurrentLog(t *testing.T) {
	writeLogsFixture(t, "demo", "", "LEGACY: all this install has ever had")

	s := NewTempSettingsServer(0)
	rec := httptest.NewRecorder()
	s.handleApiLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/logs returned %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "LEGACY") {
		t.Errorf("an install with only a legacy log got nothing: %s", rec.Body.String())
	}
}

// A log from a PREVIOUS run must not be served as if it were this session's (#2071).
//
// Only the background-mode console log lives at that path; a foreground session writes to the
// terminal and never creates one. Serving the old file is worse than serving nothing, because it
// reads as current -- which is exactly how a July bind error appeared to be recurring weeks after
// it was fixed.
func TestALogFromAPreviousRunIsNotServedAsThisSessions(t *testing.T) {
	writeLogsFixture(t, "demo", "STALE: written by a background run in July", "")

	// Backdate the log, then claim a session that started after it -- using this process's own
	// pid so client.IsPIDRunning agrees the session is live. Without a live pid the running
	// branch is unreachable and the test would pass for the wrong reason.
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".lfr-tunnel")
	logFile := filepath.Join(dir, "logs", "client-demo.log")
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(logFile, old, old); err != nil {
		t.Fatalf("backdating the log: %v", err)
	}

	pid := os.Getpid()
	started := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(dir, "lfr-tunnel-demo.pid"),
		[]byte(fmt.Sprintf("%d\n", pid)), 0o600); err != nil {
		t.Fatalf("writing pid: %v", err)
	}
	state := fmt.Sprintf(`{"pid":%d,"subdomain":"demo","start_time":%q,"inspector_port":4040}`, pid, started)
	if err := os.WriteFile(filepath.Join(dir, "lfr-tunnel-demo.state"), []byte(state), 0o600); err != nil {
		t.Fatalf("writing state: %v", err)
	}

	s := NewTempSettingsServer(0)
	rec := httptest.NewRecorder()
	s.handleApiLogs(rec, httptest.NewRequest(http.MethodGet, "/api/logs", nil))

	if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "STALE") {
		t.Error("a log written before this session started was served as if it were current -- " +
			"that is how a fixed bug appears to be recurring")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 with an explanation, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "background mode") {
		t.Errorf("the refusal does not explain WHY there is no log, so the user cannot act on "+
			"it: %s", rec.Body.String())
	}
}
