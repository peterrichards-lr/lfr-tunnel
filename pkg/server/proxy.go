package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
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

// ProxyHandler handles incoming HTTP/HTTPS proxy traffic, routing it to the active tunnel.
type ProxyHandler struct {
	registry     *Registry
	config       *config.ServerConfig
	limiters     sync.Map // Map of host -> *rate.Limiter
	caCert       *x509.Certificate
	db           *db.DB
	cookieSecret []byte
}

// NewProxyHandler creates a new ProxyHandler instance.
func NewProxyHandler(registry *Registry, cfg *config.ServerConfig) *ProxyHandler {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	return &ProxyHandler{
		registry:     registry,
		config:       cfg,
		cookieSecret: secret,
	}
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

	lease, exists := p.registry.GetLease(host)
	if !exists {
		p.serveOfflinePage(w, r, host, "No active tunnel registered for this subdomain.")
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

	// 2.3 Web Application Firewall (WAF) Protection
	if p.config != nil && p.config.EnableWAF {
		if blocked, category, reason := IsMaliciousRequest(r); blocked {
			clientIP := getClientIP(r)
			slog.Info(fmt.Sprintf("[WAF] Blocked malicious request on %s from IP %s. Category: %s, Reason: %s", host, clientIP, category, reason))
			p.serveBlockedPage(w, r, host, category, reason, clientIP)
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
			clientIP := getClientIP(r)

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
			slog.Info(fmt.Sprintf("[Proxy] Routing failure to %s (127.0.0.1:%d): %v", host, lease.LocalPort, err))
			p.serveOfflinePage(w, req, host, err.Error())
		},
	}

	// 4. Forward the request
	proxy.ServeHTTP(w, r)
}

// serveOfflinePage renders the Liferay-themed offline page.
func (p *ProxyHandler) serveOfflinePage(w http.ResponseWriter, r *http.Request, host string, reason string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)

	// Replace placeholder host in embedded HTML
	pageBytes := bytes.ReplaceAll(offlineHTML, []byte("loading..."), []byte(host))
	pageBytes = p.injectBaseTag(pageBytes, r, host)
	if _, err := w.Write(pageBytes); err != nil {
		slog.Info(fmt.Sprintf("[Proxy] Failed to write offline page: %v", err))
	}
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

	// Simple text-template replacement for blocked warning page
	tmpl := string(blockedHTML)
	tmpl = strings.ReplaceAll(tmpl, "{{.Host}}", host)
	tmpl = strings.ReplaceAll(tmpl, "{{.Category}}", category)
	tmpl = strings.ReplaceAll(tmpl, "{{.Reason}}", reason)
	tmpl = strings.ReplaceAll(tmpl, "{{.IP}}", ip)
	tmpl = strings.ReplaceAll(tmpl, "{{.TxID}}", txID)

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

	tmpl := string(passcodeHTML)
	tmpl = strings.ReplaceAll(tmpl, "{{.Host}}", host)
	tmpl = strings.ReplaceAll(tmpl, "{{.RedirectURI}}", redirectURI)
	if errStr != "" {
		tmpl = strings.ReplaceAll(tmpl, "{{if .Error}}", "")
		tmpl = strings.ReplaceAll(tmpl, "{{.Error}}", errStr)
		tmpl = strings.ReplaceAll(tmpl, "{{end}}", "")
	} else {
		// Strip error section
		idxStart := strings.Index(tmpl, "{{if .Error}}")
		idxEnd := strings.Index(tmpl, "{{end}}")
		if idxStart != -1 && idxEnd != -1 && idxEnd > idxStart {
			tmpl = tmpl[:idxStart] + tmpl[idxEnd+7:]
		}
	}

	pageBytes := p.injectBaseTag([]byte(tmpl), r, host)
	if _, err := w.Write(pageBytes); err != nil {
		slog.Info(fmt.Sprintf("[Proxy] Failed to write passcode page: %v", err))
	}
}

func (p *ProxyHandler) serveUnauthorizedIPPage(w http.ResponseWriter, r *http.Request, host, ip string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)

	tmpl := string(unauthorizedIPHTML)
	tmpl = strings.ReplaceAll(tmpl, "{{.Host}}", host)
	tmpl = strings.ReplaceAll(tmpl, "{{.IP}}", ip)

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
		_ = r.ParseForm()
		passcodeVal := r.FormValue("passcode")
		redirectURI := r.FormValue("redirect_uri")
		if redirectURI == "" {
			redirectURI = "/"
		}

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

	// 3. Evaluate configured rules
	var passcodeRequired string
	var ipWhitelist string
	accessMode := "or"

	if p.db != nil {
		parts := strings.SplitN(host, ".", 2)
		if len(parts) == 2 {
			domain := parts[1]
			res, err := p.db.GetSubdomainReservationByName(lease.SubdomainPrefix, domain)
			if err == nil && res != nil {
				passcodeRequired = res.Passcode
				ipWhitelist = res.WhitelistIPs
				if res.AccessMode != "" {
					accessMode = strings.ToLower(res.AccessMode)
				}
			}
		}
	}

	// Apply enterprise force configs
	if p.config != nil {
		if p.config.ForceClientCert && p.caCert != nil {
			p.serveUnauthorizedIPPage(w, r, host, getClientIP(r))
			return false
		}
	}

	hasPasscode := passcodeRequired != ""
	hasIPWhitelist := ipWhitelist != ""

	if !hasPasscode && !hasIPWhitelist {
		return true
	}

	visitorIP := getClientIP(r)
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
	baseTag := []byte(fmt.Sprintf("<head>\n    <base href=\"%s/\">", baseURL))
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
