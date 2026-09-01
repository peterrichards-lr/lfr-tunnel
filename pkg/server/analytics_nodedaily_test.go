package server

import (
	"testing"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// Sessions per Gateway must show every known gateway, not only those that carried sessions
// (#1648).
//
// The query behind NodeDaily groups the rows that exist, so a gateway with no sessions in the
// window produces no rows and gets no line -- it vanishes rather than flatlining. For an edge on
// a power schedule that is most of the time, which is how it came to look as though the panel
// filtered on running state. It never did; there was simply nothing to plot.
//
// A missing line and a zero line mean different things, and rendering them identically hides
// exactly what #1150 built the panel to show.
func TestPadNodeDailyFillsMissingGateways(t *testing.T) {
	existing := []db.NodeDailySession{
		{Date: "2026-09-01", NodeID: "control", Sessions: 5},
		{Date: "2026-09-02", NodeID: "control", Sessions: 3},
		{Date: "2026-09-02", NodeID: "edge-in", Sessions: 1},
	}
	known := []string{"control", "edge-in", "edge-us"}

	got := padNodeDaily(existing, known)

	// 2 dates x 3 gateways.
	if len(got) != 6 {
		t.Fatalf("expected 6 points (2 dates x 3 gateways), got %d: %+v", len(got), got)
	}

	index := make(map[string]int, len(got))
	for _, p := range got {
		index[p.Date+"|"+p.NodeID] = p.Sessions
	}

	// The real values must survive untouched -- padding must not overwrite anything.
	if index["2026-09-01|control"] != 5 {
		t.Errorf("real value overwritten: control on 09-01 = %d, want 5", index["2026-09-01|control"])
	}
	if index["2026-09-02|edge-in"] != 1 {
		t.Errorf("real value overwritten: edge-in on 09-02 = %d, want 1", index["2026-09-02|edge-in"])
	}

	// edge-us carried nothing at all and is the gateway that used to disappear entirely.
	for _, d := range []string{"2026-09-01", "2026-09-02"} {
		if v, ok := index[d+"|edge-us"]; !ok {
			t.Errorf("edge-us missing on %s -- a gateway with no sessions must flatline, not vanish", d)
		} else if v != 0 {
			t.Errorf("edge-us on %s = %d, want 0", d, v)
		}
	}

	// edge-in was absent on the first day only.
	if v, ok := index["2026-09-01|edge-in"]; !ok || v != 0 {
		t.Errorf("edge-in on 09-01: got (%d, present=%v), want (0, true)", v, ok)
	}
}

// An empty series must stay empty, so the UI can say "no session data recorded yet" rather than
// drawing flat lines through a period that has no data at all.
func TestPadNodeDailyLeavesAnEmptySeriesEmpty(t *testing.T) {
	if got := padNodeDaily(nil, []string{"control", "edge-in"}); len(got) != 0 {
		t.Errorf("an empty series must stay empty, got %d points: %+v", len(got), got)
	}
	if got := padNodeDaily([]db.NodeDailySession{}, []string{"control"}); len(got) != 0 {
		t.Errorf("an empty series must stay empty, got %d points", len(got))
	}
}

// No known gateways (no edges configured, somehow no control) must not discard real data.
func TestPadNodeDailyWithNoKnownNodesReturnsInput(t *testing.T) {
	existing := []db.NodeDailySession{{Date: "2026-09-01", NodeID: "control", Sessions: 2}}
	got := padNodeDaily(existing, nil)
	if len(got) != 1 || got[0].Sessions != 2 {
		t.Errorf("input must be returned untouched, got %+v", got)
	}
}

// Sorted by date then node, matching the query's own ORDER BY, so the UI's date axis does not
// depend on insertion order.
func TestPadNodeDailyIsSorted(t *testing.T) {
	existing := []db.NodeDailySession{
		{Date: "2026-09-02", NodeID: "edge-us", Sessions: 1},
		{Date: "2026-09-01", NodeID: "control", Sessions: 1},
	}
	got := padNodeDaily(existing, []string{"control", "edge-us"})

	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		if prev.Date > cur.Date || (prev.Date == cur.Date && prev.NodeID > cur.NodeID) {
			t.Fatalf("not sorted at %d: %+v then %+v", i, prev, cur)
		}
	}
}

// knownNodeIDs must report the configured edges plus the control plane, read through the
// reload-aware accessor rather than the startup config (#1309).
func TestKnownNodeIDs(t *testing.T) {
	srv := &Server{}
	setEdgeNodesForTest(t, srv, []config.EdgeNodeConfig{
		{ID: "edge-in"},
		{ID: "edge-us"},
	})

	got := srv.knownNodeIDs()

	want := map[string]bool{"control": false, "edge-in": false, "edge-us": false}
	for _, id := range got {
		if _, ok := want[id]; !ok {
			t.Errorf("unexpected node %q", id)
			continue
		}
		if want[id] {
			t.Errorf("node %q reported twice", id)
		}
		want[id] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("missing node %q", id)
		}
	}
}

// An edge declared with the reserved "control" id must not produce a duplicate line -- the
// metrics query coalesces an empty node_id to "control", so both would collapse onto one series
// and the padding would fight itself.
func TestKnownNodeIDsDoesNotDuplicateControl(t *testing.T) {
	srv := &Server{}
	setEdgeNodesForTest(t, srv, []config.EdgeNodeConfig{
		{ID: "control"},
		{ID: ""},
		{ID: "edge-in"},
	})

	got := srv.knownNodeIDs()

	counts := map[string]int{}
	for _, id := range got {
		counts[id]++
	}
	if counts["control"] != 1 {
		t.Errorf("control appears %d times, want exactly 1: %v", counts["control"], got)
	}
	if counts[""] != 0 {
		t.Errorf("an empty id must not be reported as a gateway: %v", got)
	}
}
