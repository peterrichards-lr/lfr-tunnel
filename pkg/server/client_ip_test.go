package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"lfr-tunnel/pkg/config"
)

// The resolved client address drives the per-tunnel IP whitelist, the API rate limiter and its
// auto-ban, and every audit entry. A header is only as trustworthy as the hop that set it, so
// these tests pin where the boundary is (#1325).

func newReq(peer string, headers map[string]string) *http.Request {
	r := httptest.NewRequest("GET", "http://example.com/", nil)
	r.RemoteAddr = peer
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestClientIP_TrustsHeadersFromALoopbackProxy(t *testing.T) {
	trusted := parseTrustedProxies(nil) // defaults to loopback

	r := newReq("127.0.0.1:34567", map[string]string{"X-Real-IP": "203.0.113.9"})

	if got := clientIPFrom(r, trusted); got != "203.0.113.9" {
		t.Errorf("got %q, want the header value -- nginx on loopback is the documented deployment", got)
	}
}

// The case the issue exists for: without a boundary, a visitor reaching the gateway directly
// walks through an IP whitelist by naming an allowed address.
func TestClientIP_IgnoresHeadersFromAnUntrustedPeer(t *testing.T) {
	trusted := parseTrustedProxies(nil)

	r := newReq("198.51.100.7:44444", map[string]string{
		"X-Real-IP":       "203.0.113.9",
		"X-Forwarded-For": "203.0.113.9",
	})

	if got := clientIPFrom(r, trusted); got != "198.51.100.7" {
		t.Errorf("got %q, want the peer address -- a spoofed header from an untrusted peer was believed", got)
	}
}

// nginx's $proxy_add_x_forwarded_for appends, so the leftmost entry is whatever the caller sent.
// Taking it would hand the caller their own answer.
func TestClientIP_TakesTheRightmostUntrustedForwardedEntry(t *testing.T) {
	trusted := parseTrustedProxies(nil)

	r := newReq("127.0.0.1:34567", map[string]string{
		"X-Forwarded-For": "1.2.3.4, 203.0.113.9",
	})

	if got := clientIPFrom(r, trusted); got != "203.0.113.9" {
		t.Errorf("got %q, want the rightmost untrusted entry -- the caller-supplied prefix was believed", got)
	}
}

func TestClientIP_SkipsTrustedHopsWalkingRightToLeft(t *testing.T) {
	trusted := parseTrustedProxies([]string{"127.0.0.1/32", "10.0.0.0/8"})

	r := newReq("127.0.0.1:34567", map[string]string{
		"X-Forwarded-For": "203.0.113.9, 10.1.1.1, 10.2.2.2",
	})

	if got := clientIPFrom(r, trusted); got != "203.0.113.9" {
		t.Errorf("got %q, want the first entry that is not one of our own proxies", got)
	}
}

func TestClientIP_PrefersRealIPOverForwardedFor(t *testing.T) {
	trusted := parseTrustedProxies(nil)

	r := newReq("127.0.0.1:34567", map[string]string{
		"X-Real-IP":       "203.0.113.9",
		"X-Forwarded-For": "1.2.3.4",
	})

	if got := clientIPFrom(r, trusted); got != "203.0.113.9" {
		t.Errorf("got %q, want X-Real-IP -- it is the single value nginx overwrites", got)
	}
}

func TestClientIP_FallsBackToThePeerWhenNoHeaders(t *testing.T) {
	trusted := parseTrustedProxies(nil)

	r := newReq("127.0.0.1:34567", nil)

	if got := clientIPFrom(r, trusted); got != "127.0.0.1" {
		t.Errorf("got %q, want the peer address with the port stripped", got)
	}
}

// Every entry in the chain being one of ours means we never received a real client address.
// Claiming one anyway would be inventing it.
func TestClientIP_AllHopsTrustedFallsBackToThePeer(t *testing.T) {
	trusted := parseTrustedProxies([]string{"127.0.0.1/32", "10.0.0.0/8"})

	r := newReq("127.0.0.1:34567", map[string]string{"X-Forwarded-For": "10.1.1.1, 10.2.2.2"})

	if got := clientIPFrom(r, trusted); got != "127.0.0.1" {
		t.Errorf("got %q, want the peer address", got)
	}
}

func TestParseTrustedProxies(t *testing.T) {
	t.Run("empty falls back to loopback", func(t *testing.T) {
		nets := parseTrustedProxies(nil)
		if !isTrustedProxy("127.0.0.1", nets) || !isTrustedProxy("::1", nets) {
			t.Error("the default must cover loopback, which is every documented deployment")
		}
		if isTrustedProxy("203.0.113.9", nets) {
			t.Error("the default must not trust anything else")
		}
	})

	t.Run("a bare address is accepted as a single host", func(t *testing.T) {
		nets := parseTrustedProxies([]string{"10.1.2.3"})
		if !isTrustedProxy("10.1.2.3", nets) {
			t.Error("a bare address should be read as a /32 -- it is the obvious way to write this")
		}
		if isTrustedProxy("10.1.2.4", nets) {
			t.Error("a bare address must not widen to its network")
		}
	})

	t.Run("a malformed entry fails closed", func(t *testing.T) {
		nets := parseTrustedProxies([]string{"not-a-cidr", "10.0.0.0/8"})
		if !isTrustedProxy("10.1.2.3", nets) {
			t.Error("a malformed entry stopped a later valid one from being used")
		}
		if isTrustedProxy("203.0.113.9", nets) {
			t.Error("a malformed entry must not trust everything")
		}
	})
}

func TestTrustsNonLoopbackProxies(t *testing.T) {
	loopbackOnly := &Server{trustedProxies: parseTrustedProxies(nil)}
	if loopbackOnly.trustsNonLoopbackProxies() {
		t.Error("the default is loopback only")
	}

	wider := &Server{trustedProxies: parseTrustedProxies([]string{"127.0.0.1/32", "10.0.0.0/8"})}
	if !wider.trustsNonLoopbackProxies() {
		t.Error("a non-loopback range should be reported, so the direct-TLS warning fires")
	}
}

// The resolver has to be reachable from both the API surface and the visitor-facing proxy,
// since both make access-control decisions on the address.
func TestClientIP_ReachableFromBothServerAndProxyHandler(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.TrustedProxies = []string{"127.0.0.1/32"}

	s := &Server{trustedProxies: parseTrustedProxies(cfg.TrustedProxies)}
	p := NewProxyHandler(nil, cfg)

	r := newReq("127.0.0.1:34567", map[string]string{"X-Real-IP": "203.0.113.9"})

	if got := s.clientIP(r); got != "203.0.113.9" {
		t.Errorf("Server.clientIP = %q", got)
	}
	if got := p.clientIP(r); got != "203.0.113.9" {
		t.Errorf("ProxyHandler.clientIP = %q", got)
	}
}

// #1357: the audit log recorded r.RemoteAddr, which behind nginx is always loopback with an
// ephemeral port. 4,896 of 4,951 rows in the production database looked like "127.0.0.1:47060".
func TestWriteAudit_RecordsTheResolvedClientIP(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.ControlPlaneURL = "" // no forwarding, no database: exercise resolution alone

	s := &Server{
		cfg:            cfg,
		trustedProxies: parseTrustedProxies(nil),
	}

	r := newReq("127.0.0.1:47060", map[string]string{"X-Real-IP": "203.0.113.9"})

	// With no database and no control plane URL the entry is dropped, so assert on the
	// resolution the writer would use rather than on a stored row.
	if got := s.clientIP(r); got != "203.0.113.9" {
		t.Fatalf("clientIP = %q, want the forwarded address", got)
	}
	if got := peerAddress(r); got != "127.0.0.1" {
		t.Fatalf("peerAddress = %q; the old code recorded this, with its port", got)
	}
	if r.RemoteAddr == s.clientIP(r) {
		t.Error("the audit path must not record the peer address behind a proxy")
	}
}
