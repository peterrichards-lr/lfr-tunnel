package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
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
var dashboardHTML string

//go:embed static/*
var staticFS embed.FS

// RegisterRequest represents the JSON request payload for registering a tunnel.
type RegisterRequest struct {
	SubdomainPrefix string            `json:"subdomain_prefix"`
	Ports           []PortMapping     `json:"ports"`
	AuthToken       string            `json:"auth_token"`
	RateLimit       int               `json:"rate_limit,omitempty"`
	BasicAuth       string            `json:"basic_auth,omitempty"`
	AddedHeaders    map[string]string `json:"added_headers,omitempty"`
	ClientVersion   string            `json:"client_version,omitempty"`
	ClientOS        string            `json:"client_os,omitempty"`
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
	cfg               *config.ServerConfig
	chiselServer      *chserver.Server
	registry          *Registry
	proxyHandler      *ProxyHandler
	chiselProxy       *httputil.ReverseProxy
	db                *db.DB
	mailSender        mail.Sender
	ctx               context.Context
	cancel            context.CancelFunc
	rateLimiters      map[string]*rate.Limiter
	rlMutex           sync.Mutex
	violations        map[string]int
	vMutex            sync.Mutex
	blacklist         sync.Map // memory cache for db blacklist
	portalMap         sync.Map // memory cache for portal magic links and sessions
	metricsQueue      chan *db.TunnelMetric
	broadcastMutex    sync.RWMutex
	broadcastMessage  string
	targetedMessages  map[string]string
	targetedMutex     sync.RWMutex
	maintenanceMode   bool
	maintTimer        *time.Timer
	maintScheduledAt  time.Time
	maintMutex        sync.RWMutex
	unsubscribeSecret string
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

	if cfg.VerificationLinkExpiry == 0 {
		cfg.VerificationLinkExpiry = 24 * time.Hour
	}
	if cfg.MagicLinkExpiry == 0 {
		cfg.MagicLinkExpiry = 15 * time.Minute
	}
	if cfg.PortalSessionDuration == 0 {
		cfg.PortalSessionDuration = 24 * time.Hour
	}

	ctx, cancel := context.WithCancel(context.Background())

	srv := &Server{
		cfg:              cfg,
		chiselServer:     chiselSrv,
		registry:         registry,
		proxyHandler:     proxyHandler,
		chiselProxy:      chiselProxy,
		db:               database,
		mailSender:       mailSender,
		ctx:              ctx,
		cancel:           cancel,
		rateLimiters:     make(map[string]*rate.Limiter),
		violations:       make(map[string]int),
		metricsQueue:     make(chan *db.TunnelMetric, 1000),
		targetedMessages: make(map[string]string),
	}

	srv.registry.OnLeaseCleanup = func(lease *TunnelLease) {
		if lease.BytesIn > 0 || lease.BytesOut > 0 {
			m := &db.TunnelMetric{
				UserID:          lease.UserID,
				SubdomainPrefix: lease.SubdomainPrefix,
				FullHost:        lease.FullHost,
				BytesIn:         int64(lease.BytesIn),
				BytesOut:        int64(lease.BytesOut),
				ConnectedAt:     lease.CreatedAt,
			}
			select {
			case srv.metricsQueue <- m:
			default:
				// Queue full, drop it
				log.Printf("[Server] Metrics queue full, dropping metrics for %s", lease.FullHost)
			}
		}
	}

	// Load DB blacklist and unsubscribe secret into cache
	if srv.db != nil {
		if list, err := srv.db.ListBlacklistedIPs(); err == nil {
			for _, entry := range list {
				srv.blacklist.Store(entry.IPAddress, true)
			}
		}

		// Initialize or load unsubscribe secret
		unsubSecret := ""
		val, err := srv.db.GetAdminSetting("unsubscribe_secret")
		if err == nil && val != "" {
			unsubSecret = val
		} else {
			// Generate secure 32-byte secret
			bytes := make([]byte, 32)
			_, _ = rand.Read(bytes)
			unsubSecret = hex.EncodeToString(bytes)
			_ = srv.db.SetAdminSetting("unsubscribe_secret", unsubSecret)
		}
		srv.unsubscribeSecret = unsubSecret

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

	go srv.processMetricsQueue()

	return srv, nil
}

func (s *Server) processMetricsQueue() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case m := <-s.metricsQueue:
			if s.db != nil {
				if err := s.db.RecordTunnelMetric(m); err != nil {
					log.Printf("[Server] Failed to record tunnel metrics for %s: %v", m.FullHost, err)
				}
			}
		case <-ticker.C:
			leases := s.registry.ListLeases()
			for _, lease := range leases {
				if lease.BytesIn > 0 || lease.BytesOut > 0 {
					m := &db.TunnelMetric{
						UserID:          lease.UserID,
						SubdomainPrefix: lease.SubdomainPrefix,
						FullHost:        lease.FullHost,
						BytesIn:         int64(lease.BytesIn),
						BytesOut:        int64(lease.BytesOut),
						ConnectedAt:     lease.CreatedAt,
					}
					if s.db != nil {
						if err := s.db.RecordTunnelMetric(m); err != nil {
							log.Printf("[Server] Failed to periodically record tunnel metrics for %s: %v", m.FullHost, err)
						}
					}
				}
			}
		}
	}
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
			privacyURL := s.cfg.PrivacyPolicyURL
			if privacyURL == "" {
				privacyURL = "/privacy"
			}
			cookieURL := s.cfg.CookiePolicyURL
			if cookieURL == "" {
				cookieURL = "/cookies"
			}

			s.maintMutex.RLock()
			isMaint := s.maintenanceMode
			maintScheduled := s.maintScheduledAt
			s.maintMutex.RUnlock()

			maintStr := "false"
			if isMaint {
				maintStr = "true"
			} else if !maintScheduled.IsZero() && time.Now().Before(maintScheduled) {
				maintStr = "pending"
			}

			consentStr := "false"
			if s.cfg.EnforcePolicyConsent {
				consentStr = "true"
			}

			respondJSON(w, http.StatusOK, map[string]string{
				"latest_version":         config.Version,
				"min_version":            s.cfg.MinClientVersion,
				"documentation_url":      s.cfg.DocumentationURL,
				"repository_url":         s.cfg.RepositoryURL,
				"privacy_policy_url":     privacyURL,
				"cookie_policy_url":      cookieURL,
				"maintenance_mode":       maintStr,
				"enforce_policy_consent": consentStr,
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

		if r.Method == http.MethodGet && r.URL.Path == "/api/unsubscribe" {
			s.handleUnsubscribe(w, r)
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

		if r.Method == http.MethodGet && r.URL.Path == "/setup" {
			s.handleSetupPage(w, r)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/complete-setup" {
			s.handleCompleteSetup(w, r)
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
		if r.Method == http.MethodGet && r.URL.Path == "/api/auth/report" {
			s.handleAuthReport(w, r)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/auth/report-registration" {
			s.handleReportRegistration(w, r)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/auth/decline" {
			s.handleAuthDecline(w, r)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/auth/verify" {
			s.handleAdminVerify(w, r)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/auth/mfa-verify" {
			s.handleMFAVerify(w, r)
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

		if r.Method == http.MethodGet && r.URL.Path == "/api/me" {
			s.handleGetMe(w, r)
			return
		}

		if r.Method == http.MethodPut && r.URL.Path == "/api/me" {
			s.handleUpdateMe(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/mfa/setup" {
			s.handleMFASetup(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/mfa/enable" {
			s.handleMFAEnable(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/mfa/disable" {
			s.handleMFADisable(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/me/dismiss-message" {
			s.handleDismissMessage(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/analytics" {
			s.handleGetAnalytics(w, r)
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
			s.maintMutex.RLock()
			isMaint := s.maintenanceMode
			s.maintMutex.RUnlock()
			if isMaint {
				http.Error(w, "Service Unavailable - Gateway is currently undergoing maintenance.", http.StatusServiceUnavailable)
				return
			}
			r.URL.Path = "/"
			s.chiselProxy.ServeHTTP(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/privacy" {
			s.handlePrivacyFallback(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/cookies" {
			s.handleCookiesFallback(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/static/") {
			http.FileServer(http.FS(staticFS)).ServeHTTP(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/favicon.ico" {
			r.URL.Path = "/static/favicon.ico"
			http.FileServer(http.FS(staticFS)).ServeHTTP(w, r)
			return
		}

		if r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "/admin" || r.URL.Path == "/portal") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(dashboardHTML))
			return
		}
	}

	// Data plane requests -> Route to ProxyHandler
	s.maintMutex.RLock()
	isMaint := s.maintenanceMode
	s.maintMutex.RUnlock()
	if isMaint {
		s.handleVisitorMaintenancePage(w, r)
		return
	}

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
	user, ok := s.isValidToken(req.AuthToken)
	if !ok {
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
	if (req.ClientVersion != "" || req.ClientOS != "") && s.db != nil {
		if userRec, err := s.db.GetUser(user.ID); err == nil {
			changed := false
			if req.ClientVersion != "" && userRec.LastClientVersion != req.ClientVersion {
				userRec.LastClientVersion = req.ClientVersion
				changed = true
			}
			if req.ClientOS != "" && userRec.LastClientOS != req.ClientOS {
				userRec.LastClientOS = req.ClientOS
				changed = true
			}
			if changed {
				_ = s.db.UpdateUser(userRec)
			}
		}
	}

	// Register in registry
	sessionToken, remotes, err := s.registry.Register(user.ID, req.SubdomainPrefix, req.Ports, activeDomains, effectiveLimit, clientIP, req.BasicAuth, req.AddedHeaders)
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

	user, ok := s.isValidToken(token)
	if !ok || user == nil {
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
	// Periodic task: Prune expired magic links every hour
	go func() {
		ticker := time.NewTicker(s.cfg.PruneInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if s.db != nil {
					_ = s.db.PruneExpiredMagicLinks()
				}
			}
		}
	}()

	return srv.ListenAndServe()
}

// RegisterRequestPayload represents the payload to request developer registration.
type RegisterRequestPayload struct {
	Email         string `json:"email"`
	FirstName     string `json:"first_name"`
	LastName      string `json:"last_name"`
	PreferredName string `json:"preferred_name"`
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
				if s.db != nil {
					s.writeAudit(req.Email, "auth.registration_blocked", "ip", getClientIP(r), "Registration blocked by email domain whitelist", r)
				}
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "email domain not allowed by server configuration"})
				return
			}
		}
	}

	// Check if user already exists
	if existingUser, err := s.db.GetUser(req.Email); err == nil {
		// Log the attempt to the audit log to keep visibility for admins
		s.writeAudit(req.Email, "auth.registration_attempt_existing", "user", existingUser.Email, "Attempted to register but account already exists", r)

		// Mimic a successful registration to prevent email enumeration
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "success",
			"message": "registration request submitted. Please check your email to verify your account.",
		})
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
		PreferredName:     req.PreferredName,
		Role:              "user",
		Status:            "unverified",
		ApprovalToken:     approvalToken,
		VerificationToken: verificationToken,
		AuthMethod:        "registration",
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
		verifyURL := fmt.Sprintf("%s://%s/setup?token=%s", scheme, r.Host, verificationToken)
		subject := "[Liferay Tunnel] Complete Your Registration"

		greetingName := "there"

		clientIP := getClientIP(r)
		reportLink := fmt.Sprintf("%s://%s/api/auth/report-registration?token=%s", scheme, r.Host, verificationToken)

		body := fmt.Sprintf(`Hi %s,

Thank you for starting your registration! Because your email domain is pre-approved by your organization, you are just one step away from accessing your account.

Click the link below to verify your email and complete your profile setup:

This link will expire in 24 hours and can only be used once. If the link doesn't work, copy and paste this URL into your browser: %s

Once you finish filling in your details, an administrator will review and approve your account.

🛡️ Didn’t request this email?
This registration request originated from the IP address: %s.

If you did not attempt to register an account with us, please let our security team know by clicking below:

👉 %s

Note: Clicking this report link will instantly deactivate this registration token, preventing anyone from completing the sign-up process with your email address.

Best regards,

Liferay Tunnel Team`, html.EscapeString(greetingName), verifyURL, clientIP, reportLink)

		go func() {
			plainBody := fmt.Sprintf("Hi %s,\n\nPlease complete your registration by visiting: %s\n\nIf you did not request this, you can report it at: %s", greetingName, verifyURL, reportLink)
			if err := s.mailSender.Send(user.Email, subject, body, plainBody); err != nil {
				log.Printf("[Mail] Failed to send verification email to %s: %v", user.Email, err)
			}
		}()
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "success", "message": "registration request submitted. Please check your email to verify your account."})
}

// handleSetupPage serves the setup page to complete registration
func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	data, err := staticFS.ReadFile("static/setup.html")
	if err != nil {
		log.Printf("[Server] Failed to read setup.html from embedded FS: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleCompleteSetup processes the profile completion form.
func (s *Server) handleCompleteSetup(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, `{"error":"Database storage not enabled"}`, http.StatusNotImplemented)
		return
	}

	var req struct {
		Token         string `json:"token"`
		FirstName     string `json:"first_name"`
		LastName      string `json:"last_name"`
		PreferredName string `json:"preferred_name"`
		PolicyConsent bool   `json:"policy_consent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid payload"}`, http.StatusBadRequest)
		return
	}

	if s.cfg.EnforcePolicyConsent && !req.PolicyConsent {
		http.Error(w, `{"error":"You must acknowledge and agree to the Privacy Policy and Cookie Disclosures to complete your setup."}`, http.StatusBadRequest)
		return
	}

	user, err := s.db.GetUserByVerificationToken(req.Token)
	if err != nil || user.Status != "unverified" {
		http.Error(w, `{"error":"Invalid or expired token"}`, http.StatusBadRequest)
		return
	}
	if time.Now().UTC().After(user.CreatedAt.Add(s.cfg.VerificationLinkExpiry)) {
		http.Error(w, `{"error":"Verification link has expired. Please register again."}`, http.StatusBadRequest)
		return
	}

	user.FirstName = req.FirstName
	user.LastName = req.LastName
	user.PreferredName = req.PreferredName
	if user.PreferredName == "" {
		user.PreferredName = req.FirstName
	}

	if req.PolicyConsent {
		now := time.Now().UTC()
		user.PolicyConsentAt = &now
	}

	// Registration must be approved by admin
	user.Status = "pending"
	user.VerificationToken = ""
	// Keep user.ApprovalToken so the admin can approve!

	if err := s.db.UpdateUser(user); err != nil {
		http.Error(w, `{"error":"Failed to update user"}`, http.StatusInternalServerError)
		return
	}

	s.writeAudit(user.Email, "user.verified", "user", user.Email, "User completed setup and is pending approval", r)
	s.sendAdminAlert("alert_notify_registration", "LFR Tunnel Alert: New User Registration", fmt.Sprintf("A new user (%s %s - %s) has verified their email and requires approval.", user.FirstName, user.LastName, user.Email))

	// Send approval email to admin
	if s.mailSender != nil && s.cfg.AdminNotificationEmail != "" {
		sendAdminEmail := true
		if s.db != nil {
			if adminUser, err := s.db.GetUserByEmail(s.cfg.AdminNotificationEmail); err == nil && adminUser != nil {
				if adminUser.NotificationPrefs == "disabled" {
					sendAdminEmail = false
				}
			}
		}

		if sendAdminEmail {
			subject := "[Liferay Tunnel] New Developer Registration Request"
			scheme := "http"
			if s.cfg.SSLCertFile != "" {
				scheme = "https"
			}
			approveURL := fmt.Sprintf("%s://%s/api/admin/approve?email=%s&token=%s", scheme, r.Host, url.QueryEscape(user.Email), user.ApprovalToken)
			body := fmt.Sprintf("<p>New registration request (Email Verified & Setup Complete):</p><ul><li>Name: %s %s</li><li>Email: %s</li></ul><p><a href=\"%s\">Click here to approve this request</a></p>", user.FirstName, user.LastName, user.Email, approveURL)

			plainBody := fmt.Sprintf("New user registered: %s. Approve here: %s", user.Email, approveURL)
			go func() { _ = s.mailSender.Send(s.cfg.AdminNotificationEmail, subject, body, plainBody) }()
		}
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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
	if time.Now().UTC().After(user.CreatedAt.Add(s.cfg.VerificationLinkExpiry)) {
		http.Error(w, "Verification link has expired. Please register again.", http.StatusBadRequest)
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
			plainBody := fmt.Sprintf("A new user (%s) requires approval. Approve here: %s", user.Email, approveURL)
			if err := s.mailSender.Send(s.cfg.AdminNotificationEmail, subject, body, plainBody); err != nil {
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
		plainBody := fmt.Sprintf("Your registration has been approved. Claim your token here: %s", claimURL)
		if err := s.mailSender.Send(user.Email, subject, body, plainBody); err != nil {
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

// isValidToken checks if a token is valid, checking personal access tokens (PATs)
// in the database.
func (s *Server) isValidToken(token string) (*db.User, bool) {
	if token == "" {
		return nil, false
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
					return user, true
				}
			}
		}
	}

	return nil, false
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
	cookie, err := r.Cookie("lfr_session")
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

	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/analytics/clients" {
		stats, err := s.db.GetClientVersionStats()
		if err != nil {
			http.Error(w, `{"error":"Failed to get client stats"}`, http.StatusInternalServerError)
			return
		}
		respondJSON(w, http.StatusOK, stats)
		return
	}

	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/users" {

		s.handleAdminListUsers(w, r, actor)
		return
	}

	if r.Method == http.MethodPost && r.URL.Path == "/api/admin/broadcast" {
		s.handleAdminBroadcast(w, r, actor)
		return
	}

	if r.Method == http.MethodPost && r.URL.Path == "/api/admin/maintenance" {
		s.handleAdminMaintenance(w, r, actor)
		return
	}

	if r.Method == http.MethodPost && r.URL.Path == "/api/admin/targeted-message" {
		s.handleAdminTargetedMessage(w, r, actor)
		return
	}

	if r.Method == http.MethodPost && r.URL.Path == "/api/admin/invite" {
		s.handleAdminInviteUser(w, r, actor)
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

	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/magic-links" {
		s.handleAdminListMagicLinks(w, r)
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

	// Enforce domain whitelist if configured (bypass for owner)
	if len(s.cfg.AllowedEmailDomains) > 0 && !isOwner {
		allowed := false
		for _, d := range s.cfg.AllowedEmailDomains {
			if strings.HasSuffix(req.Email, "@"+d) {
				allowed = true
				break
			}
		}
		if !allowed {
			// Fail silently to prevent domain-enumeration or email-enumeration attacks
			respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}
	}
	var isApproved bool
	greetingName := "there"

	if s.db != nil {
		user, err := s.db.GetUserByEmail(req.Email)
		if err == nil {
			if user.Status == "approved" {
				isApproved = true
			}
			if user.PreferredName != "" {
				greetingName = user.PreferredName
			}
		}
	}

	if isOwner {
		isApproved = true
	}

	if !isApproved {
		// Do not leak existence
		respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	magicToken, _ := generateSecureToken()
	clientIP := getClientIP(r)

	expiresAt := time.Now().Add(s.cfg.MagicLinkExpiry)
	if s.db != nil {
		h := sha256.Sum256([]byte(magicToken))
		tokenHash := hex.EncodeToString(h[:])
		_ = s.db.CreateMagicLink(req.Email, tokenHash, clientIP, expiresAt)
	} else {
		sessionData := PortalSessionData{
			Email:     req.Email,
			ExpiresAt: expiresAt,
			ClientIP:  clientIP,
		}
		s.portalMap.Store("admin_magic_"+magicToken, sessionData)
	}

	s.writeAudit(req.Email, "auth.magic_link_requested", "system", "auth", "Requested portal login link", r)

	if s.mailSender != nil {
		host := r.Host
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		link := fmt.Sprintf("%s://%s/portal?token=%s", scheme, host, magicToken)
		reportLink := fmt.Sprintf("%s://%s/api/auth/report?token=%s", scheme, host, magicToken)

		tmplStr := ""
		if s.db != nil {
			tmplStr, _ = s.db.GetAdminSetting("magic_link_email_template")
		}
		if tmplStr == "" {
			tmplStr = defaultMagicLinkEmailTemplate
		}

		tmpl, err := template.New("email").Parse(tmplStr)
		var bodyBuf bytes.Buffer
		if err == nil {
			err = tmpl.Execute(&bodyBuf, MagicLinkEmailData{
				PreferredName: greetingName,
				ExpiryMinutes: 15,
				MagicLink:     link,
				IPAddress:     clientIP,
				ReportLink:    reportLink,
			})
		}

		var body string
		if err != nil {
			// Fallback if template parsing fails
			log.Printf("[Admin] Magic link template error: %v", err)
			body = fmt.Sprintf("<p>Hi %s,</p>"+
				"<p>You requested a magic link to log into the Liferay Tunnel Portal.</p>"+
				"<p><strong>IP Address:</strong> %s</p>"+
				"<p>This link expires in 15 minutes.</p>"+
				"<p><a href=\"%s\">Log In to Portal</a></p>", html.EscapeString(greetingName), clientIP, link)
		} else {
			body = bodyBuf.String()
		}

		plainBody := fmt.Sprintf("Hi %s,\n\nUse this link to log in (expires in 15 minutes):\n%s\n\nReport abuse here:\n%s", greetingName, link, reportLink)
		go s.mailSender.Send(req.Email, "Your magic login link", body, plainBody) //nolint:errcheck
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

	var email string

	if s.db != nil {
		h := sha256.Sum256([]byte(req.Token))
		tokenHash := hex.EncodeToString(h[:])
		link, err := s.db.GetMagicLink(tokenHash)
		if err != nil || link == nil || link.UsedAt != nil {
			respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid or already used token"})
			return
		}
		if time.Now().After(link.ExpiresAt) {
			respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "Token has expired"})
			return
		}
		_ = s.db.MarkMagicLinkUsed(link.ID)
		_ = s.db.InvalidateOtherMagicLinks(link.Email, link.ID)
		email = link.Email
	} else {
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
		email = sessionData.Email
	}
	sessionToken, _ := generateSecureToken()
	clientIP := getClientIP(r)

	var previousLoginAt *time.Time
	var user *db.User
	if s.db != nil {
		u, err := s.db.GetUserByEmail(email)
		if (err != nil || u == nil) && s.cfg.Owner.UserID != "" && strings.EqualFold(email, s.cfg.Owner.UserID) {
			// Auto-create owner in DB
			u = &db.User{
				ID:        s.cfg.Owner.UserID,
				Email:     email,
				FirstName: s.cfg.Owner.Name,
				Role:      "owner",
				Status:    "approved",
			}
			_ = s.db.CreateUser(u)
		}
		user = u
		if u != nil {
			if u.LastLoginAt != nil {
				// Capture a copy of the previous login time
				prev := *u.LastLoginAt
				previousLoginAt = &prev
			}
		}
	}

	// Maintenance Mode Block for standard users: Allow only admins and owners.
	s.maintMutex.RLock()
	isMaint := s.maintenanceMode
	s.maintMutex.RUnlock()
	if isMaint {
		isAdmin := false
		if user != nil {
			if user.Role == "admin" || user.Role == "owner" {
				isAdmin = true
			}
		}
		if !isAdmin && s.cfg.Owner.UserID != "" && strings.EqualFold(email, s.cfg.Owner.UserID) {
			isAdmin = true
		}
		if !isAdmin {
			respondJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "The portal is currently undergoing administrative maintenance. Please try again later.",
			})
			return
		}
	}

	// MFA INTERCEPT: If the user has MFA enabled, do not establish the session yet.
	// Respond with status="mfa_required" and a short-lived temp_token.
	if user != nil && user.TOTPEnabled {
		tempToken, _ := generateSecureToken()
		s.portalMap.Store("pre_auth_"+tempToken, PortalSessionData{
			Email:     email,
			ClientIP:  clientIP,
			ExpiresAt: time.Now().Add(5 * time.Minute),
		})
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"status":     "mfa_required",
			"temp_token": tempToken,
		})
		return
	}

	// Standard login (MFA is not enabled)
	if s.db != nil && user != nil {
		now := time.Now().UTC()
		user.LastLoginAt = &now
		user.LastLoginIP = clientIP
		_ = s.db.UpdateUser(user)
	}

	killedPreviousSession := false
	s.portalMap.Range(func(key, value interface{}) bool {
		k := key.(string)
		if strings.HasPrefix(k, "admin_session_") {
			sessionData := value.(PortalSessionData)
			if sessionData.Email == email {
				s.portalMap.Delete(k)
				killedPreviousSession = true
			}
		}
		return true
	})

	s.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:                 email,
		ExpiresAt:             time.Now().Add(s.cfg.PortalSessionDuration),
		ClientIP:              clientIP,
		PreviousLoginAt:       previousLoginAt,
		KilledPreviousSession: killedPreviousSession,
	})

	s.writeAudit(email, "admin.login", "system", "admin", "Admin logged into dashboard via magic link", r)

	cookie := &http.Cookie{
		Name:     "lfr_session",
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
	cookie, err := r.Cookie("lfr_session")
	if err == nil {
		s.portalMap.Delete("admin_session_" + cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "lfr_session",
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

	filtered := make([]*db.User, 0, len(users))
	for _, u := range users {
		if actor == s.cfg.Owner.UserID {
			filtered = append(filtered, u)
		} else {
			if u.Email == actor {
				filtered = append(filtered, u)
			} else if u.Email == s.cfg.Owner.UserID {
				continue
			} else if u.Role == "admin" {
				continue
			} else {
				filtered = append(filtered, u)
			}
		}
	}

	_ = json.NewEncoder(w).Encode(filtered)
}

func (s *Server) handleAdminInviteUser(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}

	var req struct {
		Email     string `json:"email"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid payload"}`, http.StatusBadRequest)
		return
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	if req.Email == "" || !strings.Contains(req.Email, "@") {
		http.Error(w, `{"error":"Invalid email address"}`, http.StatusBadRequest)
		return
	}

	// Domain Validation
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
				s.writeAudit(actor, "auth.invite_blocked", "user", req.Email, "Invite blocked by email domain whitelist", r)
				http.Error(w, `{"error":"Email domain not allowed by server configuration whitelist"}`, http.StatusForbidden)
				return
			}
		}
	}

	// Check if exists
	if _, err := s.db.GetUserByEmail(req.Email); err == nil {
		http.Error(w, `{"error":"User already exists or registration is pending"}`, http.StatusConflict)
		return
	}

	// Create User
	user := &db.User{
		ID:              req.Email,
		Email:           req.Email,
		FirstName:       req.FirstName,
		LastName:        req.LastName,
		PreferredName:   req.FirstName,
		Role:            "user",
		Status:          "approved", // Instant approval because invited by Admin
		ThemePreference: "system",
		AuthMethod:      "invite",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
	if err := s.db.CreateUser(user); err != nil {
		http.Error(w, `{"error":"Failed to create user"}`, http.StatusInternalServerError)
		return
	}

	// Send Magic Link Invite Email
	magicToken, _ := generateSecureToken()
	clientIP := getClientIP(r)
	expiresAt := time.Now().Add(s.cfg.InviteLinkExpiry)
	h := sha256.Sum256([]byte(magicToken))
	tokenHash := hex.EncodeToString(h[:])
	_ = s.db.CreateMagicLink(req.Email, tokenHash, clientIP, expiresAt)

	inviteLink := fmt.Sprintf("https://%s/api/auth/verify?token=%s", r.Host, magicToken)
	declineLink := fmt.Sprintf("https://%s/api/auth/decline?token=%s", r.Host, magicToken)

	subject := fmt.Sprintf("%s has invited you to join Liferay Tunnel", actor)
	body := fmt.Sprintf(`Hi there,

%s has created an account for you on Liferay Tunnel.

Your email domain is pre-approved, so you just need to click the link below to verify your email and set up your profile details:

This invitation link will expire in 7 days. If the button doesn't work, copy and paste this URL into your browser: %s

Once your profile setup is complete, you will be redirected straight to your new dashboard.

🛡️ Wrong email or received this by mistake?
This invitation was generated by an administrator from within our portal.

If you do not know %s, or believe this invitation was sent to you in error, you can decline it and deactivate the link below:

👉 %s

What happens next? Declining will instantly invalidate this invitation link. It will also notify the administrator so they can correct their records.

Best regards,

Liferay Tunnel Team`, actor, inviteLink, actor, declineLink)

	if s.mailSender != nil {
		plainBody := fmt.Sprintf("Hi there,\n\nYou have been invited by an administrator to use the Liferay Tunnel portal.\n\nLog in here: %s\n\nDecline here: %s", inviteLink, declineLink)
		go func() { _ = s.mailSender.Send(req.Email, subject, body, plainBody) }()
	}

	s.writeAudit(actor, "user.invited", "user", req.Email, "Admin invited new user", r)

	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAdminBroadcast(w http.ResponseWriter, r *http.Request, actor string) {
	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid payload"}`, http.StatusBadRequest)
		return
	}

	s.broadcastMutex.Lock()
	s.broadcastMessage = req.Message
	s.broadcastMutex.Unlock()

	s.writeAudit(actor, "admin.broadcast", "system", "all", "Admin updated global broadcast message", r)
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAdminMaintenance(w http.ResponseWriter, r *http.Request, actor string) {
	var req struct {
		Enabled          bool `json:"enabled"`
		CountdownMinutes *int `json:"countdown_minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid payload"}`, http.StatusBadRequest)
		return
	}

	s.maintMutex.Lock()
	defer s.maintMutex.Unlock()

	// Cancel any pending timers if disabling or toggling state
	if s.maintTimer != nil {
		s.maintTimer.Stop()
		s.maintTimer = nil
	}

	action := "system.maintenance_disabled"
	desc := "Admin disabled system maintenance mode"

	if req.Enabled {
		countdown := 0
		if req.CountdownMinutes != nil {
			countdown = *req.CountdownMinutes
		}

		if countdown > 0 {
			// Schedule maintenance
			s.maintenanceMode = false
			s.maintScheduledAt = time.Now().Add(time.Duration(countdown) * time.Minute)
			s.maintTimer = time.AfterFunc(time.Duration(countdown)*time.Minute, func() {
				s.maintMutex.Lock()
				s.maintenanceMode = true
				s.maintScheduledAt = time.Time{}
				s.maintTimer = nil
				s.maintMutex.Unlock()

				// Forcefully kick all active standard (non-admin) tunnel leases
				leases := s.registry.ListLeases()
				for _, lease := range leases {
					isLeaseAdmin := false
					if s.db != nil {
						if u, err := s.db.GetUserByEmail(lease.UserID); err == nil && u != nil {
							if u.Role == "admin" || u.Role == "owner" {
								isLeaseAdmin = true
							}
						}
					}
					if !isLeaseAdmin && s.cfg.Owner.UserID != "" && strings.EqualFold(lease.UserID, s.cfg.Owner.UserID) {
						isLeaseAdmin = true
					}
					if !isLeaseAdmin {
						s.registry.KickLease(lease.SubdomainPrefix)
					}
				}
				log.Printf("[Server] Scheduled Maintenance countdown hit 0. Gateway Maintenance Mode is now ACTIVE.")
			})

			action = "system.maintenance_scheduled"
			desc = fmt.Sprintf("Admin scheduled system maintenance starting in %d minutes", countdown)
		} else {
			// Immediate activation
			s.maintenanceMode = true
			s.maintScheduledAt = time.Time{}

			// Kick all active standard (non-admin) tunnel leases
			leases := s.registry.ListLeases()
			for _, lease := range leases {
				isLeaseAdmin := false
				if s.db != nil {
					if u, err := s.db.GetUserByEmail(lease.UserID); err == nil && u != nil {
						if u.Role == "admin" || u.Role == "owner" {
							isLeaseAdmin = true
						}
					}
				}
				if !isLeaseAdmin && s.cfg.Owner.UserID != "" && strings.EqualFold(lease.UserID, s.cfg.Owner.UserID) {
					isLeaseAdmin = true
				}
				if !isLeaseAdmin {
					s.registry.KickLease(lease.SubdomainPrefix)
				}
			}

			action = "system.maintenance_enabled"
			desc = "Admin enabled system maintenance mode immediately"
		}
	} else {
		// Complete deactivation
		s.maintenanceMode = false
		s.maintScheduledAt = time.Time{}
	}

	s.writeAudit(actor, action, "system", "all", desc, r)

	maintStr := "false"
	if s.maintenanceMode {
		maintStr = "true"
	} else if !s.maintScheduledAt.IsZero() && time.Now().Before(s.maintScheduledAt) {
		maintStr = "pending"
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":           "ok",
		"maintenance_mode": maintStr,
	})
}

func (s *Server) handleVisitorMaintenancePage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Scheduled Maintenance | Liferay Tunnel</title>
    <link rel="icon" type="image/x-icon" href="/favicon.ico">
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;600;800&display=swap" rel="stylesheet">
    <style>
        body {
            font-family: 'Outfit', sans-serif;
            background: linear-gradient(135deg, #0f172a 0%, #111827 100%);
            color: #f8fafc;
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
            padding: 24px;
            margin: 0;
        }
        .card {
            background: rgba(30, 41, 59, 0.7);
            border: 1px solid rgba(255, 255, 255, 0.08);
            backdrop-filter: blur(20px);
            border-radius: 24px;
            padding: 48px 32px;
            max-width: 480px;
            width: 100%;
            text-align: center;
            box-shadow: 0 20px 40px rgba(0, 0, 0, 0.3);
        }
        .icon { font-size: 48px; margin-bottom: 20px; display: block; }
        h1 { font-size: 26px; font-weight: 800; margin-bottom: 12px; color: #f8fafc; }
        p { font-size: 15px; color: #94a3b8; line-height: 1.6; margin-bottom: 24px; font-weight: 300; }
        .footer { font-size: 11px; color: #475569; letter-spacing: 1px; }
    </style>
</head>
<body>
    <div class="card">
        <span class="icon">🛠️</span>
        <h1>Scheduled Maintenance</h1>
        <p>This Liferay Tunnel environment is currently undergoing scheduled administrative maintenance. Standard connection tunnels are paused, but we expect to be fully back online shortly. Thank you for your patience!</p>
        <div class="footer">LFR-GATEWAY &bull; MAINTENANCE MODE</div>
    </div>
</body>
</html>`))
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

	if actor != s.cfg.Owner.UserID {
		if user.Email == s.cfg.Owner.UserID || (user.Role == "admin" && user.Email != actor) {
			http.Error(w, `{"error":"Forbidden: Cannot view this user"}`, http.StatusForbidden)
			return
		}
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
		Role     *string `json:"role"`
		Status   *string `json:"status"`
		ResetMFA *bool   `json:"reset_mfa"`
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

	if actor != s.cfg.Owner.UserID {
		if user.Email == s.cfg.Owner.UserID || user.Role == "admin" || user.Email == actor {
			http.Error(w, `{"error":"Forbidden: Cannot modify this user"}`, http.StatusForbidden)
			return
		}
	} else if user.Email == s.cfg.Owner.UserID {
		http.Error(w, `{"error":"Forbidden: Cannot modify owner account status"}`, http.StatusForbidden)
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

	if req.ResetMFA != nil && *req.ResetMFA {
		details["mfa_reset"] = true
		user.TOTPSecret = ""
		user.TOTPEnabled = false
	}

	if err := s.db.UpdateUser(user); err != nil {
		http.Error(w, `{"error":"Failed to update user"}`, http.StatusInternalServerError)
		return
	}

	// Send role update email notification if configured and user has not unsubscribed
	if req.Role != nil && s.mailSender != nil && user.NotificationPrefs != "disabled" {
		subject := "Liferay Tunnel: Account Role Updated"
		greetingName := user.FirstName
		if greetingName == "" {
			greetingName = "there"
		}

		unsubToken := s.GenerateUnsubscribeToken(user.Email)
		unsubLink := fmt.Sprintf("https://%s/api/unsubscribe?token=%s", r.Host, unsubToken)

		var body string
		if *req.Role == "admin" {
			body = fmt.Sprintf(`Hi %s,<br/><br/>
Your account role has been updated on Liferay Tunnel. You have been <strong>promoted to an Administrator</strong>.<br/><br/>
You can now access administrative options on the dashboard, including managing user registrations, viewing system-wide audit logs, managing IP blacklists, scheduling maintenance modes, and broadcasting custom targeted messages to active developers.<br/><br/>
Best regards,<br/>
Liferay Tunnel Team<br/><br/>
<hr style="border:0;border-top:1px solid #eee;margin:20px 0;"/>
<p style="font-size:11px;color:#999;">You received this email because your account role was updated by an administrator. If you wish to opt-out of optional notifications, you can <a href="%s">unsubscribe with one click</a>.</p>`, html.EscapeString(greetingName), unsubLink)
		} else {
			body = fmt.Sprintf(`Hi %s,<br/><br/>
Your account role has been updated on Liferay Tunnel. Your role has been set to <strong>User</strong>.<br/><br/>
You can continue to create and manage personal access tokens and connect tunnels cleanly from your client CLI.<br/><br/>
Best regards,<br/>
Liferay Tunnel Team<br/><br/>
<hr style="border:0;border-top:1px solid #eee;margin:20px 0;"/>
<p style="font-size:11px;color:#999;">You received this email because your account role was updated by an administrator. If you wish to opt-out of optional notifications, you can <a href="%s">unsubscribe with one click</a>.</p>`, html.EscapeString(greetingName), unsubLink)
		}

		plainBody := fmt.Sprintf("Hi %s,\n\nYour account role has been updated on Liferay Tunnel to: %s.\n\nUnsubscribe from optional emails here: %s\n\nBest regards,\nLiferay Tunnel Team", greetingName, *req.Role, unsubLink)
		go func() { _ = s.mailSender.Send(user.Email, subject, body, plainBody) }()
	}

	detailsBytes, _ := json.Marshal(details)
	action := "user.updated"
	if req.Role != nil {
		action = "user.role_changed"
	} else if req.Status != nil {
		action = "user.status_changed"
	} else if req.ResetMFA != nil && *req.ResetMFA {
		action = "user.mfa_reset"
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

	// Check the admin user's personal notification preferences
	if adminUser, err := s.db.GetUserByEmail(s.cfg.AdminNotificationEmail); err == nil && adminUser != nil {
		if adminUser.NotificationPrefs == "disabled" {
			return
		}
	}

	go func() {
		if err := s.mailSender.Send(s.cfg.AdminNotificationEmail, subject, htmlBody, "An IP address has been blacklisted."); err != nil {
			log.Printf("[Mail] Failed to send admin alert %s: %v", settingKey, err)
		}
	}()
}

// respondJSON is a DRY helper for sending JSON API responses
func respondJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
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
	Email                 string
	ExpiresAt             time.Time
	ClientIP              string
	PreviousLoginAt       *time.Time
	KilledPreviousSession bool
}

func (s *Server) handleAdminListMagicLinks(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		respondJSON(w, http.StatusOK, []interface{}{})
		return
	}
	links, err := s.db.ListMagicLinks()
	if err != nil {
		http.Error(w, `{"error":"Failed to list magic links"}`, http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, links)
}

func (s *Server) handleAdminTargetedMessage(w http.ResponseWriter, r *http.Request, actor string) {
	var req struct {
		UserID  string `json:"user_id"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid payload"}`, http.StatusBadRequest)
		return
	}

	if req.UserID == "" {
		http.Error(w, `{"error":"user_id is required"}`, http.StatusBadRequest)
		return
	}

	s.targetedMutex.Lock()
	if req.Message == "" {
		delete(s.targetedMessages, req.UserID)
	} else {
		s.targetedMessages[req.UserID] = req.Message
	}
	s.targetedMutex.Unlock()

	s.writeAudit(actor, "admin.targeted_message", "user", req.UserID, "Admin sent targeted message", r)
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDismissMessage(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	s.targetedMutex.Lock()
	delete(s.targetedMessages, user.ID)
	s.targetedMutex.Unlock()

	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handlePrivacyFallback(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Privacy Policy - Liferay Tunnel</title>
    <link rel="icon" type="image/x-icon" href="/favicon.ico">
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
            background-color: #0d1117;
            color: #c9d1d9;
            line-height: 1.6;
            margin: 0;
            padding: 40px 24px;
        }
        .container {
            max-width: 800px;
            margin: 0 auto;
            background: rgba(255, 255, 255, 0.03);
            border: 1px solid rgba(255, 255, 255, 0.1);
            border-radius: 8px;
            padding: 32px;
        }
        h1 { color: #58a6ff; font-size: 28px; margin-top: 0; border-bottom: 1px solid rgba(255, 255, 255, 0.1); padding-bottom: 12px; }
        h2 { color: #58a6ff; font-size: 20px; margin-top: 24px; }
        p, li { font-size: 15px; color: #8b949e; }
        ul { padding-left: 20px; }
        a { color: #58a6ff; text-decoration: none; }
        a:hover { text-decoration: underline; }
    </style>
</head>
<body>
    <div class="container">
        <h1>Privacy Policy</h1>
        <p>This Privacy Policy describes how this self-hosted Liferay Tunnel (lfr-tunneld) gateway processes your data.</p>
        <h2>1. Information We Collect & Process</h2>
        <ul>
            <li><strong>IP Addresses</strong>: Processed strictly to route packets and enforce auto-ban security protections.</li>
            <li><strong>Email Addresses</strong>: Processed for account verification, approval notifications, and passwordless magic link logins.</li>
            <li><strong>Audit Logs</strong>: Records administrative actions, token creations, and security violations locally in the gateway database.</li>
        </ul>
        <h2>2. Data Security & Storage</h2>
        <p>Personal Access Tokens (PATs) are stored on the server using secure SHA-256 cryptographic hashes. All data is stored in a local, private SQLite database and is never shared, sold, or transmitted to external servers.</p>
        <p style="margin-top: 32px;"><a href="/">← Return to Portal</a></p>
    </div>
</body>
</html>`))
}

func (s *Server) handleCookiesFallback(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Cookie Disclosure - Liferay Tunnel</title>
    <link rel="icon" type="image/x-icon" href="/favicon.ico">
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
            background-color: #0d1117;
            color: #c9d1d9;
            line-height: 1.6;
            margin: 0;
            padding: 40px 24px;
        }
        .container {
            max-width: 800px;
            margin: 0 auto;
            background: rgba(255, 255, 255, 0.03);
            border: 1px solid rgba(255, 255, 255, 0.1);
            border-radius: 8px;
            padding: 32px;
        }
        h1 { color: #58a6ff; font-size: 28px; margin-top: 0; border-bottom: 1px solid rgba(255, 255, 255, 0.1); padding-bottom: 12px; }
        h2 { color: #58a6ff; font-size: 20px; margin-top: 24px; }
        p, li { font-size: 15px; color: #8b949e; }
        ul { padding-left: 20px; }
        a { color: #58a6ff; text-decoration: none; }
        a:hover { text-decoration: underline; }
    </style>
</head>
<body>
    <div class="container">
        <h1>Cookie Disclosure</h1>
        <p>This web application utilizes exactly <strong>one cookie</strong> to maintain your session.</p>
        <h2>Strictly Necessary Cookies</h2>
        <ul>
            <li><strong>Cookie Name</strong>: <code>lfr_session</code></li>
            <li><strong>Type</strong>: First-party, HTTP-Only, Secure, SameSite=Lax</li>
            <li><strong>Purpose</strong>: This cookie is strictly necessary to keep you securely logged into your portal session. It contains no tracking data or personal identifiers.</li>
            <li><strong>Consent</strong>: Under GDPR and the ePrivacy Directive, strictly necessary session cookies are exempt from requiring consent banner popups.</li>
        </ul>
        <p style="margin-top: 32px;"><a href="/">← Return to Portal</a></p>
    </div>
</body>
</html>`))
}
