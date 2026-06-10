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
)

//go:embed offline.html
var offlineHTML []byte

// ProxyHandler handles incoming HTTP/HTTPS proxy traffic, routing it to the active tunnel.
type ProxyHandler struct {
	registry *Registry
}

// NewProxyHandler creates a new ProxyHandler instance.
func NewProxyHandler(registry *Registry) *ProxyHandler {
	return &ProxyHandler{
		registry: registry,
	}
}

// ServeHTTP routes incoming requests based on the Host header.
func (p *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Extract hostname from Host header (strip port if present)
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	// 2. Query registry for backend port
	localPort, exists := p.registry.GetBackendPort(host)
	if !exists {
		p.serveOfflinePage(w, r, host, "No active tunnel registered for this subdomain.")
		return
	}

	// 3. Create reverse proxy
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = fmt.Sprintf("127.0.0.1:%d", localPort)

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
			log.Printf("[Proxy] Routing failure to %s (127.0.0.1:%d): %v", host, localPort, err)
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
