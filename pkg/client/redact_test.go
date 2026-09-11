package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #1696 asked for this specifically: "redact before upload, and verify it ... this needs a test,
// not an assumption". So these seed real secrets into real log files and assert they do not come
// out the other side -- not that a redactor was called.

const (
	seededPAT    = "lfr_pat_s3cr3tv4lu3thatmustnotsurvive"
	seededSecret = "hunter2correcthorsebattery"
)

// writeLogs plants the three logs with a secret in every field that could carry one.
func writeLogs(t *testing.T, sub string) string {
	t.Helper()
	dir := t.TempDir()

	traffic := []TrafficEntry{
		{
			Time: time.Now(), Method: "POST", Path: "/login?token=" + seededPAT,
			Status: 200, DurationMs: 12, TargetPort: 8080,
			// The developer's own application payload. Stripped, not redacted (#1885).
			ReqBody:  `{"username":"alice","password":"` + seededSecret + `"}`,
			RespBody: `{"session":"` + seededPAT + `"}`,
		},
		{Time: time.Now(), Method: "GET", Path: "/health", Status: 200, DurationMs: 1, TargetPort: 8080},
	}
	var tb strings.Builder
	for _, e := range traffic {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshalling a traffic entry: %v", err)
		}
		tb.Write(raw)
		tb.WriteByte('\n')
	}
	// A line that is not valid JSON at all, which the traffic pass must drop rather than pass
	// through -- it cannot be shown to contain no body.
	tb.WriteString(`{"ts":"broken","req_body":"` + seededSecret + "\n")

	events := []EventEntry{
		{Time: time.Now(), Level: "warn", Event: "auth_failed", Fields: map[string]any{
			"token":    seededPAT,
			"password": seededSecret,
			"note":     "retried with " + seededPAT,
			"port":     8080,
			"nested":   map[string]any{"api_key": seededSecret},
		}},
	}
	var eb strings.Builder
	for _, e := range events {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshalling an event entry: %v", err)
		}
		eb.Write(raw)
		eb.WriteByte('\n')
	}

	console := strings.Join([]string{
		"2026-09-11 starting client",
		"2026-09-11 using token " + seededPAT,
		"2026-09-11 GET https://user:" + seededSecret + "@example.com/thing",
		"2026-09-11 Authorization: Bearer " + seededPAT,
		"2026-09-11 connected",
	}, "\n") + "\n"

	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	write(fmt.Sprintf("traffic-%s.log", sub), tb.String())
	write(fmt.Sprintf("error-%s.log", sub), eb.String())
	write(fmt.Sprintf("client-%s.log", sub), console)
	return dir
}

// The headline assertion. If this ever fails, an upload is shipping a credential.
func TestNoSeededSecretSurvivesCollection(t *testing.T) {
	dir := writeLogs(t, "demo")

	logs, err := CollectRedactedLogs(dir, "demo", DefaultCollectionMaxBytes)
	if err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if len(logs) != 3 {
		t.Fatalf("collected %d log(s), want 3 -- a log that is not read cannot be asserted about", len(logs))
	}

	for _, l := range logs {
		content := string(l.Content)
		if strings.Contains(content, seededPAT) {
			t.Errorf("the seeded PAT survived redaction in the %s log:\n%s", l.Kind, content)
		}
		if strings.Contains(content, seededSecret) {
			t.Errorf("the seeded password survived redaction in the %s log:\n%s", l.Kind, content)
		}
	}
}

// PREMISE. Without this, the assertion above passes trivially if collection returns nothing at
// all, or if the fixture never contained the secret in the first place.
func TestTheFixtureActuallyContainsTheSecrets(t *testing.T) {
	dir := writeLogs(t, "demo")
	for _, name := range []string{"traffic-demo.log", "error-demo.log", "client-demo.log"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if !strings.Contains(string(raw), seededPAT) && !strings.Contains(string(raw), seededSecret) {
			t.Errorf("%s contains neither seeded secret, so redacting it proves nothing", name)
		}
	}
}

// CONTROL. The suite above must be capable of failing. Redaction removed, the secrets must come
// straight through -- otherwise the assertions are passing on a fixture that never had them.
func TestControlUnredactedContentStillContainsTheSecrets(t *testing.T) {
	dir := writeLogs(t, "demo")
	raw, err := os.ReadFile(filepath.Join(dir, "client-demo.log"))
	if err != nil {
		t.Fatalf("reading the console log: %v", err)
	}
	if !strings.Contains(string(raw), seededPAT) {
		t.Fatal("the unredacted console log does not contain the PAT, so the redaction test is vacuous")
	}
	if redacted := redactText(string(raw)); strings.Contains(redacted, seededPAT) {
		t.Fatal("redactText did not remove the PAT")
	}
}

// Bodies are STRIPPED, not redacted (#1885). A redactor over arbitrary application payloads
// cannot be trusted, so the fields do not travel at all.
func TestApplicationBodiesAreStrippedEntirely(t *testing.T) {
	dir := writeLogs(t, "demo")
	logs, err := CollectRedactedLogs(dir, "demo", DefaultCollectionMaxBytes)
	if err != nil {
		t.Fatalf("collecting: %v", err)
	}

	var traffic string
	for _, l := range logs {
		if l.Kind == LogKindTraffic {
			traffic = string(l.Content)
		}
	}
	if traffic == "" {
		t.Fatal("no traffic log was collected")
	}
	// Not "the secret is gone" -- the whole field is gone. "alice" was never a secret and is
	// not redacted anywhere else, so finding it proves the body travelled.
	if strings.Contains(traffic, "alice") {
		t.Errorf("an application request body survived collection:\n%s", traffic)
	}
	if strings.Contains(traffic, "req_body") || strings.Contains(traffic, "resp_body") {
		t.Errorf("the body fields are still present in the uploaded traffic log:\n%s", traffic)
	}

	// BOUNDING. The diagnosis must survive: #1763 exists because a routing problem took an
	// hour to diagnose, and that is read from method, path and status.
	if !strings.Contains(traffic, "/health") || !strings.Contains(traffic, "POST") {
		t.Errorf("stripping the bodies also removed the diagnosis:\n%s", traffic)
	}
}

func TestUnparseableTrafficLinesAreDroppedAndCounted(t *testing.T) {
	dir := writeLogs(t, "demo")
	logs, err := CollectRedactedLogs(dir, "demo", DefaultCollectionMaxBytes)
	if err != nil {
		t.Fatalf("collecting: %v", err)
	}
	for _, l := range logs {
		if l.Kind != LogKindTraffic {
			continue
		}
		if l.DroppedLines == 0 {
			t.Error("the malformed traffic line was not counted as dropped")
		}
		// Silently shorter reads as a quieter system, so the log says what it lost.
		if !strings.Contains(string(l.Content), "dropped rather than uploaded") {
			t.Errorf("the dropped line is not disclosed in the output:\n%s", l.Content)
		}
	}
}

func TestCollectionIsBoundedAndKeepsTheNewestLines(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		raw, _ := json.Marshal(EventEntry{ //nolint:errcheck
			Time: time.Now(), Level: "info", Event: fmt.Sprintf("event-%d", i),
		})
		sb.Write(raw)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "error-demo.log"), []byte(sb.String()), 0o600); err != nil {
		t.Fatalf("writing: %v", err)
	}

	const budget = 8 << 10
	logs, err := CollectRedactedLogs(dir, "demo", budget)
	if err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("collected %d logs, want 1", len(logs))
	}
	if int64(logs[0].Bytes) > budget {
		t.Errorf("collection is %d bytes, over the %d budget", logs[0].Bytes, budget)
	}
	if !logs[0].Truncated {
		t.Error("a truncated collection does not say it was truncated")
	}
	// Newest kept, oldest dropped: the lines that describe the problem being reported are the
	// recent ones.
	content := string(logs[0].Content)
	if !strings.Contains(content, "event-4999") {
		t.Error("the newest line was dropped; truncation kept the wrong end")
	}
	if strings.Contains(content, `"event-0"`) {
		t.Error("the oldest line survived a truncated collection")
	}
}

func TestMissingLogsAreNotAnError(t *testing.T) {
	// A client that has never hit an error has no error log. That is the normal case, not a
	// failure, and must not abort a collection.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "client-demo.log"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("writing: %v", err)
	}
	logs, err := CollectRedactedLogs(dir, "demo", DefaultCollectionMaxBytes)
	if err != nil {
		t.Fatalf("a missing log aborted the collection: %v", err)
	}
	if len(logs) != 1 || logs[0].Kind != LogKindConsole {
		t.Fatalf("got %d log(s), want just the console log", len(logs))
	}
}

func TestRedactTextCases(t *testing.T) {
	cases := []struct {
		name, in, mustNotContain string
	}{
		{"a PAT", "using " + seededPAT + " now", seededPAT},
		{"a bearer token", "Authorization: Bearer abc123def456ghi", "abc123def456ghi"},
		{"URL credentials", "https://alice:" + seededSecret + "@example.com/x", seededSecret},
		{"a query token", "/cb?access_token=" + seededPAT + "&next=/home", seededPAT},
		{"a query password", "/login?password=" + seededSecret, seededSecret},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactText(tc.in)
			if strings.Contains(got, tc.mustNotContain) {
				t.Errorf("redactText(%q) = %q, still contains the secret", tc.in, got)
			}
			if !strings.Contains(got, redactedPlaceholder) {
				t.Errorf("redactText(%q) = %q, with no marker that something was removed", tc.in, got)
			}
		})
	}

	// NARROWNESS CONTROL. A redactor that removed everything would satisfy every case above
	// and destroy the diagnosis.
	ordinary := "GET /api/users/42 200 in 13ms"
	if got := redactText(ordinary); got != ordinary {
		t.Errorf("redactText rewrote an ordinary line: %q -> %q", ordinary, got)
	}
}
