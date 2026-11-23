package db

import (
	"fmt"
	"lfr-tunnel/pkg/regionvocab"
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

// Renamed from TestControlMapsToCentral, which asserted the #1919 bug and is why it shipped.
//
// That test seeded a probe for the region "central" -- a value NO client ever writes. The
// gateway advertises central as both "eu" and "central", and the client keeps the shorter, so
// region_probes holds "eu". The fixture described a state production cannot reach, the code
// agreed with the fixture, and both disagreed with reality.
//
// Seeding the region a real client records is what makes this a test of the join rather than a
// test of itself.
func TestControlIsMatchedAgainstTheRegionClientsRecord(t *testing.T) {
	d := setupTestDB(t)
	day := today()
	seedProbe(t, d, "u1", day, regionvocab.CentralRegion(), ms(15))
	seedProbe(t, d, "u1", day, "in", ms(120))
	seedSession(t, d, "u1", regionvocab.CentralNodeID, day+" 10:00:00")

	rep, err := d.GetNodePlacement(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Optimal != 1 {
		t.Fatalf("a session central served, on the fastest region that user measured, was not "+
			"counted optimal: %+v", rep)
	}
	if len(rep.UnknownNodes) != 0 {
		t.Errorf("control was reported as matching no probed region: %v -- this is the 0%%"+
			" report (#1919)", rep.UnknownNodes)
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

// #1919: normaliseNodeID mapped central's node id to "central", a name the client discards in
// favour of "eu". Every central-served session therefore matched no probed region, was counted
// unverifiable, and the report read 0% closest while naming "control" as an unknown node.
func TestCentralsNodeIDMapsToTheRegionClientsActuallyRecord(t *testing.T) {
	got := normaliseNodeID(regionvocab.CentralNodeID)
	if got != regionvocab.CentralRegion() {
		t.Fatalf("normaliseNodeID(%q) = %q, want %q -- the region name clients write to "+
			"region_probes", regionvocab.CentralNodeID, got, regionvocab.CentralRegion())
	}
	if got == "central" {
		t.Error(`"central" is the alias the client DISCARDS (it is longer than "eu"), so ` +
			"matching against it can never succeed")
	}
}

func TestNodeIDPrefixesAreStripped(t *testing.T) {
	cases := map[string]string{
		"edge-in":     "in",
		"EDGE-US":     "us",
		"  edge-sa  ": "sa",
		// addEdgeRegionNames strips "aws-" as well when it builds region names, so a node
		// carrying the provisioner prefix has to be stripped the same way or it lands in
		// UnknownNodes for a reason unrelated to placement.
		"aws-edge-apac": "apac",
		"aws-in":        "in",
		// Deliberately unchanged: an unrecognised node surfaces as unknown rather than
		// being guessed at.
		"something-else": "something-else",
	}
	for in, want := range cases {
		if got := normaliseNodeID(in); got != want {
			t.Errorf("normaliseNodeID(%q) = %q, want %q", in, got, want)
		}
	}
}

// seedSample writes one five-minute tunnel_metrics sample, the shape MetricsCollector and the
// edge reporter actually write: one row per lease per interval with bytes, all carrying the
// lease's own connected_at and differing only in recorded_at and the byte delta.
func seedSample(t *testing.T, d *DB, user, nodeID, fullHost, connectedAt, recordedAt string) {
	t.Helper()
	if _, err := d.conn.Exec(
		`INSERT INTO tunnel_metrics (user_id, subdomain_prefix, full_host, bytes_in, bytes_out, connected_at, recorded_at, node_id)
		 VALUES (?,?,?,100,100,?,?,?)`,
		user, strings.SplitN(fullHost, ".", 2)[0], fullHost, connectedAt, recordedAt, nodeID); err != nil {
		t.Fatalf("seeding sample: %v", err)
	}
}

// #2566. A tunnel_metrics row is a five-minute SAMPLE, not a session. Scoring every row made
// "sessions" an order of magnitude too high (1,845 against the gateway table's 62 for EDGE-US)
// and weighted the closest percentage by how long a tunnel stayed busy: here one long session
// placed well would read as 5 of 6 optimal, when it is one of two.
func TestSamplesOfOneSessionAreScoredOnce(t *testing.T) {
	d := setupTestDB(t)
	day := today()
	seedProbe(t, d, "u1", day, "in", ms(20))
	seedProbe(t, d, "u1", day, "us", ms(200))

	// One long session on edge-in: five samples, one connected_at.
	connected := day + " 10:00:00"
	for i := 0; i < 5; i++ {
		seedSample(t, d, "u1", "edge-in", "long.example.se", connected,
			fmt.Sprintf("%s 10:%02d:00", day, 5*(i+1)))
	}
	// One short session on the slower edge-us: a single sample.
	seedSample(t, d, "u1", "edge-us", "short.example.se", day+" 11:00:00", day+" 11:05:00")

	rep, err := d.GetNodePlacement(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Sessions != 2 {
		t.Fatalf("sessions = %d, want 2 -- samples are being counted as sessions: %+v", rep.Sessions, rep)
	}
	if rep.Optimal != 1 || rep.Suboptimal != 1 || rep.Unverifiable != 0 {
		t.Errorf("verdicts = optimal %d / suboptimal %d / unverifiable %d, want 1/1/0 -- each "+
			"session must be scored once, not once per sample", rep.Optimal, rep.Suboptimal, rep.Unverifiable)
	}
	for _, n := range rep.Nodes {
		if n.Sessions != 1 {
			t.Errorf("node %s sessions = %d, want 1", n.NodeID, n.Sessions)
		}
	}
}

// #2567. The region was probed and DID NOT ANSWER (rtt NULL), and the tunnel later ran on that
// region's node anyway. That is a placement fact, not a vocabulary problem: "edge-us" maps to
// "us" perfectly well. Naming it in UnknownNodes is how the 2026-11-10 export came to say
// "Sessions ran on a node matching no probed region: control, edge-us".
//
// The fixture is a state production writes: RecordRegionProbes stores a nil RTTMs as NULL
// under the short region name the client keeps ("us"), and the edge's node id is "edge-us".
func TestAnUnreachableProbeIsNotAnUnknownNode(t *testing.T) {
	d := setupTestDB(t)
	day := today()
	seedProbe(t, d, "u1", day, "in", ms(20))
	seedProbe(t, d, "u1", day, "us", nil) // probed, no answer
	seedSession(t, d, "u1", "edge-us", day+" 10:00:00")

	rep, err := d.GetNodePlacement(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(rep.UnknownNodes) != 0 {
		t.Errorf("a node whose region was probed but did not answer was named as matching no "+
			"probed region: %v", rep.UnknownNodes)
	}
	if rep.Unverifiable != 1 || rep.Optimal != 0 || rep.Suboptimal != 0 {
		t.Errorf("the session cannot be scored without an RTT and must stay unverifiable: %+v", rep)
	}
	if rep.UnreachableAtProbe != 1 {
		t.Errorf("unreachable_at_probe = %d, want 1", rep.UnreachableAtProbe)
	}
	if len(rep.Nodes) != 1 || rep.Nodes[0].UnreachableAtProbe != 1 {
		t.Errorf("per-node unreachable_at_probe not counted: %+v", rep.Nodes)
	}
	joined := fmt.Sprint(rep.Caveats)
	if strings.Contains(joined, "matches no probed region") {
		t.Errorf("the caveats still call it an unknown node: %v", rep.Caveats)
	}
	if !strings.Contains(joined, "did not answer") {
		t.Errorf("the caveats do not explain the unreachable-at-probe count: %v", rep.Caveats)
	}
}

// Both cases in one user-day: the unreachable region must not be named, and a genuinely
// unknown node name must STILL be -- the fix must narrow UnknownNodes to vocabulary
// mismatches, not silence it.
func TestAGenuinelyUnknownNodeIsStillNamedBesideAnUnreachableOne(t *testing.T) {
	d := setupTestDB(t)
	day := today()
	seedProbe(t, d, "u1", day, "in", ms(20))
	seedProbe(t, d, "u1", day, "us", nil)
	seedSession(t, d, "u1", "edge-us", day+" 10:00:00")
	seedSession(t, d, "u1", "edge-atlantis", day+" 11:00:00")

	rep, err := d.GetNodePlacement(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(rep.UnknownNodes) != 1 || rep.UnknownNodes[0] != "edge-atlantis" {
		t.Errorf("unknown_nodes = %v, want exactly [edge-atlantis]", rep.UnknownNodes)
	}
	if rep.UnreachableAtProbe != 1 || rep.Unverifiable != 2 {
		t.Errorf("unreachable_at_probe = %d (want 1), unverifiable = %d (want 2)",
			rep.UnreachableAtProbe, rep.Unverifiable)
	}
	if !strings.Contains(fmt.Sprint(rep.Caveats), "edge-atlantis") {
		t.Errorf("the caveats no longer name the unknown node: %v", rep.Caveats)
	}
}

// A user whose ONLY probe that day failed has an empty RTT set, which reads as "no probe that
// day". It was a probe, and the session ran on the region that did not answer it.
func TestAnUnreachableProbeCountsEvenWithNoOtherAnswer(t *testing.T) {
	d := setupTestDB(t)
	day := today()
	seedProbe(t, d, "u1", day, "us", nil)
	seedSession(t, d, "u1", "edge-us", day+" 10:00:00")

	rep, err := d.GetNodePlacement(30)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Unverifiable != 1 || rep.UnreachableAtProbe != 1 || len(rep.UnknownNodes) != 0 {
		t.Errorf("unverifiable=%d unreachable_at_probe=%d unknown=%v, want 1/1/[]",
			rep.Unverifiable, rep.UnreachableAtProbe, rep.UnknownNodes)
	}
}
