package server

import (
	"fmt"
	"net/http"
	"strings"
)

// Live edits to a running tunnel from the portal: its injected headers and its rate limit
// (#2530).
//
// Both used to write to central's own registry and nothing else, so for a tunnel served by an
// edge -- most of them -- each answered 404 "Active tunnel not found". And the header edit
// replaced the whole map with what the portal sent, which since #2137/#2150 is built from header
// NAMES (values are routinely credentials and never leave the gateway). So Add sent one header
// and wiped the rest, and Remove sent none and wiped them all.
//
// The fix has the same shape for both: the change is described rather than the result, and it is
// applied by whichever gateway holds the lease. A header edit is a patch -- set these, remove
// those -- applied to the map the gateway already has, so nobody ever has to read a value back
// to write one.

// leaseHeaderPatch is one header edit. Set adds or replaces, Remove deletes; matching is by
// canonical name, which is what the proxy's req.Header.Set does with the key anyway.
type leaseHeaderPatch struct {
	Set    map[string]string `json:"set,omitempty"`
	Remove []string          `json:"remove,omitempty"`
}

// maxLeaseHeaderValue caps one injected value. Generous for a bearer token or a cookie; it is a
// bound on a portal form field, not a protocol limit.
const maxLeaseHeaderValue = 4096

// refusedLeaseHeaders are names a portal edit may not inject: framing and hop-by-hop headers the
// proxy itself owns. Setting Host or Content-Length on a forwarded request is request smuggling
// at worst and a broken request at best.
var refusedLeaseHeaders = map[string]bool{
	"Host":              true,
	"Content-Length":    true,
	"Transfer-Encoding": true,
	"Connection":        true,
	"Keep-Alive":        true,
	"Proxy-Connection":  true,
	"Te":                true,
	"Trailer":           true,
	"Upgrade":           true,
}

// validLeaseHeaderName reports whether name is an RFC 9110 token.
func validLeaseHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", c):
		default:
			return false
		}
	}
	return true
}

// validateLeaseHeaders checks names and values that are about to be injected. A CR or LF in a
// value is a header-splitting attempt; Go's transport would refuse it later, but later is after
// the edit has been reported as saved.
func validateLeaseHeaders(set map[string]string) error {
	for name, value := range set {
		if !validLeaseHeaderName(name) {
			return fmt.Errorf("%q is not a valid header name", name)
		}
		if refusedLeaseHeaders[http.CanonicalHeaderKey(name)] {
			return fmt.Errorf("%s is managed by the gateway and cannot be injected", http.CanonicalHeaderKey(name))
		}
		if len(value) > maxLeaseHeaderValue {
			return fmt.Errorf("the value for %s is longer than %d bytes", name, maxLeaseHeaderValue)
		}
		if strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("the value for %s contains a line break or NUL", name)
		}
	}
	return nil
}

func (p leaseHeaderPatch) empty() bool { return len(p.Set) == 0 && len(p.Remove) == 0 }

func (p leaseHeaderPatch) validate() error {
	if p.empty() {
		return fmt.Errorf("nothing to change: give headers to set or names to remove")
	}
	return validateLeaseHeaders(p.Set)
}

// apply returns a NEW map: cur with p applied. Never mutates cur -- the proxy ranges over a
// lease's map while serving, and ListLeases hands out the same reference, so an edit is
// published by swapping the map, not by writing into it.
func (p leaseHeaderPatch) apply(cur map[string]string) map[string]string {
	drop := map[string]bool{}
	for _, name := range p.Remove {
		drop[http.CanonicalHeaderKey(name)] = true
	}
	for name := range p.Set {
		// A set replaces any existing spelling of the same header, so "x-api-key" from the
		// client's -header flag and "X-Api-Key" from the portal do not both get injected.
		drop[http.CanonicalHeaderKey(name)] = true
	}
	out := make(map[string]string, len(cur)+len(p.Set))
	for name, value := range cur {
		if !drop[http.CanonicalHeaderKey(name)] {
			out[name] = value
		}
	}
	for name, value := range p.Set {
		out[http.CanonicalHeaderKey(name)] = value
	}
	return out
}

// applyToNames is apply for central's cached copy of an edge lease, which holds names only.
func (p leaseHeaderPatch) applyToNames(names []string) []string {
	cur := make(map[string]string, len(names))
	for _, n := range names {
		cur[n] = ""
	}
	return sortedHeaderNames(p.apply(cur))
}

// PatchLeaseHeaders applies p to an active lease's injected headers.
func (r *Registry) PatchLeaseHeaders(fullHost string, p leaseHeaderPatch) error {
	r.Lock()
	defer r.Unlock()
	lease, ok := r.leases[fullHost]
	if !ok {
		return fmt.Errorf("active tunnel host %q not found", fullHost)
	}
	lease.AddedHeaders = p.apply(lease.AddedHeaders)
	return nil
}

// findEdgeLease returns central's record of the edge-served lease for fullHost.
func (s *Server) findEdgeLease(fullHost string) (EdgeLease, bool) {
	s.edgeLeasesMu.Lock()
	defer s.edgeLeasesMu.Unlock()
	for _, leases := range s.edgeLeases {
		for _, el := range leases {
			if strings.EqualFold(el.FullHost, fullHost) {
				return el, true
			}
		}
	}
	return EdgeLease{}, false
}

// updateEdgeLease edits central's cached record of an edge lease in place, so both portals show
// an edit at once instead of after the client next re-registers.
func (s *Server) updateEdgeLease(fullHost string, edit func(*EdgeLease)) {
	s.edgeLeasesMu.Lock()
	defer s.edgeLeasesMu.Unlock()
	for _, leases := range s.edgeLeases {
		for i := range leases {
			if strings.EqualFold(leases[i].FullHost, fullHost) {
				edit(&leases[i])
			}
		}
	}
}

// sendEdgeControl writes one frame to an edge's control connection. False when the edge is not
// connected or the write failed.
func (s *Server) sendEdgeControl(nodeID string, msg ControlMessage) bool {
	s.edgeClientsMu.RLock()
	conn, ok := s.edgeClients[nodeID]
	s.edgeClientsMu.RUnlock()
	if !ok {
		return false
	}
	return conn.WriteJSON(msg) == nil
}

// The two frames below are new types rather than new fields on lease_headers. An edge running an
// older build logs a type it does not know and changes nothing; the same edge handed a
// lease_headers frame with no Headers map would have replaced the tunnel's headers with nothing.
const (
	edgeFrameLeaseHeadersPatch = "lease_headers_patch"
	edgeFrameLeaseRateLimit    = "lease_rate_limit"
)

// canEditLease reports whether user may edit a tunnel owned by ownerID.
func canEditLease(userID, role, ownerID string) bool {
	return userID == ownerID || role == "admin" || role == "owner"
}
