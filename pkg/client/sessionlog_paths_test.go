package client

import (
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

// TestSessionLogPathsNeverCarriesContents is the guard that matters.
//
// #1423 was decided as option 3 -- report the paths, serve no contents -- because the
// traffic log records request and response bodies when logBodies is on, and those
// routinely carry OAuth tokens, session cookies and customer data, while the Inspector
// binds 0.0.0.0 under Docker. On disk those bytes sit behind 0600; over HTTP they would
// not.
//
// The realistic regression is someone later adding a convenience endpoint without
// re-reading that decision. This asserts the reporting type has no field capable of
// carrying a payload, so widening it has to be deliberate.
func TestSessionLogPathsNeverCarriesContents(t *testing.T) {
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
				t.Fatalf("%s entry leaked log contents: %q", p.Kind, field)
			}
		}
		if !p.Exists {
			t.Errorf("%s written to disk but reported missing", p.Kind)
		}
	}
}
