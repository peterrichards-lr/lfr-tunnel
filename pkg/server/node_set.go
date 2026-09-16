package server

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	"lfr-tunnel/pkg/regionvocab"
)

// Telling a RUNNING client that the set of gateways changed (#1937).
//
// A client elects a gateway once, at startup, and then only ever reconsiders when its own
// gateway dies (failover) or when the primary it already elected comes back (failback). A node
// APPEARING is invisible to it. Edges run 08:00-00:00 in their own timezone, so a developer who
// starts before their nearest edge wakes elects the best of what answered -- often the control
// plane on another continent -- and stays there for the whole working day.
//
// THE TRANSPORT IS THE HEARTBEAT, for the reasons diagnostics_transport.go sets out at length:
// a client sits behind NAT, so the gateway cannot initiate anything to it, and /api/tunnel-status
// is the one gateway-to-client channel it already reads and already acts on. Nothing new is
// opened or polled.
//
// WHAT TRAVELS IS A FINGERPRINT, NOT A LIST, and not an instruction:
//
//   - The client reads the body under io.LimitReader(resp.Body, 512) (#1763). The live region
//     map is ~380 bytes on this deployment before any of the other fields, so the list does not
//     fit and would truncate the JSON, taking the shutdown warning and the diagnostics command
//     down with it. Twelve hex characters do fit.
//   - DECLARATIVE ONLY. diagnostics_transport.go records why the command channel carries exactly
//     one verb: "a general 'run this' channel to every client is a foothold, and an admin account
//     is not a safe place to put one." A fingerprint is state, not a verb -- the gateway says what
//     its roster hashes to, and every decision about whether to move, where to, and whether the
//     move is worth the interruption is taken entirely on the client, from its own measurements.
//     A hostile gateway can at most make its own clients re-probe.
//
// The client stores the first fingerprint it is given for a session and compares each later one
// against it, so it never has to agree with this function about how the hash is computed. That
// keeps the wire contract to "this value changes when the roster changes", which is the only
// property either side relies on.

// nodeSetFingerprintField is the heartbeat body key. Short, for the 512-byte budget, and
// deliberately not "status" -- the client's gatewayHasNoLease reads a top-level "status" as
// "this gateway holds no lease for me" and would tear down the tunnel it is asking about.
const nodeSetFingerprintField = "nodes"

// nodeSetFingerprintLen is how much of the digest is sent. Twelve hex characters is 48 bits,
// which for a set that changes a handful of times a day makes an undetected collision -- two
// different rosters hashing alike, so a client is never told the topology moved -- less likely
// than the client missing the change some other way. The cost of the full digest is 52 more
// bytes of a 512-byte budget already shared with two other messages.
const nodeSetFingerprintLen = 12

// nodeSetFingerprint hashes the gateways this node is currently advertising as available, or
// returns "" when this node holds no roster to fingerprint.
//
// The empty case is not a corner: an EDGE holds no roster. Measured against production, an edge
// answers /api/version with `regions` naming only itself under the central aliases and an empty
// `regions_unavailable`, because s.edgeNodes() is empty there and central never pushes the node
// list down the control channel. An edge therefore cannot say anything true about the topology,
// and saying nothing is the only honest answer -- a fingerprint derived from an edge's own view
// would be stable while the real roster changed underneath it, which is worse than silence
// because the client would trust it. Clients served by an edge are covered by #1960.
func (s *Server) nodeSetFingerprint() string {
	if len(s.edgeNodes()) == 0 {
		return ""
	}
	regions, _ := s.advertisedRegions()
	if len(regions) == 0 {
		return ""
	}

	names := make([]string, 0, len(regions))
	for name := range regions {
		names = append(names, name)
	}
	sort.Strings(names)

	// Both halves of each entry, separated by a byte that cannot occur in either, so that a
	// rename and a re-address are equally visible and no two distinct maps can produce the
	// same byte stream by running fields together.
	h := sha256.New()
	for _, name := range names {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write([]byte(regions[name]))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:nodeSetFingerprintLen]
}

// advertisedRegions returns the gateways a client may elect from, and those that are configured
// but currently down.
//
// Lifted out of the /api/version handler so the fingerprint above is computed from THE SAME
// values the client actually fetches, rather than from a second reading of the same config that
// could drift from it. Two functions that build the roster independently is exactly the shape
// CLAUDE.md/#1412 is about: they agree until the day they do not, and the failure is a client
// told the topology changed when it has not, or not told when it has.
func (s *Server) advertisedRegions() (regions, unavailable map[string]string) {
	regions = make(map[string]string)
	if len(s.cfg.Domains) > 0 {
		// Configured verbatim where set, because the construction below assumes both
		// the scheme and the hostname prefix. A deployment that is neither https nor
		// tunnel.<domain> was handed a URL that does not answer, and clients failing
		// over to it retried every attempt against the same dead address (#1286).
		centralURL := s.cfg.CentralURL
		if centralURL == "" {
			centralURL = "https://tunnel." + s.cfg.Domains[0]
		}
		// Derived from the declared vocabulary, not written out here (#1919).
		// These two names and the survivor of them have to agree with what the
		// analytics matches central's sessions against; when they were written
		// by hand in three places, one of them picked the wrong survivor.
		for _, alias := range regionvocab.SortedCentralAliases() {
			regions[alias] = centralURL
		}
	}
	// An edge that is configured but currently down is reported separately rather
	// than simply left out (#1690). Omitting it left the client unable to tell "every
	// region answered" from "a region exists but is asleep": the absent edge was not
	// unreachable, it was invisible, so an election made inside an edge's scheduled
	// power-off window looked complete and was cached for the full 24h -- stranding
	// the client on a distant gateway long after the edge came back.
	unavailable = make(map[string]string)
	s.edgeClientsMu.RLock()
	for _, edge := range s.edgeNodes() {
		if edge.URL == "" {
			continue
		}
		target := unavailable
		if _, isUp := s.edgeClients[edge.ID]; isUp {
			target = regions
		}
		addEdgeRegionNames(target, edge.ID, edge.URL)
	}
	s.edgeClientsMu.RUnlock()
	return regions, unavailable
}
