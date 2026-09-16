package server

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// Edge bandwidth reporting (#1958).
//
// An edge has no database, so the bytes its leases carry can only be measured where they
// flow -- on the edge -- and recorded where the database is -- on central. Until this
// existed, a session served by an edge contributed essentially nothing to tunnel_metrics:
// one real user across an afternoon produced 15 MB against node_id "control", 30 bytes
// against "edge-sa", and no rows at all for "edge-us" despite thirty minutes of verified
// live traffic. That was tolerable only while almost every session ran on central; #1947
// removed the incumbent's advantage in the client's latency election, so edge-served is
// now the normal case and the charts were about to read as collapsing usage rather than
// as stopped measurement.
//
// Design decisions, and why:
//
//   - It rides the EXISTING edge control channel (edge_control_ws.go), which is the channel
//     this repo's edge-sync rules designate for control/edge state and the only edge->central
//     link whose liveness is already monitored (edgeControlConnected, edge health, the
//     portal's node status). No new connection, no second thing to notice is broken.
//
//   - DELTAS, never totals. Registry.TakeByteDeltas hands each byte out exactly once, so a
//     reconnecting edge cannot re-report what it already sent. A restart of either side
//     cannot double-count either: central holds no per-edge cursor to get out of step with.
//
//   - Unreported bytes are CARRIED, not discarded. While the channel is down nothing is
//     taken off the leases at all, so the counters simply keep climbing and the next
//     successful report covers the whole outage. Deltas that were taken and then failed to
//     send go back on the pending queue for the next attempt. The one bounded loss is an
//     edge process that exits with bytes still pending -- at most one interval's worth, and
//     its clients have moved elsewhere by then anyway. "Absence looks like zero" is the trap
//     this repo keeps hitting (#1923, #1938, #1956), so every drop here is logged at Warn
//     with a count, never swallowed.
//
//   - Telemetry must NEVER be able to drop a tunnel. Every failure path on both sides logs
//     and returns. The control plane's read pump ignores a frame it cannot parse rather than
//     closing the connection, and frames are chunked well under the read limit so a large
//     batch cannot trip gorilla's limit and tear down the channel that also carries kicks,
//     schedules and blacklist pushes.

// defaultEdgeMetricsInterval is how often an edge reports, when the config does not say.
//
// This number IS the worst-case loss, so it is chosen rather than inherited. Every edge stops
// nightly (EventBridge, 00:00-08:00 local: `edge-us-stop cron(00 00 * * ? *)`), and an edge has
// no database, so anything it is still holding when the process ends is gone. A graceful stop
// flushes (flushEdgeMetrics, called by Server.stop, which systemd's SIGTERM reaches on the
// ACPI shutdown `aws ec2 stop-instances` performs), and so do the two "I am going away"
// signals -- a drain announcement and a scheduled-shutdown warning. What remains is an
// UNGRACEFUL stop: force-stop, power loss, panic. That loses at most one interval of one
// node's traffic, which is what this constant bounds. Thirty seconds, not the collector's
// five minutes and not a minute.
//
// An acknowledgement from central was considered and rejected. It would shrink the window
// further, but only by making the edge hold a delta until central confirms -- and a lost ack
// then makes the edge RESEND something central already took, turning a bounded under-count
// into a double-count. #1958 states the priority explicitly ("a reconnecting edge must not
// double-count"), and closing that hole properly needs a dedup ledger on central, which is
// more machinery than a 30-second exposure on an ungraceful stop justifies.
const defaultEdgeMetricsInterval = 30 * time.Second

// edgeMetricsMaxPerFrame chunks a report so one frame stays far below edgeControlReadLimit.
// At roughly 250 bytes of JSON per delta this is ~25 KB against a 128 KB limit; exceeding
// the limit would close the control channel, which is exactly what telemetry must not do.
const edgeMetricsMaxPerFrame = 100

// edgeMetricsMaxPending bounds the carry-over queue. At one report a minute per live lease
// this is hours of outage before anything is dropped, and a drop is logged with its count.
const edgeMetricsMaxPending = 2000

// edgeControlReadLimit is the control plane's per-frame read limit on an edge control
// connection. It was 512 bytes, which was ample while the edge only ever sent an auth
// response -- a metrics frame would have exceeded it and gorilla closes the connection when
// it does. Raised to a bounded 128 KB rather than removed: a frame this size is still two
// orders of magnitude more than any message defined here needs.
const edgeControlReadLimit = 128 * 1024

// EdgeByteDelta is one lease's traffic since the edge last reported it, as carried on the
// control channel.
//
// NodeID is deliberately NOT a field: the control plane stamps the node that the connection
// authenticated as, so an edge cannot attribute its traffic to another node.
type EdgeByteDelta struct {
	UserID      string    `json:"user_id"`
	Subdomain   string    `json:"subdomain"`
	FullHost    string    `json:"full_host"`
	BytesIn     int64     `json:"bytes_in"`
	BytesOut    int64     `json:"bytes_out"`
	ConnectedAt time.Time `json:"connected_at"`
	// RecordedAt is when the edge measured the delta, not when central received it, so a
	// report delayed by an outage lands in the bucket the traffic actually happened in.
	RecordedAt time.Time `json:"recorded_at"`
}

// edgeMetricsReporter accumulates byte deltas on an edge and reports them upstream.
type edgeMetricsReporter struct {
	mu      sync.Mutex
	pending []EdgeByteDelta
	// dropped counts deltas discarded because the queue was full, so the log can say how
	// much measurement was lost rather than leaving it to look like no traffic.
	dropped int
}

// controlWriter is the subset of safeConn the reporter needs, so a test can substitute one.
type controlWriter interface {
	WriteJSON(v interface{}) error
}

// Add queues one delta for the next report.
func (r *edgeMetricsReporter) Add(d EdgeByteDelta) {
	if d.BytesIn <= 0 && d.BytesOut <= 0 {
		return
	}
	if d.RecordedAt.IsZero() {
		d.RecordedAt = time.Now().UTC()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = append(r.pending, d)
	r.trimLocked()
}

// trimLocked enforces edgeMetricsMaxPending by discarding the OLDEST entries. Oldest rather
// than newest: if something has to be lost it should be the measurement least likely to
// still matter, and dropping the newest would mean a permanently stuck queue reporting
// ancient traffic forever.
func (r *edgeMetricsReporter) trimLocked() {
	if len(r.pending) <= edgeMetricsMaxPending {
		return
	}
	excess := len(r.pending) - edgeMetricsMaxPending
	r.pending = append([]EdgeByteDelta(nil), r.pending[excess:]...)
	r.dropped += excess
}

// Collect takes whatever the live leases have carried since the last call and queues it.
func (r *edgeMetricsReporter) Collect(registry *Registry) {
	if registry == nil {
		return
	}
	now := time.Now().UTC()
	for _, d := range registry.TakeByteDeltas() {
		r.Add(EdgeByteDelta{
			UserID:      d.UserID,
			Subdomain:   d.SubdomainPrefix,
			FullHost:    d.FullHost,
			BytesIn:     d.BytesIn,
			BytesOut:    d.BytesOut,
			ConnectedAt: d.ConnectedAt,
			RecordedAt:  now,
		})
	}
}

// Flush sends every queued delta over the supplied control connection, in chunks.
//
// A chunk that fails to send is put back at the FRONT of the queue and the rest of the
// flush is abandoned: the connection is evidently gone, and continuing would only reorder
// the backlog. Nothing is lost by the failure itself -- the next flush after the channel
// comes back sends the same bytes.
func (r *edgeMetricsReporter) Flush(conn controlWriter) {
	if conn == nil {
		return
	}

	r.mu.Lock()
	batch := r.pending
	r.pending = nil
	dropped := r.dropped
	r.dropped = 0
	r.mu.Unlock()

	if dropped > 0 {
		slog.Warn(fmt.Sprintf("[Edge Metrics] Discarded %d byte deltas that could not be reported before the queue filled; that traffic will not appear in analytics", dropped))
	}
	if len(batch) == 0 {
		return
	}

	for i := 0; i < len(batch); i += edgeMetricsMaxPerFrame {
		end := i + edgeMetricsMaxPerFrame
		if end > len(batch) {
			end = len(batch)
		}
		msg := ControlMessage{Type: "edge_metrics", Metrics: batch[i:end]}
		if err := conn.WriteJSON(msg); err != nil {
			slog.Info(fmt.Sprintf("[Edge Metrics] Failed to report %d byte deltas, keeping them for the next attempt: %v", len(batch)-i, err))
			r.requeue(batch[i:])
			return
		}
	}
	slog.Info(fmt.Sprintf("[Edge Metrics] Reported byte deltas for %d lease sample(s) to the control plane", len(batch)))
}

// requeue puts unsent deltas back at the front of the queue, ahead of anything added while
// the failed send was in flight.
func (r *edgeMetricsReporter) requeue(unsent []EdgeByteDelta) {
	if len(unsent) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = append(append([]EdgeByteDelta(nil), unsent...), r.pending...)
	r.trimLocked()
}

// pendingCount is for tests and diagnostics.
func (r *edgeMetricsReporter) pendingCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

// newEdgeMetricsReporterFor returns a reporter on an edge and nil anywhere else. The same
// pair of settings that makes a node an edge everywhere else in this package
// (control_plane_url plus edge_token, e.g. server.go's registry.SetNodeID), so there is one
// answer to "is this an edge" rather than a second, subtly different one.
func newEdgeMetricsReporterFor(cfg *config.ServerConfig) *edgeMetricsReporter {
	if cfg == nil || cfg.ControlPlaneURL == "" || cfg.EdgeToken == "" {
		return nil
	}
	return &edgeMetricsReporter{}
}

// edgeMetricsInterval is how often this edge reports, honouring the config override.
func (s *Server) edgeMetricsInterval() time.Duration {
	if s.cfg != nil && s.cfg.EdgeMetricsIntervalSeconds > 0 {
		return time.Duration(s.cfg.EdgeMetricsIntervalSeconds) * time.Second
	}
	return defaultEdgeMetricsInterval
}

// setEdgeUplink publishes (or withdraws, with nil) this edge's live control connection, so
// a graceful stop can make a final report over it.
func (s *Server) setEdgeUplink(conn *safeConn) {
	s.edgeUplinkMu.Lock()
	s.edgeUplink = conn
	s.edgeUplinkMu.Unlock()
}

// flushEdgeMetrics makes one last best-effort report. A no-op on the control plane.
//
// Whatever cannot be sent is named in the log rather than dropped quietly: an edge's
// unreported bytes have nowhere to go once the process exits, and a silent loss here is
// indistinguishable from a quiet node -- the "absence looks like zero" failure this whole
// change exists to end (#1958).
func (s *Server) flushEdgeMetrics() {
	if s.edgeMetrics == nil {
		return
	}
	s.edgeMetrics.Collect(s.registry)

	s.edgeUplinkMu.RLock()
	conn := s.edgeUplink
	s.edgeUplinkMu.RUnlock()

	if conn != nil {
		s.edgeMetrics.Flush(conn)
	}
	if remaining := s.edgeMetrics.pendingCount(); remaining > 0 {
		slog.Warn(fmt.Sprintf("[Edge Metrics] Shutting down with %d unreported byte delta(s); that traffic will be missing from analytics for this node", remaining))
	}
}

// edgeMetricRows turns reported deltas into the rows tunnel_metrics stores, stamped with the
// node that reported them.
//
// nodeID comes from the caller's authenticated identity -- the control connection's verified
// node, or the edge token presented to /api/internal/edge-metrics -- never from the payload.
// An empty one would be stored as "control" by RecordTunnelMetric, filing an edge's traffic
// under the control plane: worse than not recording it, because it is wrong rather than
// merely missing.
func edgeMetricRows(nodeID string, deltas []EdgeByteDelta) []*db.TunnelMetric {
	if nodeID == "" {
		slog.Warn("[Server Control] Refusing to record edge byte deltas with no node id; they would be attributed to the control plane")
		return nil
	}
	rows := make([]*db.TunnelMetric, 0, len(deltas))
	for _, d := range deltas {
		if d.BytesIn <= 0 && d.BytesOut <= 0 {
			continue
		}
		recordedAt := d.RecordedAt
		if recordedAt.IsZero() {
			recordedAt = time.Now().UTC()
		}
		rows = append(rows, &db.TunnelMetric{
			UserID:          d.UserID,
			SubdomainPrefix: d.Subdomain,
			FullHost:        d.FullHost,
			BytesIn:         d.BytesIn,
			BytesOut:        d.BytesOut,
			ConnectedAt:     d.ConnectedAt,
			RecordedAt:      recordedAt,
			NodeID:          nodeID,
		})
	}
	return rows
}

// applyEdgeLeaseBytes keeps the in-memory edge lease figures in step, so the dashboard's live
// bytes column moves for an edge-held tunnel rather than sitting at zero until the next
// analytics query.
func (s *Server) applyEdgeLeaseBytes(nodeID string, deltas []EdgeByteDelta) {
	s.edgeLeasesMu.Lock()
	defer s.edgeLeasesMu.Unlock()
	for _, d := range deltas {
		leases, ok := s.edgeLeases[d.UserID]
		if !ok {
			continue
		}
		for idx, el := range leases {
			if el.Subdomain == d.Subdomain && el.NodeID == nodeID {
				leases[idx].BytesIn += uint64(d.BytesIn)
				leases[idx].BytesOut += uint64(d.BytesOut)
			}
		}
	}
}

// queueEdgeMetrics is how the control plane takes in a report that arrived on the edge
// control channel.
//
// It hands the rows to MetricsCollector rather than writing them itself, because the caller
// is a WebSocket read pump: a goroutine bgWG knows nothing about, which would otherwise be
// inside database/sql when Stop closes the handle (#1833, and the gate in
// background_db_goroutines_test.go). The collector's own loop is tracked, so the write lands
// on a goroutine Stop waits for.
func (s *Server) queueEdgeMetrics(nodeID string, deltas []EdgeByteDelta) {
	if len(deltas) == 0 {
		return
	}
	rows := edgeMetricRows(nodeID, deltas)
	if len(rows) == 0 {
		return
	}
	for _, m := range rows {
		s.metrics.Queue(m)
	}
	slog.Info(fmt.Sprintf("[Server Control] Queued %d byte delta(s) reported by %s", len(rows), nodeID))
	s.applyEdgeLeaseBytes(nodeID, deltas)
}

// recordEdgeMetrics writes reported byte deltas straight to the database. Used by the legacy
// /api/internal/edge-metrics endpoint, which runs on a request goroutine rather than a
// background one and whose caller expects the write to have happened by the time it returns.
func (s *Server) recordEdgeMetrics(nodeID string, deltas []EdgeByteDelta) {
	if len(deltas) == 0 {
		return
	}
	rows := edgeMetricRows(nodeID, deltas)
	if len(rows) == 0 {
		return
	}
	if s.db != nil {
		recorded := 0
		for _, m := range rows {
			if err := s.db.RecordTunnelMetric(m); err != nil {
				// Logged, not returned: a failed write is a lost measurement, and must not
				// become a failed request or a dropped connection.
				slog.Info(fmt.Sprintf("[Server Control] Failed to record byte delta from %s for %s: %v", nodeID, m.FullHost, err))
				continue
			}
			recorded++
		}
		if recorded > 0 {
			slog.Info(fmt.Sprintf("[Server Control] Recorded %d byte delta(s) reported by %s", recorded, nodeID))
		}
	}
	s.applyEdgeLeaseBytes(nodeID, deltas)
}
