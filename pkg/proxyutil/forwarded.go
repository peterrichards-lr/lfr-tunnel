// Package proxyutil holds the pieces of httputil.ReverseProxy behaviour that a Rewrite
// function has to perform for itself.
//
// ReverseProxy.Director is deprecated as of Go 1.26, but Rewrite is not a drop-in
// replacement. ReverseProxy does two things for a Director that it deliberately does not
// do for a Rewrite:
//
//   - It leaves the inbound Forwarded / X-Forwarded-For / X-Forwarded-Host /
//     X-Forwarded-Proto headers on the outbound request. Before calling Rewrite it deletes
//     all four, so that a proxy which does not opt in cannot be made to forward a chain
//     the client made up.
//   - It appends the connecting peer's IP to X-Forwarded-For afterwards.
//
// ProxyRequest.SetXForwarded covers the second point, but it also overwrites
// X-Forwarded-Host and X-Forwarded-Proto with *this* hop's view of the world. That is
// right for an edge proxy and wrong for the two in this repo that are not one: the client
// interceptor, whose local server must still see the public host and scheme the gateway
// set, and the cross-node proxy, which must honour what the first gateway already
// recorded. These helpers reproduce the Director behaviour exactly instead, so migrating a
// Director to a Rewrite changes no header the backend sees.
package proxyutil

import (
	"net"
	"net/http/httputil"
	"strings"
)

// headerXForwardedFor is the one forwarded header these helpers build a value for, rather
// than merely copying, so it is named once.
const headerXForwardedFor = "X-Forwarded-For"

// forwardedHeaders are the four headers ReverseProxy strips from the outbound request
// before invoking a Rewrite function.
var forwardedHeaders = []string{
	"Forwarded",
	headerXForwardedFor,
	"X-Forwarded-Host",
	"X-Forwarded-Proto",
}

// RestoreInboundForwarded copies those four headers back from the inbound request onto the
// outbound one, so a Rewrite function starts from the state a Director would have seen.
//
// Call it before any logic that reads or conditionally sets a forwarding header; a Rewrite
// that inspects pr.Out.Header for one without restoring first always finds it absent, and
// "set it only if the upstream did not" then silently becomes "always set it".
func RestoreInboundForwarded(pr *httputil.ProxyRequest) {
	for _, h := range forwardedHeaders {
		v, ok := pr.In.Header[h]
		if !ok {
			continue
		}
		// A present-but-nil value is meaningful (see AppendPeerToXForwardedFor), and
		// append to a nil slice with no elements preserves it.
		pr.Out.Header[h] = append([]string(nil), v...)
	}
}

// AppendPeerToXForwardedFor appends the immediate peer's IP address to the outbound
// X-Forwarded-For, folding any existing values into one comma-separated list. This is what
// ReverseProxy itself does after calling a Director, and does not do for a Rewrite.
//
// Call it last, so it extends whatever chain the rewrite settled on rather than a chain
// later code overwrites.
func AppendPeerToXForwardedFor(pr *httputil.ProxyRequest) {
	peer, err := peerIP(pr)
	if err != nil {
		// No usable peer address. ReverseProxy leaves the header alone in this case.
		return
	}

	prior, suppressed := priorXForwardedFor(pr)
	if suppressed {
		return
	}
	appendXForwardedFor(pr, prior, peer)
}

// EnsureVisitorInXForwardedFor extends the outbound X-Forwarded-For with the resolved
// visitor address, unless that address is already accounted for.
//
// It exists because both server proxies knew the visitor's address (resolved through the
// trusted-proxy boundary in pkg/server/client_ip.go) and wrote it into X-Forwarded-For
// themselves, and AppendPeerToXForwardedFor then added the connecting peer on top. For a
// visitor connecting directly the two are the same address, so every such request carried
// a duplicated last hop -- "192.0.2.1, 192.0.2.1" (#1737).
//
// Two cases are skipped, and skipping them is the whole fix:
//
//   - The visitor IS the peer. AppendPeerToXForwardedFor is about to contribute exactly
//     this address, and it contributes the only entry in the chain that cannot be forged,
//     since it comes from the connection rather than from a header.
//   - The chain already ends with the visitor, because a trusted upstream recorded it.
//     Naming a hop twice in a row describes a hop that does not exist.
//
// It deliberately does NOT decide what the chain to the left contains. A caller that must
// not believe an inbound chain has to discard it before calling this; see the lease proxy
// in pkg/server/proxy.go, which does exactly that.
//
// Call it before AppendPeerToXForwardedFor, which closes the chain with the peer.
func EnsureVisitorInXForwardedFor(pr *httputil.ProxyRequest, visitorIP string) {
	visitorIP = strings.TrimSpace(visitorIP)
	if visitorIP == "" {
		return
	}

	prior, suppressed := priorXForwardedFor(pr)
	if suppressed {
		return
	}
	if peer, err := peerIP(pr); err == nil && peer == visitorIP {
		return
	}
	if lastXForwardedForEntry(prior) == visitorIP {
		return
	}
	appendXForwardedFor(pr, prior, visitorIP)
}

// peerIP is the address of whoever opened the connection, with the port removed. It is
// read from the INBOUND request: pr.Out is a clone whose RemoteAddr ReverseProxy clears.
func peerIP(pr *httputil.ProxyRequest) (string, error) {
	ip, _, err := net.SplitHostPort(pr.In.RemoteAddr)
	return ip, err
}

// priorXForwardedFor returns the outbound chain so far. suppressed reports Go issue 38079's
// opt-out: a header present in the map with a nil value means "do not populate
// X-Forwarded-For at all", and no helper here may override that.
func priorXForwardedFor(pr *httputil.ProxyRequest) (prior []string, suppressed bool) {
	prior, ok := pr.Out.Header[headerXForwardedFor]
	return prior, ok && prior == nil
}

// lastXForwardedForEntry is the rightmost address in the chain -- the hop nearest to us.
// Values arrive either as repeated header lines or as one comma-separated line, so both
// have to be unwrapped.
func lastXForwardedForEntry(prior []string) string {
	if len(prior) == 0 {
		return ""
	}
	last := prior[len(prior)-1]
	if i := strings.LastIndex(last, ","); i >= 0 {
		last = last[i+1:]
	}
	return strings.TrimSpace(last)
}

// appendXForwardedFor writes prior + value back as a single comma-separated header value,
// which is what ReverseProxy does when it folds a multi-valued chain.
func appendXForwardedFor(pr *httputil.ProxyRequest, prior []string, value string) {
	if len(prior) > 0 {
		value = strings.Join(prior, ", ") + ", " + value
	}
	pr.Out.Header.Set(headerXForwardedFor, value)
}
