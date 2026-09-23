package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
	"lfr-tunnel/pkg/proxyutil"

	"golang.org/x/crypto/bcrypt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

//go:embed offline.html
var offlineHTML []byte

//go:embed blocked.html
var blockedHTML []byte

//go:embed passcode.html
var passcodeHTML []byte

//go:embed unauthorized_ip.html
var unauthorizedIPHTML []byte

// RemoteRouteResolver resolves the target gateway URL and node ID for a host whose lease
// is held by another gateway in the cluster (issue #1249).
type RemoteRouteResolver func(host string) (targetURL string, nodeID string, exists bool)

// ProxyHandler handles incoming HTTP/HTTPS proxy traffic, routing it to the active tunnel.
type ProxyHandler struct {
	registry            *Registry
	config              *config.ServerConfig
	limiters            sync.Map // Map of host -> *rate.Limiter
	caCert              *x509.Certificate
	db                  *db.DB
	remoteRouteResolver RemoteRouteResolver
	// trustedProxies mirrors the server's, so the visitor-facing path resolves a client
	// address by the same rule as everything else (#1325).
	trustedProxies []*net.IPNet
	// Visitor session-cookie signing keys (#2181). VERIFY-MANY, MINT-ONE: a cookie is
	// checked against every key in sessionKeys and signed only with sessionCurrent.
	//
	// The set is the point. A session cookie lives 24 hours, so a node has to go on ACCEPTING
	// a key for at least that long after it stops SIGNING with it -- otherwise a rotation
	// would end every session in flight, which is the bug these fields exist to fix.
	//
	// sessionKeys is REPLACED, never mutated in place, so a reader may take the slice header
	// under the lock and iterate it afterwards.
	sessionMu        sync.RWMutex
	sessionKeys      []visitorSessionKey
	sessionCurrent   visitorSessionKey
	sessionBootstrap visitorSessionKey
	// nodeLocalWarnOnce holds the "this node has not been told the fleet key" warning to one
	// line per process. Minting with a node-local key is a real degradation -- those sessions
	// do not survive a failover -- and #1245 is the standing lesson that a behaviour
	// difference nobody can see is indistinguishable from one that is not happening.
	nodeLocalWarnOnce sync.Once
}

// visitorSessionKey is one signing key held in memory, decoded and ready to HMAC with.
//
// id is the generation label the control channel names the key by; it is not secret. key is.
type visitorSessionKey struct {
	id  string
	key []byte
}

// NewProxyHandler creates a new ProxyHandler instance.
func NewProxyHandler(registry *Registry, cfg *config.ServerConfig) *ProxyHandler {
	var trusted []*net.IPNet
	if cfg != nil {
		trusted = parseTrustedProxies(cfg.TrustedProxies)
	} else {
		trusted = parseTrustedProxies(nil)
	}
	p := &ProxyHandler{
		registry:       registry,
		config:         cfg,
		trustedProxies: trusted,
	}

	// The node-local bootstrap key: what this gateway signs with until central tells it the
	// fleet key (#2181). It is the pre-#2181 behaviour kept as a floor, so a gateway running
	// ahead of its control plane degrades to "sessions do not survive a move" rather than to
	// "no correct passcode is ever accepted".
	//
	// The error is handled rather than discarded. A zeroed key would be a PREDICTABLE signing
	// key, which is worse than no key: with no key this node mints nothing and says so, and
	// the passcode page is served again rather than a forgeable session being issued.
	boot := make([]byte, visitorSessionSecretBytes)
	if _, err := rand.Read(boot); err != nil {
		slog.Error(fmt.Sprintf("[Proxy] No randomness for a session signing key (%v); this gateway will not start visitor sessions until the control plane sends it one", err))
		return p
	}
	p.sessionBootstrap = visitorSessionKey{id: nodeLocalSessionKeyID, key: boot}
	p.sessionCurrent = p.sessionBootstrap
	p.sessionKeys = []visitorSessionKey{p.sessionBootstrap}
	return p
}

// SetVisitorSessionSecrets replaces the fleet-wide signing keys this handler accepts, and
// names the one it mints with (#2181).
//
// Called on central at startup from its own database, and on an edge every time central pushes
// the set down the control channel. Rejecting a malformed set with an error rather than
// applying half of it is deliberate: the caller keeps the keys it already had, which is a
// working node, instead of a node holding a set nothing agrees with.
//
// The node-local bootstrap key stays in the ACCEPTED set. Cookies this node minted before it
// was told the fleet key are still live -- they last 24 hours -- and dropping the key that
// signed them would log those visitors out at exactly the moment the fix arrived.
func (p *ProxyHandler) SetVisitorSessionSecrets(secrets []VisitorSessionSecret, currentID string) error {
	if len(secrets) == 0 {
		return fmt.Errorf("no visitor session signing keys were supplied")
	}
	if len(secrets) > maxAcceptedVisitorSessionSecrets {
		return fmt.Errorf("%d visitor session signing keys were supplied, more than the %d this node accepts", len(secrets), maxAcceptedVisitorSessionSecrets)
	}
	if currentID == "" {
		return fmt.Errorf("no current visitor session key generation was named")
	}

	keys := make([]visitorSessionKey, 0, len(secrets)+1)
	var current visitorSessionKey
	for _, secret := range secrets {
		if secret.ID == "" {
			return fmt.Errorf("a visitor session signing key was supplied with no generation id")
		}
		if secret.ID == nodeLocalSessionKeyID {
			return fmt.Errorf("a visitor session signing key was supplied under the reserved generation id %q", nodeLocalSessionKeyID)
		}
		raw, err := hex.DecodeString(secret.Key)
		if err != nil {
			return fmt.Errorf("visitor session signing key %s is not valid hex", secret.ID)
		}
		if len(raw) != visitorSessionSecretBytes {
			return fmt.Errorf("visitor session signing key %s is %d bytes, not %d", secret.ID, len(raw), visitorSessionSecretBytes)
		}
		key := visitorSessionKey{id: secret.ID, key: raw}
		keys = append(keys, key)
		if secret.ID == currentID {
			current = key
		}
	}
	if len(current.key) == 0 {
		return fmt.Errorf("the current visitor session key generation %s is not among the %d supplied", currentID, len(secrets))
	}

	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()
	if len(p.sessionBootstrap.key) > 0 {
		keys = append(keys, p.sessionBootstrap)
	}
	p.sessionKeys = keys
	p.sessionCurrent = current
	return nil
}

// visitorSessionKeys reports the key to mint with and the keys to verify against.
func (p *ProxyHandler) visitorSessionKeys() (visitorSessionKey, []visitorSessionKey) {
	p.sessionMu.RLock()
	defer p.sessionMu.RUnlock()
	return p.sessionCurrent, p.sessionKeys
}

// VisitorSessionGenerations reports the generation this node MINTS with and every generation it
// ACCEPTS, read back from the live handler state (#2195).
//
// This is what a node acknowledges a rotation with, and reading it back from the handler rather
// than echoing the frame that arrived is the point: an acknowledgement that repeated what it was
// sent would be satisfied by a node that received the keys and failed to apply them, which is
// exactly the node a prepare/commit gate exists to catch.
//
// Generation ids only. They are labels, not secrets -- VisitorSessionSecret.String() redacts the
// key for the reason #2135 and #2137 exist -- and nothing on this path may carry key material
// back up a channel it never needs to travel.
func (p *ProxyHandler) VisitorSessionGenerations() (current string, accepted []string) {
	p.sessionMu.RLock()
	defer p.sessionMu.RUnlock()
	accepted = make([]string, 0, len(p.sessionKeys))
	for _, key := range p.sessionKeys {
		accepted = append(accepted, key.id)
	}
	return p.sessionCurrent.id, accepted
}

// SetRemoteRouteResolver configures the callback used to locate and proxy traffic to
// remote gateways during DNS propagation (issue #1249).
func (p *ProxyHandler) SetRemoteRouteResolver(resolver RemoteRouteResolver) {
	p.remoteRouteResolver = resolver
}

// RemoveRateLimiter deletes the rate limiter associated with the given host.
func (p *ProxyHandler) RemoveRateLimiter(host string) {
	p.limiters.Delete(host)
}

// getRateLimiter retrieves or creates a rate limiter for a specific lease.
func (p *ProxyHandler) getRateLimiter(host string, limit int) *rate.Limiter {
	if limit <= 0 {
		return nil
	}
	limiterInterface, exists := p.limiters.Load(host)
	if exists {
		limiter := limiterInterface.(*rate.Limiter)
		if limiter.Limit() != rate.Limit(limit) {
			// Dynamically adjust the rate limit quota and burst size on-the-fly!
			limiter.SetLimit(rate.Limit(limit))
			limiter.SetBurst(limit * 2)
		}
		return limiter
	}
	// Burst size is twice the limit to allow some small spikes
	newLimiter := rate.NewLimiter(rate.Limit(limit), limit*2)
	p.limiters.Store(host, newLimiter)
	return newLimiter
}

// ServeHTTP routes incoming requests based on the Host header.
func (p *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Extract hostname from Host header (strip port if present)
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	// 2. Web Application Firewall (WAF) Protection
	if p.config != nil && p.config.EnableWAF {
		if blocked, category, reason := IsMaliciousRequest(r); blocked {
			clientIP := p.clientIP(r)
			slog.Info(fmt.Sprintf("[WAF] Blocked malicious request on %s from IP %s. Category: %s, Reason: %s", host, clientIP, category, reason))
			p.serveBlockedPage(w, r, host, category, reason, clientIP)
			return
		}
	}

	lease, exists := p.registry.GetLease(host)
	if !exists {
		if p.tryCrossNodeProxy(w, r, host) {
			return
		}
		p.serveNoTunnel(w, r, host)
		return
	}

	// 2.2 Handle CORS Preflight unconditionally for authorized domains
	if r.Method == http.MethodOptions {
		origin := r.Header.Get("Origin")
		if origin != "" && p.isOriginAllowed(origin) {
			p.injectCORSHeaders(w.Header(), origin)
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	// 2.4 Access Control Checks (IP Whitelist, Passcode, Client Cert)
	if !p.checkAccessControls(w, r, lease, host) {
		return
	}

	// 2.5 HTTP Basic Auth Protection
	if lease.BasicAuth != "" {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Basic ") {
			w.Header().Set("WWW-Authenticate", `Basic realm="Secure Liferay Tunnel"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(authHeader, "Basic "))
		if err != nil || string(payload) != lease.BasicAuth {
			w.Header().Set("WWW-Authenticate", `Basic realm="Secure Liferay Tunnel"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// 3. Enforce Subdomain Rate Limiting
	if lease.RateLimit > 0 {
		limiter := p.getRateLimiter(host, lease.RateLimit)
		if limiter != nil && !limiter.Allow() {
			http.Error(w, "Too Many Requests - Subdomain Rate Limit Exceeded", http.StatusTooManyRequests)
			return
		}
	}

	// 4. Create reverse proxy
	proxy := &httputil.ReverseProxy{
		// Migrated from the deprecated Director in #1704. Rewrite is not a rename: this is
		// where the visitor IP that reaches the tunnel table, the WAF and the audit log is
		// resolved, and ReverseProxy neither leaves the inbound forwarding headers in place
		// nor appends the peer to X-Forwarded-For for a Rewrite the way it does for a
		// Director. proxyutil restores both, framing the body below unchanged.
		Rewrite: func(pr *httputil.ProxyRequest) {
			req := pr.Out
			proxyutil.RestoreInboundForwarded(pr)

			req.URL.Scheme = "http"
			req.URL.Host = fmt.Sprintf("127.0.0.1:%d", lease.LocalPort)
			// Resolve client IP address using centralized helper from original request r
			clientIP := p.clientIP(r)

			// Update visitor IP
			lease.VisitorIPsMu.Lock()
			if lease.VisitorIPs == nil {
				lease.VisitorIPs = make(map[string]time.Time)
			}
			lease.VisitorIPs[clientIP] = time.Now()
			lease.VisitorIPsMu.Unlock()

			// Log the proxied request visitor IP
			slog.Info(fmt.Sprintf("[Proxy] Routing request on %s from visitor IP %s", host, clientIP))

			// Determine protocol
			proto := "http"
			if req.TLS != nil || strings.ToLower(req.Header.Get("X-Forwarded-Proto")) == "https" {
				proto = "https"
			}

			// Inject configured custom headers or fall back to standard defaults
			if p.config != nil && len(p.config.ProxyHeaders) > 0 {
				for k, v := range p.config.ProxyHeaders {
					interpolated := interpolateHeaderValue(v, clientIP, req.Host, proto)
					req.Header.Set(k, interpolated)
				}
			} else {
				req.Header.Set("X-Real-IP", clientIP)
				// Anti-spoofing (#1325): discard whatever chain arrived and rebuild it from
				// what this gateway knows. p.clientIP already applied the trusted-proxy
				// boundary, so clientIP is the peer address itself for an untrusted caller
				// and the header-supplied visitor only for one we vouch for. Discarding
				// first is what makes an inbound X-Forwarded-For unforgeable here; it is
				// the behaviour the previous Set had as a side effect, stated outright.
				req.Header.Del("X-Forwarded-For")
				// Then name the visitor, and let AppendPeerToXForwardedFor below close the
				// chain with the peer -- once. Setting clientIP here unconditionally was
				// what produced "192.0.2.1, 192.0.2.1" for a direct visitor (#1737).
				proxyutil.EnsureVisitorInXForwardedFor(pr, clientIP)
				req.Header.Set("X-Forwarded-Host", req.Host)
				req.Header.Set("X-Forwarded-Proto", proto)
			}

			// Inject dynamic lease headers from portal configuration
			if len(lease.AddedHeaders) > 0 {
				for k, v := range lease.AddedHeaders {
					interpolated := interpolateHeaderValue(v, clientIP, req.Host, proto)
					req.Header.Set(k, interpolated)
				}
			}

			proxyutil.AppendPeerToXForwardedFor(pr)
		},
		ModifyResponse: func(resp *http.Response) error {
			origin := r.Header.Get("Origin")
			if origin != "" && p.isOriginAllowed(origin) {
				p.injectCORSHeaders(resp.Header, origin)
			}
			return nil
		},
		Transport: &trackingTransport{
			roundTripper: http.DefaultTransport,
			lease:        lease,
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			// Deliberately still 502. A lease exists and the tunnel is up; the
			// developer's own local server is what failed, which is exactly what a bad
			// gateway is. Only the lease-miss path above changed (#1251).
			slog.Info(fmt.Sprintf("[Proxy] Routing failure to %s (127.0.0.1:%d): %v", host, lease.LocalPort, err))
			p.serveOfflinePage(w, req, host, http.StatusBadGateway)
		},
	}

	// 4. Forward the request
	proxy.ServeHTTP(w, r)
}

// tryCrossNodeProxy forwards a request to another gateway holding the lease when
// this gateway does not hold a local lease (e.g. during DNS propagation or failover, issue #1249).
// Returns true if the request was handled/proxied, false if it should fall through to serveNoTunnel.
func (p *ProxyHandler) tryCrossNodeProxy(w http.ResponseWriter, r *http.Request, host string) bool {
	if p.remoteRouteResolver == nil {
		return false
	}

	// 1. Check hop limit (max 2 hops: Edge A -> Central -> Edge B)
	hopStr := r.Header.Get("X-LFR-Cross-Node-Hop")
	hops := 0
	if hopStr != "" {
		if parsedHops, err := strconv.Atoi(hopStr); err == nil {
			hops = parsedHops
		}
		if hops >= 2 {
			slog.Info(fmt.Sprintf("[Proxy] Cross-node proxy hop limit reached for %s (hops=%d)", host, hops))
			return false
		}
	}

	// 2. Check loop prevention (visited nodes)
	visited := r.Header.Get("X-LFR-Cross-Node-Visited")
	var currentNodeID string
	if p.registry != nil {
		currentNodeID = p.registry.localNodeID()
	}
	if currentNodeID == "" {
		currentNodeID = "control"
	}

	if visited != "" {
		for _, v := range strings.Split(visited, ",") {
			if strings.TrimSpace(v) == currentNodeID {
				slog.Info(fmt.Sprintf("[Proxy] Cross-node loop detected for %s: node %s already visited in [%s]", host, currentNodeID, visited))
				return false
			}
		}
	}

	// 3. Resolve target route
	targetURL, targetNodeID, exists := p.remoteRouteResolver(host)
	if !exists || targetURL == "" {
		return false
	}

	if targetNodeID == currentNodeID {
		// Target is reported as this node, but we already know we have no local lease for it.
		return false
	}

	// Check if targetNodeID was already visited
	if visited != "" {
		for _, v := range strings.Split(visited, ",") {
			if strings.TrimSpace(v) == targetNodeID {
				slog.Info(fmt.Sprintf("[Proxy] Cross-node loop prevented for %s: target node %s already in [%s]", host, targetNodeID, visited))
				return false
			}
		}
	}

	// 4. Ensure targetURL has a scheme
	if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		targetURL = "http://" + targetURL
	}

	targetParsed, err := url.Parse(targetURL)
	if err != nil {
		slog.Info(fmt.Sprintf("[Proxy] Invalid cross-node target URL %q for %s: %v", targetURL, host, err))
		return false
	}

	// 5. Build reverse proxy
	proxy := &httputil.ReverseProxy{
		// Migrated from the deprecated Director in #1704; see the note on the other proxy in
		// this file. Restoring the inbound forwarding headers matters more here than there:
		// every "set it only if it is missing" check below is asking what the *upstream*
		// gateway already recorded, and ReverseProxy has deleted all four by this point.
		Rewrite: func(pr *httputil.ProxyRequest) {
			req := pr.Out
			proxyutil.RestoreInboundForwarded(pr)

			req.URL.Scheme = targetParsed.Scheme
			req.URL.Host = targetParsed.Host
			if targetParsed.Path != "" && targetParsed.Path != "/" {
				req.URL.Path = singleJoiningSlash(targetParsed.Path, req.URL.Path)
			}
			// Keep req.Host intact so the downstream gateway identifies the tunnel lease
			req.Host = host

			clientIP := p.clientIP(r)
			// This hop EXTENDS the chain rather than replacing it: an upstream gateway's
			// entries are the record of how the request got here. Name the visitor only if
			// the chain does not already end with it, and leave the peer to
			// AppendPeerToXForwardedFor below. Appending clientIP unconditionally added an
			// entry that the peer append then repeated (#1737) -- and behind nginx, whose
			// X-Forwarded-For is already the visitor, repeated the visitor as well.
			proxyutil.EnsureVisitorInXForwardedFor(pr, clientIP)
			if req.Header.Get("X-Real-IP") == "" {
				req.Header.Set("X-Real-IP", clientIP)
			}
			if req.Header.Get("X-Forwarded-Host") == "" {
				req.Header.Set("X-Forwarded-Host", host)
			}
			if req.Header.Get("X-Forwarded-Proto") == "" {
				proto := "http"
				if r.TLS != nil || strings.ToLower(r.Header.Get("X-Forwarded-Proto")) == "https" {
					proto = "https"
				}
				req.Header.Set("X-Forwarded-Proto", proto)
			}

			// Add cross-node tracing and loop prevention headers
			req.Header.Set("X-LFR-Cross-Node-Hop", strconv.Itoa(hops+1))
			if visited == "" {
				req.Header.Set("X-LFR-Cross-Node-Visited", currentNodeID)
			} else {
				req.Header.Set("X-LFR-Cross-Node-Visited", visited+","+currentNodeID)
			}

			proxyutil.AppendPeerToXForwardedFor(pr)
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			slog.Info(fmt.Sprintf("[Proxy] Cross-node routing failure for %s to %s (%s): %v", host, targetNodeID, targetURL, err))
			p.serveOfflinePage(w, req, host, http.StatusBadGateway)
		},
	}

	if p.config != nil && p.config.InsecureSkipVerify {
		proxy.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	slog.Info(fmt.Sprintf("[Proxy] Cross-node routing %s to %s (%s, hop %d)", host, targetNodeID, targetURL, hops+1))
	proxy.ServeHTTP(w, r)
	return true
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

// retryAfterSeconds is what a transiently-unavailable tunnel asks callers to wait. Short,
// because the gap it covers is a reconnect rather than an outage, and a visitor refreshing
// sooner costs nothing.
const retryAfterSeconds = 5

// serveNoTunnel answers a request for a host this gateway holds no lease for.
//
// A host whose lease was torn down moments ago is transient -- a failover, a client
// reconnect, a scheduled node stop -- and gets 503 with Retry-After, which tells browsers,
// caches and monitoring "come back shortly". Anything else is genuinely not here and gets
// 404.
//
// Both used to be 502, which asserts the upstream is broken. Monitoring pages on it, some
// proxies and CDNs treat it as a hard failure, and neither is true of a tunnel that simply
// moved. Nothing was logged either, so an operator could not tell whether visitors were
// hitting dead hostnames at all -- the only way to find out was to reproduce it by hand
// (#1251).
func (p *ProxyHandler) serveNoTunnel(w http.ResponseWriter, r *http.Request, host string) {
	if p.registry != nil && p.registry.RecentlyReleased(host) {
		slog.Info(fmt.Sprintf("[Proxy] No lease for %s, released within the last %s -- serving 503", host, releasedHostTTL))
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
		p.serveOfflinePage(w, r, host, http.StatusServiceUnavailable)
		return
	}
	slog.Info(fmt.Sprintf("[Proxy] No lease for %s and none released recently -- serving 404", host))
	p.serveOfflinePage(w, r, host, http.StatusNotFound)
}

// serveOfflinePage renders the Liferay-themed offline page with the given status. The page
// states the status itself, so it must not be hardcoded there.
func (p *ProxyHandler) serveOfflinePage(w http.ResponseWriter, r *http.Request, host string, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)

	// Replace placeholder host in embedded HTML. Escaped for the same reason as the other
	// visitor-facing pages: this one is reached with no lease at all, so the host is whatever
	// was asked for. The status and retry values below are generated here from an int, and are
	// left alone -- the retry value lands in a script, where HTML escaping would be wrong.
	pageBytes := bytes.ReplaceAll(offlineHTML, []byte("loading..."), []byte(html.EscapeString(host)))
	pageBytes = bytes.ReplaceAll(pageBytes, []byte("__STATUS__"), []byte(statusText(status)))
	// Only a transient status invites an automatic retry; re-fetching a 404 forever just
	// burns the visitor's battery on a tunnel that is not coming back.
	pageBytes = bytes.ReplaceAll(pageBytes, []byte("__RETRY_SECONDS__"), []byte(retryScript(status)))
	pageBytes = p.injectBaseTag(pageBytes, r, host)
	if _, err := w.Write(pageBytes); err != nil {
		slog.Info(fmt.Sprintf("[Proxy] Failed to write offline page: %v", err))
	}
}

// renderPage substitutes values into one of the visitor-facing pages, escaping every one of
// them (#1323).
//
// These pages are reached before a tunnel is identified -- the WAF branch runs before the lease
// lookup, and the passcode page renders on a *failed* passcode -- so an unauthenticated visitor
// chooses several of the values being substituted here.
//
// html.EscapeString covers both contexts the pages use: it escapes < > & ' and ", so a value is
// safe in a text node and inside a quoted attribute. That second part matters -- passcode.html
// puts RedirectURI in value="...", where escaping only the angle brackets would still let a
// quote break out of the attribute.
//
// A single-pass replacer rather than a chain of ReplaceAll calls. A chain re-scans text it has
// already substituted, so a value containing another placeholder -- "{{.Error}}" inside a
// redirect_uri, say -- would be expanded by a later pass, letting the caller decide where a
// different value lands. NewReplacer only ever matches against the original template.
func renderPage(tmpl string, values map[string]string) string {
	pairs := make([]string, 0, len(values)*2)
	for placeholder, value := range values {
		pairs = append(pairs, placeholder, html.EscapeString(value))
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

// safeRedirectPath reduces a caller-supplied redirect target to one that can only point back at
// this same host (#1324).
//
// Anything that is not a plain site-relative path becomes "/". An absolute URL, a
// protocol-relative "//elsewhere.example" and "/\elsewhere.example" -- which browsers normalise
// to the protocol-relative form -- all leave this hostname, and controlling who reaches this
// hostname's content is the entire point of the passcode gate.
//
// Falling back rather than erroring is deliberate: the redirect target is incidental to the auth
// flow, and refusing a correct passcode because the "next" parameter was malformed would be a
// worse outcome than sending the visitor to the site root.
func safeRedirectPath(uri string) string {
	if uri == "" || !strings.HasPrefix(uri, "/") ||
		strings.HasPrefix(uri, "//") || strings.HasPrefix(uri, "/\\") {
		return "/"
	}
	// The parsed form must name neither a scheme nor a host. This catches what the prefix checks
	// do not, and keeps a query string intact for the ordinary "/page?tab=2" case.
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return uri
}

// statusText renders the status line shown on the offline page.
func statusText(status int) string {
	return fmt.Sprintf("%d %s", status, http.StatusText(status))
}

// retryScript returns the auto-retry interval in seconds for the page to use, or "0" to
// disable it. Kept as data rather than markup so the page decides how to present it.
func retryScript(status int) string {
	if status == http.StatusServiceUnavailable {
		return strconv.Itoa(retryAfterSeconds)
	}
	return "0"
}

func (p *ProxyHandler) isOriginAllowed(origin string) bool {
	if p.config == nil {
		return false
	}
	for _, domain := range p.config.Domains {
		if strings.HasSuffix(origin, "."+domain) || origin == "http://"+domain || origin == "https://"+domain {
			return true
		}
	}
	return false
}

func (p *ProxyHandler) injectCORSHeaders(h http.Header, origin string) {
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "*")
}

// serveBlockedPage renders the Liferay-themed WAF blocked warning page.
func (p *ProxyHandler) serveBlockedPage(w http.ResponseWriter, r *http.Request, host, category, reason, ip string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)

	txID := fmt.Sprintf("WAF-TX-%d", time.Now().UnixNano())

	// Category, Reason and TxID are server-generated, but they go through the same escaping as
	// the rest: the safety of the page should not depend on which arguments a future caller
	// happens to pass.
	tmpl := renderPage(string(blockedHTML), map[string]string{
		"{{.Host}}":     host,
		"{{.Category}}": category,
		"{{.Reason}}":   reason,
		"{{.IP}}":       ip,
		"{{.TxID}}":     txID,
	})

	pageBytes := p.injectBaseTag([]byte(tmpl), r, host)
	if _, err := w.Write(pageBytes); err != nil {
		slog.Info(fmt.Sprintf("[Proxy] Failed to write WAF blocked page: %v", err))
	}
}

type trackingTransport struct {
	roundTripper http.RoundTripper
	lease        *TunnelLease
}

// Sizes of the fixed punctuation in an HTTP/1.1 message. Counted rather than spelled as string
// literals because goconst attributes a literal's package-wide occurrences to whichever file is
// newest, so a new file can go red for a "\r\n" that was already there ninety times (#1655).
const (
	httpCRLFBytes      = 2 // "\r\n"
	httpSpaceBytes     = 1 // " "
	httpHeaderSepBytes = 2 // ": "
	httpHostHeaderName = 4 // "Host"
)

// headerWireBytes is the size of a header block serialised as "Name: Value\r\n" per value, which
// is how net/http and every intermediary on the path write it.
func headerWireBytes(h http.Header) int {
	n := 0
	for name, values := range h {
		for _, v := range values {
			n += len(name) + httpHeaderSepBytes + len(v) + httpCRLFBytes
		}
	}
	return n
}

// requestWireHeaderBytes is the on-the-wire size of everything preceding a request's body: the
// request line, the headers, and the blank line terminating them.
//
// Computed from the struct rather than measured off the connection. Wrapping the conn would be
// exact, but this transport only ever sees the request, and a figure that is stable and symmetric
// with responseWireHeaderBytes is worth more than an exact one bought by moving the measurement
// point (#2177).
func requestWireHeaderBytes(req *http.Request) int {
	if req == nil {
		return 0
	}

	n := len(req.Method) + httpSpaceBytes + httpSpaceBytes + len(req.Proto) + httpCRLFBytes
	if req.URL != nil {
		n += len(req.URL.RequestURI())
	}

	// Host travels as a header on the wire but lives in its own field on the struct, so it is
	// absent from req.Header and has to be added by hand or every request undercounts by it.
	host := req.Host
	if host == "" && req.URL != nil {
		host = req.URL.Host
	}
	if host != "" {
		n += httpHostHeaderName + httpHeaderSepBytes + len(host) + httpCRLFBytes
	}

	return n + headerWireBytes(req.Header) + httpCRLFBytes
}

// responseWireHeaderBytes is the same measurement for a response: status line, headers, blank line.
func responseWireHeaderBytes(res *http.Response) int {
	if res == nil {
		return 0
	}
	n := len(res.Proto) + httpSpaceBytes + len(res.Status) + httpCRLFBytes
	return n + headerWireBytes(res.Header) + httpCRLFBytes
}

func (t *trackingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Headers first, and unconditionally. A GET reaches here with req.Body ALREADY NIL:
	// ReverseProxy discards a bodyless body itself ("Issue 16036: nil Body for http.Transport
	// retries", net/http/httputil/reverseproxy.go), so the wrapper below never installs and a
	// body-only measurement records exactly zero for the entire browse case. That is what made
	// Data In a flat line in both portals (#2177) -- a GET *is* its request line and headers,
	// and there is nothing else about it to count.
	atomic.AddUint64(&t.lease.BytesIn, uint64(requestWireHeaderBytes(req)))

	if req.Body != nil {
		req.Body = &trackingReadCloser{
			ReadCloser: req.Body,
			addBytes: func(n int) {
				atomic.AddUint64(&t.lease.BytesIn, uint64(n))
			},
		}
	}

	res, err := t.roundTripper.RoundTrip(req)
	if err != nil {
		return res, err
	}

	// Counted on this side too, and for symmetry rather than completeness: the two series are
	// charted against each other and summed into one quota (quota.go:27), so measuring a body
	// on one side and a body plus headers on the other trades a flat line for a skewed one.
	atomic.AddUint64(&t.lease.BytesOut, uint64(responseWireHeaderBytes(res)))

	if res.Body != nil {
		res.Body = &trackingReadCloser{
			ReadCloser: res.Body,
			addBytes: func(n int) {
				atomic.AddUint64(&t.lease.BytesOut, uint64(n))
			},
		}
	}
	return res, nil
}

type trackingReadCloser struct {
	io.ReadCloser
	addBytes func(int)
}

func (r *trackingReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		r.addBytes(n)
	}
	return n, err
}

// createSessionCookie signs a visitor's session with the CURRENT key, reporting false when
// this node has no key to sign with.
//
// The cookie deliberately does NOT carry the generation id that signed it (#2181). The
// alternative -- naming the generation so verification is one HMAC instead of up to four -- was
// considered and rejected: the accepted set is bounded at four keys, so the saving is three
// HMACs over forty bytes, and the cost is publishing this deployment's rotation cadence to
// every visitor who opens their cookie jar. The cheaper thing to leak is nothing.
func (p *ProxyHandler) createSessionCookie(subdomain string) (string, bool) {
	current, _ := p.visitorSessionKeys()
	if len(current.key) == 0 {
		return "", false
	}
	if current.id == nodeLocalSessionKeyID {
		p.nodeLocalWarnOnce.Do(func() {
			slog.Warn("[Proxy] Signing visitor sessions with a key local to this gateway: the control plane has not sent the fleet key. Sessions started now will not survive a failover or a restart (#2181).")
		})
	}

	// The constant rather than a literal 24h, because the rotation engine's retirement lag is
	// derived from this number (#2195): a generation must stay VERIFIABLE for at least as long
	// as the cookies it signed can live, or retiring it logs those visitors out. Two places
	// spelling the lifetime separately is how that guarantee drifts silently.
	expiration := time.Now().Add(visitorSessionCookieLifetime).Unix()
	payload := fmt.Sprintf("%s:%d", subdomain, expiration)

	h := hmac.New(sha256.New, current.key)
	h.Write([]byte(payload))
	signature := hex.EncodeToString(h.Sum(nil))

	return fmt.Sprintf("%s:%s", payload, signature), true
}

// verifySessionCookie checks a visitor's session against EVERY key this node accepts.
//
// Trying the whole set is what lets a cookie minted on one gateway be honoured on another, and
// what will let a rotation retire a key without ending the sessions it signed. No candidate
// short-circuits the loop, for the reason the edge token check already states (#1491): an early
// exit would make the time taken report WHICH generation signed the cookie, which during a
// rotation is a statement about the fleet nobody needs to be able to read from outside.
func (p *ProxyHandler) verifySessionCookie(cookieValue, subdomain string) bool {
	parts := strings.Split(cookieValue, ":")
	if len(parts) != 3 {
		return false
	}

	cookieSubdomain := parts[0]
	expStr := parts[1]
	signature := parts[2]

	if cookieSubdomain != subdomain {
		return false
	}

	expiration, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > expiration {
		return false
	}

	payload := fmt.Sprintf("%s:%s", cookieSubdomain, expStr)
	_, accepted := p.visitorSessionKeys()
	matched := false
	for _, candidate := range accepted {
		if len(candidate.key) == 0 {
			continue
		}
		h := hmac.New(sha256.New, candidate.key)
		h.Write([]byte(payload))
		expectedSignature := hex.EncodeToString(h.Sum(nil))
		if hmac.Equal([]byte(signature), []byte(expectedSignature)) {
			matched = true
		}
	}
	return matched
}

func (p *ProxyHandler) servePasscodePage(w http.ResponseWriter, r *http.Request, host, redirectURI, errStr string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)

	// Normalised here as well as at the form handler, because two of the three callers pass
	// r.RequestURI straight in. A choke point on the way to the page is worth more than a rule
	// every caller has to remember.
	redirectURI = safeRedirectPath(redirectURI)

	// The conditional markers are structural, so they are resolved against the raw template
	// before any value is substituted -- never against text that came from a visitor.
	tmpl := string(passcodeHTML)
	values := map[string]string{
		"{{.Host}}":        host,
		"{{.RedirectURI}}": redirectURI,
		// Absolute, so the injected <base href> cannot re-point it at the portal.
		"{{.VerifyURL}}": p.requestOrigin(r, host) + "/lfr-tunnel-verify",
	}
	if errStr != "" {
		tmpl = strings.ReplaceAll(tmpl, "{{if .Error}}", "")
		tmpl = strings.ReplaceAll(tmpl, "{{end}}", "")
		values["{{.Error}}"] = errStr
	} else {
		// Strip error section
		idxStart := strings.Index(tmpl, "{{if .Error}}")
		idxEnd := strings.Index(tmpl, "{{end}}")
		if idxStart != -1 && idxEnd != -1 && idxEnd > idxStart {
			tmpl = tmpl[:idxStart] + tmpl[idxEnd+7:]
		}
	}
	tmpl = renderPage(tmpl, values)

	pageBytes := p.injectBaseTag([]byte(tmpl), r, host)
	if _, err := w.Write(pageBytes); err != nil {
		slog.Info(fmt.Sprintf("[Proxy] Failed to write passcode page: %v", err))
	}
}

func (p *ProxyHandler) serveUnauthorizedIPPage(w http.ResponseWriter, r *http.Request, host, ip string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)

	tmpl := renderPage(string(unauthorizedIPHTML), map[string]string{
		"{{.Host}}": host,
		"{{.IP}}":   ip,
	})

	pageBytes := p.injectBaseTag([]byte(tmpl), r, host)
	if _, err := w.Write(pageBytes); err != nil {
		slog.Info(fmt.Sprintf("[Proxy] Failed to write unauthorized IP page: %v", err))
	}
}

func (p *ProxyHandler) checkAccessControls(w http.ResponseWriter, r *http.Request, lease *TunnelLease, host string) bool {
	// 1. Client Certificate validation bypass
	if p.caCert != nil {
		if cn, ok := VerifyClientCertificate(r, p.caCert); ok {
			if cn == "user:"+lease.UserID {
				return true
			}
			if p.db != nil {
				parts := strings.SplitN(host, ".", 2)
				if len(parts) == 2 {
					domain := parts[1]
					aclSub := lease.SubdomainPrefix

					acl, err := p.db.GetSubdomainACLByName(aclSub, domain, cn)
					if err == nil && acl != nil {
						if acl.ExpiresAt == nil || acl.ExpiresAt.After(time.Now()) {
							return true
						}
					}
				}
			}
		}
	}

	// 2. Intercept passcode verification POST /lfr-tunnel-verify
	if r.Method == "POST" && r.URL.Path == "/lfr-tunnel-verify" {
		_ = r.ParseForm() //nolint:errcheck
		passcodeVal := r.FormValue("passcode")
		// Reduced to a site-relative path before it reaches either sink: the redirect below, and
		// the form field it is echoed into on a wrong passcode. Without this, a correct passcode
		// sends the visitor to any origin the caller names (#1324).
		redirectURI := safeRedirectPath(r.FormValue("redirect_uri"))

		// THE VALUE THAT DECIDED TO CHALLENGE IS THE VALUE THAT IS CHECKED (#2125).
		//
		// This used to read the reservation out of the database, while the enforcement path
		// below reads lease.AccessControls(). On an edge -- no database at all -- that left
		// passcodeRequired empty, the comparison below could never be true, and every correct
		// passcode was answered with "Incorrect passcode. Please try again." The edge locked
		// the door and held no key.
		//
		// #1367 moved enforcement onto the lease precisely so an edge could enforce; this half
		// was left behind, and the two have been diverged since. Each is defensible read on its
		// own, which is why nobody noticed: only the pairing is wrong.
		passcodeRequired, _, _ := lease.AccessControls()

		// The database is still consulted, for the one thing that genuinely needs it: upgrading
		// a legacy hash in place once the plaintext has been proven correct. That is an
		// optimisation and already best-effort, so an edge simply skips it -- letting a valid
		// visitor in does not depend on it.
		var passcodeRes *db.SubdomainReservation
		if p.db != nil {
			parts := strings.SplitN(host, ".", 2)
			if len(parts) == 2 {
				domain := parts[1]
				res, err := p.db.GetSubdomainReservationByName(lease.SubdomainPrefix, domain)
				if err == nil && res != nil {
					passcodeRes = res
				}
			}
		}

		if passcodeRequired != "" && VerifyPasscode(passcodeVal, passcodeRequired) {
			// Upgrade a legacy value in place, now that it has been proven correct. This is the
			// only moment the plaintext passcode is available, so a migration cannot be done any
			// other way -- and it means the weak formats retire themselves as they are used
			// rather than needing a reset (#1490).
			//
			// Best-effort: a visitor who entered the right passcode must be let in whether or not
			// the upgrade write succeeds.
			if passcodeRes != nil && PasscodeNeedsUpgrade(passcodeRequired) {
				if upgraded := HashPasscode(passcodeVal); upgraded != "" {
					passcodeRes.Passcode = upgraded
					if err := p.db.UpdateSubdomainReservation(passcodeRes); err != nil {
						slog.Info(fmt.Sprintf("[Proxy] Could not upgrade a legacy passcode hash for %s: %v", host, err))
					} else {
						slog.Info(fmt.Sprintf("[Proxy] Upgraded a legacy passcode hash for %s", host))
					}
				}
			}
			parts := strings.SplitN(host, ".", 2)
			subdomain := parts[0]
			cookieVal, signed := p.createSessionCookie(subdomain)
			if !signed {
				// The passcode was right and there is still no session to hand back, because
				// this gateway has no key to sign one with. Issuing an unsigned or
				// zero-key-signed cookie here would be a session anyone could forge, so the
				// visitor is asked again instead -- and told it is the gateway, not them.
				slog.Error(fmt.Sprintf("[Proxy] %s: the correct passcode was entered but this gateway holds no session signing key, so no session could be started", host))
				p.servePasscodePage(w, r, host, redirectURI, "This gateway is still starting up. Please try again in a moment.")
				return false
			}

			http.SetCookie(w, &http.Cookie{
				Name:     "lfr_tunnel_session",
				Value:    cookieVal,
				Path:     "/",
				MaxAge:   86400,
				HttpOnly: true,
				Secure:   true,
				SameSite: http.SameSiteLaxMode,
			})

			http.Redirect(w, r, redirectURI, http.StatusSeeOther)
			return false
		}

		p.servePasscodePage(w, r, host, redirectURI, "Incorrect passcode. Please try again.")
		return false
	}

	// 3. Evaluate configured rules, read from the lease rather than the database.
	//
	// This used to query the reservation row on every request, purely to discover that the
	// tunnel has no access control -- which is the answer for almost every request, for every
	// asset on every page (#1329). pkg/db deliberately runs a single connection (#464), so that
	// query serialised the whole data plane behind one connection alongside metric writes,
	// portal reads and audit writes.
	//
	// Reading the lease also makes the rules available on an edge, which has no database at all
	// and therefore used to enforce nothing (#1367).
	passcodeRequired, ipWhitelist, accessMode := lease.AccessControls()
	accessMode = strings.ToLower(accessMode)

	// Apply enterprise force configs
	if p.config != nil {
		if p.config.ForceClientCert && p.caCert != nil {
			p.serveUnauthorizedIPPage(w, r, host, p.clientIP(r))
			return false
		}
	}

	hasPasscode := passcodeRequired != ""
	hasIPWhitelist := ipWhitelist != ""

	// The mode decides WHICH factors apply. Before this, enforcement was driven purely by
	// whether a passcode or whitelist was non-empty, and the mode only chose AND vs OR -- so
	// there was no way to express "keep these, do not apply them", and every UI offering
	// Public/Passcode/Whitelist was sending a value the API rejected outright (#2098).
	//
	// Public returns here with the values still stored: selecting it stops enforcement without
	// discarding the passcode, so switching back re-applies it without retyping.
	switch accessMode {
	case "public":
		return true
	case "passcode":
		hasIPWhitelist = false
	case "whitelist":
		hasPasscode = false
	}

	if !hasPasscode && !hasIPWhitelist {
		return true
	}

	visitorIP := p.clientIP(r)
	ipAllowed := false
	if hasIPWhitelist {
		ipAllowed = checkIPInWhitelist(visitorIP, ipWhitelist)
	}

	passcodeAllowed := false
	if hasPasscode {
		if cookie, err := r.Cookie("lfr_tunnel_session"); err == nil {
			parts := strings.SplitN(host, ".", 2)
			subdomain := parts[0]
			passcodeAllowed = p.verifySessionCookie(cookie.Value, subdomain)
		}
	}

	if accessMode == "and" {
		if hasIPWhitelist && !ipAllowed {
			p.serveUnauthorizedIPPage(w, r, host, visitorIP)
			return false
		}
		if hasPasscode && !passcodeAllowed {
			p.servePasscodePage(w, r, host, r.RequestURI, "")
			return false
		}
	} else {
		if hasIPWhitelist && ipAllowed {
			return true
		}
		if hasPasscode && passcodeAllowed {
			return true
		}
		if hasPasscode {
			p.servePasscodePage(w, r, host, r.RequestURI, "")
			return false
		}
		if hasIPWhitelist && !ipAllowed {
			p.serveUnauthorizedIPPage(w, r, host, visitorIP)
			return false
		}
	}

	return true
}

func checkIPInWhitelist(visitorIP, whitelist string) bool {
	vIP := net.ParseIP(visitorIP)
	if vIP == nil {
		return false
	}

	ips := strings.Split(whitelist, ",")
	for _, rawIP := range ips {
		rawIP = strings.TrimSpace(rawIP)
		if rawIP == "" {
			continue
		}
		if _, ipNet, err := net.ParseCIDR(rawIP); err == nil {
			if ipNet.Contains(vIP) {
				return true
			}
		}
		if targetIP := net.ParseIP(rawIP); targetIP != nil {
			if targetIP.Equal(vIP) {
				return true
			}
		}
	}
	return false
}

func interpolateHeaderValue(val, clientIP, host, proto string) string {
	val = strings.ReplaceAll(val, "$client_ip", clientIP)
	val = strings.ReplaceAll(val, "$remote_addr", clientIP)
	val = strings.ReplaceAll(val, "$host", host)
	val = strings.ReplaceAll(val, "$proto", proto)
	return val
}

// requestOrigin is the scheme and host the visitor is actually on.
//
// Needed because injectBaseTag rewrites every proxied page with <base href> pointing at the
// PORTAL, so that shared assets load from one place. A <base> also re-points root-relative URLs:
// the passcode form's action="/lfr-tunnel-verify" resolved against the control plane, which does
// not route it, so submitting a passcode landed on a 404 and no passcode could ever be entered.
//
// The verification endpoint is intercepted inside the proxy path, per tunnel host, so the form
// has to post to the host the visitor came in on.
func (p *ProxyHandler) requestOrigin(r *http.Request, host string) string {
	scheme := "https"
	if r != nil && r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "http"
	}
	return scheme + "://" + host
}

func (p *ProxyHandler) getPortalBaseURL(r *http.Request, host string) string {
	scheme := "https"
	if r != nil && r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "http"
	}

	if p.config != nil {
		for _, domain := range p.config.Domains {
			if host == domain || strings.HasSuffix(host, "."+domain) {
				return fmt.Sprintf("%s://tunnel.%s", scheme, domain)
			}
		}
		if len(p.config.Domains) > 0 {
			return fmt.Sprintf("%s://tunnel.%s", scheme, p.config.Domains[0])
		}
	}
	return scheme + "://localhost"
}

func (p *ProxyHandler) injectBaseTag(htmlBytes []byte, r *http.Request, host string) []byte {
	baseURL := p.getPortalBaseURL(r, host)
	// Escaped because this is an attribute value built from the requested host. Go's Host header
	// validation happens to exclude a quote today, so this is not reachable -- but that is a
	// property of net/http, not of this line, and every other page value is escaped.
	baseTag := []byte(fmt.Sprintf("<head>\n    <base href=\"%s/\">", html.EscapeString(baseURL)))
	return bytes.Replace(htmlBytes, []byte("<head>"), baseTag, 1)
}

// Subdomain passcode storage (#1490).
//
// #466 called for "SHA-256 with salt, or bcrypt". What shipped was unsalted single-round SHA-256
// with a plaintext fallback, so the control was documented as fixed and was not -- the worst state
// for a control to be in, because the next person to reason about it trusts the closed issue.
//
// Passcodes are short and human-chosen, so an unsalted single-round hash makes a leaked
// subdomain_reservations table a lookup rather than a cracking exercise, and identical passcodes
// across tenants are visibly identical in the database. bcrypt fixes both: per-hash salt, and a
// work factor.
//
// Legacy values still verify, and are upgraded in place on the next successful use -- see
// PasscodeNeedsUpgrade. Rejecting them outright would lock out every existing deployment; this
// repo is MIT-licensed and the production gateway is not the only one.

// bcryptPasscodeCost is deliberately the library default. A passcode is checked once per visitor
// session on an interactive path, so the usual "raise it until it hurts" advice for login
// endpoints does not apply, and a higher cost here is a denial-of-service lever on a page anyone
// can POST to.
const bcryptPasscodeCost = bcrypt.DefaultCost

// HashPasscode hashes a passcode for storage.
func HashPasscode(passcode string) string {
	if passcode == "" {
		return ""
	}
	// bcrypt silently truncates past 72 bytes, which would make two long passcodes sharing a
	// prefix interchangeable. Refuse rather than store something that verifies more than it
	// should; the caller treats "" as "no passcode set".
	if len(passcode) > 72 {
		slog.Info("[Proxy] Refusing to hash a passcode longer than bcrypt's 72-byte limit")
		return ""
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(passcode), bcryptPasscodeCost)
	if err != nil {
		slog.Info(fmt.Sprintf("[Proxy] Failed to hash passcode: %v", err))
		return ""
	}
	return string(hashed)
}

// legacyPasscodeHash is what HashPasscode produced before #1490: unsalted SHA-256, hex-encoded.
func legacyPasscodeHash(passcode string) string {
	hash := sha256.Sum256([]byte(passcode))
	return hex.EncodeToString(hash[:])
}

// PasscodeNeedsUpgrade reports whether a stored value is in a legacy format and should be
// re-hashed the next time it verifies. Callers that can persist the result should do so; the
// value keeps working either way.
func PasscodeNeedsUpgrade(storedPasscode string) bool {
	if storedPasscode == "" {
		return false
	}
	_, err := bcrypt.Cost([]byte(storedPasscode))
	return err != nil
}

// VerifyPasscode reports whether rawPasscode matches what is stored, in any format this has ever
// written.
func VerifyPasscode(rawPasscode, hashedPasscode string) bool {
	if hashedPasscode == "" || rawPasscode == "" {
		return false
	}

	// Current format. bcrypt.CompareHashAndPassword is constant-time and carries its own salt.
	if _, err := bcrypt.Cost([]byte(hashedPasscode)); err == nil {
		return bcrypt.CompareHashAndPassword([]byte(hashedPasscode), []byte(rawPasscode)) == nil
	}

	// Legacy: unsalted SHA-256, upgraded on the next successful use.
	if subtle.ConstantTimeCompare([]byte(legacyPasscodeHash(rawPasscode)), []byte(hashedPasscode)) == 1 {
		return true
	}

	// Legacy: plaintext, from before #466 hashed anything at all. Kept for the same reason and on
	// the same terms -- it upgrades itself away on first use.
	return subtle.ConstantTimeCompare([]byte(rawPasscode), []byte(hashedPasscode)) == 1
}
