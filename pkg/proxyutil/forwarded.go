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

// forwardedHeaders are the four headers ReverseProxy strips from the outbound request
// before invoking a Rewrite function.
var forwardedHeaders = []string{
	"Forwarded",
	"X-Forwarded-For",
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
	clientIP, _, err := net.SplitHostPort(pr.In.RemoteAddr)
	if err != nil {
		// No usable peer address. ReverseProxy leaves the header alone in this case.
		return
	}

	prior, ok := pr.Out.Header["X-Forwarded-For"]
	if ok && prior == nil {
		// Go issue 38079: a header present in the map with a nil value means "do not
		// populate X-Forwarded-For at all".
		return
	}
	if len(prior) > 0 {
		clientIP = strings.Join(prior, ", ") + ", " + clientIP
	}
	pr.Out.Header.Set("X-Forwarded-For", clientIP)
}
