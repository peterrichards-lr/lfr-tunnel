package db

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// Did tunnels start on the closest node? (#1888)
//
// Both halves of the answer were already being stored and nothing joined them:
//
//   - region_probes (#1151): every client reports its full latency probe set at registration,
//     one row per user per region per day.
//   - tunnel_metrics: node_id records which gateway actually served each session.
//
// So this needs no new instrumentation, no log collection and no consent -- an RTT is not
// personal data in the way an IP is, which is the argument #1151 already made and the reason
// that table exists at all.
//
// THE TWO TABLES DO NOT SPEAK THE SAME DIALECT, and that is the trap. The gateway advertises
// each region under two names -- "in" and "edge-in" are the same host -- and the client's
// dedupeRegionsByHost keeps the SHORTER one, so region_probes holds "in". node_id comes from the
// edge's own token and holds "edge-in", or "control" on central. Joining on string equality
// would match nothing, score every session suboptimal, and look entirely plausible doing it.
//
// Hence normaliseNodeID below, and hence UnknownNodes: a node this cannot map is reported as
// unverifiable and NAMED, rather than being quietly counted as a miss. A report that cannot tell
// "we placed this badly" from "I could not read the node name" is worse than no report.

// PlacementVerdict is why a session was counted the way it was.
const (
	// PlacementOptimal means the node that served the session was the fastest region that
	// user measured that day.
	PlacementOptimal = "optimal"
	// PlacementSuboptimal means the user had measured something faster.
	PlacementSuboptimal = "suboptimal"
	// PlacementUnverifiable means there is nothing to compare against. The commonest cause is
	// the client's 24h region cache: a cached choice runs no probe, so no row exists for that
	// day. It is NOT evidence of a bad placement, and is counted separately for that reason.
	PlacementUnverifiable = "unverifiable"
)

// NodePlacementNode is one node's record.
type NodePlacementNode struct {
	NodeID       string `json:"node_id"`
	Sessions     int    `json:"sessions"`
	Optimal      int    `json:"optimal"`
	Suboptimal   int    `json:"suboptimal"`
	Unverifiable int    `json:"unverifiable"`
	// WorstMissMs is the largest RTT gap seen on this node: how much faster the region the
	// user should have landed on was. Zero when nothing was suboptimal.
	WorstMissMs int `json:"worst_miss_ms"`
}

// NodePlacementReport answers "are tunnels starting on the closest node".
type NodePlacementReport struct {
	Days         int                 `json:"days"`
	Sessions     int                 `json:"sessions"`
	Optimal      int                 `json:"optimal"`
	Suboptimal   int                 `json:"suboptimal"`
	Unverifiable int                 `json:"unverifiable"`
	Nodes        []NodePlacementNode `json:"nodes"`
	// UnknownNodes are node_id values that matched no region any user probed. Surfaced rather
	// than folded into the counts: this is the shape of a rename or a new edge, and it makes
	// the report wrong in a way nothing else would reveal.
	UnknownNodes []string `json:"unknown_nodes,omitempty"`
	// Caveats states in the payload what the numbers cannot mean, so a consumer rendering
	// this cannot present it as more certain than it is.
	Caveats []string `json:"caveats"`
}

// normaliseNodeID maps a tunnel_metrics node_id onto the region vocabulary region_probes uses.
//
// "edge-in" -> "in" because the client keeps the shorter of the two advertised names.
// "control" -> "central" because that is what central advertises itself as.
// Anything else is returned unchanged and will simply fail to match, which is deliberate: an
// unrecognised node should surface as unknown, not be guessed at.
func normaliseNodeID(nodeID string) string {
	n := strings.ToLower(strings.TrimSpace(nodeID))
	if n == "control" {
		return "central"
	}
	return strings.TrimPrefix(n, "edge-")
}

// measurementKey identifies one user's probe set on one day.
type measurementKey struct{ user, day string }

// loadMeasurements returns, per user and day, the best RTT that user recorded for each region.
//
// Keyed by day as well as user because a session is judged against what that user measured on
// the day it started, not against a fleet average -- someone on a hotel network should not be
// scored against the office.
func (repo *SQLiteRegionProbeRepo) loadMeasurements(since string) (map[measurementKey]map[string]int, error) {
	rows, err := repo.conn.Query(`
		SELECT user_id, day, region, rtt_ms FROM region_probes WHERE day >= ?`, since)
	if err != nil {
		return nil, fmt.Errorf("node placement (probes): %w", err)
	}
	defer func() { _ = rows.Close() }()

	measured := map[measurementKey]map[string]int{}
	for rows.Next() {
		var user, day, region string
		var rtt sql.NullInt64
		if err := rows.Scan(&user, &day, &region, &rtt); err != nil {
			return nil, fmt.Errorf("node placement (probe scan): %w", err)
		}
		if !rtt.Valid {
			// The region did not answer. Recorded by #1151 as a placement fact, but it
			// cannot be "the fastest", so it does not belong in this comparison.
			continue
		}
		k := measurementKey{user, day}
		if measured[k] == nil {
			measured[k] = map[string]int{}
		}
		region = strings.ToLower(strings.TrimSpace(region))
		if prev, seen := measured[k][region]; !seen || int(rtt.Int64) < prev {
			measured[k][region] = int(rtt.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("node placement (probes): %w", err)
	}
	return measured, nil
}

// scoreSession decides how one session counts. Returns the verdict and, for a miss, how much
// faster the region the user should have landed on was.
func scoreSession(rtts map[string]int, nodeID string) (verdict string, missMs int) {
	if len(rtts) == 0 {
		// No probe that day -- almost always the 24h region cache. Not a bad placement.
		return PlacementUnverifiable, 0
	}
	servedRTT, known := rtts[normaliseNodeID(nodeID)]
	if !known {
		// The node that served this session is not a region this user measured: a rename, a
		// new edge, or a node the probe set does not cover. The caller names it.
		return PlacementUnverifiable, 0
	}
	best := servedRTT
	for _, v := range rtts {
		if v < best {
			best = v
		}
	}
	if servedRTT <= best {
		return PlacementOptimal, 0
	}
	return PlacementSuboptimal, servedRTT - best
}

// GetNodePlacement builds the report over the last `days` days.
func (repo *SQLiteRegionProbeRepo) GetNodePlacement(days int) (*NodePlacementReport, error) {
	since := analyticsFloor(days)

	measured, err := repo.loadMeasurements(since)
	if err != nil {
		return nil, err
	}

	sessionRows, err := repo.conn.Query(`
		SELECT user_id, COALESCE(NULLIF(node_id, ''), 'control'), date(connected_at)
		FROM tunnel_metrics WHERE connected_at >= ?`, since)
	if err != nil {
		return nil, fmt.Errorf("node placement (sessions): %w", err)
	}
	defer func() { _ = sessionRows.Close() }()

	report := &NodePlacementReport{Days: days}
	perNode := map[string]*NodePlacementNode{}
	unknown := map[string]bool{}

	for sessionRows.Next() {
		var user, nodeID, day string
		if err := sessionRows.Scan(&user, &nodeID, &day); err != nil {
			return nil, fmt.Errorf("node placement (session scan): %w", err)
		}
		report.Sessions++
		node := perNode[nodeID]
		if node == nil {
			node = &NodePlacementNode{NodeID: nodeID}
			perNode[nodeID] = node
		}
		node.Sessions++

		rtts := measured[measurementKey{user, day}]
		verdict, miss := scoreSession(rtts, nodeID)
		switch verdict {
		case PlacementOptimal:
			report.Optimal++
			node.Optimal++
		case PlacementSuboptimal:
			report.Suboptimal++
			node.Suboptimal++
			if miss > node.WorstMissMs {
				node.WorstMissMs = miss
			}
		default:
			// Unverifiable. Name the node when it was the node we could not place, rather
			// than when the user simply had no probe that day.
			if len(rtts) > 0 {
				unknown[nodeID] = true
			}
			report.Unverifiable++
			node.Unverifiable++
		}
	}
	if err := sessionRows.Err(); err != nil {
		return nil, fmt.Errorf("node placement (sessions): %w", err)
	}

	for id := range unknown {
		report.UnknownNodes = append(report.UnknownNodes, id)
	}
	sort.Strings(report.UnknownNodes)

	report.Nodes = make([]NodePlacementNode, 0, len(perNode))
	for _, n := range perNode {
		report.Nodes = append(report.Nodes, *n)
	}
	sort.Slice(report.Nodes, func(i, j int) bool { return report.Nodes[i].NodeID < report.Nodes[j].NodeID })

	report.Caveats = placementCaveats(report)
	return report, nil
}

// placementCaveats states what the numbers cannot mean. Carried in the payload rather than left
// to whoever renders it, because the failure this report invites is being read as a score.
func placementCaveats(r *NodePlacementReport) []string {
	c := []string{
		"A session is compared against what that user measured on the day it started, not against a fleet average.",
		"Unverifiable is not a failure. The client caches its region choice for 24h, and a cached choice runs no probe, so there is nothing that day to compare against.",
		"region_probes holds one row per user per region per DAY, so a user who changed network mid-day has one blended measurement.",
		"An explicitly pinned region (-region or -server) is indistinguishable here from an unprobed choice.",
	}
	if len(r.UnknownNodes) > 0 {
		c = append(c, "Some sessions ran on a node that matches no probed region: "+
			strings.Join(r.UnknownNodes, ", ")+". Those are counted unverifiable, not suboptimal.")
	}
	return c
}
