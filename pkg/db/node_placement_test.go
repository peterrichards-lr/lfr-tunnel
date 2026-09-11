package db

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Tests for the node placement report (#1888).
//
// The defect this guards is not an arithmetic slip -- it is a report that looks credible and is
// wrong. The two tables it joins use different names for the same node ("in" in region_probes,
// "edge-in" in tunnel_metrics), so the natural implementation scores EVERY session suboptimal
// and reads as a fleet-wide placement failure. Several cases below exist only to keep that
// from coming back.

func seedProbe(t *testing.T, d *DB, user, day, region string, rtt *int) {
	t.Helper()
	if _, err := d.conn.Exec(
		`INSERT OR REPLACE INTO region_probes (user_id, region, day, rtt_ms) VALUES (?,?,?,?)`,
		user, region, day, rtt); err != nil {
		t.Fatalf("seeding probe: %v", err)
	}
}

func seedSession(t *testing.T, d *DB, user, nodeID, connectedAt string) {
	t.Helper()
	if _, err := d.conn.Exec(
		`INSERT INTO tunnel_metrics (user_id, subdomain_prefix, full_host, bytes_in, bytes_out, connected_at, node_id)
		 VALUES (?,?,?,0,0,?,?)`,
		user, "sub", "sub.example.se", connectedAt, nodeID); err != nil {
		t.Fatalf("seeding session: %v", err)
	}
}

func today() string { return time.Now().UTC().Format("2006-01-02") }

// THE CASE THAT MATTERS. region_probes says "in"; tunnel_metrics says "edge-in". They are the
// same node. A join on string equality scores this suboptimal and the whole report becomes a
// fiction.
func TestEdgePrefixedNodeIDMatchesTheProbedRegion(t *testing.T) {
	d := setupTestDB(t)
	day := today()
	seedProbe(t, d, "u1", day, "in", ms(20))
	seedProbe(t, d, "u1", day, "us", ms(200))
	seedSession(t, d, "u1", "edge-in", day+" 10:00:00")

	rep, err := d.GetNodePlacement(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Optimal != 1 || rep.Suboptimal != 0 {
		t.Fatalf("edge-in was not matched to the probed region \"in\": optimal=%d suboptimal=%d unverifiable=%d",
			rep.Optimal, rep.Suboptimal, rep.Unverifiable)
	}
	if len(rep.UnknownNodes) != 0 {
		t.Errorf("edge-in was reported as an unknown node: %v", rep.UnknownNodes)
	}
}

func TestControlMapsToCentral(t *testing.T) {
	d := setupTestDB(t)
	day := today()
	seedProbe(t, d, "u1", day, "central", ms(15))
	seedProbe(t, d, "u1", day, "in", ms(120))
	seedSession(t, d, "u1", "control", day+" 10:00:00")

	rep, err := d.GetNodePlacement(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Optimal != 1 {
		t.Fatalf("control was not matched to \"central\": %+v", rep)
	}
}

// FIRING. A genuine misplacement must be reported as one, with the size of the miss.
func TestASlowerNodeIsReportedSuboptimalWithTheGap(t *testing.T) {
	d := setupTestDB(t)
	day := today()
	seedProbe(t, d, "u1", day, "in", ms(20))
	seedProbe(t, d, "u1", day, "us", ms(200))
	seedSession(t, d, "u1", "edge-us", day+" 10:00:00")

	rep, err := d.GetNodePlacement(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Suboptimal != 1 || rep.Optimal != 0 {
		t.Fatalf("a session on the slower node was not reported suboptimal: %+v", rep)
	}
	if len(rep.Nodes) != 1 || rep.Nodes[0].WorstMissMs != 180 {
		t.Errorf("worst miss = %v, want 180ms (200 - 20)", rep.Nodes)
	}
}

// BOUNDING. The commonest real case, and it must NOT be counted as a failure: the client's 24h
// cache means a cached choice runs no probe, so there is nothing to compare against.
func TestNoProbeThatDayIsUnverifiableNotSuboptimal(t *testing.T) {
	d := setupTestDB(t)
	day := today()
	seedSession(t, d, "u1", "edge-us", day+" 10:00:00")

	rep, err := d.GetNodePlacement(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Unverifiable != 1 || rep.Suboptimal != 0 {
		t.Fatalf("a session with no probe was not counted unverifiable: %+v", rep)
	}
}

// A node nothing probed must be NAMED, not silently scored. This is the shape of a rename or a
// new edge, and it is the one way the report can be wrong without anything else revealing it.
func TestAnUnmappableNodeIsSurfacedNotCountedAsAMiss(t *testing.T) {
	d := setupTestDB(t)
	day := today()
	seedProbe(t, d, "u1", day, "in", ms(20))
	seedSession(t, d, "u1", "edge-atlantis", day+" 10:00:00")

	rep, err := d.GetNodePlacement(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Suboptimal != 0 {
		t.Errorf("an unmappable node was counted as a misplacement: %+v", rep)
	}
	if rep.Unverifiable != 1 {
		t.Errorf("an unmappable node was not counted unverifiable: %+v", rep)
	}
	found := false
	for _, n := range rep.UnknownNodes {
		if n == "edge-atlantis" {
			found = true
		}
	}
	if !found {
		t.Errorf("the unmappable node was not named: %v", rep.UnknownNodes)
	}
	// And the caveat must say so, since that is what a reader sees.
	joined := fmt.Sprint(rep.Caveats)
	if !strings.Contains(joined, "edge-atlantis") {
		t.Errorf("the caveats do not mention the unmappable node: %v", rep.Caveats)
	}
}

// A region that did not answer is recorded by #1151 as a placement fact, but it cannot be "the
// fastest" -- treating a NULL rtt as 0 would make every session look suboptimal against it.
func TestAnUnreachableRegionIsNotTreatedAsFastest(t *testing.T) {
	d := setupTestDB(t)
	day := today()
	seedProbe(t, d, "u1", day, "in", ms(20))
	seedProbe(t, d, "u1", day, "sa", nil) // no answer
	seedSession(t, d, "u1", "edge-in", day+" 10:00:00")

	rep, err := d.GetNodePlacement(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Optimal != 1 {
		t.Fatalf("an unreachable region was treated as the fastest: %+v", rep)
	}
}

// PREMISE. An empty database must produce an empty report rather than an error or a
// confident-looking zero-session verdict.
func TestEmptyDatabaseProducesAnEmptyReport(t *testing.T) {
	d := setupTestDB(t)
	rep, err := d.GetNodePlacement(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Sessions != 0 || len(rep.Nodes) != 0 {
		t.Fatalf("empty database produced %+v", rep)
	}
	if len(rep.Caveats) == 0 {
		t.Error("the report carries no caveats, so a consumer could read it as a score")
	}
}

// The per-user join must not leak across users: one user's measurements cannot judge another's
// session.
func TestOneUsersProbesDoNotJudgeAnothersSession(t *testing.T) {
	d := setupTestDB(t)
	day := today()
	seedProbe(t, d, "u1", day, "in", ms(20))
	seedSession(t, d, "u2", "edge-us", day+" 10:00:00")

	rep, err := d.GetNodePlacement(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Suboptimal != 0 {
		t.Fatalf("u2's session was judged against u1's measurements: %+v", rep)
	}
	if rep.Unverifiable != 1 {
		t.Fatalf("u2's session should be unverifiable: %+v", rep)
	}
}
