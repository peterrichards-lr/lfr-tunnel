package server

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Telling "this edge is quiet" from "this edge stopped reporting" (#1980).
//
// Nothing recorded that an edge was DELIVERING metrics. EdgeHealthStatus carried reachability
// -- Status, LastCheckAt, Version, ResolvedIP -- so an edge could hold live leases, serve
// traffic, report nothing, and still show green. Both ends were silent on success as well, so
// an absence of log lines was equally consistent with "never ran" and "worked every time".
// #1958 took a day to characterise for exactly that reason, and three hypotheses were refuted
// before the data showed nothing had been broken at all.
//
// The fix has two halves and NEITHER works alone:
//
//  1. an edge with no traffic now sends an EMPTY metrics frame each interval (edge_metrics.go),
//     so silence means a broken path rather than a quiet one;
//  2. the control plane stamps when each node last delivered a frame, below, so silence is
//     something it can see.
//
// Without (1) this would flag every idle edge; without (2) the heartbeat would arrive and be
// forgotten.

// edgeMetricsDelivery is what the control plane knows about one node's reporting.
type edgeMetricsDelivery struct {
	// LastFrameAt is when a frame last arrived, empty ones included. This, not the last
	// non-empty frame, is the liveness signal: a node with no traffic still heartbeats.
	LastFrameAt time.Time
	// LastDataAt is when a frame last carried actual deltas, kept apart so the portal can say
	// "reporting, no traffic" rather than implying idleness is a fault.
	LastDataAt time.Time
	Frames     uint64
	Deltas     uint64
	// Alerted stops one stall producing an alert every sweep. Cleared when a frame arrives,
	// so a node that recovers and stalls again is reported again.
	Alerted bool
}

// edgeMetricsTracker records per-node delivery.
type edgeMetricsTracker struct {
	mu sync.RWMutex
	by map[string]edgeMetricsDelivery
}

func newEdgeMetricsTracker() *edgeMetricsTracker {
	return &edgeMetricsTracker{by: map[string]edgeMetricsDelivery{}}
}

// Note records that nodeID delivered a frame carrying deltaCount deltas (0 for a heartbeat).
func (t *edgeMetricsTracker) Note(nodeID string, deltaCount int, now time.Time) {
	if t == nil || nodeID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	d := t.by[nodeID]
	d.LastFrameAt = now
	d.Frames++
	// A frame is a recovery: clear the latch so a later stall alerts again.
	d.Alerted = false
	if deltaCount > 0 {
		d.LastDataAt = now
		d.Deltas += uint64(deltaCount)
	}
	t.by[nodeID] = d
}

// Get returns what is known about a node, and whether anything is known at all.
func (t *edgeMetricsTracker) Get(nodeID string) (edgeMetricsDelivery, bool) {
	if t == nil {
		return edgeMetricsDelivery{}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	d, ok := t.by[nodeID]
	return d, ok
}

// Forget drops a node's record when its control channel closes.
//
// Otherwise a node that legitimately went away -- a scheduled power-off -- would keep its last
// frame time and start reporting as stalled, which is the false alarm that gets a signal muted.
func (t *edgeMetricsTracker) Forget(nodeID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.by, nodeID)
}

// edgeMetricsStaleAfter is how long without a frame counts as stalled.
//
// A multiple of the reporting interval, not a fixed duration: the interval is configurable, and
// a threshold that does not move with it would either alarm constantly on a slow setting or
// never fire on a fast one. Three intervals tolerates one lost frame and a late one without
// crying wolf -- a signal that fires spuriously gets switched off, which is worse than no signal.
const edgeMetricsStaleIntervals = 3

// edgeMetricsStalled reports whether a node that SHOULD be reporting has stopped.
//
// Pure, so every branch is reachable in a test -- including the ones that must NOT fire, which
// are the half that matters. A detector that always says "stalled" passes the obvious test.
//
// connected: the node holds a live control channel. A node that is simply gone is Offline, and
// saying "stalled" about it adds nothing.
//
// everReported: at least one frame has arrived since it connected. An edge running a build
// older than the heartbeat never sends one, and must read as "unknown", never as broken --
// otherwise every node reports stalled for the length of a rollout and the signal is discredited
// on the day it ships.
func edgeMetricsStalled(now time.Time, d edgeMetricsDelivery, connected, everReported bool, interval time.Duration) bool {
	if !connected || !everReported || d.LastFrameAt.IsZero() {
		return false
	}
	if interval <= 0 {
		interval = defaultEdgeMetricsInterval
	}
	return now.Sub(d.LastFrameAt) > time.Duration(edgeMetricsStaleIntervals)*interval
}

// describeEdgeMetricsHealth renders the delivery state for the portal.
//
// "Not reporting" and "reporting, no traffic" are different sentences on purpose: the second is
// a normal state for a quiet node and must never look like a fault, or operators learn to
// ignore the field.
func describeEdgeMetricsHealth(now time.Time, d edgeMetricsDelivery, connected, everReported bool, interval time.Duration) string {
	if !connected {
		return ""
	}
	if !everReported {
		return "no bandwidth reports seen yet from this node"
	}
	if edgeMetricsStalled(now, d, connected, everReported, interval) {
		return "bandwidth reporting has stopped: last report " +
			formatAgo(now.Sub(d.LastFrameAt)) + " ago, which is more than " +
			formatAgo(time.Duration(edgeMetricsStaleIntervals)*interval)
	}
	if d.LastDataAt.IsZero() {
		return "reporting, no traffic measured yet"
	}
	return ""
}

// formatAgo renders a duration for an operator rather than for a machine.
func formatAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return d.Round(time.Second).String()
	case d < time.Hour:
		return d.Round(time.Minute).String()
	default:
		return d.Round(time.Minute).String()
	}
}

// MarkAlerted latches a node as already reported, returning false if it was latched already.
//
// The caller alerts only on a true return, so a stall that lasts an hour produces one message
// rather than one per sweep. An operator who is emailed every 30 seconds filters the sender.
func (t *edgeMetricsTracker) MarkAlerted(nodeID string) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	d, ok := t.by[nodeID]
	if !ok || d.Alerted {
		return false
	}
	d.Alerted = true
	t.by[nodeID] = d
	return true
}

// watchEdgeMetricsDelivery alerts when a connected edge stops reporting bandwidth (#1980).
//
// A sweep rather than a check at receipt time, because the event being detected is the ABSENCE
// of a frame -- there is no arrival to hang it off. That is the whole reason this failure was
// invisible: nothing happens when reporting dies.
func (s *Server) watchEdgeMetricsDelivery(ctx context.Context) {
	interval := s.edgeMetricsInterval()
	// Swept every interval, judged against edgeMetricsStaleIntervals of them, so detection
	// lags a stall by at most one sweep.
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepEdgeMetricsDelivery(time.Now())
		}
	}
}

// sweepEdgeMetricsDelivery is the body of the watch, separated so a test can drive it with an
// explicit clock instead of waiting on a ticker.
func (s *Server) sweepEdgeMetricsDelivery(now time.Time) {
	interval := s.edgeMetricsInterval()

	s.edgeClientsMu.RLock()
	connected := make([]string, 0, len(s.edgeClients))
	for nodeID := range s.edgeClients {
		connected = append(connected, nodeID)
	}
	s.edgeClientsMu.RUnlock()

	for _, nodeID := range connected {
		d, everReported := s.edgeMetricsSeen.Get(nodeID)
		if !edgeMetricsStalled(now, d, true, everReported, interval) {
			continue
		}
		if !s.edgeMetricsSeen.MarkAlerted(nodeID) {
			continue
		}
		since := formatAgo(now.Sub(d.LastFrameAt))
		slog.Warn(fmt.Sprintf("[Edge Metrics] %s holds a control channel but has not reported bandwidth for %s", nodeID, since))
		s.sendAdminAlert(
			"alert_notify_edge_metrics_stalled",
			"LFR Tunnel Alert: Edge stopped reporting bandwidth",
			fmt.Sprintf("Edge node %s is connected to the control plane but has not delivered a "+
				"bandwidth report for %s.\n\n"+
				"Traffic served by this node is not being recorded, so analytics understate it and "+
				"any quota sized from this data will not see it.\n\n"+
				"The node is still reachable -- this is about reporting, not availability.", nodeID, since),
		)
	}
}
