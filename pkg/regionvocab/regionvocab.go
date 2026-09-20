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

// How a client chose its gateway (#1922).
//
// A PINNED client runs no latency probe at all, so it already sends an empty probe set -- which
// is indistinguishable from a client with reporting switched off, or one too old to send any.
// All three looked the same to the analytics, so "my US colleagues are on the Irish node" could
// not be answered from data and had to be asked person by person.
//
// Sent as a stable token rather than the English sentence the client prints locally: the portal
// has to translate and group these, and prose cannot be either.
const (
	// SourceProbe -- a fresh latency probe elected the gateway. The intended path.
	SourceProbe = "probe"
	// SourceCache -- a previous election was reused from the local cache, so no probe ran
	// this start. Not a problem: the cache is one hour (#1706) and a changed candidate set
	// invalidates it immediately.
	SourceCache = "cache"
	// SourceExplicitRegion -- the user named a region with -region. No probe, but failover
	// still works, so this is a preference rather than a pin.
	SourceExplicitRegion = "explicit_region"
	// SourceExplicitServer -- the user named a gateway with -server or one of the
	// LFT_SERVER* variables. Region election AND failover are off (#1691): this client
	// cannot move, whatever happens to the gateway it is on. The case worth acting on.
	SourceExplicitServer = "explicit_server"
	// SourceGiven -- the gateway was used as configured with no election, because the
	// client learned no region list at all. A gateway-side problem, not a user choice.
	SourceGiven = "given"
	// SourceFailover -- the gateway the client was on became unusable and it re-elected onto
	// this one (#2086). Without this token a failover is indistinguishable from a cold start:
	// the failover path clears cfg.Region and re-resolves, so it reported `probe` or `cache`,
	// exactly like a client that had just launched and probed its way to the same region.
	SourceFailover = "failover"
	// SourceFailback -- the client returned to the region it came from, or to the region the
	// user named, once that region was reachable again (#2086).
	//
	// Separate from SourceFailover because the pair is the thing worth measuring: a failover
	// with no matching failback is a client that never came home, and until now the server
	// could not see either half. No completed failback has ever been observed in production.
	SourceFailback = "failback"
)

// Sources is the complete vocabulary, in the order a report should present it.
var Sources = []string{
	SourceProbe, SourceCache, SourceExplicitRegion, SourceExplicitServer, SourceGiven,
	SourceFailover, SourceFailback,
}

// ValidSource reports whether a token is one this gateway understands.
//
// An unknown token is stored as-is but must not be counted as anything: a client newer than the
// gateway could send a source this build has never heard of, and silently folding it into an
// existing bucket would misreport it.
func ValidSource(s string) bool {
	for _, v := range Sources {
		if v == s {
			return true
		}
	}
	return false
}

// IsPinned reports whether a source means the client cannot move between gateways.
//
// ONLY SourceExplicitServer. -region skips the probe but keeps failover -- PinnedRoutingNotice
// actively recommends `server_url:` in the config file over `-server` for that reason, and
// treating the two alike would send someone to fix a client that is behaving correctly.
func IsPinned(source string) bool { return source == SourceExplicitServer }
