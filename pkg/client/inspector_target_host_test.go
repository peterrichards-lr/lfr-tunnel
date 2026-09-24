package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// unroutableTarget is a target host that is neither "localhost" nor "127.0.0.1", which is the
// whole point: an assertion made against either of those is satisfied by the hardcoded constant
// #2191 removed, and would pass on the broken build.
//
// The realistic value is host.docker.internal -- it is what the client's own -target-host help
// text advertises, and a developer proxying into a container is who this was reported by. A test
// cannot use it: whether it resolves depends on whether Docker wrote it into the runner's hosts
// file, and this package runs on all three matrix legs. `.invalid` is reserved by RFC 6761 and
// resolves nowhere, so the dial fails immediately and identically on Linux, macOS and Windows.
//
// The dial failing is not a problem for these tests and is in fact the cheapest way to reach the
// capture site: interceptorTransport.RoundTrip records the request with Status 502 before it
// returns the error (see the ErrorHandler comment in InterceptPort), so the record exists by the
// time the proxied request comes back.
const unroutableTarget = "target.example.invalid"

// TestCapturedRecordNamesTheHostItWasProxiedTo is the reported defect (#2191), driven through the
// real proxy rather than by building a record in the test.
//
// The Inspector's details pane read "Target: localhost:8080" for every request, because the port
// came from the record and the host was a literal in dashboard.html -- it could not come from the
// record, which had no host in it. This asserts the record now carries the host the request was
// actually proxied to, by VALUE: a build that still answers "localhost" fails saying what it said.
func TestCapturedRecordNamesTheHostItWasProxiedTo(t *testing.T) {
	engine := NewInterceptorEngine(unroutableTarget, nil)

	const destPort = 8080
	interceptPort, err := engine.InterceptPort(destPort)
	if err != nil {
		t.Fatalf("InterceptPort: %v", err)
	}

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/some/path", interceptPort))
	if err != nil {
		t.Fatalf("requesting the intercepting proxy: %v", err)
	}
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("closing the proxy response body: %v", cerr)
	}

	engine.mu.RLock()
	history := append([]*RequestRecord(nil), engine.History...)
	engine.mu.RUnlock()

	if len(history) != 1 {
		t.Fatalf("expected the proxied request to be captured once, got %d records -- this test "+
			"cannot report on the target host until the capture itself works", len(history))
	}
	rec := history[0]
	if rec.TargetHost != unroutableTarget {
		t.Errorf("the request was proxied to %q (NewInterceptorEngine was given it, and "+
			"InterceptPort dials it) but the captured record says TargetHost=%q. The Inspector's "+
			"details pane renders this field, so a wrong value is read as a fact about where the "+
			"request went (#2191).", unroutableTarget, rec.TargetHost)
	}
	if rec.TargetPort != destPort {
		t.Errorf("record names port %d, want %d -- the host and port are one statement and the "+
			"pane prints them together", rec.TargetPort, destPort)
	}

	// The field the page actually reads, over the wire it actually reads it on. Asserted through
	// /api/state rather than on the struct alone because renaming the json tag would leave every
	// Go assertion above green and blank the pane.
	port := startInspectorForTest(t, engine, 56291)
	_, body := getInspector(t, port, "/api/state")

	var state struct {
		History []struct {
			TargetHost string `json:"target_host"`
			TargetPort int    `json:"target_port"`
		} `json:"history"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatalf("decoding /api/state: %v", err)
	}
	if len(state.History) != 1 {
		t.Fatalf("/api/state served %d history records, want 1", len(state.History))
	}
	if state.History[0].TargetHost != unroutableTarget {
		t.Errorf("/api/state serves target_host=%q, want %q -- this is the value the details pane "+
			"renders, so the page states the wrong target whatever the Go struct holds (#2191)",
			state.History[0].TargetHost, unroutableTarget)
	}
}

// TestReplayRecordNamesTheHostItWasSentTo covers the second construction site. A replayed request
// is a new record shown in the same pane, and ReplayRequest is the one path whose target can
// differ from engine.TargetHost: it substitutes 127.0.0.1 for an empty host before dialling, so
// the record has to name the substituted value rather than the caller's empty one.
func TestReplayRecordNamesTheHostItWasSentTo(t *testing.T) {
	t.Run("named host", func(t *testing.T) {
		rec, err := ReplayRequest(unroutableTarget, &RequestRecord{
			Method: "GET", Path: "/replay-me", TargetPort: 8080,
		})
		if err != nil {
			t.Fatalf("ReplayRequest: %v", err)
		}
		if rec.TargetHost != unroutableTarget {
			t.Errorf("ReplayRequest sent the replay to %q and recorded TargetHost=%q",
				unroutableTarget, rec.TargetHost)
		}
	})

	t.Run("empty host is recorded as the host actually dialled", func(t *testing.T) {
		rec, err := ReplayRequest("", &RequestRecord{
			Method: "GET", Path: "/replay-me", TargetPort: 8080,
		})
		if err != nil {
			t.Fatalf("ReplayRequest: %v", err)
		}
		// 127.0.0.1, not "localhost" and not "": ReplayRequest's own default is the IPv4
		// literal, chosen because "localhost" commonly resolves to ::1 first (see
		// normalizeDiscoveredHost in cmd/lfr-tunnel/main.go). The record must name what was
		// dialled, not what the caller passed.
		if rec.TargetHost != "127.0.0.1" {
			t.Errorf("ReplayRequest defaulted an empty target host to 127.0.0.1 and dialled it, "+
				"but recorded TargetHost=%q -- the pane would name a host the replay never used",
				rec.TargetHost)
		}
	})
}

// TestAddRecordStampsTheTargetHost is the durable half on the Go side, and the reason this fix is
// not two edits that have to be remembered.
//
// #2191 changes a record contract, and §5b's failure mode here is a construction site nobody
// found: it produces a record with no host, and the pane then says something WORSE than a wrong
// constant. AddRecord is the single funnel every record passes through on its way into the
// history and the session log, so it stamps the engine's host onto any record that does not name
// one. A new capture site can forget the field and still cannot blank the pane.
func TestAddRecordStampsTheTargetHost(t *testing.T) {
	engine := NewInterceptorEngine(unroutableTarget, nil)

	// What a construction site that has never heard of this field produces.
	engine.AddRecord(&RequestRecord{Method: "GET", Path: "/unstamped", TargetPort: 8080})

	// A record that knows better keeps its own answer -- ReplayRequest depends on this.
	engine.AddRecord(&RequestRecord{Method: "GET", Path: "/stamped", TargetPort: 8080,
		TargetHost: "already-known.example"})

	engine.mu.RLock()
	history := append([]*RequestRecord(nil), engine.History...)
	engine.mu.RUnlock()

	if len(history) != 2 {
		t.Fatalf("expected 2 records, got %d", len(history))
	}
	byPath := map[string]string{}
	for _, rec := range history {
		byPath[rec.Path] = rec.TargetHost
	}
	if byPath["/unstamped"] != unroutableTarget {
		t.Errorf("AddRecord stored a record with TargetHost=%q for a request proxied to %q -- a "+
			"record with no host renders as \"Target: :8080\", which is worse than the constant "+
			"#2191 removed", byPath["/unstamped"], unroutableTarget)
	}
	if byPath["/stamped"] != "already-known.example" {
		t.Errorf("AddRecord overwrote a record's own TargetHost with the engine's (%q) -- "+
			"ReplayRequest sets a host the engine field does not necessarily equal",
			byPath["/stamped"])
	}
}

// TestSessionLogRecordsTheTargetHost keeps the on-disk traffic log and the pane telling the same
// story, and pins what a log line written before this field existed decodes to.
func TestSessionLogRecordsTheTargetHost(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewSessionLogger(dir, "alpha-se", false)
	if err != nil {
		t.Fatalf("NewSessionLogger: %v", err)
	}
	logger.Traffic(&RequestRecord{
		Method: "GET", Path: "/logged", Status: 200, TargetPort: 8080,
		TargetHost: unroutableTarget,
	}, "eu")
	if cerr := logger.Close(); cerr != nil {
		t.Fatalf("closing the session logger: %v", cerr)
	}

	lines := readLines(t, filepath.Join(dir, "traffic-alpha-se.log"))
	if len(lines) != 1 {
		t.Fatalf("expected one traffic line, got %d", len(lines))
	}
	var entry TrafficEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decoding the traffic line %q: %v", lines[0], err)
	}
	if entry.TargetHost != unroutableTarget {
		t.Errorf("the traffic log recorded target_host=%q, want %q -- the log names the port a "+
			"request went to, and a port without a host is the same half-answer the pane gave "+
			"(#2191)", entry.TargetHost, unroutableTarget)
	}

	// A line from a client built before the field existed. It has to decode, and it has to decode
	// to "unknown" rather than to a host it never recorded -- the readers of this format (the
	// Inspector's log viewer, redactTrafficLine) must not invent localhost either.
	var legacy TrafficEntry
	const legacyLine = `{"ts":"2026-09-01T00:00:00Z","method":"GET","path":"/old","status":200,` +
		`"dur_ms":3,"port":8080,"region":"eu"}`
	if err := json.Unmarshal([]byte(legacyLine), &legacy); err != nil {
		t.Fatalf("a traffic line written before target_host existed no longer decodes: %v", err)
	}
	if legacy.TargetHost != "" {
		t.Errorf("a legacy traffic line decoded to TargetHost=%q, want empty", legacy.TargetHost)
	}
	if legacy.TargetPort != 8080 {
		t.Errorf("a legacy traffic line lost its port: got %d", legacy.TargetPort)
	}
}

// hostBesideInterpolation matches the SHAPE of the defect rather than the four host names it
// happened to use: a literal glued by a colon to an interpolated value, as in
// "localhost:" + the record's port. No spaces around the colon, which is what keeps it off every
// CSS declaration in the page ("background: ${x}") while still catching any host somebody writes.
var hostBesideInterpolation = regexp.MustCompile(`[A-Za-z0-9.\-]+:\$\{`)

// TestDashboardNamesNoHostAsFactBesideAnInterpolatedValue is the durable half on the page side.
//
// It fails on ANY member of the class, not on "localhost" -- a build that fixed the details pane
// and hardcoded "127.0.0.1" into the next one goes red here. The translation bundles are NOT this
// test's business: TestInspectorBundleStatesNoHostPortAsFact owns those, and this is raw markup it
// does not and should not read.
//
// Blind spot, stated as an assertion rather than left in prose: full-line comments are skipped, so
// the comment above targetLabel may quote the defect it describes. Nothing executable can hide
// there, because a comment is not markup -- and the FIRING control below fails if the detector
// stops detecting.
//
// Re-run the enumeration by hand with:
//
//	grep -nE '[A-Za-z0-9.-]+:\$\{' pkg/client/dashboard.html
func TestDashboardNamesNoHostAsFactBesideAnInterpolatedValue(t *testing.T) {
	page, err := os.ReadFile("dashboard.html")
	if err != nil {
		t.Fatalf("reading dashboard.html: %v", err)
	}

	// FIRING control: the defect exactly as it was written, so a regex that has stopped matching
	// cannot report a clean page.
	if !hostBesideInterpolation.MatchString("<span>Target: localhost:${req.target_port}</span>") {
		t.Fatal("the detector no longer matches the reported defect (#2191) -- every result below " +
			"is vacuous until it does")
	}

	var offenders []string
	for i, line := range strings.Split(string(page), "\n") {
		if isCommentLine(line) {
			continue
		}
		if found := hostBesideInterpolation.FindString(line); found != "" {
			offenders = append(offenders,
				fmt.Sprintf("line %d: %q in %s", i+1, found, strings.TrimSpace(line)))
		}
	}
	if len(offenders) > 0 {
		t.Errorf("%d place(s) in dashboard.html state a host as a literal next to a value taken "+
			"from the record:\n  %s\n\nThe port came from the data and the host did not, which is "+
			"#2191: the pane read \"Target: localhost:8080\" for a request that went to "+
			"host.docker.internal. Take the host from the record too.",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

func isCommentLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") ||
		strings.HasPrefix(trimmed, "<!--")
}

// TestDashboardDetailsPaneRendersTheRecordsHost pins what the absence check above cannot: that the
// line still exists and still says something.
//
// An absence-only assertion is satisfied by deleting the Target line altogether, and by a page
// that renders no target at all. So this names the mechanism -- the pane goes through targetLabel,
// targetLabel reads target_host -- and pins the decision about a record that has no host: it names
// the PORT ALONE. "localhost" as a fallback would restate the defect, and is wrong besides, since
// an unset target host is dialled as 127.0.0.1.
func TestDashboardDetailsPaneRendersTheRecordsHost(t *testing.T) {
	page, err := os.ReadFile("dashboard.html")
	if err != nil {
		t.Fatalf("reading dashboard.html: %v", err)
	}
	src := string(page)

	if !strings.Contains(src, "Target: ${escapeHtml(targetLabel(req))}") {
		t.Error("the details pane no longer renders its target through targetLabel -- the pane is " +
			"where #2191 was reported, and a hand-built target string there is how the literal " +
			"host came back")
	}

	body := functionBody(t, src, "function targetLabel(req) {")
	if !strings.Contains(body, "req.target_host") {
		t.Errorf("targetLabel does not read req.target_host:\n%s", body)
	}
	namedHost := regexp.MustCompile(`(?i)localhost|127\.0\.0\.1|0\.0\.0\.0|host\.docker\.internal`)
	if found := namedHost.FindString(body); found != "" {
		t.Errorf("targetLabel names the host %q itself:\n%s\n\nA record that does not know where "+
			"it went must not be given a guessed host -- that is the defect (#2191), and "+
			"\"localhost\" is not even the right guess: an unset target host is dialled as "+
			"127.0.0.1.", found, body)
	}
	if !strings.Contains(body, "port ${req.target_port}") {
		t.Errorf("targetLabel no longer names the port alone when the record has no host:\n%s\n\n"+
			"The alternatives are a guessed host and \"Target: :8080\"; the first is the defect "+
			"and the second reads as a broken page.", body)
	}
}

// functionBody returns the source of the function opened by header, up to the closing brace at the
// same indentation. Crude on purpose: it is reading one known function out of one file in this
// repository, not parsing JavaScript.
func functionBody(t *testing.T, src, header string) string {
	t.Helper()
	start := strings.Index(src, header)
	if start < 0 {
		t.Fatalf("dashboard.html no longer defines %q", header)
	}
	indent := ""
	for i := start - 1; i >= 0 && (src[i] == ' ' || src[i] == '\t'); i-- {
		indent = string(src[i]) + indent
	}
	rest := src[start:]
	end := strings.Index(rest, "\n"+indent+"}")
	if end < 0 {
		t.Fatalf("could not find the end of %q", header)
	}
	return rest[:end]
}
