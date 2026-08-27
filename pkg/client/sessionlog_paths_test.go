package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSessionLogPathsReportsAllThree is the reader half of #1423: #1129 added the traffic
// and error logs and nothing ever told the operator they existed.
//
// Paths are compared against the same helpers the writers use rather than hardcoded
// strings, so moving the log directory moves the writer and this reporter together
// instead of leaving the panel pointing at where logs used to be.
func TestSessionLogPathsReportsAllThree(t *testing.T) {
	dir := t.TempDir()
	SetLogDir(dir)
	t.Cleanup(func() { SetLogDir("") })

	paths := SessionLogPaths("alpha-se")
	if len(paths) != 3 {
		t.Fatalf("expected 3 log paths, got %d: %+v", len(paths), paths)
	}

	byKind := map[string]SessionLogPath{}
	for _, p := range paths {
		byKind[p.Kind] = p
	}
	for _, kind := range []string{"console", "traffic", "error"} {
		if _, ok := byKind[kind]; !ok {
			t.Fatalf("no %q entry in %+v", kind, paths)
		}
	}

	wantConsole, err := ResolveClientLogPath("alpha-se")
	if err != nil {
		t.Fatalf("ResolveClientLogPath: %v", err)
	}
	if got := byKind["console"].Path; got != wantConsole {
		t.Errorf("console path = %q, want %q", got, wantConsole)
	}
	for _, kind := range []string{"traffic", "error"} {
		want := filepath.Join(dir, fmt.Sprintf("%s-alpha-se.log", kind))
		if got := byKind[kind].Path; got != want {
			t.Errorf("%s path = %q, want %q", kind, got, want)
		}
	}
}

// A log that has not been written yet must still be listed. "There is no traffic log yet"
// answers "where are my logs?" as often as a path does, and hiding the row recreates the
// gap #1423 closes.
func TestSessionLogPathsMarksMissingFilesRatherThanOmittingThem(t *testing.T) {
	dir := t.TempDir()
	SetLogDir(dir)
	t.Cleanup(func() { SetLogDir("") })

	for _, p := range SessionLogPaths("alpha-se") {
		if p.Exists {
			t.Errorf("%s reported as existing in an empty directory", p.Kind)
		}
		if p.Size != 0 {
			t.Errorf("%s reported size %d with no file present", p.Kind, p.Size)
		}
	}

	trafficPath := filepath.Join(dir, "traffic-alpha-se.log")
	if err := os.WriteFile(trafficPath, []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var traffic SessionLogPath
	for _, p := range SessionLogPaths("alpha-se") {
		if p.Kind == "traffic" {
			traffic = p
		}
	}
	if !traffic.Exists {
		t.Error("traffic log exists on disk but was reported missing")
	}
	if traffic.Size != 6 {
		t.Errorf("traffic size = %d, want 6", traffic.Size)
	}
}

// No subdomain means no session, so there is nothing to report -- and guessing a filename
// would put a path on screen that nothing will ever write.
func TestSessionLogPathsEmptyWithoutASubdomain(t *testing.T) {
	if got := SessionLogPaths(""); got != nil {
		t.Errorf("expected nil for an empty subdomain, got %+v", got)
	}
}

// The reporting type carries locations, not contents -- the viewers at
// /api/logs/traffic and /api/logs/error serve the bytes, and this response is polled
// alongside the config panel where a log payload has no business appearing.
//
// This replaces a test that asserted contents must NEVER be served anywhere, which
// encoded a rationale #1423 later withdrew: /api/state already returns ReqBody,
// RespBody, ReqHeaders and the passcode for the last 100 requests through the same
// origin check, so withholding the traffic log protected nothing.
func TestSessionLogPathsReportsLocationsNotContents(t *testing.T) {
	dir := t.TempDir()
	SetLogDir(dir)
	t.Cleanup(func() { SetLogDir("") })

	const secret = "Authorization: Bearer sk-live-not-a-real-token"
	for _, name := range []string{"traffic-alpha-se.log", "error-alpha-se.log", "client-alpha-se.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(secret+"\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	for _, p := range SessionLogPaths("alpha-se") {
		for _, field := range []string{p.Kind, p.Path} {
			if strings.Contains(field, secret) {
				t.Fatalf("%s entry carried log contents: %q", p.Kind, field)
			}
		}
		if !p.Exists {
			t.Errorf("%s written to disk but reported missing", p.Kind)
		}
	}
}

// ReadSessionLog is what the traffic and error viewers serve (#1423).
func TestReadSessionLogReturnsEachLog(t *testing.T) {
	dir := t.TempDir()
	SetLogDir(dir)
	t.Cleanup(func() { SetLogDir("") })

	for kind, body := range map[string]string{
		"traffic": `{"ts":"2026-08-27T00:00:00Z","method":"GET","path":"/a"}` + "\n",
		"error":   `{"ts":"2026-08-27T00:00:00Z","level":"warn","event":"x"}` + "\n",
		"console": "2026/08/27 client: started\n",
	} {
		name := fmt.Sprintf("%s-alpha-se.log", kind)
		if kind == "console" {
			name = "client-alpha-se.log"
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	for _, kind := range []string{"traffic", "error", "console"} {
		data, truncated, err := ReadSessionLog(kind, "alpha-se", DefaultLogTailBytes)
		if err != nil {
			t.Fatalf("ReadSessionLog(%s): %v", kind, err)
		}
		if truncated {
			t.Errorf("%s reported truncated for a one-line file", kind)
		}
		if len(data) == 0 {
			t.Errorf("%s returned no data", kind)
		}
	}
}

// A tail must not begin mid-object, or the first line of a JSON Lines log fails to parse
// in the viewer. Splitting on the first newline after the offset is what prevents that.
func TestReadSessionLogTailStartsOnALineBoundary(t *testing.T) {
	dir := t.TempDir()
	SetLogDir(dir)
	t.Cleanup(func() { SetLogDir("") })

	var buf strings.Builder
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&buf, `{"ts":"2026-08-27T00:00:00Z","n":%d,"pad":"%s"}`+"\n", i, strings.Repeat("x", 200))
	}
	path := filepath.Join(dir, "traffic-alpha-se.log")
	if err := os.WriteFile(path, []byte(buf.String()), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	data, truncated, err := ReadSessionLog("traffic", "alpha-se", 4096)
	if err != nil {
		t.Fatalf("ReadSessionLog: %v", err)
	}
	if !truncated {
		t.Fatal("expected truncated=true for a file well over the limit")
	}
	if int64(len(data)) > 4096 {
		t.Errorf("returned %d bytes, more than the 4096 requested", len(data))
	}
	for i, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line %d of the tail is not valid JSON (%v): %q", i, err, line)
		}
	}
}

func TestReadSessionLogRejectsUnknownKind(t *testing.T) {
	if _, _, err := ReadSessionLog("../../etc/passwd", "alpha-se", 0); err == nil {
		t.Error("expected an error for an unknown log kind")
	}
}
