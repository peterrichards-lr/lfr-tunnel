package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// #1947: the probe used http.DefaultTransport, which is shared process-wide and pools idle
// connections. resolveServerURL fetches /api/version from the CURRENT gateway immediately
// before electing, so that gateway entered its own election on a warm connection -- measured at
// ~1 round trip while every rival paid a fresh handshake at ~3. The incumbent won elections it
// should have lost, fleet-wide.
//
// THE WARM-INCUMBENT PATH IS THE WHOLE TEST. A probe run from a cold process cannot tell the
// fixed code from the broken code: both open fresh connections when there is nothing to reuse.
// So every case here warms one host first, exactly as the client does.

// countingServer is an HTTP gateway stub that counts how many distinct connections it accepts.
type countingServer struct {
	*httptest.Server
	mu    sync.Mutex
	conns int
}

func newCountingServer(t *testing.T) *countingServer {
	t.Helper()
	cs := &countingServer{}
	cs.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// GatewayCanCarrySession requires a 200 and a body that is not "draining".
		// Handled, not discarded: errcheck runs with check-blank: true here, so `_, _ =`
		// is not an escape. A stub whose write fails would make every probe look
		// unreachable and the test would misreport the reason.
		if _, err := w.Write([]byte(`{"status":"ok"}`)); err != nil {
			panic("probe stub could not write its body: " + err.Error())
		}
	}))
	cs.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			cs.mu.Lock()
			cs.conns++
			cs.mu.Unlock()
		}
	}
	cs.Start()
	t.Cleanup(cs.Close)
	return cs
}

func (cs *countingServer) accepted() int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.conns
}

// warm does what fetchRemoteRegions does: a request to the current gateway over the SHARED
// default transport, leaving a pooled keep-alive connection behind.
func warm(t *testing.T, url string) {
	t.Helper()
	resp, err := http.Get(url + "/api/version") //nolint:noctx
	if err != nil {
		t.Fatalf("warming %s: %v", url, err)
	}
	// The body MUST be drained to EOF, not merely closed. Go returns a connection to the
	// idle pool only once its body is fully read; closing early discards it. An earlier
	// version of this helper closed without reading, so nothing was ever pooled and the
	// test passed against the unfixed code -- a fixture describing a state the client
	// never reaches, which is the failure mode this whole test exists to catch.
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("draining %s: %v", url, err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("closing %s: %v", url, err)
	}
}

func TestTheProbeDoesNotReuseTheIncumbentsWarmConnection(t *testing.T) {
	incumbent := newCountingServer(t)
	rival := newCountingServer(t)

	// The client is already talking to the incumbent when the election starts.
	warm(t, incumbent.URL)
	afterWarm := incumbent.accepted()
	if afterWarm != 1 {
		t.Fatalf("setup: expected the warm-up to open exactly 1 connection, got %d", afterWarm)
	}

	probeFastestRegion(map[string]string{
		"incumbent": incumbent.URL,
		"rival":     rival.URL,
	})

	gotIncumbent := incumbent.accepted() - afterWarm
	gotRival := rival.accepted()

	// The assertion: the probe opened its own connection to the incumbent rather than
	// riding the warm one. Equal treatment is the property; the timing follows from it.
	if gotIncumbent != 1 {
		t.Errorf("the probe opened %d new connection(s) to the incumbent, want 1 -- "+
			"0 means it reused the pooled connection and measured the incumbent at a "+
			"fraction of its true cost (#1947)", gotIncumbent)
	}
	if gotRival != 1 {
		t.Errorf("the probe opened %d connection(s) to the rival, want 1", gotRival)
	}
	if gotIncumbent != gotRival {
		t.Errorf("the two candidates were not measured alike: incumbent opened %d, rival %d",
			gotIncumbent, gotRival)
	}
}

func TestTheProbeLeavesNothingPooledForTheNextElection(t *testing.T) {
	// Failover re-elects through the same function (reregisterAcrossRegions ->
	// resolveServerURL -> probeFastestRegion). If a probe left connections pooled, the
	// SECOND election would find some candidates warm and some cold -- the same bias,
	// arriving arbitrarily depending on which hosts were probed within the idle window.
	a := newCountingServer(t)
	b := newCountingServer(t)
	regions := map[string]string{"a": a.URL, "b": b.URL}

	probeFastestRegion(regions)
	firstA, firstB := a.accepted(), b.accepted()

	probeFastestRegion(regions)
	secondA, secondB := a.accepted()-firstA, b.accepted()-firstB

	if secondA != 1 || secondB != 1 {
		t.Errorf("the second election reused connections from the first (a=%d b=%d, want 1 each) -- "+
			"every failover re-election must measure candidates as they are now (#1947)",
			secondA, secondB)
	}
}

func TestEveryProbeGetsAFreshTransport(t *testing.T) {
	// A package-level transport would reintroduce the defect a release later: it is exactly
	// what http.DefaultTransport was. Two calls must not hand back the same instance.
	first, second := newProbeTransport(), newProbeTransport()
	if first == second {
		t.Error("newProbeTransport returned a shared instance; a shared pool is the bug (#1947)")
	}
	tr := first
	if !tr.DisableKeepAlives {
		t.Error("DisableKeepAlives must be set, or connections are pooled and reused")
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.ClientSessionCache == nil {
		t.Error("the probe needs its own TLS session cache: shared resumption favours a host " +
			"this process has already negotiated with, which is the same bias one layer down")
	}
}
