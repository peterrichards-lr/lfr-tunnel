package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

// The width rule on the gateway's OWN trust boundary (#1801).
//
// #1792 established the rule -- a forwarded-header trust entry must name exactly one host, or lie
// entirely inside address space no internet host can occupy -- and enforced it on the four places
// pkg/ops accepts or inspects one. This is the fifth: trusted_proxies decides whose X-Real-IP /
// X-Forwarded-For clientIPFrom will believe AT ALL, it takes CIDRs by design, and it accepted any
// parseable one. `trusted_proxies: ["0.0.0.0/0"]` started normally and honoured a forged header
// from every caller on the internet, whatever nginx in front of it did -- and only
// `lfr-tunnel-ops check-config` would ever have said so.

// widthTestConfig is the smallest config NewServer will accept, so the only thing these tests can
// be reporting on is trusted_proxies.
func widthTestConfig(t *testing.T, trusted ...string) *config.ServerConfig {
	t.Helper()
	return &config.ServerConfig{
		Domains:                []string{"example.com"},
		DisableBackupScheduler: true,
		DBPath:                 filepath.Join(t.TempDir(), "width.db"),
		TrustedProxies:         trusted,
	}
}

func TestNewServer_RefusesAnOverWideTrustedProxy(t *testing.T) {
	for _, entry := range []string{"0.0.0.0/0", "::/0", "203.0.113.0/24", "10.0.0.0/7"} {
		t.Run(entry, func(t *testing.T) {
			srv, err := NewServer(widthTestConfig(t, "127.0.0.1/32", entry))
			if err == nil {
				srv.Stop()
				t.Fatalf("trusted_proxies %s lets any caller inside the range choose its own "+
					"client address and must be refused", entry)
			}
			// Assert the cause. NewServer also fails on a chisel init error, an unopenable
			// database and a bad CA path, all of them non-nil errors -- so "err != nil" is
			// satisfied by any of them, and by a t.TempDir() that could not be created. Only the
			// width refusal names the entry, the key and the guarantee the range reopens.
			if !strings.Contains(err.Error(), entry) {
				t.Errorf("the refusal must name the entry, got %q", err)
			}
			if !strings.Contains(err.Error(), "trusted_proxies") {
				t.Errorf("the refusal must name the key so the operator knows which file to edit, got %q", err)
			}
			if !strings.Contains(err.Error(), "#1325") {
				t.Errorf("the refusal must say which guarantee the range reopens, got %q", err)
			}
			if srv != nil {
				t.Error("a refused config must not yield a server")
			}
		})
	}
}

// The other half of the rule. Without this the guard could be "refuse every prefix", or "refuse
// everything", and every refusal above would still pass.
//
// Accepted has to mean TRUSTED, not merely "no error": a guard that dropped the entry on its way
// through would satisfy an error-only assertion while leaving the gateway attributing every
// visitor to nginx -- which is the outage this refusal exists to avoid, arrived at silently.
func TestNewServer_AcceptsExactAndPrivateTrustedProxies(t *testing.T) {
	cfg := widthTestConfig(t, "127.0.0.1/32", "10.20.0.0/16", "203.0.113.7")

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("an exact address and a private subnet are both within the rule: %v", err)
	}
	defer srv.Stop()

	for _, addr := range []string{"127.0.0.1", "10.20.5.6", "203.0.113.7"} {
		if !isTrustedProxy(addr, srv.trustedProxies) {
			t.Errorf("%s was configured and must be trusted, not merely not-refused", addr)
		}
		// Both consumers of the same config value, so a guard applied to one of them is not
		// enough -- resolution runs on the visitor-facing path too (#1325).
		if !isTrustedProxy(addr, srv.proxyHandler.trustedProxies) {
			t.Errorf("%s must be trusted by the visitor-facing proxy as well", addr)
		}
	}
	if isTrustedProxy("198.51.100.7", srv.trustedProxies) {
		t.Error("nothing outside the configured set may be trusted")
	}
}

// The empty default is what almost every gateway runs, and it must keep starting.
func TestNewServer_AcceptsTheEmptyDefault(t *testing.T) {
	srv, err := NewServer(widthTestConfig(t))
	if err != nil {
		t.Fatalf("the default trusted set must start: %v", err)
	}
	defer srv.Stop()

	if !isTrustedProxy("127.0.0.1", srv.trustedProxies) {
		t.Error("the loopback default must survive the width check -- it is one host per family")
	}
}

// NewServer refuses, but NewProxyHandler returns no error and so has nowhere to report one, and
// nothing stops a future caller reaching parseTrustedProxies directly. The invariant therefore
// has to hold in the parser itself: no matcher it returns ever covers more than one host in
// routable space.
//
// Asserted as behaviour rather than as a count of matchers, because the count is satisfied by a
// parser that dropped the wrong entry. Before this change the forged header below was believed.
func TestParseTrustedProxies_DropsAnOverWideEntryWithoutDroppingTheRest(t *testing.T) {
	trusted := parseTrustedProxies([]string{"127.0.0.1/32", "0.0.0.0/0"})

	forged := newReq("198.51.100.7:44444", map[string]string{"X-Real-IP": "10.0.0.1"})
	if got := clientIPFrom(forged, trusted); got != "198.51.100.7" {
		t.Errorf("got %q, want the peer address -- 0.0.0.0/0 made every caller a trusted proxy", got)
	}
	// The narrow entry alongside it must survive, or this would pass just as well by dropping
	// everything, and nginx on loopback would stop being believed.
	fromNginx := newReq("127.0.0.1:34567", map[string]string{"X-Real-IP": "203.0.113.9"})
	if got := clientIPFrom(fromNginx, trusted); got != "203.0.113.9" {
		t.Errorf("got %q, want the forwarded address -- the loopback entry was dropped too", got)
	}
}

// The same invariant through production's own construction path for the visitor-facing proxy,
// rather than through a direct call to the parser.
func TestNewProxyHandler_DoesNotTrustAnOverWideEntry(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.TrustedProxies = []string{"127.0.0.1/32", "0.0.0.0/0"}

	p := NewProxyHandler(nil, cfg)

	forged := newReq("198.51.100.7:44444", map[string]string{"X-Real-IP": "10.0.0.1"})
	if got := p.clientIP(forged); got != "198.51.100.7" {
		t.Errorf("got %q, want the peer address -- the visitor-facing proxy believed a forged header", got)
	}
}
