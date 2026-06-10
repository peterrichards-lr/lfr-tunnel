package server

import (
	"bytes"
	_ "embed"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

//go:embed offline.html
var offlineHTML []byte

// ProxyHandler handles incoming HTTP/HTTPS proxy traffic, routing it to the active tunnel.
type ProxyHandler struct {
	registry *Registry
	limiters sync.Map // Map of host -> *rate.Limiter
}

// NewProxyHandler creates a new ProxyHandler instance.
func NewProxyHandler(registry *Registry) *ProxyHandler {
	return &ProxyHandler{
		registry: registry,
	}
}

// getRateLimiter retrieves or creates a rate limiter for a specific lease.
func (p *ProxyHandler) getRateLimiter(host string, limit int) *rate.Limiter {
	if limit <= 0 {
		return nil
	}
	limiterInterface, exists := p.limiters.Load(host)
	if exists {
		return limiterInterface.(*rate.Limiter)
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

	// 2. Query registry for lease
	lease, exists := p.registry.GetLease(host)
	if !exists {
		p.serveOfflinePage(w, r, host, "No active tunnel registered for this subdomain.")
		return
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

			// Resolve client IP address
			clientIP, _, _ := net.SplitHostPort(req.RemoteAddr)

			// Handle cases where RemoteAddr is not host:port (e.g. Unix socket, test context)
			if clientIP == "" {
				clientIP = req.RemoteAddr
			}

			// Inject standard proxy headers
			req.Header.Set("X-Real-IP", clientIP)
			req.Header.Set("X-Forwarded-For", clientIP)
			req.Header.Set("X-Forwarded-Host", req.Host)

			// Determine protocol
			proto := "http"
			if req.TLS != nil || strings.ToLower(req.Header.Get("X-Forwarded-Proto")) == "https" {
				proto = "https"
			}
			req.Header.Set("X-Forwarded-Proto", proto)
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			log.Printf("[Proxy] Routing failure to %s (127.0.0.1:%d): %v", host, lease.LocalPort, err)
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
	pageBytes := bytes.Replace(offlineHTML, []byte("loading..."), []byte(host), -1)
	if _, err := w.Write(pageBytes); err != nil {
		log.Printf("[Proxy] Failed to write offline page: %v", err)
	}
}
