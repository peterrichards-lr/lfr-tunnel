package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseNodeSetFingerprint(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
		ok   bool
	}{
		"a fingerprint on its own":      {`{"nodes":"a1b2c3d4e5f6"}`, "a1b2c3d4e5f6", true},
		"beside a shutdown warning":     {`{"type":"node_shutdown_warning","shutdown_at":1893456000,"nodes":"deadbeef0000"}`, "deadbeef0000", true},
		"an older gateway sends none":   {`{"type":"node_shutdown_warning","shutdown_at":1893456000}`, "", false},
		"a plain acknowledgement":       {`{"status":"ok"}`, "", false},
		"an empty body":                 {``, "", false},
		"an unparseable body":           {`{"nodes":`, "", false},
		"present but empty is not news": {`{"nodes":""}`, "", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := ParseNodeSetFingerprint([]byte(tc.body))
			if ok != tc.ok || got != tc.want {
				t.Errorf("ParseNodeSetFingerprint(%q) = %q, %v; want %q, %v", tc.body, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// The first fingerprint of a session is a baseline, not news. Raising a change on it would make
// every client reconsider its gateway five seconds after connecting, every time -- which is a
// re-probe and possibly a move for every client in the fleet on every reconnect.
func TestNodeSetChangeIsRaisedOnlyByAChange(t *testing.T) {
	e := NewInterceptorEngine("", nil)

	e.NoteNodeSetFingerprint("aaaaaaaaaaaa")
	if e.ConsumeNodeSetChange() {
		t.Fatal("the first fingerprint of a session was reported as a change")
	}

	// Repeats are what the heartbeat mostly carries: the same value every five seconds.
	for i := 0; i < 5; i++ {
		e.NoteNodeSetFingerprint("aaaaaaaaaaaa")
	}
	if e.ConsumeNodeSetChange() {
		t.Fatal("an unchanged fingerprint was reported as a change")
	}

	e.NoteNodeSetFingerprint("bbbbbbbbbbbb")
	if !e.ConsumeNodeSetChange() {
		t.Fatal("a changed fingerprint raised nothing -- this is the whole signal (#1937)")
	}
	// Read-and-clear: one change, one reconsideration.
	if e.ConsumeNodeSetChange() {
		t.Error("the change signal survived being consumed")
	}

	// And a second change still registers -- the baseline moves forward rather than latching on
	// whatever the session started with.
	e.NoteNodeSetFingerprint("cccccccccccc")
	if !e.ConsumeNodeSetChange() {
		t.Error("a second roster change raised nothing")
	}
}

// A gateway that says nothing must not read as "the roster is now empty". Older gateways send no
// fingerprint at all, and one that starts sending one mid-session must not be treated as a change
// away from nothing.
func TestAnAbsentFingerprintIsNotAChange(t *testing.T) {
	e := NewInterceptorEngine("", nil)
	e.NoteNodeSetFingerprint("")
	e.NoteNodeSetFingerprint("")
	if e.ConsumeNodeSetChange() {
		t.Fatal("a gateway that sends no fingerprint was treated as a roster change")
	}
	e.NoteNodeSetFingerprint("aaaaaaaaaaaa")
	if e.ConsumeNodeSetChange() {
		t.Fatal("a gateway that started advertising a fingerprint was treated as a roster change")
	}
}

// A new session is a new publisher. Without the reset, a pending change from the gateway we just
// left would be acted on by the session that has this second finished moving -- and a gateway
// whose fingerprint simply differs from the last one's would move the client straight back.
func TestResetNodeSetClearsTheBaselineAndAnyPendingChange(t *testing.T) {
	e := NewInterceptorEngine("", nil)
	e.NoteNodeSetFingerprint("aaaaaaaaaaaa")
	e.NoteNodeSetFingerprint("bbbbbbbbbbbb")

	e.ResetNodeSet()

	if e.ConsumeNodeSetChange() {
		t.Fatal("a pending change survived into the next session")
	}
	if e.NodeSetFingerprint() != "" {
		t.Fatalf("the baseline survived the reset: %q", e.NodeSetFingerprint())
	}
	// The next gateway's first value is a baseline again, whatever it happens to be.
	e.NoteNodeSetFingerprint("cccccccccccc")
	if e.ConsumeNodeSetChange() {
		t.Error("the first fingerprint of the new session was reported as a change")
	}
}

// THE WIRING, which is the part that silently does nothing when it breaks: the parser and the
// recorder can both be perfect while nothing ever calls them.
//
// It also pins the #1238 rule in the same run. The client heartbeats to its serving gateway AND
// to central, and central's reply describes central -- for an edge-served session it holds no
// lease and knows a different roster. So this gives the two DIFFERENT fingerprints and requires
// the serving gateway's to be the one recorded: an implementation that parsed both would end up
// with central's value here, and would then report a change on every other heartbeat forever.
func TestHeartbeatRecordsTheServingGatewaysFingerprintOnly(t *testing.T) {
	const servingFingerprint = "5e2f1a7c9b40"
	const centralFingerprint = "ffffffffffff"

	var servingHits int64
	serving := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&servingHits, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"nodes":%q}`, servingFingerprint)
	}))
	defer serving.Close()

	var centralHits int64
	central := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&centralHits, 1)
		w.Header().Set("Content-Type", "application/json")
		// Central legitimately answers the no-lease branch for an edge-served session, and
		// knows its own roster. Both are here because both are true in production.
		fmt.Fprintf(w, `{"status":"ok","nodes":%q}`, centralFingerprint)
	}))
	defer central.Close()

	engine := NewInterceptorEngine("", nil)
	// Addressed as "localhost", not as the "127.0.0.1" httptest hands out. statusReportTargets
	// drops the central target when sameGatewayHost says it is the same gateway, and that
	// comparison is on the HOSTNAME ALONE -- two httptest servers differ only by port, so a
	// naively wired test never pings central at all and its "only the serving gateway" half is
	// satisfied by central never having been asked. That is #1961, and this line is what stops
	// this test having the same hole.
	centralURL := strings.Replace(central.URL, "127.0.0.1", "localhost", 1)
	if centralURL == central.URL {
		t.Fatalf("the central stand-in is not addressed differently from the serving gateway (%s)", central.URL)
	}
	engine.SetCentralURL(centralURL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine.StartHealthChecks(ctx, func() {}, serving.URL, "us", "session-token", []int{})

	if !waitFor(func() bool { return atomic.LoadInt64(&servingHits) > 0 }) {
		t.Fatal("the serving gateway never received a heartbeat -- the harness failed, not the subject")
	}
	// PREMISE. The assertion below is only evidence that central's body was IGNORED if central
	// was actually asked; without this it is satisfied by central never having been pinged.
	//
	// WAITED FOR, not read once (#2026). statusReportTargets returns [serving, central] and the
	// health-check loop pings them in that order, sequentially -- so at the instant servingHits
	// becomes 1, central's POST has not been issued yet. Polling on servingHits alone and then
	// reading centralHits immediately is a race a loaded runner loses, and it lost it on PR #2028
	// at exactly 5.00s: the first tick, with this premise reported as a failure of the heartbeat
	// path rather than of the wait.
	if !waitFor(func() bool { return atomic.LoadInt64(&centralHits) > 0 }) {
		t.Fatal("the control plane never received a heartbeat -- the serving-gateway assertion " +
			"below would pass for the wrong reason")
	}
	// The record happens after the response body is read, so let the tick finish. Polled rather
	// than slept for 500ms: waiting longer can only make a recorded value more likely to be
	// visible, and can never turn a correct one into a wrong one, so the switch below still
	// diagnoses what was actually recorded.
	waitFor(func() bool { return engine.NodeSetFingerprint() != "" })

	switch got := engine.NodeSetFingerprint(); got {
	case servingFingerprint:
	case "":
		t.Fatalf("the heartbeat carried a fingerprint and the client recorded none -- nothing reads it")
	case centralFingerprint:
		t.Fatalf("the client recorded the CONTROL PLANE's fingerprint (%q); only the serving gateway's body may be parsed (#1238)", got)
	default:
		t.Fatalf("the client recorded %q, which is neither gateway's fingerprint", got)
	}
}

// The heartbeat body is read through io.LimitReader(resp.Body, 512). Everything that rides it
// shares that budget, so this bounds the wire format rather than trusting that it stays small.
// Extends the #1763 bound with #1937's field: the fingerprint is the third thing on this body.
func TestHeartbeatBodyWithAFingerprintFitsTheClientReadLimit(t *testing.T) {
	const clientReadLimit = 512

	body, err := json.Marshal(map[string]any{
		"type":              "node_shutdown_warning",
		"action":            "shutdown_warning",
		"seconds_remaining": 900,
		"shutdown_at":       1893456000,
		"reason":            strings.Repeat("scheduled maintenance ", 5),
		"node_id":           "edge-apac-1",
		"nodes":             "0123456789ab",
		"commands": []DiagnosticsCommand{
			{ID: "0123456789abcdef0123456789abcdef", Type: DiagnosticsCommandCollectLogs},
		},
	})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if len(body) > clientReadLimit {
		t.Fatalf("a shutdown warning, a command and a node-set fingerprint together are %d bytes, "+
			"over the %d-byte read limit -- the body would truncate and NONE of them would parse",
			len(body), clientReadLimit)
	}

	// And all three parsers must still work at that size, not merely fit.
	if _, ok := ParseNodeShutdownWarning(body); !ok {
		t.Error("the shutdown warning did not parse at the size limit")
	}
	if len(ParseDiagnosticsCommands(body)) != 1 {
		t.Error("the command did not parse at the size limit")
	}
	if fp, ok := ParseNodeSetFingerprint(body); !ok || fp != "0123456789ab" {
		t.Errorf("the node-set fingerprint did not parse at the size limit: %q, %v", fp, ok)
	}
}
