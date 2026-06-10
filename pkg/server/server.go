package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"lfr-tunnel/pkg/config"

	"github.com/jpillora/chisel/server"
)

// RegisterRequest represents the JSON request payload for registering a tunnel.
type RegisterRequest struct {
	SubdomainPrefix string        `json:"subdomain_prefix"`
	Ports           []PortMapping `json:"ports"`
	AuthToken       string        `json:"auth_token"`
}

// RegisterResponse represents the JSON response payload.
type RegisterResponse struct {
	Status       string   `json:"status"`
	SessionToken string   `json:"session_token,omitempty"`
	Remotes      []string `json:"remotes,omitempty"`
	Domains      []string `json:"domains,omitempty"`
	Error        string   `json:"error,omitempty"`
}

// CheckSubdomainResponse represents the JSON response payload for subdomain checks.
type CheckSubdomainResponse struct {
	Available   bool     `json:"available"`
	Subdomain   string   `json:"subdomain"`
	Reason      string   `json:"reason,omitempty"`
	Suggestions []string `json:"suggestions,omitempty"`
}

// Server coordinates the entire gateway operations.
type Server struct {
	cfg          *config.ServerConfig
	chiselServer *chserver.Server
	registry     *Registry
	proxyHandler *ProxyHandler
	chiselProxy  *httputil.ReverseProxy
	ctx          context.Context
	cancel       context.CancelFunc
}

// NewServer initializes and returns a new Server instance.
func NewServer(cfg *config.ServerConfig) (*Server, error) {
	// Initialize Chisel server config
	chiselCfg := &chserver.Config{
		Reverse: true,
	}
	chiselSrv, err := chserver.NewServer(chiselCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize chisel server: %v", err)
	}

	registry := NewRegistry(chiselSrv)
	proxyHandler := NewProxyHandler(registry)

	// Setup internal reverse proxy to Chisel server
	chiselURL, err := url.Parse("http://127.0.0.1:8081")
	if err != nil {
		return nil, err
	}
	chiselProxy := httputil.NewSingleHostReverseProxy(chiselURL)

	ctx, cancel := context.WithCancel(context.Background())

	return &Server{
		cfg:          cfg,
		chiselServer: chiselSrv,
		registry:     registry,
		proxyHandler: proxyHandler,
		chiselProxy:  chiselProxy,
		ctx:          ctx,
		cancel:       cancel,
	}, nil
}

// ServeHTTP multiplexes control plane (registration & chisel WebSocket) and data plane (tunnel routing).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract hostname (strip port)
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)

	// Identify if host is a control domain
	isControl := false
	controlDomains := []string{
		strings.ToLower(s.cfg.Domain1),
		strings.ToLower(s.cfg.Domain2),
		"localhost",
		"127.0.0.1",
	}

	for _, d := range controlDomains {
		if d == "" {
			continue
		}
		if host == d || host == "tunnel."+d {
			isControl = true
			break
		}
	}

	if isControl {
		// Route control plane requests
		if r.Method == http.MethodPost && r.URL.Path == "/api/register" {
			s.handleRegister(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/domains" {
			s.handleDomains(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/check-subdomain" {
			s.handleCheckSubdomain(w, r)
			return
		}

		// Route Chisel WebSocket handshake/tunnel request
		isUpgrade := strings.ToLower(r.Header.Get("Upgrade")) == "websocket"
		if isUpgrade || strings.HasPrefix(r.URL.Path, "/tunnel") {
			r.URL.Path = "/"
			s.chiselProxy.ServeHTTP(w, r)
			return
		}

		// Render a simple gateway landing page
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`
			<!DOCTYPE html>
			<html>
			<head>
				<title>Liferay Tunnel Gateway</title>
				<style>
					body { font-family: sans-serif; text-align: center; padding: 50px; background: #0f172a; color: #f8fafc; }
					h1 { color: #38bdf8; }
				</style>
			</head>
			<body>
				<h1>Liferay Tunnel Gateway</h1>
				<p>The gateway is online and running.</p>
			</body>
			</html>
		`)); err != nil {
			log.Printf("[Server] Failed to write landing page: %v", err)
		}
		return
	}

	// Data plane requests -> Route to ProxyHandler
	s.proxyHandler.ServeHTTP(w, r)
}

// handleRegister parses registration request and responds with leases.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		if err := json.NewEncoder(w).Encode(RegisterResponse{Status: "error", Error: "invalid JSON payload"}); err != nil {
			log.Printf("[Server] Failed to encode register error response: %v", err)
		}
		return
	}

	// Validate auth token
	if s.cfg.AuthToken != "" && req.AuthToken != s.cfg.AuthToken {
		w.WriteHeader(http.StatusUnauthorized)
		if err := json.NewEncoder(w).Encode(RegisterResponse{Status: "error", Error: "unauthorized"}); err != nil {
			log.Printf("[Server] Failed to encode unauthorized response: %v", err)
		}
		return
	}

	// Determine active domains to register
	var activeDomains []string
	if s.cfg.Domain1 != "" {
		activeDomains = append(activeDomains, s.cfg.Domain1)
	}
	if s.cfg.Domain2 != "" {
		activeDomains = append(activeDomains, s.cfg.Domain2)
	}
	if len(activeDomains) == 0 {
		// Fallback for local testing
		activeDomains = append(activeDomains, "localhost")
	}

	// Register in registry
	sessionToken, remotes, err := s.registry.Register(req.SubdomainPrefix, req.Ports, activeDomains)
	if err != nil {
		w.WriteHeader(http.StatusConflict)
		if err := json.NewEncoder(w).Encode(RegisterResponse{Status: "error", Error: err.Error()}); err != nil {
			log.Printf("[Server] Failed to encode conflict response: %v", err)
		}
		return
	}

	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(RegisterResponse{
		Status:       "success",
		SessionToken: sessionToken,
		Remotes:      remotes,
		Domains:      activeDomains,
	}); err != nil {
		log.Printf("[Server] Failed to encode success response: %v", err)
	}
}

// handleDomains responds with the supported root domains.
func (s *Server) handleDomains(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var domains []string
	if s.cfg.Domain1 != "" {
		domains = append(domains, s.cfg.Domain1)
	}
	if s.cfg.Domain2 != "" {
		domains = append(domains, s.cfg.Domain2)
	}
	if len(domains) == 0 {
		domains = append(domains, "localhost")
	}
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(domains); err != nil {
		log.Printf("[Server] Failed to encode domains response: %v", err)
	}
}

// handleCheckSubdomain verifies if a subdomain prefix is available and generates suggestions if not.
func (s *Server) handleCheckSubdomain(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Validate auth token
	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.URL.Query().Get("auth_token")
	}
	if token == "" {
		token = r.Header.Get("X-Auth-Token")
	}
	if token == "" {
		if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			token = authHeader[7:]
		}
	}

	if s.cfg.AuthToken != "" && token != s.cfg.AuthToken {
		w.WriteHeader(http.StatusUnauthorized)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"}); err != nil {
			log.Printf("[Server] Failed to encode unauthorized response: %v", err)
		}
		return
	}

	subdomain := r.URL.Query().Get("subdomain")
	if subdomain == "" {
		w.WriteHeader(http.StatusBadRequest)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": "missing subdomain parameter"}); err != nil {
			log.Printf("[Server] Failed to encode missing subdomain response: %v", err)
		}
		return
	}

	var activeDomains []string
	if s.cfg.Domain1 != "" {
		activeDomains = append(activeDomains, s.cfg.Domain1)
	}
	if s.cfg.Domain2 != "" {
		activeDomains = append(activeDomains, s.cfg.Domain2)
	}
	if len(activeDomains) == 0 {
		activeDomains = append(activeDomains, "localhost")
	}

	available, reason := s.registry.CheckSubdomain(subdomain, activeDomains)
	resp := CheckSubdomainResponse{
		Available: available,
		Subdomain: subdomain,
		Reason:    reason,
	}

	if !available {
		resp.Suggestions = s.registry.GenerateSuggestions(subdomain, activeDomains)
	}

	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("[Server] Failed to encode check subdomain response: %v", err)
	}
}

// Start kicks off the background processes and listens for gateway traffic.
func (s *Server) Start() error {
	// 1. Start Chisel Server on localhost:8081
	go func() {
		log.Println("[Server] Starting internal Chisel tunnel engine on 127.0.0.1:8081...")
		if err := s.chiselServer.StartContext(s.ctx, "127.0.0.1", "8081"); err != nil {
			log.Fatalf("[Server] Internal Chisel server crashed: %v", err)
		}
	}()

	// 2. Start registry cleanup routine
	s.registry.StartCleanupRoutine(10 * time.Second)

	// 3. Start HTTPS / HTTP Gateway listener
	if s.cfg.SSLCertFile != "" && s.cfg.SSLKeyFile != "" {
		// HTTP redirect server
		go func() {
			log.Printf("[Server] Starting HTTP redirect gateway on %s...", s.cfg.HTTPBindAddr)
			redirectSrv := &http.Server{
				Addr: s.cfg.HTTPBindAddr,
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					target := "https://" + r.Host + r.URL.Path
					if r.URL.RawQuery != "" {
						target += "?" + r.URL.RawQuery
					}
					http.Redirect(w, r, target, http.StatusMovedPermanently)
				}),
			}
			if err := redirectSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("[Server] HTTP redirect server failed: %v", err)
			}
		}()

		log.Printf("[Server] Starting HTTPS gateway on %s (TLS offloaded)...", s.cfg.BindAddr)
		srv := &http.Server{
			Addr:    s.cfg.BindAddr,
			Handler: s,
		}
		return srv.ListenAndServeTLS(s.cfg.SSLCertFile, s.cfg.SSLKeyFile)
	}

	// HTTP-only mode
	log.Printf("[Server] Starting HTTP gateway on %s (TLS disabled)...", s.cfg.HTTPBindAddr)
	srv := &http.Server{
		Addr:    s.cfg.HTTPBindAddr,
		Handler: s,
	}
	return srv.ListenAndServe()
}

// Stop shuts down the server.
func (s *Server) Stop() {
	s.cancel()
	s.chiselServer.Close()
}
