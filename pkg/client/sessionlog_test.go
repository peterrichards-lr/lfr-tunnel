package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open %s: %v", path, err)
	}
	defer f.Close() //nolint:errcheck

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("failed to scan %s: %v", path, err)
	}
	return lines
}

// TestRotatingFilePreservesPreviousRun is the core of #1128: the old background log was
// opened O_TRUNC, so restarting destroyed the log of the run that had just ended.
func TestRotatingFilePreservesPreviousRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.log")

	first, err := OpenRotatingFile(path, DefaultLogMaxBytes, 3)
	if err != nil {
		t.Fatalf("open failed: %v", err)
	}
	if _, err := first.Write([]byte("run one\n")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	second, err := OpenRotatingFile(path, DefaultLogMaxBytes, 3)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	defer second.Close() //nolint:errcheck
	if _, err := second.Write([]byte("run two\n")); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	if got := readLines(t, path); len(got) != 1 || got[0] != "run two" {
		t.Errorf("expected the live file to hold only the new run, got %v", got)
	}
	if got := readLines(t, path+".1"); len(got) != 1 || got[0] != "run one" {
		t.Errorf("expected the previous run to be preserved in generation 1, got %v", got)
	}
}

// TestRotatingFileDoesNotRollWhenEmpty guards against a start/stop cycle pushing real
// content out through empty generations.
func TestRotatingFileDoesNotRollWhenEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.log")

	f, err := OpenRotatingFile(path, DefaultLogMaxBytes, 2)
	if err != nil {
		t.Fatalf("open failed: %v", err)
	}
	if _, err := f.Write([]byte("real content\n")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	_ = f.Close() //nolint:errcheck

	// Two starts that write nothing at all.
	for i := 0; i < 2; i++ {
		empty, err := OpenRotatingFile(path, DefaultLogMaxBytes, 2)
		if err != nil {
			t.Fatalf("open failed: %v", err)
		}
		_ = empty.Close() //nolint:errcheck
	}

	if got := readLines(t, path+".1"); len(got) != 1 || got[0] != "real content" {
		t.Errorf("empty runs should not displace real content, generation 1 holds %v", got)
	}
}

// TestRotateFileKeepsPlainOsFileUsable covers the background-mode console log. That
// handle is assigned to exec.Cmd.Stdout, and os/exec only passes a file descriptor
// straight through to the child when it is an *os.File -- any other io.Writer is wrapped
// in a pipe serviced by a goroutine in the parent, which for a detached background run
// exits immediately and takes the child's stdout with it. So rotation must be available
// without forcing the caller to hold a RotatingFile.
func TestRotateFileKeepsPlainOsFileUsable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.log")

	if err := os.WriteFile(path, []byte("previous run\n"), 0600); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if err := RotateFile(path, 3); err != nil {
		t.Fatalf("RotateFile failed: %v", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatalf("open failed: %v", err)
	}
	if _, err := f.WriteString("current run\n"); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	_ = f.Close() //nolint:errcheck

	if got := readLines(t, path); len(got) != 1 || got[0] != "current run" {
		t.Errorf("expected a fresh file for the new run, got %v", got)
	}
	if got := readLines(t, path+".1"); len(got) != 1 || got[0] != "previous run" {
		t.Errorf("expected the previous run preserved, got %v", got)
	}
}

// TestRotateFileNoopsOnMissingOrEmpty makes sure a first-ever run, or one that produced
// no output, does not create empty generations.
func TestRotateFileNoopsOnMissingOrEmpty(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "never-written.log")
	if err := RotateFile(missing, 3); err != nil {
		t.Errorf("rotating a missing file should be a no-op, got %v", err)
	}
	if _, err := os.Stat(missing + ".1"); !os.IsNotExist(err) {
		t.Errorf("rotating a missing file must not create a generation")
	}

	empty := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if err := RotateFile(empty, 3); err != nil {
		t.Errorf("rotating an empty file should be a no-op, got %v", err)
	}
	if _, err := os.Stat(empty + ".1"); !os.IsNotExist(err) {
		t.Errorf("rotating an empty file must not create a generation")
	}
}

// TestRotatingFileRotatesAtSizeCap covers the in-flight rotation path and pruning of the
// oldest generation.
func TestRotatingFileRotatesAtSizeCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.log")

	// Cap small enough that each write after the first forces a rotation.
	f, err := OpenRotatingFile(path, 16, 2)
	if err != nil {
		t.Fatalf("open failed: %v", err)
	}
	defer f.Close() //nolint:errcheck

	for i := 0; i < 4; i++ {
		if _, err := fmt.Fprintf(f, "entry-%d\n", i); err != nil {
			t.Fatalf("write %d failed: %v", i, err)
		}
	}

	if got := readLines(t, path); len(got) == 0 {
		t.Errorf("expected the live file to hold the most recent entry")
	}
	// generations=2 means .1 and .2 exist, and .3 must have been pruned.
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Errorf("expected generation 3 to be pruned beyond the configured limit")
	}
}

func TestSessionLoggerWritesJSONL(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewSessionLogger(dir, "alpha-se", false)
	if err != nil {
		t.Fatalf("NewSessionLogger failed: %v", err)
	}
	defer logger.Close() //nolint:errcheck

	logger.Traffic(&RequestRecord{
		Time:       time.Now().UTC(),
		Method:     "GET",
		Path:       "/web/guest",
		Status:     200,
		DurationMs: 17,
		TargetPort: 8080,
		ReqBody:    "secret-request",
		RespBody:   "secret-response",
	}, "apac")
	logger.Event("warn", "failover_started", map[string]any{"failed_region": "apac"})

	trafficLines := readLines(t, filepath.Join(dir, "traffic-alpha-se.log"))
	if len(trafficLines) != 1 {
		t.Fatalf("expected one traffic line, got %d", len(trafficLines))
	}
	var entry TrafficEntry
	if err := json.Unmarshal([]byte(trafficLines[0]), &entry); err != nil {
		t.Fatalf("traffic line is not valid JSON: %v", err)
	}
	if entry.Method != "GET" || entry.Status != 200 || entry.Region != "apac" || entry.TargetPort != 8080 {
		t.Errorf("traffic entry did not round-trip: %+v", entry)
	}

	// Bodies must not reach disk unless explicitly enabled -- they routinely carry
	// tokens and customer data.
	if entry.ReqBody != "" || entry.RespBody != "" {
		t.Errorf("bodies must be omitted by default, got req=%q resp=%q", entry.ReqBody, entry.RespBody)
	}
	if strings.Contains(trafficLines[0], "secret-") {
		t.Errorf("body content leaked into the default traffic log: %s", trafficLines[0])
	}

	eventLines := readLines(t, filepath.Join(dir, "error-alpha-se.log"))
	if len(eventLines) != 1 {
		t.Fatalf("expected one event line, got %d", len(eventLines))
	}
	var event EventEntry
	if err := json.Unmarshal([]byte(eventLines[0]), &event); err != nil {
		t.Fatalf("event line is not valid JSON: %v", err)
	}
	if event.Level != "warn" || event.Event != "failover_started" {
		t.Errorf("event did not round-trip: %+v", event)
	}
	if event.Fields["failed_region"] != "apac" {
		t.Errorf("expected the failed region in the event fields, got %v", event.Fields)
	}
}

func TestSessionLoggerBodiesOptIn(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewSessionLogger(dir, "alpha-se", true)
	if err != nil {
		t.Fatalf("NewSessionLogger failed: %v", err)
	}
	defer logger.Close() //nolint:errcheck

	logger.Traffic(&RequestRecord{Method: "POST", Path: "/webhook", ReqBody: "payload-here"}, "eu")

	var entry TrafficEntry
	lines := readLines(t, filepath.Join(dir, "traffic-alpha-se.log"))
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if entry.ReqBody != "payload-here" {
		t.Errorf("expected the body to be recorded when opted in, got %q", entry.ReqBody)
	}
}

func TestSessionLoggerTruncatesLargeBodies(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewSessionLogger(dir, "alpha-se", true)
	if err != nil {
		t.Fatalf("NewSessionLogger failed: %v", err)
	}
	defer logger.Close() //nolint:errcheck

	logger.Traffic(&RequestRecord{ReqBody: strings.Repeat("x", maxLoggedBodyBytes*2)}, "eu")

	var entry TrafficEntry
	lines := readLines(t, filepath.Join(dir, "traffic-alpha-se.log"))
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if len(entry.ReqBody) != maxLoggedBodyBytes {
		t.Errorf("expected the body capped at %d bytes, got %d", maxLoggedBodyBytes, len(entry.ReqBody))
	}
}

// TestNilSessionLoggerIsSafe covers the documented "nil discards" contract, which is
// what keeps the tunnel running when the log files can't be opened.
func TestNilSessionLoggerIsSafe(t *testing.T) {
	var logger *SessionLogger
	logger.Traffic(&RequestRecord{Method: "GET"}, "eu")
	logger.Event("error", "boom", nil)
	if err := logger.Close(); err != nil {
		t.Errorf("closing a nil logger should be a no-op, got %v", err)
	}

	engine := NewInterceptorEngine("127.0.0.1", nil)
	engine.AddRecord(&RequestRecord{Method: "GET"}) // no logger attached
	engine.LogEvent("error", "boom", nil)
}

// TestSessionLoggerConcurrentWrites exercises the writer under -race, since traffic is
// logged from every proxied request while events come from the failover path.
func TestSessionLoggerConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewSessionLogger(dir, "alpha-se", false)
	if err != nil {
		t.Fatalf("NewSessionLogger failed: %v", err)
	}
	defer logger.Close() //nolint:errcheck

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			logger.Traffic(&RequestRecord{Method: "GET", Path: fmt.Sprintf("/p/%d", n), Status: 200}, "eu")
		}(i)
		go func(n int) {
			defer wg.Done()
			logger.Event("info", "tick", map[string]any{"n": n})
		}(i)
	}
	wg.Wait()

	if got := len(readLines(t, filepath.Join(dir, "traffic-alpha-se.log"))); got != 20 {
		t.Errorf("expected 20 traffic lines, got %d", got)
	}
	if got := len(readLines(t, filepath.Join(dir, "error-alpha-se.log"))); got != 20 {
		t.Errorf("expected 20 event lines, got %d", got)
	}
}

// TestEngineAddRecordPersists checks the engine writes through to the traffic log while
// still populating the in-memory ring the Inspector serves.
func TestEngineAddRecordPersists(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewSessionLogger(dir, "alpha-se", false)
	if err != nil {
		t.Fatalf("NewSessionLogger failed: %v", err)
	}
	defer logger.Close() //nolint:errcheck

	engine := NewInterceptorEngine("127.0.0.1", nil)
	engine.SetSessionLogger(logger)
	engine.SetRegionEndpoint("apac", "https://edge.example", nil)

	engine.AddRecord(&RequestRecord{Method: "GET", Path: "/x", Status: 204})

	engine.mu.RLock()
	historyLen := len(engine.History)
	engine.mu.RUnlock()
	if historyLen != 1 {
		t.Errorf("expected the in-memory history to still be populated, got %d entries", historyLen)
	}

	lines := readLines(t, filepath.Join(dir, "traffic-alpha-se.log"))
	if len(lines) != 1 {
		t.Fatalf("expected the record to be persisted, got %d lines", len(lines))
	}
	var entry TrafficEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if entry.Region != "apac" {
		t.Errorf("expected the engine's current region to be stamped on the entry, got %q", entry.Region)
	}
}

// TestTruncateBodyCutsOnRuneBoundary is the regression test for the byte-wise cut noted
// in #1137. Bodies are arbitrary UTF-8, and slicing at a fixed byte offset can land
// mid-character; json.Marshal then rewrites the orphaned bytes to U+FFFD, so the logged
// body ends in a corrupted glyph rather than simply stopping short.
func TestTruncateBodyCutsOnRuneBoundary(t *testing.T) {
	// Three-byte runes do not divide into the cap, so a byte-wise cut is guaranteed to
	// land inside the rune straddling the boundary.
	body := strings.Repeat("　", maxLoggedBodyBytes)

	got := truncateBody(body)

	if len(got) > maxLoggedBodyBytes {
		t.Errorf("truncated body is %d bytes, over the %d cap", len(got), maxLoggedBodyBytes)
	}
	if !utf8.ValidString(got) {
		t.Error("truncated body is not valid UTF-8 -- the cut landed inside a character")
	}
	if len(got) < maxLoggedBodyBytes-3 {
		t.Errorf("expected the cut to give up at most one rune, got %d bytes", len(got))
	}

	// A body already inside the cap is returned untouched, multi-byte or not.
	short := "héllo ✅"
	if got := truncateBody(short); got != short {
		t.Errorf("expected a short body to pass through unchanged, got %q", got)
	}
}

// The log directory is configurable (#1223). These cover the resolution rules without
// touching the disk; LogDir itself only adds MkdirAll on top.

func TestResolveLogDir_DefaultsToTheHomeDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot resolve home directory in this environment")
	}

	got, err := resolveLogDir("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(home, ".lfr-tunnel", "logs")
	if got != want {
		t.Errorf("expected the default %q, got %q", want, got)
	}
}

func TestResolveLogDir_UsesAnAbsolutePathAsGiven(t *testing.T) {
	dir := t.TempDir()
	got, err := resolveLogDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != dir {
		t.Errorf("expected %q, got %q", dir, got)
	}
}

// A value people type by hand, so a leading ~ has to work.
func TestResolveLogDir_ExpandsLeadingTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot resolve home directory in this environment")
	}

	got, err := resolveLogDir("~/tunnel-logs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(home, "tunnel-logs")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

// A tilde anywhere but the front is an ordinary character in a path, not a home reference.
func TestExpandHome_OnlyTreatsALeadingTildeSpecially(t *testing.T) {
	home := "/home/someone"
	cases := map[string]string{
		"~":              home,
		"~/logs":         filepath.Join(home, "logs"),
		"/var/log/~keep": "/var/log/~keep",
		"/var/log":       "/var/log",
		"relative/path":  "relative/path",
	}
	for in, want := range cases {
		if got := expandHome(in, home); got != want {
			t.Errorf("expandHome(%q) = %q, want %q", in, got, want)
		}
	}
}

// SetLogDir is process-wide state, so it must be readable back and clearable -- the
// Inspector's log reader resolves through the same value as the writer.
func TestSetLogDir_RoundTripsAndClears(t *testing.T) {
	t.Cleanup(func() { SetLogDir("") })

	SetLogDir("  /var/log/lfr  ")
	if got := ConfiguredLogDir(); got != "/var/log/lfr" {
		t.Errorf("expected the value trimmed and stored, got %q", got)
	}

	SetLogDir("")
	if got := ConfiguredLogDir(); got != "" {
		t.Errorf("expected empty to restore the default, got %q", got)
	}
}

// The whole point of holding this process-wide: the reader must follow the writer, or the
// Inspector shows an empty default directory while logs pile up elsewhere.
func TestLogDir_ReaderFollowsTheConfiguredWriter(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() { SetLogDir("") })
	SetLogDir(dir)

	effective, err := LogDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if effective != dir {
		t.Fatalf("expected LogDir to honour the configured directory, got %q", effective)
	}

	logPath, err := ClientLogPath("demo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Dir(logPath) != dir {
		t.Errorf("expected the console log path to sit under %q, got %q", dir, logPath)
	}
}
