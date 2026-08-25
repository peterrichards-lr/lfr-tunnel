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
	cookieSecret        []byte
	remoteRouteResolver RemoteRouteResolver
	// trustedProxies mirrors the server's, so the visitor-facing path resolves a client
	// address by the same rule as everything else (#1325).
	trustedProxies []*net.IPNet
}

// NewProxyHandler creates a new ProxyHandler instance.
func NewProxyHandler(registry *Registry, cfg *config.ServerConfig) *ProxyHandler {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret) //nolint:errcheck
	var trusted []*net.IPNet
	if cfg != nil {
		trusted = parseTrustedProxies(cfg.TrustedProxies)
	} else {
		trusted = parseTrustedProxies(nil)
	}
	return &ProxyHandler{
		registry:       registry,
		config:         cfg,
		cookieSecret:   secret,
		trustedProxies: trusted,
	}
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
		Director: func(req *http.Request) {
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
				req.Header.Set("X-Forwarded-For", clientIP)
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
		Director: func(req *http.Request) {
			req.URL.Scheme = targetParsed.Scheme
			req.URL.Host = targetParsed.Host
			if targetParsed.Path != "" && targetParsed.Path != "/" {
				req.URL.Path = singleJoiningSlash(targetParsed.Path, req.URL.Path)
			}
			// Keep req.Host intact so the downstream gateway identifies the tunnel lease
			req.Host = host

			clientIP := p.clientIP(r)
			if priorFor := req.Header.Get("X-Forwarded-For"); priorFor != "" {
				req.Header.Set("X-Forwarded-For", priorFor+", "+clientIP)
			} else {
				req.Header.Set("X-Forwarded-For", clientIP)
			}
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

func (t *trackingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
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

func (p *ProxyHandler) createSessionCookie(subdomain string) string {
	expiration := time.Now().Add(24 * time.Hour).Unix()
	payload := fmt.Sprintf("%s:%d", subdomain, expiration)

	h := hmac.New(sha256.New, p.cookieSecret)
	h.Write([]byte(payload))
	signature := hex.EncodeToString(h.Sum(nil))

	return fmt.Sprintf("%s:%s", payload, signature)
}

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
	h := hmac.New(sha256.New, p.cookieSecret)
	h.Write([]byte(payload))
	expectedSignature := hex.EncodeToString(h.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedSignature))
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

		passcodeRequired := ""
		if p.db != nil {
			parts := strings.SplitN(host, ".", 2)
			if len(parts) == 2 {
				domain := parts[1]
				res, err := p.db.GetSubdomainReservationByName(lease.SubdomainPrefix, domain)
				if err == nil && res != nil {
					passcodeRequired = res.Passcode
				}
			}
		}

		if passcodeRequired != "" && VerifyPasscode(passcodeVal, passcodeRequired) {
			parts := strings.SplitN(host, ".", 2)
			subdomain := parts[0]
			cookieVal := p.createSessionCookie(subdomain)

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

func HashPasscode(passcode string) string {
	if passcode == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(passcode))
	return hex.EncodeToString(hash[:])
}

func VerifyPasscode(rawPasscode, hashedPasscode string) bool {
	if hashedPasscode == "" {
		return false
	}
	computed := HashPasscode(rawPasscode)
	if subtle.ConstantTimeCompare([]byte(computed), []byte(hashedPasscode)) == 1 {
		return true
	}
	// Legacy fallback to support plain-text comparison
	return subtle.ConstantTimeCompare([]byte(rawPasscode), []byte(hashedPasscode)) == 1
}
