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

//go:embed dashboard.html
var dashboardHTML []byte

// RegisterRequest represents the JSON request payload for registering a tunnel.
type RegisterRequest struct {
	SubdomainPrefix string            `json:"subdomain_prefix"`
	Ports           []PortMapping     `json:"ports"`
	AuthToken       string            `json:"auth_token"`
	RateLimit       int               `json:"rate_limit,omitempty"`
	BasicAuth       string            `json:"basic_auth,omitempty"`
	AddedHeaders    map[string]string `json:"added_headers,omitempty"`
	ClientVersion   string            `json:"client_version,omitempty"`
}

// RegisterResponse represents the JSON response payload.
type RegisterResponse struct {
	Status       string   `json:"status"`
	SessionToken string   `json:"session_token,omitempty"`
	Remotes      []string `json:"remotes,omitempty"`
	Domains      []string `json:"domains,omitempty"`
	Error        string   `json:"error,omitempty"`
	Warning      string   `json:"warning,omitempty"`
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
	portalMap    sync.Map // memory cache for portal magic links and sessions

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
	}

	var mailSender mail.Sender
	if cfg.SMTPServer.Host != "" {
		mailSender = mail.NewSMTPClient(&mail.Config{
			SMTPHost:           cfg.SMTPServer.Host,
			SMTPPort:           cfg.SMTPServer.Port,
			SMTPUsername:       cfg.SMTPServer.Username,
			SMTPPassword:       cfg.SMTPServer.Password,
			SMTPFromAddress:    cfg.SMTPServer.FromAddress,
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

		if cfg.Owner.UserID != "" {
			ownerUser, err := srv.db.GetUserByEmail(cfg.Owner.UserID)
			if err != nil {
				parts := strings.SplitN(cfg.Owner.Name, " ", 2)
				first := parts[0]
				last := ""
				if len(parts) > 1 {
					last = parts[1]
				}
				_ = srv.db.CreateUser(&db.User{
					ID:        cfg.Owner.UserID,
					Email:     cfg.Owner.UserID,
					FirstName: first,
					LastName:  last,
					Role:      "owner",
					Status:    "approved",
				})
			} else if ownerUser.Role != "owner" {
				ownerUser.Role = "owner"
				_ = srv.db.UpdateUser(ownerUser)
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
	ip := getClientIP(r)
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
					s.writeAudit("system", "ip.blacklisted", "ip", ip, "Auto-banned by Rate Limiter for DDOS", r)
					s.sendAdminAlert("alert_notify_blacklist", "LFR Tunnel Alert: IP Auto-Banned", fmt.Sprintf("IP %s was auto-banned by the Rate Limiter for exceeding thresholds.", ip))
				}

				http.Error(w, "Forbidden", http.StatusForbidden)
				return
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
		"localhost",
		"127.0.0.1",
	}
	for _, d := range s.cfg.Domains {
		controlDomains = append(controlDomains, strings.ToLower(d))
	}

	for _, d := range controlDomains {
		if d == "" {
			continue
		}
		if host == d || host == "tunnel."+d || host == "portal."+d || host == "api."+d {
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

		if r.Method == http.MethodGet && r.URL.Path == "/api/version" {
			respondJSON(w, http.StatusOK, map[string]string{
				"latest_version": config.Version,
				"min_version":    s.cfg.MinClientVersion,
			})
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

		if r.Method == http.MethodGet && r.URL.Path == "/api/verify-email" {
			s.handleVerifyEmail(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/auth/magic-link" {
			s.handleAdminMagicLink(w, r)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/auth/verify" {
			s.handleAdminVerify(w, r)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/auth/logout" {
			s.handleAdminLogout(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/auth/providers" {
			s.handleAuthProviders(w, r)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/auth/login" {
			s.handleSSOLogin(w, r)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/auth/callback" {
			s.handleSSOCallback(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/portal") {
			s.handlePortalEndpoints(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/me" {
			s.handleGetMe(w, r)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/tokens" {
			s.handleListTokens(w, r)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/tokens" {
			s.handleCreateToken(w, r)
			return
		}
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/tokens/") {
			s.handleDeleteToken(w, r)
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

		if r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "/admin" || r.URL.Path == "/portal") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(dashboardHTML)
			return
		}
	}

	// Data plane requests -> Route to ProxyHandler
	s.proxyHandler.ServeHTTP(w, r)
}

// handleRegister parses registration request and responds with leases.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, RegisterResponse{Status: "error", Error: "invalid JSON payload"})
		return
	}

	// Validate auth token
	if !s.isValidToken(req.AuthToken) {
		respondJSON(w, http.StatusUnauthorized, RegisterResponse{Status: "error", Error: "unauthorized"})
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
	clientIP := getClientIP(r)

	// Register in registry
	sessionToken, remotes, err := s.registry.Register(req.SubdomainPrefix, req.Ports, activeDomains, effectiveLimit, clientIP, req.BasicAuth, req.AddedHeaders)
	if err != nil {
		respondJSON(w, http.StatusConflict, RegisterResponse{Status: "error", Error: err.Error()})
		return
	}

	var warning string
	if req.ClientVersion != "" && req.ClientVersion != config.Version {
		warning = fmt.Sprintf("Version mismatch! Server is running %s but client is %s. Please consider upgrading using 'lfr-tunnel -upgrade'", config.Version, req.ClientVersion)
	}

	respondJSON(w, http.StatusOK, RegisterResponse{
		Status:       "success",
		SessionToken: sessionToken,
		Remotes:      remotes,
		Domains:      activeDomains,
		Warning:      warning,
	})
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
		if req.Status == "down" {
			s.sendAdminAlert("alert_notify_tunnel_offline", "LFR Tunnel Alert: Tunnel Offline", "A client tunnel has reported its status as offline/down.")
		}
		w.WriteHeader(http.StatusOK)
	} else {
		http.Error(w, "session not found", http.StatusNotFound)
	}
}

// handleDomains responds with the supported root domains.
func (s *Server) handleDomains(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	domains := make([]string, len(s.cfg.Domains))
	copy(domains, s.cfg.Domains)
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
			log.Printf("[Server] Loaded config: Bind=%s, HTTPBind=%s, Domains=%v, DB=%s",
				s.cfg.BindAddr, s.cfg.HTTPBindAddr, s.cfg.Domains, s.cfg.DBPath)
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

	if len(s.cfg.AllowedEmailDomains) > 0 {
		parts := strings.Split(req.Email, "@")
		if len(parts) == 2 {
			domain := parts[1]
			allowed := false
			for _, d := range s.cfg.AllowedEmailDomains {
				if domain == d {
					allowed = true
					break
				}
			}
			if !allowed {
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "email domain not allowed by server configuration"})
				return
			}
		}
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

	verificationToken, err := generateSecureToken()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to generate verification token"})
		return
	}

	user := &db.User{
		ID:                req.Email,
		Email:             req.Email,
		FirstName:         req.FirstName,
		LastName:          req.LastName,
		Role:              "user",
		Status:            "unverified",
		ApprovalToken:     approvalToken,
		VerificationToken: verificationToken,
	}

	if err := s.db.CreateUser(user); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to save registration request"})
		return
	}

	s.writeAudit(user.Email, "user.registered", "user", user.Email, "", r)

	// Send verification email to the user
	if s.mailSender != nil {
		scheme := "https"
		if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
			scheme = "http"
		}
		verifyURL := fmt.Sprintf("%s://%s/api/verify-email?token=%s", scheme, r.Host, verificationToken)
		subject := "[Liferay Tunnel] Please Verify Your Email Address"
		body := fmt.Sprintf("<p>Hi %s,</p><p>Please verify your email address by clicking the link below:</p><p><a href=\"%s\">Verify Email</a></p><p>Once verified, an admin will review your request.</p>", req.FirstName, verifyURL)

		go func() {
			if err := s.mailSender.Send(user.Email, subject, body); err != nil {
				log.Printf("[Mail] Failed to send verification email to %s: %v", user.Email, err)
			}
		}()
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "success", "message": "registration request submitted. Please check your email to verify your account."})
}

// handleVerifyEmail processes the email verification link clicked by the user.
func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}

	if s.db == nil {
		http.Error(w, "database storage not enabled", http.StatusNotImplemented)
		return
	}

	user, err := s.db.GetUserByVerificationToken(token)
	if err != nil || user.Status != "unverified" {
		http.Error(w, "invalid or expired token", http.StatusBadRequest)
		return
	}

	user.Status = "pending"
	user.VerificationToken = "" // Clear it
	if err := s.db.UpdateUser(user); err != nil {
		http.Error(w, "failed to update user", http.StatusInternalServerError)
		return
	}

	s.writeAudit(user.Email, "user.verified", "user", user.Email, "", r)
	s.sendAdminAlert("alert_notify_registration", "LFR Tunnel Alert: New User Registration", fmt.Sprintf("A new user (%s) has verified their email and requires approval.", user.Email))

	// Also send the original admin approval email now
	if s.mailSender != nil && s.cfg.AdminNotificationEmail != "" {
		subject := "[Liferay Tunnel] New Developer Registration Request"
		scheme := "http"
		if s.cfg.SSLCertFile != "" {
			scheme = "https"
		}
		host := r.Host
		approveURL := fmt.Sprintf("%s://%s/api/admin/approve?email=%s&token=%s", scheme, host, url.QueryEscape(user.Email), user.ApprovalToken)
		body := fmt.Sprintf("<p>New registration request (Email Verified):</p><ul><li>Name: %s %s</li><li>Email: %s</li></ul><p><a href=\"%s\">Click here to approve this request</a></p>", user.FirstName, user.LastName, user.Email, approveURL)

		go func() {
			if err := s.mailSender.Send(s.cfg.AdminNotificationEmail, subject, body); err != nil {
				log.Printf("[Server] Failed to send admin alert email: %v", err)
			}
		}()
	}

	w.Header().Set("Content-Type", "text/html")
	_, _ = w.Write([]byte(`<html><head><title>Email Verified</title><style>body{font-family:sans-serif;text-align:center;padding:50px;color:#333;background:#f8fafc;}h1{color:#10b981;}</style></head><body><h1>Email Verified! ✅</h1><p>Your email has been verified successfully. An administrator has been notified to review and approve your account.</p></body></html>`))
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
	s.chiselServer.Close() //nolint:errcheck
	if s.db != nil {
		s.db.Close() //nolint:errcheck
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
	for _, d := range s.cfg.Domains {
		if strings.Contains(host, strings.ToLower(d)) || host == strings.ToLower(d) {
			matchedDomain = d
			break
		}
	}

	if matchedDomain != "" {
		return []string{matchedDomain}
	}

	activeDomains := make([]string, len(s.cfg.Domains))
	copy(activeDomains, s.cfg.Domains)
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

	// Legacy config token removed
	return false
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

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	var actorEmail string
	var actorRole string
	var authenticated bool

	// 1. Check HTTP-only cookie
	cookie, err := r.Cookie("lfr_admin_session")
	if err == nil && cookie.Value != "" {
		if val, ok := s.portalMap.Load("admin_session_" + cookie.Value); ok {
			sessionData := val.(PortalSessionData)
			if time.Now().Before(sessionData.ExpiresAt) {
				actorEmail = sessionData.Email
				actorRole = "admin"
				if s.cfg.Owner.UserID != "" && strings.EqualFold(actorEmail, s.cfg.Owner.UserID) {
					actorRole = "owner"
				}
				authenticated = true

				// Optional: sliding expiration
				sessionData.ExpiresAt = time.Now().Add(s.cfg.PortalSessionDuration)
				s.portalMap.Store("admin_session_"+cookie.Value, sessionData)
			} else {
				s.portalMap.Delete("admin_session_" + cookie.Value)
			}
		}
	}

	// 2. Fallback to API Token (PAT)
	if !authenticated {
		token := r.Header.Get("Authorization")
		if strings.HasPrefix(strings.ToLower(token), "bearer ") {
			token = token[7:]
		} else {
			token = r.URL.Query().Get("token")
		}

		if token != "" && strings.HasPrefix(token, "lfr_pat_") && s.db != nil {
			hashBytes := sha256.Sum256([]byte(token))
			tokenHash := hex.EncodeToString(hashBytes[:])

			pat, err := s.db.GetPATByHash(tokenHash)
			if err == nil {
				now := time.Now().UTC()
				if pat.RevokedAt == nil && (pat.ExpiresAt == nil || pat.ExpiresAt.After(now)) {
					user, err := s.db.GetUser(pat.UserID)
					if err == nil && user.Status == "approved" && (user.Role == "admin" || user.Role == "owner") {
						go func(patID int64) { _ = s.db.UpdatePATUsed(patID) }(pat.ID)
						actorEmail = user.Email
						actorRole = "admin"
						if s.cfg.Owner.UserID != "" && strings.EqualFold(actorEmail, s.cfg.Owner.UserID) {
							actorRole = "owner"
						}
						authenticated = true
					}
				}
			}
		}
	}

	if !authenticated {
		http.Error(w, `{"error":"Unauthorized: admin access required"}`, http.StatusUnauthorized)
		return "", "", false
	}
	return actorEmail, actorRole, true
}

func (s *Server) handleAdminEndpoints(w http.ResponseWriter, r *http.Request) {
	actor, role, ok := s.requireAdmin(w, r)
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
		s.handleAdminPatchUser(w, r, actor, role)
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

	if strings.HasPrefix(r.URL.Path, "/api/admin/settings") {
		s.handleAdminSettings(w, r, actor)
		return
	}

	if r.URL.Path == "/api/admin/auth/magic-link" && r.Method == http.MethodPost {
		s.handleAdminMagicLink(w, r)
		return
	}

	if r.URL.Path == "/api/admin/auth/verify" && r.Method == http.MethodPost {
		s.handleAdminVerify(w, r)
		return
	}

	if r.URL.Path == "/api/admin/auth/logout" && r.Method == http.MethodPost {
		s.handleAdminLogout(w, r)
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleAdminMagicLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		return
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	isOwner := s.cfg.Owner.UserID != "" && req.Email == s.cfg.Owner.UserID
	var isAdmin bool

	if !isOwner && s.db != nil {
		user, err := s.db.GetUserByEmail(req.Email)
		if err == nil && user.Status == "approved" && (user.Role == "admin" || user.Role == "owner") {
			isAdmin = true
		}
	}

	if !isOwner && !isAdmin {
		// Do not leak existence
		respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	magicToken, _ := generateSecureToken()
	clientIP := getClientIP(r)

	sessionData := PortalSessionData{
		Email:     req.Email,
		ExpiresAt: time.Now().Add(15 * time.Minute),
		ClientIP:  clientIP,
	}
	s.portalMap.Store("admin_magic_"+magicToken, sessionData)

	s.writeAudit(req.Email, "admin.magic_link_requested", "system", "admin", "Requested admin dashboard login link", r)

	if s.mailSender != nil {
		host := r.Host
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		link := fmt.Sprintf("%s://%s/admin?token=%s", scheme, host, magicToken)
		body := fmt.Sprintf("<p>You requested a magic link to log into the Liferay Tunnel Admin Dashboard.</p>"+
			"<p><strong>IP Address:</strong> %s</p>"+
			"<p>This link expires in 15 minutes.</p>"+
			"<p><a href=\"%s\">Log In to Admin Dashboard</a></p>", clientIP, link)
		go s.mailSender.Send(req.Email, "Liferay Tunnel - Admin Login", body) //nolint:errcheck
	} else {
		log.Printf("[Admin] Magic Link for %s: /admin?token=%s", req.Email, magicToken)
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAdminVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		return
	}

	val, ok := s.portalMap.LoadAndDelete("admin_magic_" + req.Token)
	if !ok {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid or expired token"})
		return
	}

	sessionData := val.(PortalSessionData)
	if time.Now().After(sessionData.ExpiresAt) {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "Token has expired"})
		return
	}

	email := sessionData.Email
	sessionToken, _ := generateSecureToken()

	s.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     email,
		ExpiresAt: time.Now().Add(s.cfg.PortalSessionDuration),
	})

	s.writeAudit(email, "admin.login", "system", "admin", "Admin logged into dashboard via magic link", r)

	cookie := &http.Cookie{
		Name:     "lfr_admin_session",
		Value:    sessionToken,
		Path:     "/",
		Expires:  time.Now().Add(s.cfg.PortalSessionDuration),
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteStrictMode,
	}
	http.SetCookie(w, cookie)

	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("lfr_admin_session")
	if err == nil {
		s.portalMap.Delete("admin_session_" + cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "lfr_admin_session",
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteStrictMode,
	})
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAuthProviders(w http.ResponseWriter, r *http.Request) {
	type ProviderResponse struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Icon string `json:"icon"`
	}
	var providers []ProviderResponse
	for _, p := range s.cfg.SSOProviders {
		providers = append(providers, ProviderResponse{
			ID:   p.ID,
			Name: p.Name,
			Icon: p.Icon,
		})
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"sso_enabled": len(providers) > 0,
		"providers":   providers,
	})
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

func (s *Server) handleAdminPatchUser(w http.ResponseWriter, r *http.Request, actor, actorRole string) {
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
		if actorRole != "owner" {
			http.Error(w, `{"error":"Only the Owner can change user roles"}`, http.StatusForbidden)
			return
		}
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
			http.Error(w, `{"error":"Failed to add IP to blacklist"}`, http.StatusInternalServerError)
			return
		}
		s.blacklist.Store(payload.IPAddress, true)
		s.writeAudit(actor, "ip.blacklisted", "ip", payload.IPAddress, payload.Reason, r)
		s.sendAdminAlert("alert_notify_blacklist", "LFR Tunnel Alert: IP Banned", fmt.Sprintf("IP %s was manually banned by admin %s.", payload.IPAddress, actor))
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

func (s *Server) handleAdminSettings(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}

	if r.Method == http.MethodGet {
		// Fetch settings
		notifyReg, _ := s.db.GetAdminSetting("alert_notify_registration")
		notifyBan, _ := s.db.GetAdminSetting("alert_notify_blacklist")
		notifyOffline, _ := s.db.GetAdminSetting("alert_notify_tunnel_offline")

		// Default to true for critical security alerts if not set
		if notifyReg == "" {
			notifyReg = "true"
		}
		if notifyBan == "" {
			notifyBan = "true"
		}
		if notifyOffline == "" {
			notifyOffline = "false"
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"alert_notify_registration":   notifyReg,
			"alert_notify_blacklist":      notifyBan,
			"alert_notify_tunnel_offline": notifyOffline,
			"owner_email":                 s.cfg.Owner.UserID,
			"allowed_email_domains":       s.cfg.AllowedEmailDomains,
			"smtp_host":                   s.cfg.SMTPServer.Host,
			"smtp_from":                   s.cfg.SMTPServer.FromAddress,
			"admin_notification_email":    s.cfg.AdminNotificationEmail,
		})
		return
	}

	if r.Method == http.MethodPost {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, `{"error":"Invalid request payload"}`, http.StatusBadRequest)
			return
		}

		for key, value := range payload {
			// Validate keys to prevent spamming db
			if key == "alert_notify_registration" || key == "alert_notify_blacklist" || key == "alert_notify_tunnel_offline" {
				if err := s.db.SetAdminSetting(key, value); err != nil {
					log.Printf("[Admin] Failed to save setting %s: %v", key, err)
				}
			}
		}

		s.writeAudit(actor, "admin.settings_updated", "system", "email_alerts", "Admin updated email alert configuration", r)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Settings updated"})
		return
	}

	http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
}

func (s *Server) sendAdminAlert(settingKey, subject, htmlBody string) {
	if s.db == nil || s.mailSender == nil || s.cfg.AdminNotificationEmail == "" {
		return
	}

	val, err := s.db.GetAdminSetting(settingKey)
	if err != nil {
		log.Printf("[Warning] Failed to fetch admin setting %s: %v", settingKey, err)
		return
	}

	// Default true for "alert_notify_registration" and "alert_notify_blacklist"
	if val == "false" {
		return
	}
	if val == "" && settingKey == "alert_notify_tunnel_offline" {
		return // default false
	}

	go func() {
		if err := s.mailSender.Send(s.cfg.AdminNotificationEmail, subject, htmlBody); err != nil {
			log.Printf("[Mail] Failed to send admin alert %s: %v", settingKey, err)
		}
	}()
}

// respondJSON is a DRY helper for sending JSON API responses
func respondJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("[Server] Failed to encode JSON response: %v", err)
	}
}

// getClientIP extracts the real client IP, respecting proxy headers if present
func getClientIP(r *http.Request) string {
	ip := r.Header.Get("X-Real-IP")
	if ip == "" {
		ip = r.Header.Get("X-Forwarded-For")
	}
	if ip == "" {
		ip, _, _ = net.SplitHostPort(r.RemoteAddr)
		if ip == "" {
			ip = r.RemoteAddr
		}
	}
	return strings.Split(ip, ",")[0]
}

type PortalSessionData struct {
	Email     string
	ExpiresAt time.Time
	ClientIP  string
}

// handlePortalEndpoints multiplexes the portal API endpoints
func (s *Server) handlePortalEndpoints(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Database not configured"})
		return
	}

	if r.Method == http.MethodPost && r.URL.Path == "/api/portal/magic-link" {
		var req struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request"})
			return
		}

		user, err := s.db.GetUserByEmail(strings.ToLower(strings.TrimSpace(req.Email)))
		if err != nil || user.Status != "approved" {
			// Don't leak user existence
			respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}

		magicToken, _ := generateSecureToken()
		clientIP := getClientIP(r)

		sessionData := PortalSessionData{
			Email:     user.Email,
			ExpiresAt: time.Now().Add(15 * time.Minute),
			ClientIP:  clientIP,
		}
		s.portalMap.Store("magic_"+magicToken, sessionData)

		s.writeAudit(user.Email, "portal.magic_link_requested", "user", "portal", "Requested magic login link", r)

		if s.mailSender != nil {
			host := r.Host
			scheme := "http"
			if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				scheme = "https"
			}
			link := fmt.Sprintf("%s://%s/portal?token=%s", scheme, host, magicToken)
			reportLink := fmt.Sprintf("%s://%s/api/portal/report?token=%s", scheme, host, magicToken)
			body := fmt.Sprintf("<p>You requested a magic link to log into the Liferay Tunnel Portal.</p>"+
				"<p><strong>IP Address:</strong> %s</p>"+
				"<p>This link expires in 15 minutes.</p>"+
				"<p><a href=\"%s\">Log In to Portal</a></p>"+
				"<hr>"+
				"<p><em>If you did not request this, <a href=\"%s\">click here to immediately invalidate the link and report it to security</a>.</em></p>", clientIP, link, reportLink)
			go s.mailSender.Send(user.Email, "Liferay Tunnel - Portal Login", body) //nolint:errcheck
		} else {
			// For testing locally without SMTP
			log.Printf("[Portal] Magic Link for %s: /portal?token=%s", user.Email, magicToken)
		}

		respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	if r.Method == http.MethodGet && r.URL.Path == "/api/portal/report" {
		token := r.URL.Query().Get("token")
		val, ok := s.portalMap.LoadAndDelete("magic_" + token)
		if ok {
			sessionData := val.(PortalSessionData)
			s.writeAudit(sessionData.Email, "portal.magic_link_abuse_reported", "system", "portal", fmt.Sprintf("User reported abuse from IP: %s", sessionData.ClientIP), r)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html><head><title>Report Submitted</title><style>body{font-family:sans-serif;text-align:center;padding:50px;color:#333;background:#f8fafc;}h1{color:#10b981;}</style></head><body><h1>Report Submitted ✅</h1><p>Thank you for reporting. This magic link has been invalidated and administrators have been notified.</p></body></html>`))
		return
	}

	if r.Method == http.MethodPost && r.URL.Path == "/api/portal/verify" {
		var req struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request"})
			return
		}

		val, ok := s.portalMap.LoadAndDelete("magic_" + req.Token)
		if !ok {
			respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid or expired token"})
			return
		}

		sessionData := val.(PortalSessionData)
		if time.Now().After(sessionData.ExpiresAt) {
			respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "Token has expired"})
			return
		}

		email := sessionData.Email
		sessionToken, _ := generateSecureToken()

		s.portalMap.Store("session_"+sessionToken, PortalSessionData{
			Email:     email,
			ExpiresAt: time.Now().Add(s.cfg.PortalSessionDuration),
		})

		respondJSON(w, http.StatusOK, map[string]string{"session_token": sessionToken})
		return
	}

	// Require a valid session token for subsequent endpoints
	authHeader := r.Header.Get("Authorization")
	sessionToken := strings.TrimPrefix(authHeader, "Bearer ")
	val, ok := s.portalMap.Load("session_" + sessionToken)
	if !ok {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		return
	}

	sessionData := val.(PortalSessionData)
	if time.Now().After(sessionData.ExpiresAt) {
		s.portalMap.Delete("session_" + sessionToken)
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "Session expired"})
		return
	}

	// Sliding expiration: reset expiry time
	sessionData.ExpiresAt = time.Now().Add(s.cfg.PortalSessionDuration)
	s.portalMap.Store("session_"+sessionToken, sessionData)

	email := sessionData.Email
	user, err := s.db.GetUserByEmail(email)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "User not found"})
		return
	}

	if r.Method == http.MethodGet && r.URL.Path == "/api/portal/me" {
		pats, _ := s.db.ListPATs(user.ID)

		// Map active leases for portal dashboard (dummy filtering for now)
		var userTunnels []map[string]interface{}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"user":    user,
			"tokens":  pats,
			"tunnels": userTunnels,
		})
		return
	}

	if r.Method == http.MethodPost && r.URL.Path == "/api/portal/tokens" {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid payload"})
			return
		}

		tokenStr, _ := generateSecureToken()
		hashBytes := sha256.Sum256([]byte(tokenStr))
		tokenHash := hex.EncodeToString(hashBytes[:])

		tokenPrefix := tokenStr
		if len(tokenPrefix) > 12 {
			tokenPrefix = tokenPrefix[:12]
		}

		pat := &db.PersonalAccessToken{
			UserID:      user.ID,
			TokenHash:   tokenHash,
			TokenPrefix: tokenPrefix,
			Name:        req.Name,
		}

		if err := s.db.CreatePAT(pat); err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save token"})
			return
		}

		s.writeAudit(user.Email, "portal.token_created", "user", "tokens", "Generated a new PAT: "+req.Name, r)

		respondJSON(w, http.StatusOK, map[string]string{"token": tokenStr})
		return
	}

	if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/portal/tokens/") {
		tokenHash := strings.TrimPrefix(r.URL.Path, "/api/portal/tokens/")
		pat, err := s.db.GetPATByHash(tokenHash)
		if err != nil {
			respondJSON(w, http.StatusNotFound, map[string]string{"error": "Token not found"})
			return
		}
		if err := s.db.RevokePAT(pat.ID); err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to delete token"})
			return
		}

		s.writeAudit(user.Email, "portal.token_revoked", "user", "tokens", "Revoked PAT", r)

		respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
		return
	}

	respondJSON(w, http.StatusNotFound, map[string]string{"error": "Not Found"})
}
