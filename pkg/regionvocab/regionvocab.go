// Package regionvocab declares the region naming rules that the gateway, the client and the
// analytics all have to agree on (#1919).
//
// The gateway advertises every region under more than one name -- "in" and "edge-in" are the
// same host, and central answers to both "eu" and "central". The client collapses those to one
// name per host before probing, and whichever name survives is what ends up in region_probes.
// Anything that later wants to match a stored region name has to reach the SAME answer.
//
// Three places were deciding that independently and one of them was wrong: the gateway wrote
// the alias pair by hand (server.go), the client picked a survivor by its own rule
// (dedupeRegionsByHost), and pkg/db guessed the survivor was "central" when the rule actually
// yields "eu". Every session served by central therefore matched no probed region, was counted
// as unverifiable, and the node placement report read 0% closest with "control" named as an
// unknown node.
//
// So the rule and the alias list live here, once, and the three consumers derive from them.
package regionvocab

import "sort"

// CentralAliases are the names central advertises for itself, all resolving to the same URL.
//
// Declared here rather than written into the regions map by hand so that anything needing to
// know "what does central call itself" cannot answer differently from the gateway.
var CentralAliases = []string{"eu", "central"}

// CentralNodeID is the node_id central records in tunnel_metrics for sessions it served.
//
// It is NOT one of the aliases above, which is the whole reason a mapping is needed: joining
// tunnel_metrics.node_id against region_probes.region on string equality matches nothing for
// central, and does so silently.
const CentralNodeID = "control"

// Canonical returns the name that survives deduplication among names for one host.
//
// The rule -- shortest, then alphabetically first -- is the client's, because the client is
// what writes region_probes and therefore decides the vocabulary everything else must match.
// Restating it here rather than leaving it in cmd/lfr-tunnel means the analytics can apply the
// same rule without importing a main package.
//
// Note it yields "eu" over "central" and "in" over "edge-in". The first of those is easy to get
// wrong by eye, and did get wrong: "central" reads like the canonical name and is the longer
// string, so the rule discards it.
func Canonical(names []string) string {
	if len(names) == 0 {
		return ""
	}
	best := ""
	for _, n := range names {
		if best == "" || len(n) < len(best) || (len(n) == len(best) && n < best) {
			best = n
		}
	}
	return best
}

// CentralRegion is the region name central's sessions must be matched against -- the survivor
// among its own aliases.
func CentralRegion() string { return Canonical(CentralAliases) }

// SortedCentralAliases returns the aliases in a stable order, for callers that build maps or
// render them and should not depend on declaration order.
func SortedCentralAliases() []string {
	out := append([]string(nil), CentralAliases...)
	sort.Strings(out)
	return out
}
