package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

// newTestLease builds a lease in the state the proxy path actually leaves one in: it lives
// in the registry's host map (Registry.Register), and its byte counters have been advanced
// by trackingTransport (proxy.go, atomic.AddUint64 on BytesIn/BytesOut). Nothing here is a
// state the running gateway cannot reach.
func newTestLease(host string, bytesIn, bytesOut uint64) *TunnelLease {
	l := &TunnelLease{
		UserID:          "user-1",
		SubdomainPrefix: "demo",
		FullHost:        host,
		CreatedAt:       time.Now().UTC().Add(-time.Minute),
	}
	atomic.StoreUint64(&l.BytesIn, bytesIn)
	atomic.StoreUint64(&l.BytesOut, bytesOut)
	return l
}

func addLease(reg *Registry, l *TunnelLease) {
	reg.Lock()
	reg.leases[l.FullHost] = l
	reg.Unlock()
}

// TestTakeByteDeltasReportsEachByteExactlyOnce is the delta guarantee the whole of #1958
// rests on: a reconnecting edge, or a second tick on central, must not re-send what has
// already been accounted for.
//
// The bug this replaces was not that the arithmetic was wrong -- it was that the watermark
// was written onto ListLeases' *copy* of the lease, so the real lease's watermark never
// moved and every pass re-reported the cumulative total. Hence the third phase below:
// after taking, the lease's own LastBytes* must have advanced.
func TestTakeByteDeltasReportsEachByteExactlyOnce(t *testing.T) {
	reg := NewRegistry(nil)
	lease := newTestLease("demo.example.se", 100, 900)
	addLease(reg, lease)

	first := reg.TakeByteDeltas()
	if len(first) != 1 {
		t.Fatalf("expected one delta on the first take, got %d", len(first))
	}
	if first[0].BytesIn != 100 || first[0].BytesOut != 900 {
		t.Fatalf("first take should report everything carried so far, got in=%d out=%d", first[0].BytesIn, first[0].BytesOut)
	}

	// Nothing new has been carried: a second take must report nothing at all. Under the
	// previous code this returned 100/900 again, and every five minutes thereafter.
	if second := reg.TakeByteDeltas(); len(second) != 0 {
		t.Fatalf("a take with no new traffic must report nothing, got %+v", second)
	}

	// More traffic: only the increment is reported.
	atomic.AddUint64(&lease.BytesOut, 50)
	third := reg.TakeByteDeltas()
	if len(third) != 1 || third[0].BytesOut != 50 || third[0].BytesIn != 0 {
		t.Fatalf("expected only the 50 new bytes out, got %+v", third)
	}

	// The watermark has to be on the real lease, not on a snapshot of it.
	reg.RLock()
	got := reg.leases["demo.example.se"]
	reg.RUnlock()
	if got.LastBytesOut != 950 || got.LastBytesIn != 100 {
		t.Fatalf("watermark did not advance on the lease itself: in=%d out=%d", got.LastBytesIn, got.LastBytesOut)
	}
}

// failingWriter stands in for a control connection that has gone away mid-report.
type failingWriter struct {
	mu     sync.Mutex
	fail   bool
	frames []ControlMessage
}

func (f *failingWriter) WriteJSON(v interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("connection closed")
	}
	msg, ok := v.(ControlMessage)
	if !ok {
		return errors.New("unexpected frame type")
	}
	f.frames = append(f.frames, msg)
	return nil
}

func (f *failingWriter) reported() []EdgeByteDelta {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []EdgeByteDelta
	for _, m := range f.frames {
		out = append(out, m.Metrics...)
	}
	return out
}

// TestEdgeMetricsSurviveAFailedReport covers the case the issue calls out explicitly: an
// edge whose channel is down must not silently lose the interval. A failed send keeps the
// deltas queued, and the next successful send delivers them -- once, not twice.
func TestEdgeMetricsSurviveAFailedReport(t *testing.T) {
	reg := NewRegistry(nil)
	addLease(reg, newTestLease("demo.example.se", 10, 20))

	reporter := &edgeMetricsReporter{}
	conn := &failingWriter{fail: true}

	reporter.Collect(reg)
	reporter.Flush(conn)

	if got := reporter.pendingCount(); got != 1 {
		t.Fatalf("a failed report must keep its deltas, pending=%d", got)
	}
	if n := len(conn.reported()); n != 0 {
		t.Fatalf("nothing should have been reported through a failing connection, got %d", n)
	}

	conn.mu.Lock()
	conn.fail = false
	conn.mu.Unlock()
	reporter.Flush(conn)

	delivered := conn.reported()
	if len(delivered) != 1 || delivered[0].BytesIn != 10 || delivered[0].BytesOut != 20 {
		t.Fatalf("the carried-over delta should have been delivered intact, got %+v", delivered)
	}
	if got := reporter.pendingCount(); got != 0 {
		t.Fatalf("a successful report must clear the queue, pending=%d", got)
	}

	// And it must not be delivered a second time.
	reporter.Flush(conn)
	if n := len(conn.reported()); n != 1 {
		t.Fatalf("a delta must be reported exactly once, got %d deliveries", n)
	}
}

// TestRecordEdgeMetricsRefusesUnattributedDeltas guards the one outcome that is worse than
// not recording an edge's traffic: recording it as the control plane's. RecordTunnelMetric
// stores an empty node_id as "control", so an unattributed batch would inflate central's
// figures and deflate the edge's -- the exact distortion #1958 exists to remove.
func TestRecordEdgeMetricsRefusesUnattributedDeltas(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.DBPath = filepath.Join(t.TempDir(), "control.db")
	cfg.Domains = []string{"example.se"}
	cfg.DisableBackupScheduler = true

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create control server: %v", err)
	}
	defer srv.Stop()

	srv.recordEdgeMetrics("", []EdgeByteDelta{{
		UserID:      "user-1",
		Subdomain:   "demo",
		FullHost:    "demo.example.se",
		BytesIn:     1,
		BytesOut:    2,
		ConnectedAt: time.Now().UTC(),
		RecordedAt:  time.Now().UTC(),
	}})

	analytics, err := srv.db.GetUserAnalytics("user-1", 30)
	if err != nil {
		t.Fatalf("failed to read user analytics: %v", err)
	}
	if len(analytics.Tunnels) != 0 {
		t.Fatalf("an unattributed batch must not be recorded at all, got %+v", analytics.Tunnels)
	}
}

// TestEdgeReportsLeaseBytesOverControlChannel is the end-to-end claim: a real edge, holding
// a real lease that has carried bytes, gets those bytes into the control plane's
// tunnel_metrics table against its OWN node id, over the control channel and nothing else.
//
// It exercises the whole path -- edge registry -> TakeByteDeltas -> edge_metrics frame ->
// the control plane's read pump -> queueEdgeMetrics -> MetricsCollector -> tunnel_metrics --
// with two real Server instances and a real WebSocket between them, the same harness the rest
// of edge_control_ws_test.go uses.
func TestEdgeReportsLeaseBytesOverControlChannel(t *testing.T) {
	cfgControl := config.DefaultServerConfig()
	cfgControl.DBPath = filepath.Join(t.TempDir(), "control.db")
	cfgControl.Domains = []string{"example.se"}
	cfgControl.DisableBackupScheduler = true

	edgeToken := "usedge-mysecrettokenvalue"
	tokenHash := sha256.Sum256([]byte(edgeToken))
	cfgControl.EdgeNodes = []config.EdgeNodeConfig{
		{ID: "usedge", TokenHash: hex.EncodeToString(tokenHash[:])},
	}

	controlSrv, err := NewServer(cfgControl)
	if err != nil {
		t.Fatalf("failed to create control server: %v", err)
	}
	defer controlSrv.Stop()

	ts := httptest.NewServer(controlSrv)
	defer ts.Close()

	cfgEdge := config.DefaultServerConfig()
	cfgEdge.DBPath = "" // Stateless edge: no database, which is the whole problem.
	cfgEdge.Domains = []string{"example.se"}
	cfgEdge.ControlPlaneURL = ts.URL
	cfgEdge.EdgeToken = edgeToken
	cfgEdge.DisableBackupScheduler = true
	cfgEdge.EdgeMetricsIntervalSeconds = 1

	edgeSrv, err := NewServer(cfgEdge)
	if err != nil {
		t.Fatalf("failed to create edge server: %v", err)
	}
	defer edgeSrv.Stop()

	if edgeSrv.edgeMetrics == nil {
		t.Fatal("an edge must have a metrics reporter; without one its traffic is unmeasurable")
	}

	// A lease on the EDGE, carrying bytes. Central holds no lease for this host and never
	// sees a byte of it, so anything that appears in its database arrived by the reporting
	// path under test and by no other route.
	addLease(edgeSrv.registry, newTestLease("demo.example.se", 128, 4096))

	deadline := time.Now().Add(10 * time.Second)
	for {
		analytics, err := controlSrv.db.GetUserAnalytics("user-1", 30)
		if err != nil {
			t.Fatalf("failed to read user analytics: %v", err)
		}
		if len(analytics.Tunnels) == 1 {
			got := analytics.Tunnels[0]
			if got.FullHost != "demo.example.se" || got.BytesIn != 128 || got.BytesOut != 4096 {
				t.Fatalf("edge bytes reached the database but wrong: %+v", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the edge's lease bytes never reached the control plane's tunnel_metrics table")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Attribution: the row has to name the edge. Sessions counted against node "usedge" in
	// the node/day breakdown can only come from a tunnel_metrics row carrying that node_id,
	// and RecordTunnelMetric only ever reaches a non-"control" node_id through an edge
	// report.
	global, err := controlSrv.db.GetGlobalAnalytics(30)
	if err != nil {
		t.Fatalf("failed to read global analytics: %v", err)
	}
	found := false
	for _, nd := range global.NodeDaily {
		if nd.NodeID == "usedge" && nd.Sessions > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("edge bytes were recorded but not against the edge: %+v", global.NodeDaily)
	}

	// And a second interval with no new traffic must add nothing -- the delta guarantee,
	// observed through the real reporting loop rather than only at the registry.
	time.Sleep(2500 * time.Millisecond)
	analytics, err := controlSrv.db.GetUserAnalytics("user-1", 30)
	if err != nil {
		t.Fatalf("failed to re-read user analytics: %v", err)
	}
	if len(analytics.Tunnels) != 1 || analytics.Tunnels[0].BytesOut != 4096 {
		t.Fatalf("idle intervals must not re-report the same bytes, got %+v", analytics.Tunnels)
	}

	// A reconnect must not replay what was already accepted. Every edge drops its control
	// channel routinely -- nightly power cycles, deploys, network blips -- and this is the
	// case #1958 names outright: "a reconnecting edge must not double-count".
	controlSrv.edgeClientsMu.Lock()
	if existing, ok := controlSrv.edgeClients["usedge"]; ok {
		_ = existing.Close() //nolint:errcheck
	}
	controlSrv.edgeClientsMu.Unlock()

	reconnected := false
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		controlSrv.edgeClientsMu.RLock()
		_, ok := controlSrv.edgeClients["usedge"]
		controlSrv.edgeClientsMu.RUnlock()
		if ok {
			reconnected = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !reconnected {
		t.Fatal("the edge never re-established its control channel, so the replay check proves nothing")
	}

	time.Sleep(2500 * time.Millisecond)
	analytics, err = controlSrv.db.GetUserAnalytics("user-1", 30)
	if err != nil {
		t.Fatalf("failed to read user analytics after the reconnect: %v", err)
	}
	if len(analytics.Tunnels) != 1 || analytics.Tunnels[0].BytesOut != 4096 || analytics.Tunnels[0].BytesIn != 128 {
		t.Fatalf("a reconnect replayed bytes that had already been reported: %+v", analytics.Tunnels)
	}
}
