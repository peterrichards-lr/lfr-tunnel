package server

import (
	"strings"
	"testing"
	"time"
)

// Telling a quiet edge from a broken one (#1980).
//
// The firing case is the easy half. The controls below are the ones that decide whether this is
// a signal or a nuisance: a detector that reports "stalled" for an idle node, or for a node
// running an older build, gets muted within a week -- and a muted alert is worse than none,
// because it looks like coverage.

const testInterval = 30 * time.Second

func delivered(at time.Time, withData bool) edgeMetricsDelivery {
	d := edgeMetricsDelivery{LastFrameAt: at, Frames: 1}
	if withData {
		d.LastDataAt = at
		d.Deltas = 1
	}
	return d
}

// FIRING. The defect: a connected edge that has stopped delivering frames.
func TestAConnectedEdgeThatStopsReportingIsStalled(t *testing.T) {
	now := time.Now()
	d := delivered(now.Add(-4*testInterval), true)
	if !edgeMetricsStalled(now, d, true, true, testInterval) {
		t.Fatal("an edge silent for four intervals was not reported as stalled")
	}
	note := describeEdgeMetricsHealth(now, d, true, true, testInterval)
	if !strings.Contains(note, "reporting has stopped") {
		t.Fatalf("the note does not say reporting stopped: %q", note)
	}
}

// CONTROL. An edge reporting normally must NOT be stalled. Without this, every case here would
// pass on a detector that always says stalled.
func TestAReportingEdgeIsNotStalled(t *testing.T) {
	now := time.Now()
	if edgeMetricsStalled(now, delivered(now.Add(-5*time.Second), true), true, true, testInterval) {
		t.Fatal("CONTROL: a healthy edge was reported as stalled -- the firing case proves nothing")
	}
	if note := describeEdgeMetricsHealth(now, delivered(now, true), true, true, testInterval); note != "" {
		t.Fatalf("CONTROL: a healthy edge carries a warning note: %q", note)
	}
}

// CONTROL, and the reason the edge heartbeats at all. An idle edge sends an EMPTY frame every
// interval, so it stays live here. Before the heartbeat it sent nothing and would have been
// indistinguishable from a dead one -- which is why absence alone could never be the signal.
func TestAnIdleEdgeHeartbeatsAndIsNotStalled(t *testing.T) {
	now := time.Now()
	idle := delivered(now.Add(-2*time.Second), false) // a frame, but no traffic in it
	if edgeMetricsStalled(now, idle, true, true, testInterval) {
		t.Fatal("CONTROL: an idle but heartbeating edge was reported as stalled")
	}
	note := describeEdgeMetricsHealth(now, idle, true, true, testInterval)
	if strings.Contains(note, "stopped") {
		t.Fatalf("an idle edge reads as a fault: %q", note)
	}
	if !strings.Contains(note, "no traffic") {
		t.Fatalf("an idle edge should be described as idle, got %q", note)
	}
}

// CONTROL. An edge older than the heartbeat never sends one. Reporting every such node as
// stalled would discredit the alert for the length of a rollout, on the day it ships.
func TestAnEdgeThatHasNeverReportedIsUnknownNotStalled(t *testing.T) {
	now := time.Now()
	if edgeMetricsStalled(now, edgeMetricsDelivery{}, true, false, testInterval) {
		t.Fatal("CONTROL: a node that never reported was called stalled rather than unknown")
	}
	note := describeEdgeMetricsHealth(now, edgeMetricsDelivery{}, true, false, testInterval)
	if !strings.Contains(note, "no bandwidth reports seen yet") {
		t.Fatalf("an unseen node should say so plainly, got %q", note)
	}
}

// CONTROL. A disconnected node is Offline, and saying "its metrics stopped" adds nothing --
// it would also fire on every scheduled power-off, which edge-us and edge-sa have nightly.
func TestADisconnectedEdgeIsNotReportedAsStalled(t *testing.T) {
	now := time.Now()
	d := delivered(now.Add(-10*testInterval), true)
	if edgeMetricsStalled(now, d, false, true, testInterval) {
		t.Fatal("CONTROL: an offline node was reported as stalled, which would alarm on every power window")
	}
	if note := describeEdgeMetricsHealth(now, d, false, true, testInterval); note != "" {
		t.Fatalf("CONTROL: a disconnected node carries a metrics note: %q", note)
	}
}

// BOUNDING. The threshold moves with the configured interval rather than being a fixed wall
// clock, or a slow setting alarms constantly and a fast one never fires.
func TestTheStaleThresholdScalesWithTheInterval(t *testing.T) {
	now := time.Now()
	gap := 4 * time.Minute
	d := delivered(now.Add(-gap), true)
	if !edgeMetricsStalled(now, d, true, true, time.Minute) {
		t.Fatal("four minutes of silence on a one-minute interval is not stalled")
	}
	if edgeMetricsStalled(now, d, true, true, 10*time.Minute) {
		t.Fatal("four minutes of silence on a ten-minute interval was called stalled")
	}
}

// The tracker treats an empty frame as liveness -- that is the heartbeat's entire purpose --
// while keeping "has traffic" separate so the portal can word the two differently.
func TestAnEmptyFrameCountsAsLivenessButNotAsTraffic(t *testing.T) {
	tr := newEdgeMetricsTracker()
	now := time.Now()
	tr.Note("edge-us", 0, now)

	d, ok := tr.Get("edge-us")
	if !ok || d.LastFrameAt.IsZero() {
		t.Fatal("an empty heartbeat did not register as a delivery")
	}
	if !d.LastDataAt.IsZero() || d.Deltas != 0 {
		t.Fatal("an empty heartbeat was counted as traffic")
	}

	tr.Note("edge-us", 3, now.Add(time.Second))
	d, _ = tr.Get("edge-us")
	if d.LastDataAt.IsZero() || d.Deltas != 3 {
		t.Fatalf("a frame with deltas was not recorded as traffic: %+v", d)
	}
}

// One stall must produce ONE alert. An operator emailed every sweep filters the sender, and
// then the next real alert is invisible.
func TestAStallAlertsOnceAndAgainAfterRecovery(t *testing.T) {
	tr := newEdgeMetricsTracker()
	now := time.Now()
	tr.Note("edge-sa", 1, now)

	if !tr.MarkAlerted("edge-sa") {
		t.Fatal("the first stall did not alert")
	}
	if tr.MarkAlerted("edge-sa") {
		t.Fatal("the same stall alerted twice -- this is what gets an alert muted")
	}

	// A frame is a recovery, so a later stall is a new episode and alerts again.
	tr.Note("edge-sa", 0, now.Add(time.Minute))
	if !tr.MarkAlerted("edge-sa") {
		t.Fatal("a stall after a recovery did not alert, so a flapping node reports once forever")
	}
}

// Dropping the record with the connection is what stops a scheduled power-off looking like a
// fault when the node comes back.
func TestForgettingANodeClearsItsHistory(t *testing.T) {
	tr := newEdgeMetricsTracker()
	tr.Note("edge-apac", 2, time.Now())
	tr.Forget("edge-apac")
	if _, ok := tr.Get("edge-apac"); ok {
		t.Fatal("a forgotten node kept its delivery record, so it would return as stalled")
	}
}
