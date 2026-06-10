package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
	"lfr-tunnel/pkg/mail"

	"github.com/jpillora/chisel/server"
)

//go:embed admin.html
var adminHTML []byte

// RegisterRequest represents the JSON request payload for registering a tunnel.
type RegisterRequest struct {
	SubdomainPrefix string            `json:"subdomain_prefix"`
	Ports           []PortMapping     `json:"ports"`
	AuthToken       string            `json:"auth_token"`
	RateLimit       int               `json:"rate_limit,omitempty"`
	BasicAuth       string            `json:"basic_auth,omitempty"`
	AddedHeaders    map[string]string `json:"added_headers,omitempty"`
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
	db           *db.DB
	mailSender   mail.Sender
	ctx          context.Context
	cancel       context.CancelFunc
	rateLimiters map[string]*rate.Limiter
	rlMutex      sync.Mutex
	violations   map[string]int
	vMutex       sync.Mutex
	blacklist    sync.Map // memory cache for db blacklist
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

	var database *db.DB
	if cfg.DBPath != "" {
		var err error
		database, err = db.Open(cfg.DBPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open database at %s: %v", cfg.DBPath, err)
		}

		// Seed statically provisioned developer tokens
		for _, st := range cfg.StaticTokens {
			st.UserID = strings.ToLower(strings.TrimSpace(st.UserID))
			st.Token = strings.TrimSpace(st.Token)
			if st.UserID == "" || st.Token == "" {
				continue
			}

			// 1. Ensure User exists and is approved
			role := st.Role
			if role == "" {
				role = "user"
			}
			_, err := database.GetUser(st.UserID)
			if err == db.ErrNotFound {
				log.Printf("[Server] Seeding static user: %s (role: %s)", st.UserID, role)
				userModel := &db.User{
					ID:     st.UserID,
					Email:  st.UserID,
					Role:   role,
					Status: "approved",
				}
				if err := database.CreateUser(userModel); err != nil {
					return nil, fmt.Errorf("failed to seed static user %s: %v", st.UserID, err)
				}
			} else if err != nil {
				return nil, fmt.Errorf("failed to check static user %s: %v", st.UserID, err)
			}

			// 2. Ensure Personal Access Token exists
			hashBytes := sha256.Sum256([]byte(st.Token))
			tokenHash := hex.EncodeToString(hashBytes[:])

			_, err = database.GetPATByHash(tokenHash)
			if err == db.ErrNotFound {
				name := st.Name
				if name == "" {
					name = "Static Config Token"
				}
				log.Printf("[Server] Seeding static token for user: %s (name: %s)", st.UserID, name)

				tokenPrefix := st.Token
				if len(tokenPrefix) > 12 {
					tokenPrefix = tokenPrefix[:12]
				}

				patModel := &db.PersonalAccessToken{
					UserID:      st.UserID,
					TokenHash:   tokenHash,
					TokenPrefix: tokenPrefix,
					Name:        name,
				}
				if err := database.CreatePAT(patModel); err != nil {
					return nil, fmt.Errorf("failed to seed static token for %s: %v", st.UserID, err)
				}
			} else if err != nil {
				return nil, fmt.Errorf("failed to check static token for %s: %v", st.UserID, err)
			}
		}
	}

	var mailSender mail.Sender
	if cfg.SMTPHost != "" {
		mailSender = mail.NewSMTPClient(&mail.Config{
			SMTPHost:           cfg.SMTPHost,
			SMTPPort:           cfg.SMTPPort,
			SMTPUsername:       cfg.SMTPUsername,
			SMTPPassword:       cfg.SMTPPassword,
			SMTPFromAddress:    cfg.SMTPFromAddress,
			InsecureSkipVerify: cfg.InsecureSkipVerify,
		})
	}

	ctx, cancel := context.WithCancel(context.Background())

	srv := &Server{
		cfg:          cfg,
		chiselServer: chiselSrv,
		registry:     registry,
		proxyHandler: proxyHandler,
		chiselProxy:  chiselProxy,
		db:           database,
		mailSender:   mailSender,
		ctx:          ctx,
		cancel:       cancel,
		rateLimiters: make(map[string]*rate.Limiter),
		violations:   make(map[string]int),
	}

	// Load DB blacklist into cache
	if srv.db != nil {
		if list, err := srv.db.ListBlacklistedIPs(); err == nil {
			for _, entry := range list {
				srv.blacklist.Store(entry.IPAddress, true)
			}
		}
	}

	return srv, nil
}

// getRateLimiter retrieves or creates a rate limiter for an IP.
func (s *Server) getRateLimiter(ip string) *rate.Limiter {
	s.rlMutex.Lock()
	defer s.rlMutex.Unlock()
	limiter, exists := s.rateLimiters[ip]
	if !exists {
		// 10 requests per second, burst of 20
		limiter = rate.NewLimiter(rate.Limit(10), 20)
		s.rateLimiters[ip] = limiter
	}
	return limiter
}

// ServeHTTP multiplexes control plane (registration & chisel WebSocket) and data plane (tunnel routing).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. IP Blacklist Defense (Config + DB Cache)
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	for _, blockedIP := range s.cfg.IPBlacklist {
		if ip == blockedIP {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}
	if _, blocked := s.blacklist.Load(ip); blocked {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// 2. Rate Limiting and Auto-Ban for API routes
	if strings.HasPrefix(r.URL.Path, "/api/") {
		limiter := s.getRateLimiter(ip)
		if !limiter.Allow() {
			s.vMutex.Lock()
			s.violations[ip]++
			vCount := s.violations[ip]
			s.vMutex.Unlock()

			if vCount >= 50 {
				// Auto-ban!
				log.Printf("[Defense] Auto-banning IP %s after 50 violations", ip)
				s.blacklist.Store(ip, true)
				if s.db != nil {
					_ = s.db.AddBlacklistIP(ip, "Auto-banned by Rate Limiter for DDOS")
					s.writeAudit("system", "system.auto_blacklisted", "ip", ip, "", r)
				}
			}

			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
	}

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

		if r.Method == http.MethodPost && r.URL.Path == "/api/tunnel-status" {
			s.handleTunnelStatus(w, r)
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

		if r.Method == http.MethodPost && r.URL.Path == "/api/register-request" {
			s.handleRegisterRequest(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/admin/approve" {
			s.handleApproveUser(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/claim" {
			s.handleClaimToken(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/admin/") && r.URL.Path != "/api/admin/approve" {
			s.handleAdminEndpoints(w, r)
			return
		}

		// Route Chisel WebSocket handshake/tunnel request
		isUpgrade := strings.ToLower(r.Header.Get("Upgrade")) == "websocket"
		if isUpgrade || strings.HasPrefix(r.URL.Path, "/tunnel") {
			r.URL.Path = "/"
			s.chiselProxy.ServeHTTP(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/admin" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(adminHTML)
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
	if !s.isValidToken(req.AuthToken) {
		w.WriteHeader(http.StatusUnauthorized)
		if err := json.NewEncoder(w).Encode(RegisterResponse{Status: "error", Error: "unauthorized"}); err != nil {
			log.Printf("[Server] Failed to encode unauthorized response: %v", err)
		}
		return
	}

	// Determine active domains to register dynamically based on request Host
	activeDomains := s.getActiveDomainsForRequest(r)

	// Determine effective rate limit
	effectiveLimit := req.RateLimit
	if s.cfg.MaxTunnelRateLimit > 0 {
		if effectiveLimit <= 0 || effectiveLimit > s.cfg.MaxTunnelRateLimit {
			effectiveLimit = s.cfg.MaxTunnelRateLimit
		}
	} else if effectiveLimit <= 0 {
		effectiveLimit = 0 // unlimited
	}

	// Get client IP
	clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		clientIP = r.RemoteAddr
	}

	// Register in registry
	sessionToken, remotes, err := s.registry.Register(req.SubdomainPrefix, req.Ports, activeDomains, effectiveLimit, clientIP, req.BasicAuth, req.AddedHeaders)
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

// handleTunnelStatus updates the maintenance/health status of a client's tunnel.
func (s *Server) handleTunnelStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionToken string `json:"session_token"`
		Status       string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if s.registry.UpdateLeaseStatus(req.SessionToken, req.Status) {
		w.WriteHeader(http.StatusOK)
	} else {
		http.Error(w, "session not found", http.StatusNotFound)
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

	if !s.isValidToken(token) {
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

	// Determine active domains to check dynamically based on request Host
	activeDomains := s.getActiveDomainsForRequest(r)

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

// RegisterRequestPayload represents the payload to request developer registration.
type RegisterRequestPayload struct {
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

func generateSecureToken() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// handleRegisterRequest creates a pending user registration request.
func (s *Server) handleRegisterRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.db == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "database storage not enabled"})
		return
	}

	var req RegisterRequestPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON payload"})
		return
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid email address"})
		return
	}

	// Check if user already exists
	if _, err := s.db.GetUser(req.Email); err == nil {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "registration request already exists or email is registered"})
		return
	}

	approvalToken, err := generateSecureToken()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to generate approval token"})
		return
	}

	user := &db.User{
		ID:            req.Email,
		Email:         req.Email,
		FirstName:     req.FirstName,
		LastName:      req.LastName,
		Role:          "user",
		Status:        "pending",
		ApprovalToken: approvalToken,
	}

	if err := s.db.CreateUser(user); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to save registration request"})
		return
	}

	if s.db != nil {
		s.writeAudit(user.Email, "user.registered", "user", user.Email, "", r)
	}

	// Send notification email to Admin if mail client is active
	if s.mailSender != nil && s.cfg.AdminNotificationEmail != "" {
		subject := "[Liferay Tunnel] New Developer Registration Request"
		scheme := "http"
		if s.cfg.SSLCertFile != "" {
			scheme = "https"
		}
		host := r.Host
		approveURL := fmt.Sprintf("%s://%s/api/admin/approve?email=%s&token=%s", scheme, host, url.QueryEscape(user.Email), approvalToken)
		body := fmt.Sprintf("<p>New registration request:</p><ul><li>Name: %s %s</li><li>Email: %s</li></ul><p><a href=\"%s\">Click here to approve this request</a></p>", user.FirstName, user.LastName, user.Email, approveURL)
		if err := s.mailSender.Send(s.cfg.AdminNotificationEmail, subject, body); err != nil {
			log.Printf("[Server] Failed to send admin alert email: %v", err)
		}
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "success", "message": "registration request submitted and is pending admin approval"})
}

// handleApproveUser handles admin clicks on approval links.
func (s *Server) handleApproveUser(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "Database storage not enabled", http.StatusNotImplemented)
		return
	}

	email := r.URL.Query().Get("email")
	token := r.URL.Query().Get("token")

	if email == "" || token == "" {
		http.Error(w, "Missing email or token parameters", http.StatusBadRequest)
		return
	}

	user, err := s.db.GetUser(email)
	if err != nil {
		http.Error(w, "User request not found", http.StatusNotFound)
		return
	}

	if user.Status != "pending" || user.ApprovalToken != token {
		http.Error(w, "Invalid approval link or request already processed", http.StatusGone)
		return
	}

	claimToken, err := generateSecureToken()
	if err != nil {
		http.Error(w, "Failed to generate claim token", http.StatusInternalServerError)
		return
	}

	// Generate PAT
	patBytes := make([]byte, 16)
	if _, err := rand.Read(patBytes); err != nil {
		http.Error(w, "Failed to generate personal access token", http.StatusInternalServerError)
		return
	}
	pat := "lfr_pat_" + hex.EncodeToString(patBytes)
	hashBytes := sha256.Sum256([]byte(pat))
	tokenHash := hex.EncodeToString(hashBytes[:])

	// Create PAT entry
	tokenPrefix := pat[:12]
	patModel := &db.PersonalAccessToken{
		UserID:      user.ID,
		TokenHash:   tokenHash,
		TokenPrefix: tokenPrefix,
		Name:        "Default CLI Token",
	}

	if err := s.db.CreatePAT(patModel); err != nil {
		http.Error(w, "Failed to create PAT", http.StatusInternalServerError)
		return
	}

	user.Status = "approved"
	user.ApprovalToken = ""
	user.ClaimToken = claimToken + ":" + pat

	if err := s.db.UpdateUser(user); err != nil {
		http.Error(w, "Failed to update user status", http.StatusInternalServerError)
		return
	}

	// Send approval email to developer with claim link
	if s.mailSender != nil {
		subject := "[Liferay Tunnel] Registration Approved!"
		scheme := "http"
		if s.cfg.SSLCertFile != "" {
			scheme = "https"
		}
		host := r.Host
		claimURL := fmt.Sprintf("%s://%s/api/claim?token=%s", scheme, host, claimToken)
		body := fmt.Sprintf("<p>Your registration request has been approved!</p><p><a href=\"%s\">Click here to claim your personal access token</a></p><p>Note: this link can only be used once.</p>", claimURL)
		if err := s.mailSender.Send(user.Email, subject, body); err != nil {
			log.Printf("[Server] Failed to send developer approval email: %v", err)
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("<h1>Approval Successful</h1><p>The user has been approved, and an email has been sent to them with instructions to claim their token.</p>"))
}

// handleClaimToken allows developers to claim their generated PAT.
func (s *Server) handleClaimToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.db == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "database storage not enabled"})
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing claim token"})
		return
	}

	// Find user by claim token prefix
	users, err := s.db.ListUsers()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to list users"})
		return
	}

	var targetUser *db.User
	var plaintextPat string

	for _, u := range users {
		if u.ClaimToken != "" && strings.HasPrefix(u.ClaimToken, token+":") {
			targetUser = u
			plaintextPat = strings.TrimPrefix(u.ClaimToken, token+":")
			break
		}
	}

	if targetUser == nil {
		w.WriteHeader(http.StatusGone)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid or expired claim token"})
		return
	}

	// Clear claim token so it can never be claimed again
	targetUser.ClaimToken = ""
	if err := s.db.UpdateUser(targetUser); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to update user"})
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":                "success",
		"personal_access_token": plaintextPat,
	})

	if s.db != nil {
		s.writeAudit(targetUser.Email, "token.claimed", "user", targetUser.Email, "", r)
	}
}

// Stop shuts down the server.
func (s *Server) Stop() {
	s.cancel()
	s.chiselServer.Close()
	if s.db != nil {
		s.db.Close()
	}
}

// getActiveDomainsForRequest extracts the requested root domain from the HTTP Host header.
// It returns a slice with only the matched domain if the Host header matches Domain1 or Domain2,
// keeping them independent. It falls back to all configured domains if no match is found.
func (s *Server) getActiveDomainsForRequest(r *http.Request) []string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)

	var matchedDomain string
	if s.cfg.Domain1 != "" && (strings.Contains(host, strings.ToLower(s.cfg.Domain1)) || host == strings.ToLower(s.cfg.Domain1)) {
		matchedDomain = s.cfg.Domain1
	} else if s.cfg.Domain2 != "" && (strings.Contains(host, strings.ToLower(s.cfg.Domain2)) || host == strings.ToLower(s.cfg.Domain2)) {
		matchedDomain = s.cfg.Domain2
	}

	if matchedDomain != "" {
		return []string{matchedDomain}
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
	return activeDomains
}

// isValidToken checks if a token is valid, checking both personal access tokens (PATs)
// in the database and the server's master auth_token configuration.
func (s *Server) isValidToken(token string) bool {
	if token == "" {
		return false
	}
	if s.db != nil {
		hashBytes := sha256.Sum256([]byte(token))
		tokenHash := hex.EncodeToString(hashBytes[:])

		pat, err := s.db.GetPATByHash(tokenHash)
		if err == nil {
			now := time.Now().UTC()
			if pat.RevokedAt == nil && (pat.ExpiresAt == nil || pat.ExpiresAt.After(now)) {
				user, err := s.db.GetUser(pat.UserID)
				if err == nil && user.Status == "approved" {
					// Update last used asynchronously
					go func(patID int64) {
						if err := s.db.UpdatePATUsed(patID); err != nil {
							log.Printf("[Server] Failed to update PAT last used time: %v", err)
						}
					}(pat.ID)
					return true
				}
			}
		}
	}

	// Fallback to server config shared token
	return s.cfg.AuthToken != "" && token == s.cfg.AuthToken
}

// isAdminToken checks if the provided token belongs to an admin user or is the static admin token.
func (s *Server) isAdminToken(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	if s.db != nil {
		hashBytes := sha256.Sum256([]byte(token))
		tokenHash := hex.EncodeToString(hashBytes[:])

		pat, err := s.db.GetPATByHash(tokenHash)
		if err == nil {
			now := time.Now().UTC()
			if pat.RevokedAt == nil && (pat.ExpiresAt == nil || pat.ExpiresAt.After(now)) {
				user, err := s.db.GetUser(pat.UserID)
				if err == nil && user.Status == "approved" && user.Role == "admin" {
					// Async update last used
					go func(patID int64) {
						_ = s.db.UpdatePATUsed(patID)
					}(pat.ID)
					return user.Email, true
				}
			}
		}
	}

	// Check static tokens for admin role
	for _, st := range s.cfg.StaticTokens {
		if st.Token == token && st.Role == "admin" {
			return st.UserID, true
		}
	}

	return "", false
}

func (s *Server) writeAudit(actorID, action, targetType, targetID string, details string, r *http.Request) {
	if s.db == nil {
		return
	}
	entry := &db.AuditEntry{
		ActorID:    actorID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Details:    details,
		IPAddress:  r.RemoteAddr,
	}
	// Run in a goroutine so it doesn't block the HTTP response
	go func() {
		if err := s.db.WriteAuditEntry(entry); err != nil {
			log.Printf("[Server] Failed to write audit log: %v", err)
		}
	}()
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (string, bool) {
	token := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = token[7:]
	} else {
		token = r.URL.Query().Get("token")
	}

	actor, ok := s.isAdminToken(token)
	if !ok {
		http.Error(w, `{"error":"Unauthorized: admin access required"}`, http.StatusUnauthorized)
		return "", false
	}
	return actor, true
}

func (s *Server) handleAdminEndpoints(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}

	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/users" {
		s.handleAdminListUsers(w, r, actor)
		return
	}

	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/admin/users/") {
		s.handleAdminGetUser(w, r, actor)
		return
	}

	if r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/admin/users/") {
		s.handleAdminPatchUser(w, r, actor)
		return
	}

	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/tokens" {
		s.handleAdminListTokens(w, r, actor)
		return
	}

	if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/admin/tokens/") {
		s.handleAdminDeleteToken(w, r, actor)
		return
	}

	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/leases" {
		s.handleAdminListLeases(w, r, actor)
		return
	}

	if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/admin/leases/") {
		s.handleAdminKickLease(w, r, actor)
		return
	}

	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/audit" {
		s.handleAdminAuditLog(w, r, actor)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/api/admin/blacklist") {
		s.handleAdminBlacklist(w, r, actor)
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}
	users, err := s.db.ListUsers()
	if err != nil {
		http.Error(w, `{"error":"Failed to list users"}`, http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(users)
}

func (s *Server) handleAdminGetUser(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}
	email, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/admin/users/"))
	if err != nil {
		http.Error(w, `{"error":"Invalid user email"}`, http.StatusBadRequest)
		return
	}

	user, err := s.db.GetUserByEmail(email)
	if err != nil {
		if err == db.ErrNotFound {
			http.Error(w, `{"error":"User not found"}`, http.StatusNotFound)
		} else {
			http.Error(w, `{"error":"Failed to get user"}`, http.StatusInternalServerError)
		}
		return
	}

	pats, err := s.db.ListPATs(user.ID)
	if err != nil {
		http.Error(w, `{"error":"Failed to get PATs"}`, http.StatusInternalServerError)
		return
	}

	resp := struct {
		User *db.User                  `json:"user"`
		PATs []*db.PersonalAccessToken `json:"pats"`
	}{
		User: user,
		PATs: pats,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleAdminPatchUser(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}
	email, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/admin/users/"))
	if err != nil {
		http.Error(w, `{"error":"Invalid user email"}`, http.StatusBadRequest)
		return
	}

	var req struct {
		Role   *string `json:"role"`
		Status *string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request body"}`, http.StatusBadRequest)
		return
	}

	user, err := s.db.GetUserByEmail(email)
	if err != nil {
		if err == db.ErrNotFound {
			http.Error(w, `{"error":"User not found"}`, http.StatusNotFound)
		} else {
			http.Error(w, `{"error":"Failed to get user"}`, http.StatusInternalServerError)
		}
		return
	}

	details := make(map[string]interface{})

	if req.Role != nil {
		if *req.Role == "user" && user.Role == "admin" {
			// Check if this is the last admin
			count, err := s.db.CountAdmins()
			if err != nil {
				http.Error(w, `{"error":"Failed to verify admin count"}`, http.StatusInternalServerError)
				return
			}
			if count <= 1 {
				http.Error(w, `{"error":"Cannot demote the last admin"}`, http.StatusConflict)
				return
			}
		}
		details["role_before"] = user.Role
		details["role_after"] = *req.Role
		user.Role = *req.Role
	}

	if req.Status != nil {
		details["status_before"] = user.Status
		details["status_after"] = *req.Status
		user.Status = *req.Status
	}

	if err := s.db.UpdateUser(user); err != nil {
		http.Error(w, `{"error":"Failed to update user"}`, http.StatusInternalServerError)
		return
	}

	detailsBytes, _ := json.Marshal(details)
	action := "user.updated"
	if req.Role != nil {
		action = "user.role_changed"
	} else if req.Status != nil {
		action = "user.status_changed"
	}
	s.writeAudit(actor, action, "user", user.Email, string(detailsBytes), r)

	_ = json.NewEncoder(w).Encode(user)
}

func (s *Server) handleAdminListTokens(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}
	pats, err := s.db.ListAllPATs()
	if err != nil {
		http.Error(w, `{"error":"Failed to list tokens"}`, http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(pats)
}

func (s *Server) handleAdminDeleteToken(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}
	patIDStr := strings.TrimPrefix(r.URL.Path, "/api/admin/tokens/")
	patID, err := strconv.ParseInt(patIDStr, 10, 64)
	if err != nil {
		http.Error(w, `{"error":"Invalid token ID"}`, http.StatusBadRequest)
		return
	}

	if err := s.db.RevokePAT(patID); err != nil {
		if err == db.ErrNotFound {
			// Idempotent delete
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
			return
		}
		http.Error(w, `{"error":"Failed to revoke token"}`, http.StatusInternalServerError)
		return
	}

	s.writeAudit(actor, "token.revoked", "token", patIDStr, "", r)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *Server) handleAdminListLeases(w http.ResponseWriter, r *http.Request, actor string) {
	// The registry must return a list of active leases
	// I need to add a ListLeases() method to registry later
	leases := s.registry.ListLeases()
	_ = json.NewEncoder(w).Encode(leases)
}

func (s *Server) handleAdminKickLease(w http.ResponseWriter, r *http.Request, actor string) {
	subdomain, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/admin/leases/"))
	if err != nil {
		http.Error(w, `{"error":"Invalid lease subdomain"}`, http.StatusBadRequest)
		return
	}

	found := s.registry.KickLease(subdomain)
	if !found {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "success", "message": "Lease not found or already gone"})
		return
	}

	s.writeAudit(actor, "lease.kicked", "lease", subdomain, "", r)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *Server) handleAdminAuditLog(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}

	filter := db.AuditFilter{
		ActorID:  r.URL.Query().Get("actor"),
		Action:   r.URL.Query().Get("action"),
		TargetID: r.URL.Query().Get("target"),
	}
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil {
			filter.Limit = limit
		}
	}
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if offset, err := strconv.Atoi(offsetStr); err == nil {
			filter.Offset = offset
		}
	}

	entries, err := s.db.ListAuditEntries(filter)
	if err != nil {
		http.Error(w, `{"error":"Failed to list audit entries"}`, http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(entries)
}

func (s *Server) handleAdminBlacklist(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}

	if r.Method == http.MethodGet {
		list, err := s.db.ListBlacklistedIPs()
		if err != nil {
			http.Error(w, `{"error":"Failed to list blacklisted IPs"}`, http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(list)
		return
	}

	if r.Method == http.MethodPost {
		var payload struct {
			IPAddress string `json:"ip_address"`
			Reason    string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, `{"error":"Invalid payload"}`, http.StatusBadRequest)
			return
		}
		if payload.IPAddress == "" {
			http.Error(w, `{"error":"IP Address is required"}`, http.StatusBadRequest)
			return
		}

		if err := s.db.AddBlacklistIP(payload.IPAddress, payload.Reason); err != nil {
			http.Error(w, `{"error":"Failed to blacklist IP"}`, http.StatusInternalServerError)
			return
		}
		s.blacklist.Store(payload.IPAddress, true)
		s.writeAudit(actor, "ip.blacklisted", "ip", payload.IPAddress, payload.Reason, r)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
		return
	}

	if r.Method == http.MethodDelete {
		ip := strings.TrimPrefix(r.URL.Path, "/api/admin/blacklist/")
		if ip == "" || ip == "/api/admin/blacklist" {
			http.Error(w, `{"error":"Missing IP address"}`, http.StatusBadRequest)
			return
		}
		if err := s.db.RemoveBlacklistIP(ip); err != nil {
			http.Error(w, `{"error":"Failed to remove IP from blacklist"}`, http.StatusInternalServerError)
			return
		}
		s.blacklist.Delete(ip)
		s.writeAudit(actor, "ip.unblacklisted", "ip", ip, "", r)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}
