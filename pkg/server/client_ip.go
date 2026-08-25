package server

import (
	"net"
	"net/http"
	"strconv"
	"strings"
)

// Resolving the real client address behind a reverse proxy (#1325).
//
// The gateway binds loopback and nginx owns 80/443, so r.RemoteAddr is always 127.0.0.1 and the
// forwarding headers are the only source of a real client address. That makes reading them
// mandatory -- and trusting them unconditionally a mistake, because a header is only as
// trustworthy as the hop that set it.
//
// The rule: honour X-Real-IP and X-Forwarded-For only when the request arrived from an address
// in trusted_proxies. From anywhere else, the peer address is the client address and the
// headers are ignored, because whoever sent them chose their own value.
//
// This matters beyond tidiness: the resolved address drives the per-tunnel IP whitelist, the API
// rate limiter and its auto-ban, and every audit entry. Without the boundary, a deployment
// serving TLS directly (ssl_cert_file/ssl_key_file, no nginx) lets a visitor walk through an IP
// whitelist by naming an allowed address in a header.

// defaultTrustedProxies is used when the config names none. Loopback matches every documented
// deployment, where nginx runs on the same host and proxies to 127.0.0.1.
var defaultTrustedProxies = []string{"127.0.0.1/32", "::1/128"}

// parseTrustedProxies turns the configured CIDRs into matchers.
//
// A malformed entry is skipped rather than fatal. The consequence is a narrower trusted set --
// headers from that hop stop being honoured, so the address falls back to the peer -- which
// fails closed. Refusing to start would be a worse trade for a typo.
func parseTrustedProxies(entries []string) []*net.IPNet {
	if len(entries) == 0 {
		entries = defaultTrustedProxies
	}
	var nets []*net.IPNet
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		// A bare address is accepted as a single-host range, since writing "127.0.0.1" rather
		// than "127.0.0.1/32" is the obvious mistake to make here.
		if !strings.Contains(entry, "/") {
			if ip := net.ParseIP(entry); ip != nil {
				bits := 32
				if ip.To4() == nil {
					bits = 128
				}
				entry = entry + "/" + strconv.Itoa(bits)
			}
		}
		if _, network, err := net.ParseCIDR(entry); err == nil {
			nets = append(nets, network)
		}
	}
	return nets
}

func isTrustedProxy(ip string, trusted []*net.IPNet) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}
	for _, network := range trusted {
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}

// peerAddress is who actually opened the connection, with any port removed.
func peerAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		return r.RemoteAddr
	}
	return host
}

// clientIPFrom resolves the client address for a request, given the set of proxies whose
// forwarding headers may be believed.
func clientIPFrom(r *http.Request, trusted []*net.IPNet) string {
	peer := peerAddress(r)

	// Not behind a proxy we trust, so the headers are whatever the caller decided to send.
	if !isTrustedProxy(peer, trusted) {
		return peer
	}

	// X-Real-IP is a single value that nginx sets to $remote_addr, overwriting anything the
	// client sent, so it is unambiguous where it is present.
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}

	// X-Forwarded-For is client-first: "client, proxy1, proxy2". nginx's
	// $proxy_add_x_forwarded_for APPENDS the peer to whatever arrived, so the leftmost entry is
	// supplied by the caller and forgeable. Walk from the right instead and take the first
	// address that is not itself a trusted proxy -- that is the nearest hop we did not vouch
	// for, which is the closest thing to the real client we can honestly claim.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			candidate := strings.TrimSpace(parts[i])
			if candidate == "" {
				continue
			}
			if !isTrustedProxy(candidate, trusted) {
				return candidate
			}
		}
	}

	return peer
}

// clientIP resolves the client address for a request reaching the gateway.
func (s *Server) clientIP(r *http.Request) string {
	return clientIPFrom(r, s.trustedProxies)
}

// clientIP resolves the client address for a request reaching the visitor-facing proxy.
func (p *ProxyHandler) clientIP(r *http.Request) string {
	return clientIPFrom(r, p.trustedProxies)
}

// trustsNonLoopbackProxies reports whether any trusted range reaches beyond the local host.
// Loopback-only is safe even without a proxy in front, because a request cannot arrive from
// off-box claiming a loopback peer address.
func (s *Server) trustsNonLoopbackProxies() bool {
	for _, network := range s.trustedProxies {
		if !network.IP.IsLoopback() {
			return true
		}
	}
	return false
}
