package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"log"
	"log/slog"
	mathrand "math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	texttemplate "text/template"
	"time"

	"golang.org/x/time/rate"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
	"lfr-tunnel/pkg/mail"
	"lfr-tunnel/pkg/nginx"
	"lfr-tunnel/pkg/webhook"

	"github.com/gorilla/websocket"
	chserver "github.com/jpillora/chisel/server"
)

//go:embed dashboard.html
var dashboardHTML string

//go:embed static/*
var staticFS embed.FS

//go:embed templates/*
var templatesFS embed.FS

// RegisterRequest represents the JSON request payload for registering a tunnel.
type RegisterRequest struct {
	SubdomainPrefix string            `json:"subdomain_prefix"`
	CustomDomain    string            `json:"custom_domain,omitempty"`
	Ports           []PortMapping     `json:"ports"`
	AuthToken       string            `json:"auth_token"`
	RateLimit       int               `json:"rate_limit,omitempty"`
	BasicAuth       string            `json:"basic_auth,omitempty"`
	AddedHeaders    map[string]string `json:"added_headers,omitempty"`
	ClientVersion   string            `json:"client_version,omitempty"`
	ClientOS        string            `json:"client_os,omitempty"`
	Passcode        string            `json:"passcode,omitempty"`
	WhitelistIPs    string            `json:"whitelist_ips,omitempty"`
}

// RegisterResponse represents the JSON response payload.
type RegisterResponse struct {
	Status             string   `json:"status"`
	SessionToken       string   `json:"session_token,omitempty"`
	SubdomainPrefix    string   `json:"subdomain_prefix,omitempty"`
	Remotes            []string `json:"remotes,omitempty"`
	Domains            []string `json:"domains,omitempty"`
	Error              string   `json:"error,omitempty"`
	Warning            string   `json:"warning,omitempty"`
	PortalURL          string   `json:"portal_url,omitempty"`
	LanguagePreference string   `json:"language_preference,omitempty"`
	ThemePreference    string   `json:"theme_preference,omitempty"`
	ServerVersion      string   `json:"server_version,omitempty"`
}

// CheckSubdomainResponse represents the JSON response payload for subdomain checks.
type CheckSubdomainResponse struct {
	Available   bool     `json:"available"`
	Subdomain   string   `json:"subdomain"`
	Reason      string   `json:"reason,omitempty"`
	Suggestions []string `json:"suggestions,omitempty"`
}

// ipLimiter wraps a rate.Limiter and a lastSeen timestamp for stale entry cleanup.
type ipLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type EdgeHealthStatus struct {
	Status       string `json:"status"`
	LatencyMs    int64  `json:"latency_ms"`
	LastCheckAt  int64  `json:"last_check_at"`
	ErrorMessage string `json:"error_message,omitempty"`
	ResolvedIP   string `json:"resolved_ip,omitempty"`
	Version      string `json:"version,omitempty"`
}

type safeConn struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (s *safeConn) WriteJSON(v interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(v)
}

func (s *safeConn) WriteMessage(messageType int, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteMessage(messageType, data)
}

func (s *safeConn) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.Close()
}

func (s *safeConn) RemoteAddr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.RemoteAddr()
}

// Server coordinates the entire gateway operations.
type Server struct {
	cfg                *config.ServerConfig
	chiselServer       *chserver.Server
	registry           *Registry
	proxyHandler       *ProxyHandler
	chiselProxy        *httputil.ReverseProxy
	db                 *db.DB
	portalService      PortalService
	notifications      *NotificationService
	ctx                context.Context
	cancel             context.CancelFunc
	rateLimiters       map[string]*ipLimiter
	rlMutex            sync.Mutex
	violations         map[string]int
	vMutex             sync.Mutex
	blacklist          sync.Map // memory cache for db blacklist
	portalMap          sync.Map // memory cache for portal magic links and sessions
	metrics            *MetricsCollector
	nginxManager       *nginx.MaintenanceManager
	caCert             *x509.Certificate
	caKey              *rsa.PrivateKey
	broadcastMutex     sync.RWMutex
	broadcastMessage   string
	targetedMessages   map[string]string
	targetedMutex      sync.RWMutex
	maintenanceMode    bool
	maintTimer         *time.Timer
	maintScheduledAt   time.Time
	maintMutex         sync.RWMutex
	unsubscribeSecret  string
	translations       map[string]map[string]string
	lastPortalActivity map[string]time.Time
	portalActivityMu   sync.RWMutex
	maintReason        string
	maintAction        string
	maintDuration      int
	maintEndTime       time.Time
	wsClients          map[*wsClient]bool
	wsMutex            sync.RWMutex
	edgeClients        map[string]*safeConn
	edgeVersions       map[string]string // node_id -> version
	edgeIPs            map[string]string // node_id -> public IP
	edgeClientsMu      sync.RWMutex
	startTime          time.Time
	edgeLeases         map[string][]EdgeLease
	edgeLeasesMu       sync.Mutex
	edgeHealth         map[string]EdgeHealthStatus
	edgeHealthMu       sync.RWMutex
	outboundConnected  bool
	outboundMutex      sync.RWMutex
	userCache          sync.Map // email -> *db.User cache to prevent SQLite read contention
	httpServer         *http.Server
	redirectSrv        *http.Server
	webhooks           *webhook.WebhookService
	lastTestTimes      map[string]time.Time
	testLimiterMu      sync.Mutex
	roundRobinCounter  uint64
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
	proxyHandler := NewProxyHandler(registry, cfg)

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

	var caCert *x509.Certificate
	var caKey *rsa.PrivateKey
	if cfg.ClientCAFile != "" && cfg.ClientCAKeyFile != "" {
		var err error
		caCert, caKey, err = LoadOrCreateCA(cfg.ClientCAFile, cfg.ClientCAKeyFile)
		if err != nil {
			slog.Info(fmt.Sprintf("[Server] Failed to load/create client CA: %v", err))
		} else {
			slog.Info(fmt.Sprintf("[Server] Loaded Client Root CA from certificate: %s", cfg.ClientCAFile))
		}
	}

	ctx, cancel := context.WithCancel(context.Background())

	srv := &Server{
		cfg:                cfg,
		chiselServer:       chiselSrv,
		registry:           registry,
		proxyHandler:       proxyHandler,
		chiselProxy:        chiselProxy,
		db:                 database,
		notifications:      NewNotificationService(mailSender, database, cfg),
		ctx:                ctx,
		cancel:             cancel,
		rateLimiters:       make(map[string]*ipLimiter),
		violations:         make(map[string]int),
		metrics:            NewMetricsCollector(database, cfg, registry),
		nginxManager:       nginx.NewMaintenanceManager(cfg.MaintenanceTriggerPath),
		targetedMessages:   make(map[string]string),
		lastPortalActivity: make(map[string]time.Time),
		wsClients:          make(map[*wsClient]bool),
		edgeClients:        make(map[string]*safeConn),
		edgeVersions:       make(map[string]string),
		edgeIPs:            make(map[string]string),
		startTime:          time.Now(),
		caCert:             caCert,
		caKey:              caKey,
		edgeLeases:         make(map[string][]EdgeLease),
		edgeHealth:         make(map[string]EdgeHealthStatus),
		outboundConnected:  true,
		lastTestTimes:      make(map[string]time.Time),
	}

	srv.proxyHandler.db = database
	srv.proxyHandler.caCert = caCert
	srv.webhooks = webhook.NewWebhookService(cfg.Webhooks, database)
	srv.portalService = NewPortalService(srv.db, srv.cfg, srv.notifications, &srv.portalMap, caCert, caKey)

	// Initialize i18n dynamic engine
	if err := srv.initI18n(); err != nil {
		return nil, err
	}

	srv.registry.OnLeaseCleanup = func(lease *TunnelLease) {
		bytesIn := atomic.LoadUint64(&lease.BytesIn)
		bytesOut := atomic.LoadUint64(&lease.BytesOut)
		diffIn := int64(bytesIn - lease.LastBytesIn)
		diffOut := int64(bytesOut - lease.LastBytesOut)

		if diffIn > 0 || diffOut > 0 {
			m := &db.TunnelMetric{
				UserID:          lease.UserID,
				SubdomainPrefix: lease.SubdomainPrefix,
				FullHost:        lease.FullHost,
				BytesIn:         diffIn,
				BytesOut:        diffOut,
				ConnectedAt:     lease.CreatedAt,
				RecordedAt:      time.Now().UTC(),
			}
			srv.metrics.Queue(m)
		}
		if srv.proxyHandler != nil {
			srv.proxyHandler.RemoveRateLimiter(lease.FullHost)
		}
		if srv.isCustomDomain(lease.FullHost) {
			go srv.runVanityDomainHook("remove", lease.FullHost)
		}
		if srv.cfg.ControlPlaneURL != "" {
			go srv.notifyControlPlaneDeregister(lease.UserID, lease.SubdomainPrefix)
		}
		srv.BroadcastTelemetry()
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
			_, _ = rand.Read(bytes) //nolint:errcheck
			unsubSecret = hex.EncodeToString(bytes)
			_ = srv.db.SetAdminSetting("unsubscribe_secret", unsubSecret) //nolint:errcheck
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
				_ = srv.db.CreateUser(&db.User{ //nolint:errcheck
					ID:        cfg.Owner.UserID,
					Email:     cfg.Owner.UserID,
					FirstName: first,
					LastName:  last,
					Role:      "owner",
					Status:    "approved",
				})
			} else if ownerUser.Role != "owner" {
				ownerUser.Role = "owner"
				_ = srv.db.UpdateUser(ownerUser) //nolint:errcheck
			}
		}
	}

	if srv.db != nil && !srv.cfg.DisableBackupScheduler {
		srv.startDatabaseBackupScheduler()
	}

	go srv.metrics.Start(ctx)

	if srv.webhooks != nil {
		interval := 10 * time.Second
		if srv.cfg.Webhooks.BatchIntervalSeconds > 0 {
			interval = time.Duration(srv.cfg.Webhooks.BatchIntervalSeconds) * time.Second
		}
		go srv.webhooks.StartQueueConsumer(ctx, interval)
	}

	srv.startRateLimiterCleaner(ctx)

	if srv.db != nil {
		_ = srv.db.RecordGatewayStart(srv.startTime) //nolint:errcheck
	}

	if srv.cfg.ControlPlaneURL != "" && srv.cfg.EdgeToken != "" {
		go srv.runEdgeControlChannel()
	}

	return srv, nil
}

// BackupDatabase clones the active SQLite database to a secure backups folder.
func (s *Server) BackupDatabase() error {
	if s.db == nil {
		return fmt.Errorf("database not configured")
	}

	backupsDir := filepath.Join(filepath.Dir(s.cfg.DBPath), "backups")
	if err := os.MkdirAll(backupsDir, 0755); err != nil {
		return fmt.Errorf("failed to create backups directory: %v", err)
	}

	// Generate date-stamped filename: lfr-tunnel_backup_2026-06-18.db
	timeStamp := time.Now().Format("2006-01-02_15-04-05")
	backupPath := filepath.Join(backupsDir, fmt.Sprintf("lfr-tunnel_backup_%s.db", timeStamp))

	// Safely clone the database online thread-safely!
	_, err := s.db.GetConnection().Exec(fmt.Sprintf("VACUUM INTO '%s'", backupPath))
	if err != nil {
		return fmt.Errorf("failed to execute SQLite hot online backup: %v", err)
	}

	slog.Info(fmt.Sprintf("[Server] SQLite hot online database backup completed successfully: %s", backupPath))
	return nil
}

// startDatabaseBackupScheduler triggers daily automated background backups.
func (s *Server) startDatabaseBackupScheduler() {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		// Execute initial database backup on server startup
		if err := s.BackupDatabase(); err != nil {
			slog.Info(fmt.Sprintf("[Warning] Initial database startup backup failed: %v", err))
		}

		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if err := s.BackupDatabase(); err != nil {
					slog.Info(fmt.Sprintf("[Error] Scheduled daily database backup failed: %v", err))
				}
			}
		}
	}()
}

// getRateLimiter retrieves or creates a rate limiter for an IP.
func (s *Server) getRateLimiter(ip string) *rate.Limiter {
	s.rlMutex.Lock()
	defer s.rlMutex.Unlock()
	entry, exists := s.rateLimiters[ip]
	if !exists {
		// 10 requests per second, burst of 20
		entry = &ipLimiter{
			limiter:  rate.NewLimiter(rate.Limit(10), 20),
			lastSeen: time.Now(),
		}
		s.rateLimiters[ip] = entry
	} else {
		entry.lastSeen = time.Now()
	}
	return entry.limiter
}

// startRateLimiterCleaner runs a background routine that periodically prunes stale IP rate limiters.
func (s *Server) startRateLimiterCleaner(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.rlMutex.Lock()
				now := time.Now()
				for ip, entry := range s.rateLimiters {
					if now.Sub(entry.lastSeen) > 1*time.Hour {
						delete(s.rateLimiters, ip)
					}
				}
				s.rlMutex.Unlock()
			}
		}
	}()
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
	if strings.HasPrefix(r.URL.Path, "/api/") && !s.cfg.DisableAPIRateLimit {
		limiter := s.getRateLimiter(ip)
		if !limiter.Allow() {
			s.vMutex.Lock()
			s.violations[ip]++
			vCount := s.violations[ip]
			s.vMutex.Unlock()

			if vCount >= 50 {
				// Auto-ban!
				slog.Info(fmt.Sprintf("[Defense] Auto-banning IP %s after 50 violations", ip))
				s.blacklist.Store(ip, true)
				s.BroadcastBlacklistUpdate("add", ip)
				if s.db != nil {
					_ = s.db.AddBlacklistIP(ip, "Auto-banned by Rate Limiter for DDOS") //nolint:errcheck
					s.writeAudit("system", "ip.blacklisted", "ip", ip, "Auto-banned by Rate Limiter for DDOS", r)
					body, _ := s.renderNotificationTemplate("en", "admin_ip_autobanned.txt", map[string]interface{}{"IP": ip}) //nolint:errcheck
					s.notifications.SendAdminAlert("alert_notify_blacklist", "LFR Tunnel Alert: IP Auto-Banned", body)
					s.webhooks.SendRateLimitBanAlert(ip, 24*time.Hour, "Exceeded API rate limit (50 violations)")
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
		if s.cfg.ForceMFA && strings.HasPrefix(r.URL.Path, "/api/") {
			bypass := false
			switch r.URL.Path {
			case "/api/me", "/api/me/onboarding", "/api/mfa/setup", "/api/mfa/enable", "/api/auth/logout", "/api/version", "/api/i18n", "/api/complete-setup":
				bypass = true
			}
			if strings.HasPrefix(r.URL.Path, "/api/auth/") && r.URL.Path != "/api/auth/login" {
				bypass = true
			}
			if !bypass {
				if user, err := s.getCurrentUser(r); err == nil && user != nil {
					if !user.TOTPEnabled {
						respondJSON(w, http.StatusForbidden, map[string]interface{}{
							"error":        "MFA setup required",
							"mfa_required": true,
						})
						return
					}
				}
			}
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/register" {
			s.handleRegister(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/internal/edge-register" {
			s.handleEdgeRegister(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/internal/edge-metrics" {
			s.handleEdgeMetrics(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/internal/edge-kick" {
			s.handleEdgeKick(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/internal/edge-deregister" {
			s.handleEdgeDeregister(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/internal/edge-audit-log" {
			s.handleEdgeAuditLog(w, r)
			return
		}

		if r.URL.Path == "/api/internal/edge-control-ws" {
			s.handleEdgeControlWS(w, r)
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

		if r.Method == http.MethodPost && r.URL.Path == "/api/local/broadcast" {
			s.handleLocalBroadcast(w, r)
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

			// Apply default InstallDir fallbacks if not configured
			effectivePlatforms := make(map[string]config.PlatformConfig)
			for k, v := range s.cfg.ClientPlatforms {
				effectivePlatforms[k] = v
			}
			fallbacks := map[string]string{
				"macos_arm64":   "/usr/local/bin",
				"macos_amd64":   "/usr/local/bin",
				"linux_amd64":   "/usr/local/bin",
				"windows_amd64": "$env:LOCALAPPDATA\\Programs\\lfr-tunnel",
			}
			for key, fallback := range fallbacks {
				if p, ok := effectivePlatforms[key]; ok {
					if p.InstallDir == "" {
						p.InstallDir = fallback
						effectivePlatforms[key] = p
					}
				}
			}

			dockerImg := s.cfg.DockerImage

			latestClientVer := s.cfg.LatestClientVersion
			if latestClientVer == "" {
				latestClientVer = config.Version
			}

			regions := make(map[string]string)
			if len(s.cfg.Domains) > 0 {
				regions["eu"] = "https://tunnel." + s.cfg.Domains[0]
			}
			s.edgeClientsMu.RLock()
			for _, edge := range s.cfg.EdgeNodes {
				if _, isUp := s.edgeClients[edge.ID]; isUp {
					parts := strings.Split(edge.ID, "-")
					regionName := parts[0]
					if regionName != "" {
						regions[regionName] = edge.URL
					}
				}
			}
			s.edgeClientsMu.RUnlock()

			respondJSON(w, http.StatusOK, map[string]interface{}{
				"latest_version":           latestClientVer,
				"min_version":              s.cfg.MinClientVersion,
				"server_version":           config.Version,
				"documentation_url":        s.cfg.DocumentationURL,
				"repository_url":           s.cfg.RepositoryURL,
				"secure_token_guide_url":   s.cfg.SecureTokenGuideURL,
				"docker_hub_url":           s.cfg.DockerHubURL,
				"status_page_url":          s.cfg.StatusPageURL,
				"privacy_policy_url":       privacyURL,
				"cookie_policy_url":        cookieURL,
				"maintenance_mode":         maintStr,
				"enforce_policy_consent":   consentStr,
				"docker_image":             dockerImg,
				"docker_bypass_url":        s.cfg.DockerBypassURL,
				"client_platforms":         effectivePlatforms,
				"regions":                  regions,
				"disable_client_downloads": s.cfg.DisableClientDownloads,
				"disable_brew":             s.cfg.DisableBrew,
				"disable_scoop":            s.cfg.DisableScoop,
				"start_time":               s.startTime.Format(time.RFC3339),
				"uptime_seconds":           int(time.Since(s.startTime).Seconds()),
				"force_mfa":                s.cfg.ForceMFA,
				"enable_onboarding":        s.cfg.EnableOnboarding,
			})
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/check-subdomain" {
			s.handleCheckSubdomain(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/portal/telemetry/ws" {
			s.handleTelemetryWS(w, r)
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

		if r.Method == http.MethodGet && r.URL.Path == "/api/i18n" {
			s.handleGetI18n(w, r)
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

		if r.Method == http.MethodPost && r.URL.Path == "/api/me/delete-account" {
			s.handleSelfDeleteAccount(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/me/onboarding" {
			s.handleUpdateOnboarding(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/portal/edge-health" {
			s.handleEdgeHealth(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/portal/edge-action" {
			s.handleEdgeAction(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/portal/reservations" {
			s.handleListReservations(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/portal/reservations" {
			s.handleCreateReservation(w, r)
			return
		}

		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/portal/reservations/") {
			s.handleDeleteReservation(w, r)
			return
		}

		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/portal/reservations/") && strings.HasSuffix(r.URL.Path, "/request-extension") {
			s.handleRequestExtension(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/portal/reservations/promote" {
			s.handlePromoteReservation(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/portal/reservations/access-control" {
			s.handleUpdateReservationAccessControl(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/portal/reservations/headers" {
			s.handleUpdateReservationHeaders(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/portal/generate-subdomain" {
			s.handleGenerateSubdomain(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/portal/invitations" {
			s.handleListInvitations(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/portal/invitations" {
			s.handleCreateInvitation(w, r)
			return
		}

		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/portal/invitations/") {
			s.handleDeleteInvitation(w, r)
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/portal/invitations/claim" {
			s.handleClaimInvitation(w, r)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/portal/csr/sign" {
			s.handleCSRSignInvitation(w, r)
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

		if r.Method == http.MethodGet && (r.URL.Path == "/install" || r.URL.Path == "/install.sh") {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			scriptBytes, err := staticFS.ReadFile("static/install.sh")
			if err != nil {
				http.Error(w, "Failed to read install script", http.StatusInternalServerError)
				return
			}

			scriptStr := string(scriptBytes)
			scriptStr = strings.ReplaceAll(scriptStr, "{{SERVER_URL}}", strings.TrimRight(s.cfg.ControlPlaneURL, "/"))

			getInstallDir := func(platform string, fallback string) string {
				if p, ok := s.cfg.ClientPlatforms[platform]; ok && p.InstallDir != "" {
					return p.InstallDir
				}
				return fallback
			}

			scriptStr = strings.ReplaceAll(scriptStr, "{{MACOS_AMD64_INSTALL_DIR}}", getInstallDir("macos_amd64", "/usr/local/bin"))
			scriptStr = strings.ReplaceAll(scriptStr, "{{MACOS_ARM64_INSTALL_DIR}}", getInstallDir("macos_arm64", "/usr/local/bin"))
			scriptStr = strings.ReplaceAll(scriptStr, "{{LINUX_AMD64_INSTALL_DIR}}", getInstallDir("linux_amd64", "/usr/local/bin"))

			if _, err := w.Write([]byte(scriptStr)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/install.ps1" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			scriptBytes, err := staticFS.ReadFile("static/install.ps1")
			if err != nil {
				http.Error(w, "Failed to read install script", http.StatusInternalServerError)
				return
			}

			scriptStr := string(scriptBytes)
			scriptStr = strings.ReplaceAll(scriptStr, "{{SERVER_URL}}", strings.TrimRight(s.cfg.ControlPlaneURL, "/"))

			getInstallDir := func(platform string, fallback string) string {
				if p, ok := s.cfg.ClientPlatforms[platform]; ok && p.InstallDir != "" {
					return p.InstallDir
				}
				return fallback
			}

			scriptStr = strings.ReplaceAll(scriptStr, "{{WINDOWS_AMD64_INSTALL_DIR}}", getInstallDir("windows_amd64", "$env:LOCALAPPDATA\\Programs\\lfr-tunnel"))

			if _, err := w.Write([]byte(scriptStr)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/api/healthz" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`{"status":"healthy"}`)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
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

		if r.Method == http.MethodGet && r.URL.Path == "/robots.txt" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("User-agent: *\nDisallow: /\n")); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
			return
		}

		if r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "/admin" || r.URL.Path == "/portal") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			htmlContent := strings.ReplaceAll(dashboardHTML, "static/dashboard.js", "static/dashboard.js?v="+config.Version)
			htmlContent = strings.ReplaceAll(htmlContent, "/static/dashboard.css", "/static/dashboard.css?v="+config.Version)
			if _, err := w.Write([]byte(htmlContent)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
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

	if inQuarantine, qHost, qRelease := s.checkQuarantineStatus(host); inQuarantine {
		s.handleVisitorGonePage(w, r, qHost, qRelease)
		return
	}

	s.proxyHandler.ServeHTTP(w, r)
}

// handleRegister parses registration request and responds with leases.
func (s *Server) canUserAutoReserve(userRec *db.User) bool {
	if userRec != nil && s.cfg.RoleSettings != nil {
		if rs, ok := s.cfg.RoleSettings[userRec.Role]; ok && rs.AllowAutoReservation != nil {
			return *rs.AllowAutoReservation
		}
	}
	return s.cfg.AllowClientAutoReservation
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondRegisterResponse(w, http.StatusBadRequest, r, RegisterResponse{Status: "error", Error: "invalid JSON payload"})
		return
	}

	if s.cfg.ControlPlaneURL != "" {
		s.handleEdgeRegisterProxy(w, r, req)
		return
	}

	// Validate auth token
	user, ok := s.isValidToken(req.AuthToken)
	if !ok {
		s.respondRegisterResponse(w, http.StatusUnauthorized, r, RegisterResponse{Status: "error", Error: "unauthorized"})
		return
	}

	// Fetch database user record if available to enforce user-level quota and preferences
	var userRec *db.User
	if s.db != nil {
		userRec, _ = s.db.GetUser(user.ID) //nolint:errcheck
	}

	// Determine active domains to register dynamically based on rules and request Host
	activeDomains := s.getActiveDomainsForRequest(r, userRec)

	// Validate custom domain format if provided
	if req.CustomDomain != "" && !isValidCustomDomain(req.CustomDomain) {
		s.respondRegisterResponse(w, http.StatusBadRequest, r, RegisterResponse{Status: "error", Error: "invalid custom domain format"})
		return
	}

	if req.CustomDomain != "" {
		activeDomains = []string{req.CustomDomain}
		req.SubdomainPrefix = ""
	}

	// Enforce subdomain/custom domain reservation checks and handle random generation
	if req.CustomDomain != "" {
		// Custom domain requested
		// 1. Verify availability in registry (in-memory leases)
		if _, exists := s.registry.GetLease(req.CustomDomain); exists {
			s.respondRegisterResponse(w, http.StatusConflict, r, RegisterResponse{Status: "error", Error: "Domain is already taken"})
			return
		}

		// 2. Verify reservation and quarantine rules in DB
		if s.db != nil {
			var domainsToReserve []string
			d := req.CustomDomain
			existing, err := s.db.GetSubdomainReservationByName("", d)
			if err == nil && existing != nil {
				if existing.ExpiresAt != nil && existing.ExpiresAt.Before(time.Now()) {
					quarantineCutoff := existing.ExpiresAt.AddDate(0, 0, s.cfg.SubdomainQuarantineDays)
					if time.Now().Before(quarantineCutoff) {
						if existing.UserID != user.ID {
							s.respondRegisterResponse(w, http.StatusConflict, r, RegisterResponse{Status: "error", Error: "Domain is currently in quarantine"})
							return
						}
						// Quarantined but belongs to this user. We need to extend/re-reserve it.
						domainsToReserve = append(domainsToReserve, d)
					} else {
						// Past quarantine, delete expired reservation and re-reserve
						_ = s.db.DeleteSubdomainReservation(existing.ID) //nolint:errcheck
						domainsToReserve = append(domainsToReserve, d)
					}
				} else {
					if existing.UserID != user.ID {
						s.respondRegisterResponse(w, http.StatusConflict, r, RegisterResponse{Status: "error", Error: "Domain is reserved by another user"})
						return
					}
				}
			} else {
				// No reservation exists
				if s.canUserAutoReserve(userRec) {
					domainsToReserve = append(domainsToReserve, d)
				} else {
					s.respondRegisterResponse(w, http.StatusForbidden, r, RegisterResponse{Status: "error", Error: "Custom domains must be reserved in the portal prior to connecting"})
					return
				}
			}

			// If we have domains to auto-reserve, verify quota limit first
			if len(domainsToReserve) > 0 {
				limit := s.cfg.DefaultMaxReservations
				if userRec != nil {
					limit = s.getUserMaxReservations(userRec)
				}

				list, err := s.db.ListSubdomainReservationsByUserID(user.ID)
				activeCount := 0
				if err == nil {
					for _, res := range list {
						if res.ExpiresAt == nil || res.ExpiresAt.After(time.Now()) {
							activeCount++
						}
					}
				}

				needed := len(domainsToReserve)
				if limit >= 0 && activeCount+needed > limit {
					s.respondRegisterResponse(w, http.StatusForbidden, r, RegisterResponse{Status: "error", Error: "Domain reservation quota limit reached"})
					return
				}

				// Create the reservations
				for _, d := range domainsToReserve {
					// Delete any existing quarantined or expired reservation for this user first
					if existing, err := s.db.GetSubdomainReservationByName("", d); err == nil && existing != nil {
						_ = s.db.DeleteSubdomainReservation(existing.ID) //nolint:errcheck
					}
					res := &db.SubdomainReservation{
						UserID:    user.ID,
						Subdomain: "",
						Domain:    d,
						ExpiresAt: s.getUserSubdomainExpiry(user),
					}
					if err := s.db.CreateSubdomainReservation(res); err != nil {
						slog.Info(fmt.Sprintf("[Server] Failed to auto-create reservation for custom domain %s: %v", d, err))
					}
				}
			}
		}
	} else {
		requestedRandom := req.SubdomainPrefix == "" || req.SubdomainPrefix == "random"
		if requestedRandom {
			found := false
			for attempt := 0; attempt < 10; attempt++ {
				candidate := s.generateRandomSubdomainPrefix("liferay")
				available, _ := s.registry.CheckSubdomain(candidate, activeDomains)
				if available {
					dbOk := true
					if s.db != nil {
						for _, d := range activeDomains {
							existing, err := s.db.GetSubdomainReservationByName(candidate, d)
							if err == nil && existing != nil {
								dbOk = false
								break
							}
						}
					}
					if dbOk {
						req.SubdomainPrefix = candidate
						found = true
						break
					}
				}
			}
			if !found {
				s.respondRegisterResponse(w, http.StatusInternalServerError, r, RegisterResponse{Status: "error", Error: "failed to generate unique random subdomain"})
				return
			}
		} else {
			// 1. Verify availability in registry (in-memory leases)
			var available bool
			var reason string
			var pickedDomain string

			for _, d := range activeDomains {
				avail, rsn := s.registry.CheckSubdomain(req.SubdomainPrefix, []string{d})
				if avail {
					available = true
					pickedDomain = d
					break
				}
				reason = rsn
			}

			if !available {
				s.respondRegisterResponse(w, http.StatusConflict, r, RegisterResponse{Status: "error", Error: "Subdomain is already taken: " + reason})
				return
			}

			activeDomains = []string{pickedDomain}

			// 2. Verify reservation and quarantine rules in DB
			if s.db != nil {
				var domainsToReserve []string
				for _, d := range activeDomains {
					existing, err := s.db.GetSubdomainReservationByName(req.SubdomainPrefix, d)
					if err == nil && existing != nil {
						if existing.ExpiresAt != nil && existing.ExpiresAt.Before(time.Now()) {
							quarantineCutoff := existing.ExpiresAt.AddDate(0, 0, s.cfg.SubdomainQuarantineDays)
							if time.Now().Before(quarantineCutoff) {
								if existing.UserID != user.ID {
									s.respondRegisterResponse(w, http.StatusConflict, r, RegisterResponse{Status: "error", Error: "Subdomain is currently in quarantine"})
									return
								}
								// Quarantined but belongs to this user. We need to extend/re-reserve it.
								domainsToReserve = append(domainsToReserve, d)
							} else {
								// Past quarantine, delete expired reservation and re-reserve
								_ = s.db.DeleteSubdomainReservation(existing.ID) //nolint:errcheck
								domainsToReserve = append(domainsToReserve, d)
							}
						} else {
							if existing.UserID != user.ID {
								s.respondRegisterResponse(w, http.StatusConflict, r, RegisterResponse{Status: "error", Error: "Subdomain is reserved by another user"})
								return
							}
						}
					} else {
						// No reservation exists
						if s.canUserAutoReserve(userRec) {
							domainsToReserve = append(domainsToReserve, d)
						} else {
							s.respondRegisterResponse(w, http.StatusForbidden, r, RegisterResponse{Status: "error", Error: "Custom subdomains must be reserved in the portal prior to connecting"})
							return
						}
					}
				}

				// If we have domains to auto-reserve, verify quota limit first
				if len(domainsToReserve) > 0 {
					limit := s.cfg.DefaultMaxReservations
					if userRec != nil {
						limit = s.getUserMaxReservations(userRec)
					}

					list, err := s.db.ListSubdomainReservationsByUserID(user.ID)
					activeCount := 0
					if err == nil {
						for _, res := range list {
							if res.ExpiresAt == nil || res.ExpiresAt.After(time.Now()) {
								activeCount++
							}
						}
					}

					needed := len(domainsToReserve)
					if limit >= 0 && activeCount+needed > limit {
						s.respondRegisterResponse(w, http.StatusForbidden, r, RegisterResponse{Status: "error", Error: "Subdomain reservation quota limit reached"})
						return
					}

					// Create the reservations
					for _, d := range domainsToReserve {
						// Delete any existing quarantined or expired reservation for this user first
						if existing, err := s.db.GetSubdomainReservationByName(req.SubdomainPrefix, d); err == nil && existing != nil {
							_ = s.db.DeleteSubdomainReservation(existing.ID) //nolint:errcheck
						}
						res := &db.SubdomainReservation{
							UserID:    user.ID,
							Subdomain: req.SubdomainPrefix,
							Domain:    d,
							ExpiresAt: s.getUserSubdomainExpiry(user),
						}
						if err := s.db.CreateSubdomainReservation(res); err != nil {
							slog.Info(fmt.Sprintf("[Server] Failed to auto-create reservation for %s on %s: %v", req.SubdomainPrefix, d, err))
						}
					}
				}
			}
		}
	}

	// Update reservation with registration passcode & whitelist_ips if specified
	if s.db != nil {
		for _, d := range activeDomains {
			existing, err := s.db.GetSubdomainReservationByName(req.SubdomainPrefix, d)
			if err == nil && existing != nil && existing.UserID == user.ID {
				updated := false
				if req.Passcode != "" {
					existing.Passcode = req.Passcode
					updated = true
				}
				if req.WhitelistIPs != "" {
					existing.WhitelistIPs = req.WhitelistIPs
					updated = true
				}
				if updated {
					if err := s.db.UpdateSubdomainReservation(existing); err != nil {
						slog.Info(fmt.Sprintf("[Server] Failed to update access controls on registration: %v", err))
					}
				}
			}
		}
	}

	// Determine effective rate limit
	effectiveLimit := req.RateLimit
	if userRec != nil && userRec.RateLimit > 0 {
		if effectiveLimit <= 0 || effectiveLimit > userRec.RateLimit {
			effectiveLimit = userRec.RateLimit
		}
	}

	if s.cfg.MaxTunnelRateLimit > 0 {
		if effectiveLimit <= 0 || effectiveLimit > s.cfg.MaxTunnelRateLimit {
			effectiveLimit = s.cfg.MaxTunnelRateLimit
		}
	} else if effectiveLimit <= 0 {
		effectiveLimit = 0
	}

	clientIP := getClientIP(r)
	if (req.ClientVersion != "" || req.ClientOS != "") && userRec != nil {
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
			_ = s.db.UpdateUser(userRec) //nolint:errcheck
		}
	}

	// Enforce active tunnels limit
	if s.registry != nil {
		leases := s.registry.ListLeases()
		uniqueSubs := make(map[string]bool)
		for _, l := range leases {
			if l.UserID == user.ID {
				uniqueSubs[l.SubdomainPrefix] = true
			}
		}
		userTunnelsCount := len(uniqueSubs)

		maxTunnels := s.cfg.DefaultMaxActiveTunnels
		if user.Role == "admin" && s.cfg.AdminMaxActiveTunnels != nil {
			maxTunnels = *s.cfg.AdminMaxActiveTunnels
		} else if user.Role == "owner" && s.cfg.OwnerMaxActiveTunnels != nil {
			maxTunnels = *s.cfg.OwnerMaxActiveTunnels
		}

		if userRec != nil && userRec.MaxTunnels != nil {
			maxTunnels = *userRec.MaxTunnels
		}

		isReconnecting := uniqueSubs[req.SubdomainPrefix]

		if maxTunnels > 0 && userTunnelsCount >= maxTunnels && !isReconnecting {
			s.respondRegisterResponse(w, http.StatusForbidden, r, RegisterResponse{
				Status: "error",
				Error:  fmt.Sprintf("Active tunnels concurrency limit reached (%d). Stop another active tunnel or ask an administrator to increase your limit.", maxTunnels),
			})
			return
		}
	}

	// Register in registry
	sessionToken, remotes, err := s.registry.Register(user.ID, req.SubdomainPrefix, req.Ports, activeDomains, effectiveLimit, clientIP, req.BasicAuth, req.AddedHeaders)
	if err != nil {
		s.respondRegisterResponse(w, http.StatusConflict, r, RegisterResponse{Status: "error", Error: err.Error()})
		return
	}

	// Trigger vanity domain hook for custom domains if configured
	if s.cfg.VanityDomainHook != "" {
		leases := s.registry.GetSessionLeases(sessionToken)
		for _, lease := range leases {
			if s.isCustomDomain(lease.FullHost) {
				go s.runVanityDomainHook("add", lease.FullHost)
			}
		}
	}

	var warning string
	if req.ClientVersion != "" && req.ClientVersion != config.Version {
		warning = fmt.Sprintf("Version mismatch! Server is running %s but client is %s. Please consider upgrading using 'lfr-tunnel -upgrade'", config.Version, req.ClientVersion)
	}

	go s.BroadcastTelemetry()

	var langPref string
	var themePref string
	if userRec != nil {
		langPref = userRec.LanguagePreference
		themePref = userRec.ThemePreference
	}

	s.respondRegisterResponse(w, http.StatusOK, r, RegisterResponse{
		Status:             "success",
		SessionToken:       sessionToken,
		SubdomainPrefix:    req.SubdomainPrefix,
		Remotes:            remotes,
		Domains:            activeDomains,
		Warning:            warning,
		LanguagePreference: langPref,
		ThemePreference:    themePref,
		ServerVersion:      config.Version,
	})
}

// respondRegisterResponse sends a RegisterResponse with automated PortalURL enrichment.
func (s *Server) respondRegisterResponse(w http.ResponseWriter, status int, r *http.Request, resp RegisterResponse) {
	if resp.PortalURL == "" {
		portalURL := s.cfg.PortalURL
		if portalURL == "" {
			portalURL = s.getPortalBaseURL(r) + "/portal"
		}
		resp.PortalURL = portalURL
	}
	resp.ServerVersion = config.Version
	respondJSON(w, status, resp)
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
			body, _ := s.renderNotificationTemplate("en", "admin_tunnel_offline.txt", nil) //nolint:errcheck
			s.notifications.SendAdminAlert("alert_notify_tunnel_offline", "LFR Tunnel Alert: Tunnel Offline", body)
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
		slog.Info(fmt.Sprintf("[Server] Failed to encode domains response: %v", err))
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
			slog.Info(fmt.Sprintf("[Server] Failed to encode unauthorized response: %v", err))
		}
		return
	}

	subdomain := r.URL.Query().Get("subdomain")
	if subdomain == "" {
		w.WriteHeader(http.StatusBadRequest)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": "missing subdomain parameter"}); err != nil {
			slog.Info(fmt.Sprintf("[Server] Failed to encode missing subdomain response: %v", err))
		}
		return
	}

	var userRec *db.User
	if s.db != nil {
		userRec, _ = s.db.GetUser(user.ID) //nolint:errcheck
	}

	// Determine active domains to check dynamically based on request Host
	activeDomains := s.getActiveDomainsForRequest(r, userRec)

	var available bool
	var reason string
	var pickedDomain string

	for _, d := range activeDomains {
		avail, rsn := s.registry.CheckSubdomain(subdomain, []string{d})
		if avail {
			available = true
			pickedDomain = d
			break
		}
		reason = rsn
	}

	if available {
		activeDomains = []string{pickedDomain}
	}
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
		slog.Info(fmt.Sprintf("[Server] Failed to encode check subdomain response: %v", err))
	}
}

// Start kicks off the background processes and listens for gateway traffic.
func (s *Server) Start() error {
	// Start telemetry WebSocket tick loop
	s.StartTelemetryTicker()

	go s.monitorEdgeHealth()

	// Periodic task: Prune and check expiring reservations every hour
	go func() {
		ticker := time.NewTicker(s.cfg.PruneInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if s.db != nil {
					_ = s.db.PruneExpiredMagicLinks()                          //nolint:errcheck
					_ = s.db.PruneExpiredOrRevokedPATs(s.cfg.PATRetentionDays) //nolint:errcheck
					s.checkExpiringReservations()
				}
			}
		}
	}()

	// 1. Start Chisel Server on localhost:8081
	go func() {
		slog.Info("[Server] Starting internal Chisel tunnel engine on 127.0.0.1:8081...")
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
			slog.Info(fmt.Sprintf("[Server] Loaded config: Bind=%s, HTTPBind=%s, Domains=%v, DB=%s",
				s.cfg.BindAddr, s.cfg.HTTPBindAddr, s.cfg.Domains, s.cfg.DBPath))
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
			s.redirectSrv = redirectSrv
			if err := redirectSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("[Server] HTTP redirect server failed: %v", err)
			}
		}()

		slog.Info(fmt.Sprintf("[Server] Starting HTTPS gateway on %s (TLS offloaded)...", s.cfg.BindAddr))
		srv := &http.Server{
			Addr:    s.cfg.BindAddr,
			Handler: s,
		}
		s.httpServer = srv
		return srv.ListenAndServeTLS(s.cfg.SSLCertFile, s.cfg.SSLKeyFile)
	}

	// HTTP-only mode
	slog.Info(fmt.Sprintf("[Server] Starting HTTP gateway on %s (TLS disabled)...", s.cfg.HTTPBindAddr))
	srv := &http.Server{
		Addr:    s.cfg.HTTPBindAddr,
		Handler: s,
	}
	s.httpServer = srv
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

	if s.cfg.DisableEmailLogin {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Registration via email is disabled. Please use SSO."}) //nolint:errcheck
		return
	}

	if s.db == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "database storage not enabled"}) //nolint:errcheck
		return
	}

	var req RegisterRequestPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON payload"}) //nolint:errcheck
		return
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid email address"}) //nolint:errcheck
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
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "email domain not allowed by server configuration"}) //nolint:errcheck
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
		_ = json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"status":  "success",
			"message": "registration request submitted. Please check your email to verify your account.",
		})
		return
	}

	approvalToken, err := generateSecureToken()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to generate approval token"}) //nolint:errcheck
		return
	}

	verificationToken, err := generateSecureToken()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to generate verification token"}) //nolint:errcheck
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
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to save registration request"}) //nolint:errcheck
		return
	}

	s.writeAudit(user.Email, "user.registered", "user", user.Email, "", r)

	// Send verification email to the user
	if s.notifications != nil && s.notifications.Sender() != nil {
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
			if err := s.notifications.Sender().Send(user.Email, subject, body, plainBody); err != nil {
				slog.Info(fmt.Sprintf("[Mail] Failed to send verification email to %s: %v", user.Email, err))
			}
		}()
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "success", "message": "registration request submitted. Please check your email to verify your account."}) //nolint:errcheck
}

// handleSetupPage serves the setup page to complete registration
func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	data, err := staticFS.ReadFile("static/setup.html")
	if err != nil {
		slog.Info(fmt.Sprintf("[Server] Failed to read setup.html from embedded FS: %v", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
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
	body, _ := s.renderNotificationTemplate("en", "admin_registration_request.txt", map[string]interface{}{ //nolint:errcheck
		"FirstName": user.FirstName,
		"LastName":  user.LastName,
		"Email":     user.Email,
	})
	s.notifications.SendAdminAlert("alert_notify_registration", "LFR Tunnel Alert: New User Registration", body)
	s.webhooks.SendRegistrationAlert(user.Email, "Pending admin approval")

	// Send approval email to admin
	if s.notifications != nil && s.notifications.Sender() != nil && s.cfg.AdminNotificationEmail != "" {
		sendAdminEmail := true
		adminLang := "en"
		if s.db != nil {
			if adminUser, err := s.db.GetUserByEmail(s.cfg.AdminNotificationEmail); err == nil && adminUser != nil {
				adminLang = adminUser.LanguagePreference
				if adminUser.NotificationPrefs == "disabled" {
					sendAdminEmail = false
				}
			}
		}

		if sendAdminEmail {
			subject := s.GetTranslation(adminLang, "registration_pending_subject")
			scheme := "http"
			if s.cfg.SSLCertFile != "" {
				scheme = "https"
			}
			approveURL := fmt.Sprintf("%s://%s/api/admin/approve?email=%s&token=%s", scheme, r.Host, url.QueryEscape(user.Email), user.ApprovalToken)

			body, err := s.renderEmailTemplate("en", "admin_registration_request.html", map[string]interface{}{
				"FirstName":  user.FirstName,
				"LastName":   user.LastName,
				"Email":      user.Email,
				"ApproveURL": approveURL,
				"Status":     "Email Verified & Setup Complete",
			})
			if err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to render admin_registration_request template: %v", err))
				body = fmt.Sprintf("<p>New registration request (Email Verified & Setup Complete):</p><ul><li>Name: %s %s</li><li>Email: %s</li></ul><p><a href=\"%s\">Click here to approve this request</a></p>", user.FirstName, user.LastName, user.Email, approveURL)
			}

			plainBody := fmt.Sprintf("New user registered: %s. Approve here: %s", user.Email, approveURL)
			go func() { _ = s.notifications.Sender().Send(s.cfg.AdminNotificationEmail, subject, body, plainBody) }() //nolint:errcheck
		}
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
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
	body, _ := s.renderNotificationTemplate("en", "admin_registration_request.txt", map[string]interface{}{ //nolint:errcheck
		"Email": user.Email,
	})
	s.notifications.SendAdminAlert("alert_notify_registration", "LFR Tunnel Alert: New User Registration", body)

	// Also send the original admin approval email now
	if s.notifications != nil && s.notifications.Sender() != nil && s.cfg.AdminNotificationEmail != "" {
		adminLang := "en"
		if s.db != nil {
			if adminUser, err := s.db.GetUserByEmail(s.cfg.AdminNotificationEmail); err == nil && adminUser != nil {
				adminLang = adminUser.LanguagePreference
			}
		}
		subject := s.GetTranslation(adminLang, "registration_pending_subject")
		scheme := "http"
		if s.cfg.SSLCertFile != "" {
			scheme = "https"
		}
		host := r.Host
		approveURL := fmt.Sprintf("%s://%s/api/admin/approve?email=%s&token=%s", scheme, host, url.QueryEscape(user.Email), user.ApprovalToken)
		body, err := s.renderEmailTemplate("en", "admin_registration_request.html", map[string]interface{}{
			"FirstName":  user.FirstName,
			"LastName":   user.LastName,
			"Email":      user.Email,
			"ApproveURL": approveURL,
			"Status":     "Email Verified",
		})
		if err != nil {
			slog.Info(fmt.Sprintf("[Server] Failed to render admin_registration_request template: %v", err))
			body = fmt.Sprintf("<p>New registration request (Email Verified):</p><ul><li>Name: %s %s</li><li>Email: %s</li></ul><p><a href=\"%s\">Click here to approve this request</a></p>", user.FirstName, user.LastName, user.Email, approveURL)
		}

		go func() {
			plainBody := fmt.Sprintf("A new user (%s) requires approval. Approve here: %s", user.Email, approveURL)
			if err := s.notifications.Sender().Send(s.cfg.AdminNotificationEmail, subject, body, plainBody); err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to send admin alert email: %v", err))
			}
		}()
	}

	w.Header().Set("Content-Type", "text/html")
	htmlBytes, err := staticFS.ReadFile("static/email_verified.html")
	if err != nil {
		if _, err := w.Write([]byte(`<html><head><title>Email Verified</title><style>body{font-family:sans-serif;text-align:center;padding:50px;color:#333;background:#f8fafc;}h1{color:#10b981;}</style></head><body><h1>Email Verified! ✅</h1><p>Your email has been verified successfully. An administrator has been notified to review and approve your account.</p></body></html>`)); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
		return
	}
	if _, err := w.Write(htmlBytes); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
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
	if s.notifications != nil && s.notifications.Sender() != nil {
		subject := "[Liferay Tunnel] Registration Approved!"
		scheme := "http"
		if s.cfg.SSLCertFile != "" {
			scheme = "https"
		}
		host := r.Host
		claimURL := fmt.Sprintf("%s://%s/api/claim?token=%s", scheme, host, claimToken)
		body := fmt.Sprintf("<p>Your registration request has been approved!</p><p><a href=\"%s\">Click here to claim your personal access token</a></p><p>Note: this link can only be used once.</p>", claimURL)
		plainBody := fmt.Sprintf("Your registration has been approved. Claim your token here: %s", claimURL)
		if err := s.notifications.Sender().Send(user.Email, subject, body, plainBody); err != nil {
			slog.Info(fmt.Sprintf("[Server] Failed to send developer approval email: %v", err))
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("<h1>Approval Successful</h1><p>The user has been approved, and an email has been sent to them with instructions to claim their token.</p>")); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
}

// handleClaimToken allows developers to claim their generated PAT.
func (s *Server) handleClaimToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.db == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "database storage not enabled"}) //nolint:errcheck
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing claim token"}) //nolint:errcheck
		return
	}

	// Find user by claim token prefix
	users, err := s.db.ListUsers()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to list users"}) //nolint:errcheck
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
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid or expired claim token"}) //nolint:errcheck
		return
	}

	// Clear claim token so it can never be claimed again
	targetUser.ClaimToken = ""
	if err := s.db.UpdateUser(targetUser); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to update user"}) //nolint:errcheck
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if s.httpServer != nil {
		_ = s.httpServer.Shutdown(ctx) //nolint:errcheck
	}
	if s.redirectSrv != nil {
		_ = s.redirectSrv.Shutdown(ctx) //nolint:errcheck
	}

	s.chiselServer.Close() //nolint:errcheck
	if s.db != nil {
		_ = s.db.RecordGatewayCleanShutdown() //nolint:errcheck
		s.db.Close()                          //nolint:errcheck
	}
}

// getActiveDomainsForRequest evaluates the configured DomainAllocationRule to return a sorted slice of candidate domains.
// It falls back to contextual or preference rules as necessary.
func (s *Server) getActiveDomainsForRequest(r *http.Request, user *db.User) []string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)

	rule := s.cfg.DomainAllocationRule
	if s.db != nil {
		if dbRule, err := s.db.GetAdminSetting("domain_allocation_rule"); err == nil && dbRule != "" {
			rule = dbRule
		}
	}
	if rule == "" {
		rule = "contextual"
	}

	if rule == "user-preference" {
		if user != nil && user.PreferredDomain != "" {
			for _, d := range s.cfg.Domains {
				if d == user.PreferredDomain {
					return []string{d}
				}
			}
		}
		// Fallback if no user pref or invalid pref
		rule = "contextual"
	}

	if rule == "contextual" {
		for _, d := range s.cfg.Domains {
			if strings.Contains(host, strings.ToLower(d)) || host == strings.ToLower(d) {
				return []string{d}
			}
		}
		// Fallback to default domain if configured
		defaultDom := s.cfg.DefaultDomain
		if s.db != nil {
			if dbDef, err := s.db.GetAdminSetting("default_domain"); err == nil && dbDef != "" {
				defaultDom = dbDef
			}
		}
		if defaultDom != "" {
			for _, d := range s.cfg.Domains {
				if d == defaultDom {
					return []string{d}
				}
			}
		}
		// Fallback to preference
		rule = "preference"
	}

	if rule == "hashing" {
		if len(s.cfg.Domains) > 0 {
			var hashStr string
			if user != nil {
				hashStr = user.ID
			} else {
				hashStr = getClientIP(r)
			}
			h := sha256.New()
			h.Write([]byte(hashStr))
			hashBytes := h.Sum(nil)
			var idx uint64
			for i := 0; i < 8; i++ {
				idx = (idx << 8) | uint64(hashBytes[i])
			}
			startIdx := int(idx % uint64(len(s.cfg.Domains)))
			var ordered []string
			for i := 0; i < len(s.cfg.Domains); i++ {
				ordered = append(ordered, s.cfg.Domains[(startIdx+i)%len(s.cfg.Domains)])
			}
			return ordered
		}
	}

	if rule == "least-connections" {
		if len(s.cfg.Domains) > 0 {
			counts := make(map[string]int)
			for _, d := range s.cfg.Domains {
				counts[d] = 0
			}

			s.registry.RLock()
			for host := range s.registry.leases {
				for _, d := range s.cfg.Domains {
					if strings.HasSuffix(host, "."+d) {
						counts[d]++
						break
					}
				}
			}
			s.registry.RUnlock()

			activeDomains := make([]string, len(s.cfg.Domains))
			copy(activeDomains, s.cfg.Domains)
			sort.SliceStable(activeDomains, func(i, j int) bool {
				return counts[activeDomains[i]] < counts[activeDomains[j]]
			})
			return activeDomains
		}
	}

	if rule == "round-robin" {
		idx := atomic.AddUint64(&s.roundRobinCounter, 1)
		if len(s.cfg.Domains) > 0 {
			startIdx := int(idx) % len(s.cfg.Domains)
			var ordered []string
			for i := 0; i < len(s.cfg.Domains); i++ {
				ordered = append(ordered, s.cfg.Domains[(startIdx+i)%len(s.cfg.Domains)])
			}
			return ordered
		}
	}

	if rule == "random" {
		if len(s.cfg.Domains) > 0 {
			activeDomains := make([]string, len(s.cfg.Domains))
			copy(activeDomains, s.cfg.Domains)
			mathrand.Shuffle(len(activeDomains), func(i, j int) {
				activeDomains[i], activeDomains[j] = activeDomains[j], activeDomains[i]
			})
			return activeDomains
		}
	}

	if rule == "preference" {
		if len(s.cfg.Domains) > 0 {
			activeDomains := make([]string, len(s.cfg.Domains))
			copy(activeDomains, s.cfg.Domains)
			return activeDomains
		}
	}

	return []string{"localhost"}
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
					dbConn := s.db
					go func(patID int64) {
						if err := dbConn.UpdatePATUsed(patID); err != nil {
							slog.Info(fmt.Sprintf("[Server] Failed to update PAT last used time: %v", err))
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
		if s.cfg.ControlPlaneURL != "" {
			ip := ""
			if r != nil {
				ip = r.RemoteAddr
			}
			go s.forwardAuditToControlPlane(actorID, action, targetType, targetID, details, ip)
		}
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
	dbConn := s.db
	// Run in a goroutine so it doesn't block the HTTP response
	go func() {
		if err := dbConn.WriteAuditEntry(entry); err != nil {
			slog.Info(fmt.Sprintf("[Server] Failed to write audit log: %v", err))
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
				if s.db != nil {
					if u, err := s.db.GetUserByEmail(actorEmail); err == nil && u != nil {
						actorRole = u.Role
					}
				}
				if actorRole == "" || actorRole == "user" {
					if s.cfg.Owner.UserID != "" && strings.EqualFold(actorEmail, s.cfg.Owner.UserID) {
						actorRole = "owner"
					} else {
						actorRole = "admin"
					}
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
						go func(patID int64) { _ = s.db.UpdatePATUsed(patID) }(pat.ID) //nolint:errcheck
						actorEmail = user.Email
						actorRole = user.Role
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

func (s *Server) isOwner(actor string) bool {
	if s.cfg.Owner.UserID != "" && strings.EqualFold(actor, s.cfg.Owner.UserID) {
		return true
	}
	if s.db != nil {
		if u, err := s.db.GetUserByEmail(actor); err == nil && u != nil {
			return u.Role == "owner"
		}
	}
	return false
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

	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/uptime-history" {
		s.handleAdminGetUptimeHistory(w, r, actor)
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

	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/maintenance" {
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

		ironCurtain := false
		triggerPath := s.cfg.MaintenanceTriggerPath
		if triggerPath == "" {
			triggerPath = "/var/lib/lfr-tunneld/maintenance.enable"
		}
		if _, err := os.Stat(triggerPath); err == nil {
			ironCurtain = true
		}

		testTarget := "Email: " + s.cfg.AdminNotificationEmail
		if s.cfg.Webhooks.Enabled {
			if s.cfg.Webhooks.SlackURL != "" {
				testTarget = "Slack Channel (Webhook)"
			} else if s.cfg.Webhooks.TeamsURL != "" {
				testTarget = "Microsoft Teams Channel"
			}
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"maintenance_mode": maintStr,
			"iron_curtain":     ironCurtain,
			"test_target":      testTarget,
		})
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

	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/reservations/extensions" {
		s.handleAdminListExtensions(w, r, actor)
		return
	}

	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/admin/reservations/") && strings.HasSuffix(r.URL.Path, "/approve-extension") {
		s.handleAdminApproveExtension(w, r, actor)
		return
	}

	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/admin/reservations/") && strings.HasSuffix(r.URL.Path, "/demote") {
		s.handleAdminDemoteReservation(w, r, actor)
		return
	}

	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/admin/users/") && strings.HasSuffix(r.URL.Path, "/limit") {
		s.handleAdminOverrideLimit(w, r, actor)
		return
	}

	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/admin/users/") && strings.HasSuffix(r.URL.Path, "/tunnels-limit") {
		s.handleAdminOverrideTunnelsLimit(w, r, actor)
		return
	}

	if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/admin/users/") && strings.HasSuffix(r.URL.Path, "/preferred-domain") {
		s.handleAdminOverridePreferredDomain(w, r, actor)
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

	if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/admin/users/") {
		s.handleAdminDeleteUser(w, r, actor)
		return
	}

	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/system-settings" {
		s.handleAdminGetSystemSettings(w, r, actor)
		return
	}

	if r.Method == http.MethodPut && r.URL.Path == "/api/admin/system-settings" {
		s.handleAdminUpdateSystemSettings(w, r, actor)
		return
	}

	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/tokens" {
		s.handleAdminListTokens(w, r, actor)
		return
	}

	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/admin/tokens/") && strings.HasSuffix(r.URL.Path, "/extend") {
		s.handleAdminExtendToken(w, r, actor)
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

	if (r.Method == http.MethodPost || r.Method == http.MethodPut) && r.URL.Path == "/api/admin/leases/rate-limit" {
		s.handleAdminOverrideRateLimit(w, r, actor)
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

	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/audit/export" {
		s.handleAdminAuditExport(w, r, actor)
		return
	}

	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/backups" {
		s.handleAdminListBackups(w, r, actor)
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

	if r.Method == http.MethodPost && r.URL.Path == "/api/admin/test-webhook" {
		s.handleAdminTestWebhook(w, r, actor)
		return
	}

	if r.Method == http.MethodGet && r.URL.Path == "/api/admin/config-view" {
		s.handleAdminConfigView(w, r, actor, role)
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

func (s *Server) handleAdminGetUptimeHistory(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}
	runs, err := s.db.GetGatewayRuns(50)
	if err != nil {
		http.Error(w, `{"error":"Failed to retrieve uptime history"}`, http.StatusInternalServerError)
		return
	}
	respondJSON(w, http.StatusOK, runs)
}

func (s *Server) handleAdminMagicLink(w http.ResponseWriter, r *http.Request) {
	if s.cfg.DisableEmailLogin {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "Email login is disabled. Please use SSO."})
		return
	}

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

	magicToken, _ := generateSecureToken() //nolint:errcheck
	clientIP := getClientIP(r)

	expiresAt := time.Now().Add(s.cfg.MagicLinkExpiry)
	if s.db != nil {
		h := sha256.Sum256([]byte(magicToken))
		tokenHash := hex.EncodeToString(h[:])

		// 🛡️ Security hardening: instantly invalidate any older unused magic links for this email!
		_ = s.db.InvalidateOtherMagicLinks(req.Email, -1) //nolint:errcheck

		_ = s.db.CreateMagicLink(req.Email, tokenHash, clientIP, expiresAt) //nolint:errcheck
	} else {
		sessionData := PortalSessionData{
			Email:     req.Email,
			ExpiresAt: expiresAt,
			ClientIP:  clientIP,
		}
		s.portalMap.Store("admin_magic_"+magicToken, sessionData)
	}

	s.writeAudit(req.Email, "auth.magic_link_requested", "system", "auth", "Requested portal login link", r)

	if s.notifications != nil && s.notifications.Sender() != nil {
		// Determine target locale for the email subject (welcome page explicit override takes precedence)
		lang := r.URL.Query().Get("lang")
		if lang == "" {
			if s.db != nil {
				if u, err := s.db.GetUserByEmail(req.Email); err == nil && u != nil {
					lang = u.LanguagePreference
				}
			}
		}
		if lang == "" {
			lang = s.ResolveLocale(r)
		}

		host := r.Host
		scheme := "https"
		if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
			scheme = "http"
		}
		link := fmt.Sprintf("%s://%s/portal?token=%s&lang=%s", scheme, host, magicToken, lang)
		reportLink := fmt.Sprintf("%s://%s/api/auth/report?token=%s", scheme, host, magicToken)

		// Render localized dynamic HTML template from properties / external folder
		body, err := s.renderEmailTemplate(lang, "magic_link.html", map[string]interface{}{
			"Name":       greetingName,
			"Link":       link,
			"ReportLink": reportLink,
		})
		if err != nil {
			slog.Info(fmt.Sprintf("[Server] Failed to render magic link email template: %v", err))
			// Hardcoded fallback
			body = fmt.Sprintf("<p>Hi %s,</p>"+
				"<p>You requested a magic link to log into the Liferay Tunnel Portal.</p>"+
				"<p><strong>IP Address:</strong> %s</p>"+
				"<p>This link expires in 15 minutes.</p>"+
				"<p><a href=\"%s\">Login to Portal</a></p>", html.EscapeString(greetingName), clientIP, link)
		}

		plainBody := fmt.Sprintf("Hi %s,\n\nUse this link to log in (expires in 15 minutes):\n%s\n\nReport abuse here:\n%s", greetingName, link, reportLink)
		subject := s.GetTranslation(lang, "magic_link_subject")

		go s.notifications.Sender().Send(req.Email, subject, body, plainBody) //nolint:errcheck
	} else {
		slog.Info(fmt.Sprintf("[Admin] Magic Link for %s: /admin?token=%s", req.Email, magicToken))
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAdminVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
		Lang  string `json:"lang"`
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
		_ = s.db.MarkMagicLinkUsed(link.ID)                     //nolint:errcheck
		_ = s.db.InvalidateOtherMagicLinks(link.Email, link.ID) //nolint:errcheck
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
	sessionToken, _ := generateSecureToken() //nolint:errcheck
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
			_ = s.db.CreateUser(u) //nolint:errcheck
		}
		user = u
		if u != nil {
			// Update the user's language preference dynamically on login if passed
			if req.Lang != "" && req.Lang != u.LanguagePreference {
				u.LanguagePreference = req.Lang
				_ = s.db.UpdateUser(u) //nolint:errcheck
			}

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
		tempToken, _ := generateSecureToken() //nolint:errcheck
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
		_ = s.db.UpdateUser(user) //nolint:errcheck
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
		"sso_enabled":         len(providers) > 0,
		"providers":           providers,
		"disable_email_login": s.cfg.DisableEmailLogin,
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

	isOwner := s.isOwner(actor)
	filtered := make([]*db.User, 0, len(users))
	for _, u := range users {
		if isOwner {
			filtered = append(filtered, u)
		} else {
			if strings.EqualFold(u.Email, actor) {
				filtered = append(filtered, u)
			} else if strings.EqualFold(u.Email, s.cfg.Owner.UserID) || u.Role == "owner" {
				continue
			} else if u.Role == "admin" {
				continue
			} else {
				filtered = append(filtered, u)
			}
		}
	}

	type AdminUserResponse struct {
		*db.User
		PortalActive  bool           `json:"portal_active"`
		ActiveTunnels []*TunnelLease `json:"active_tunnels"`
	}

	var allLeases []*TunnelLease
	if s.registry != nil {
		allLeases = s.registry.ListLeases()
	}

	responseList := make([]*AdminUserResponse, 0, len(filtered))
	for _, u := range filtered {
		s.portalActivityMu.RLock()
		lastCheckin, found := s.lastPortalActivity[u.ID]
		s.portalActivityMu.RUnlock()

		portalActive := false
		if found && time.Since(lastCheckin) < 30*time.Second {
			portalActive = true
		}

		userLeases := make([]*TunnelLease, 0)
		for _, l := range allLeases {
			if l.UserID == u.ID {
				userLeases = append(userLeases, l)
			}
		}

		s.edgeLeasesMu.Lock()
		for _, el := range s.edgeLeases[u.ID] {
			userLeases = append(userLeases, &TunnelLease{
				UserID:          el.UserID,
				SubdomainPrefix: el.Subdomain,
				FullHost:        el.FullHost,
				LocalPort:       el.LocalPort,
				ClientIP:        el.ClientIP,
				Status:          "up",
				BytesIn:         el.BytesIn,
				BytesOut:        el.BytesOut,
				CreatedAt:       el.CreatedAt,
				NodeID:          el.NodeID,
			})
		}
		s.edgeLeasesMu.Unlock()

		responseList = append(responseList, &AdminUserResponse{
			User:          u,
			PortalActive:  portalActive,
			ActiveTunnels: userLeases,
		})
	}

	_ = json.NewEncoder(w).Encode(responseList) //nolint:errcheck
}

func (s *Server) handleAdminInviteUser(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}

	var req struct {
		Email              string `json:"email"`
		FirstName          string `json:"first_name"`
		LastName           string `json:"last_name"`
		LanguagePreference string `json:"language_preference"`
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

	if _, err := s.db.GetUserByEmail(req.Email); err == nil {
		http.Error(w, `{"error":"User already exists or registration is pending"}`, http.StatusConflict)
		return
	}

	user := &db.User{
		ID:                 req.Email,
		Email:              req.Email,
		FirstName:          req.FirstName,
		LastName:           req.LastName,
		PreferredName:      req.FirstName,
		Role:               "user",
		Status:             "approved", // Instant approval because invited by Admin
		ThemePreference:    "system",
		LanguagePreference: req.LanguagePreference,
		AuthMethod:         "invite",
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	}
	if err := s.db.CreateUser(user); err != nil {
		http.Error(w, `{"error":"Failed to create user"}`, http.StatusInternalServerError)
		return
	}

	// Send Magic Link Invite Email
	magicToken, _ := generateSecureToken() //nolint:errcheck
	clientIP := getClientIP(r)
	expiresAt := time.Now().Add(s.cfg.InviteLinkExpiry)
	h := sha256.Sum256([]byte(magicToken))
	tokenHash := hex.EncodeToString(h[:])
	_ = s.db.CreateMagicLink(req.Email, tokenHash, clientIP, expiresAt) //nolint:errcheck

	inviteLink := fmt.Sprintf("https://%s/api/auth/verify?token=%s", r.Host, magicToken)
	declineLink := fmt.Sprintf("https://%s/api/auth/decline?token=%s", r.Host, magicToken)

	lang := req.LanguagePreference
	if lang == "" {
		lang = "en"
	}
	subject := s.GetTranslation(lang, "invite_subject")
	if strings.Contains(subject, "Liferay Tunnel") && actor != "" {
		// Customise subject to include the inviter if applicable
		subject = fmt.Sprintf("%s has invited you to join Liferay Tunnel", actor)
	}

	// Render localized dynamic HTML template from properties / external folder
	body, err := s.renderEmailTemplate(lang, "invitation.html", map[string]interface{}{
		"Link":        inviteLink,
		"DeclineLink": declineLink,
	})
	if err != nil {
		slog.Info(fmt.Sprintf("[Server] Failed to render invitation email template: %v", err))
		// Hardcoded fallback
		body = fmt.Sprintf(`Hi there,

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
	}

	if s.notifications != nil && s.notifications.Sender() != nil {
		plainBody := fmt.Sprintf("Hi there,\n\nYou have been invited by an administrator to use the Liferay Tunnel portal.\n\nLog in here: %s\n\nDecline here: %s", inviteLink, declineLink)
		go func() { _ = s.notifications.Sender().Send(req.Email, subject, body, plainBody) }() //nolint:errcheck
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

	s.BroadcastTelemetry()

	s.writeAudit(actor, "admin.broadcast", "system", "all", "Admin updated global broadcast message", r)
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleLocalBroadcast(w http.ResponseWriter, r *http.Request) {
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteHost = r.RemoteAddr
	}

	if remoteHost != "127.0.0.1" && remoteHost != "::1" && remoteHost != "localhost" {
		http.Error(w, `{"error":"Forbidden: localhost access only"}`, http.StatusForbidden)
		return
	}

	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Forwarded-Host") != "" || r.Header.Get("X-Real-IP") != "" {
		http.Error(w, `{"error":"Forbidden: direct localhost connection required"}`, http.StatusForbidden)
		return
	}

	var req struct {
		Message          string `json:"message"`
		CountdownSeconds *int   `json:"countdown_seconds,omitempty"`
		DurationMinutes  *int   `json:"duration_minutes,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid payload"}`, http.StatusBadRequest)
		return
	}

	s.broadcastMutex.Lock()
	s.broadcastMessage = req.Message
	s.broadcastMutex.Unlock()

	if req.CountdownSeconds != nil && *req.CountdownSeconds > 0 {
		duration := 5
		if req.DurationMinutes != nil && *req.DurationMinutes > 0 {
			duration = *req.DurationMinutes
		}
		s.maintMutex.Lock()
		s.maintenanceMode = false
		s.maintScheduledAt = time.Now().Add(time.Duration(*req.CountdownSeconds) * time.Second)
		if s.maintTimer != nil {
			s.maintTimer.Stop()
		}
		s.maintTimer = time.AfterFunc(time.Duration(*req.CountdownSeconds)*time.Second, func() {
			s.maintMutex.Lock()
			s.maintenanceMode = true
			s.maintEndTime = time.Now().Add(time.Duration(duration) * time.Minute)
			s.maintScheduledAt = time.Time{}
			s.maintTimer = nil
			s.maintMutex.Unlock()

			s.BroadcastMaintenance("enable", duration, "System upgrade and maintenance")

			// Kick standard tunnels
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
			slog.Info("[Server] Local-triggered Scheduled Soft Maintenance countdown hit 0. Soft Maintenance Mode is now ACTIVE.")
			go s.BroadcastTelemetry()
		})
		s.maintMutex.Unlock()
	}

	s.BroadcastTelemetry()

	s.writeAudit("system-local", "admin.broadcast", "system", "all", "Local localhost update to global broadcast message", r)
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAdminMaintenance(w http.ResponseWriter, r *http.Request, actor string) {
	var req struct {
		Enabled          bool   `json:"enabled"`
		CountdownMinutes *int   `json:"countdown_minutes,omitempty"`
		Action           string `json:"action,omitempty"`
		Reason           string `json:"reason,omitempty"`
		Duration         int    `json:"duration,omitempty"`
		IronCurtain      bool   `json:"iron_curtain,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid payload"}`, http.StatusBadRequest)
		return
	}

	// Owner check for Iron Curtain Mode
	if req.IronCurtain {
		isOwner := strings.EqualFold(actor, s.cfg.Owner.UserID)
		if !isOwner && s.db != nil {
			if u, err := s.db.GetUserByEmail(actor); err == nil && u != nil {
				if u.Role == "owner" {
					isOwner = true
				}
			}
		}
		if !isOwner {
			http.Error(w, `{"error":"Forbidden: Only the system owner is authorized to toggle Nginx Iron Curtain Maintenance Mode."}`, http.StatusForbidden)
			return
		}
	}

	s.maintMutex.Lock()
	defer s.maintMutex.Unlock()

	// Cancel any pending countdown timers
	if s.maintTimer != nil {
		s.maintTimer.Stop()
		s.maintTimer = nil
	}

	action := "system.maintenance_disabled"
	desc := "Admin disabled system maintenance mode"
	if req.IronCurtain {
		action = "system.nginx_maintenance_disabled"
		desc = "Owner disabled Nginx Iron Curtain maintenance mode"
	}

	if req.Enabled {
		if req.Action == "" {
			req.Action = "Server Upgrade"
		}
		if req.Reason == "" {
			req.Reason = "System upgrade and maintenance"
		}
		if req.Duration <= 0 {
			req.Duration = 30
		}

		s.maintAction = req.Action
		s.maintReason = req.Reason
		s.maintDuration = req.Duration

		if req.IronCurtain {
			// Nginx Hard Maintenance (Iron Curtain)
			s.maintEndTime = time.Now().Add(time.Duration(req.Duration) * time.Minute)

			var templateBytes []byte
			var err error
			useCustom := false

			if s.db != nil {
				if customPath, errPath := s.db.GetAdminSetting("maintenance_page_path"); errPath == nil && customPath != "" {
					if b, errRead := os.ReadFile(customPath); errRead == nil {
						templateBytes = b
						useCustom = true
					} else {
						slog.Error(fmt.Sprintf("[Server] Failed to read custom maintenance page %s: %v", customPath, errRead))
					}
				}
			}

			if !useCustom {
				templateBytes, err = staticFS.ReadFile("static/maintenance.html")
				if err != nil {
					slog.Info(fmt.Sprintf("[Server] Failed to load maintenance template: %v", err))
				}
			}

			if len(templateBytes) > 0 {
				s.nginxManager.Enable(req.Action, req.Reason, req.Duration, s.maintEndTime, string(templateBytes))
			}

			action = "system.nginx_maintenance_enabled"
			desc = fmt.Sprintf("Owner enabled Nginx Iron Curtain maintenance mode immediate: %s (%s)", req.Action, req.Reason)
		} else {
			// In-App Soft Maintenance
			countdown := 0
			if req.CountdownMinutes != nil {
				countdown = *req.CountdownMinutes
			}

			if countdown > 0 {
				s.maintenanceMode = false
				s.maintScheduledAt = time.Now().Add(time.Duration(countdown) * time.Minute)
				s.maintTimer = time.AfterFunc(time.Duration(countdown)*time.Minute, func() {
					s.maintMutex.Lock()
					s.maintenanceMode = true
					s.maintEndTime = time.Now().Add(time.Duration(req.Duration) * time.Minute)
					s.maintScheduledAt = time.Time{}
					s.maintTimer = nil
					s.maintMutex.Unlock()

					s.BroadcastMaintenance("enable", req.Duration, req.Reason)

					// Kick standard tunnels
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
					slog.Info("[Server] Scheduled Soft Maintenance countdown hit 0. Soft Maintenance Mode is now ACTIVE.")
					go s.BroadcastTelemetry()
				})

				action = "system.maintenance_scheduled"
				desc = fmt.Sprintf("Admin scheduled soft maintenance in %d minutes: %s (%s)", countdown, req.Action, req.Reason)
			} else {
				s.maintenanceMode = true
				s.maintEndTime = time.Now().Add(time.Duration(req.Duration) * time.Minute)
				s.maintScheduledAt = time.Time{}
				s.BroadcastMaintenance("enable", req.Duration, req.Reason)

				// Kick standard tunnels
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
				desc = fmt.Sprintf("Admin enabled soft maintenance mode immediate: %s (%s)", req.Action, req.Reason)
			}
		}
	} else {
		// Deactivate
		s.maintenanceMode = false
		s.maintScheduledAt = time.Time{}
		s.BroadcastMaintenance("disable", 0, "")
		s.maintEndTime = time.Time{}
		s.maintAction = ""
		s.maintReason = ""
		s.maintDuration = 0

		if req.IronCurtain {
			s.nginxManager.Disable()
		}
	}

	s.writeAudit(actor, action, "system", "all", desc, r)

	maintStr := "false"
	if s.maintenanceMode {
		maintStr = "true"
	} else if !s.maintScheduledAt.IsZero() && time.Now().Before(s.maintScheduledAt) {
		maintStr = "pending"
	}

	hardActive := s.nginxManager.IsActive()

	go s.BroadcastTelemetry()

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":           "ok",
		"maintenance_mode": maintStr,
		"iron_curtain":     hardActive,
	})
}

func (s *Server) handleVisitorMaintenancePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)

	var htmlBytes []byte
	var err error
	useCustom := false

	if s.db != nil {
		if customPath, errPath := s.db.GetAdminSetting("maintenance_page_path"); errPath == nil && customPath != "" {
			if b, errRead := os.ReadFile(customPath); errRead == nil {
				htmlBytes = b
				useCustom = true
			} else {
				slog.Error(fmt.Sprintf("[Server] Failed to read custom maintenance page %s: %v", customPath, errRead))
			}
		}
	}

	if !useCustom {
		htmlBytes, err = staticFS.ReadFile("static/maintenance.html")
		if err != nil {
			if _, err := w.Write([]byte(`<h1>Scheduled Maintenance</h1><p>The gateway is undergoing administrative updates.</p>`)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
			return
		}
	}

	htmlContent := string(htmlBytes)
	s.maintMutex.RLock()
	action := s.maintAction
	reason := s.maintReason
	duration := s.maintDuration
	endTime := s.maintEndTime
	s.maintMutex.RUnlock()

	if action == "" {
		action = "Server Upgrade"
	}
	if reason == "" {
		reason = "System upgrade and maintenance"
	}
	durationStr := fmt.Sprintf("%d minutes", duration)
	if duration >= 60 {
		durationStr = fmt.Sprintf("%d hour(s)", (duration+59)/60)
	}

	htmlContent = strings.ReplaceAll(htmlContent, "__ACTION__", action)
	htmlContent = strings.ReplaceAll(htmlContent, "__REASON__", reason)
	htmlContent = strings.ReplaceAll(htmlContent, "__DURATION__", durationStr)

	epochSecs := endTime.Unix()
	if endTime.IsZero() {
		epochSecs = time.Now().Unix()
	}
	htmlContent = strings.ReplaceAll(htmlContent, "__END_TIME__", strconv.FormatInt(epochSecs, 10))

	finalBytes := s.injectBaseTag([]byte(htmlContent), r)
	if _, err := w.Write(finalBytes); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
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

	if !s.isOwner(actor) {
		if strings.EqualFold(user.Email, s.cfg.Owner.UserID) || user.Role == "owner" || (user.Role == "admin" && !strings.EqualFold(user.Email, actor)) {
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
	_ = json.NewEncoder(w).Encode(resp) //nolint:errcheck
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
		Role            *string `json:"role"`
		Status          *string `json:"status"`
		ResetMFA        *bool   `json:"reset_mfa"`
		RateLimit       *int    `json:"rate_limit"`
		MaxReservations *int    `json:"max_reservations"`
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

	if !s.isOwner(actor) {
		if strings.EqualFold(user.Email, s.cfg.Owner.UserID) || user.Role == "owner" || user.Role == "admin" || strings.EqualFold(user.Email, actor) {
			http.Error(w, `{"error":"Forbidden: Cannot modify this user"}`, http.StatusForbidden)
			return
		}
	} else if strings.EqualFold(user.Email, s.cfg.Owner.UserID) || user.Role == "owner" {
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

	if req.RateLimit != nil {
		if *req.RateLimit < 0 {
			http.Error(w, `{"error":"Rate limit cannot be negative"}`, http.StatusBadRequest)
			return
		}
		details["rate_limit_before"] = user.RateLimit
		details["rate_limit_after"] = *req.RateLimit
		user.RateLimit = *req.RateLimit
	}

	if req.MaxReservations != nil {
		details["max_reservations_before"] = user.MaxReservations
		details["max_reservations_after"] = *req.MaxReservations
		user.MaxReservations = req.MaxReservations
	}

	if err := s.db.UpdateUser(user); err != nil {
		http.Error(w, `{"error":"Failed to update user"}`, http.StatusInternalServerError)
		return
	}

	// Send status update/revocation email notification if configured
	if req.Status != nil && s.notifications != nil && s.notifications.Sender() != nil {
		subject := s.GetTranslation(user.LanguagePreference, "access_suspended_subject")
		greetingName := user.FirstName
		if greetingName == "" {
			greetingName = "there"
		}

		if *req.Status == "revoked" {
			body := fmt.Sprintf(`Hi %s,<br/><br/>
Your access permissions on Liferay Tunnel have been <strong>suspended</strong> by an administrator.<br/><br/>
All your active tunnel connections have been closed, and you will no longer be able to establish connections or access the portal.<br/><br/>
If you believe this is in error, please contact your administrator.<br/><br/>
Best regards,<br/>
Liferay Tunnel Team`, html.EscapeString(greetingName))

			plainBody := fmt.Sprintf("Hi %s,\n\nYour access on Liferay Tunnel has been suspended by an administrator.\n\nBest regards,\nLiferay Tunnel Team", greetingName)
			go func() { _ = s.notifications.Sender().Send(user.Email, subject, body, plainBody) }() //nolint:errcheck
		}
	}

	// Send role update email notification if configured and user has not unsubscribed
	if req.Role != nil && s.notifications != nil && s.notifications.Sender() != nil && user.NotificationPrefs != "disabled" {
		subject := s.GetTranslation(user.LanguagePreference, "role_updated_subject")
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
		go func() { _ = s.notifications.Sender().Send(user.Email, subject, body, plainBody) }() //nolint:errcheck
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

	_ = json.NewEncoder(w).Encode(user) //nolint:errcheck
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
	_ = json.NewEncoder(w).Encode(pats) //nolint:errcheck
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
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"}) //nolint:errcheck
			return
		}
		http.Error(w, `{"error":"Failed to revoke token"}`, http.StatusInternalServerError)
		return
	}

	s.writeAudit(actor, "token.revoked", "token", patIDStr, "", r)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"}) //nolint:errcheck
}

func (s *Server) handleAdminExtendToken(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}

	suffix := strings.TrimPrefix(r.URL.Path, "/api/admin/tokens/")
	parts := strings.Split(suffix, "/")
	if len(parts) == 0 {
		http.Error(w, `{"error":"Invalid token ID"}`, http.StatusBadRequest)
		return
	}
	patID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, `{"error":"Invalid token ID"}`, http.StatusBadRequest)
		return
	}

	var req struct {
		Days int `json:"days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request"}`, http.StatusBadRequest)
		return
	}

	var expiresAt *time.Time
	if req.Days > 0 {
		exp := time.Now().AddDate(0, 0, req.Days)
		expiresAt = &exp
	}

	if err := s.db.UpdatePATExpiry(patID, expiresAt); err != nil {
		if err == db.ErrNotFound {
			http.Error(w, `{"error":"Token not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error":"Failed to extend token"}`, http.StatusInternalServerError)
		return
	}

	s.writeAudit(actor, "token.extended", "token", strconv.FormatInt(patID, 10), fmt.Sprintf("Extended by %d days (permanent if 0)", req.Days), r)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"}) //nolint:errcheck
}

func (s *Server) handleAdminListLeases(w http.ResponseWriter, r *http.Request, actor string) {
	leases := s.registry.ListLeases()

	s.edgeLeasesMu.Lock()
	for _, userLeasesList := range s.edgeLeases {
		for _, el := range userLeasesList {
			leases = append(leases, &TunnelLease{
				UserID:          el.UserID,
				SubdomainPrefix: el.Subdomain,
				FullHost:        el.FullHost,
				LocalPort:       el.LocalPort,
				ClientIP:        el.ClientIP,
				Status:          "up",
				BytesIn:         el.BytesIn,
				BytesOut:        el.BytesOut,
				CreatedAt:       el.CreatedAt,
				NodeID:          el.NodeID,
			})
		}
	}
	s.edgeLeasesMu.Unlock()

	_ = json.NewEncoder(w).Encode(leases) //nolint:errcheck
}

func (s *Server) handleAdminKickLease(w http.ResponseWriter, r *http.Request, actor string) {
	subdomain, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/admin/leases/"))
	if err != nil {
		http.Error(w, `{"error":"Invalid lease subdomain"}`, http.StatusBadRequest)
		return
	}

	// 1. Check if the lease is hosted on an Edge server
	var targetEdgeLease *EdgeLease
	s.edgeLeasesMu.Lock()
	for _, userLeasesList := range s.edgeLeases {
		for _, el := range userLeasesList {
			if el.Subdomain == subdomain {
				targetEdgeLease = &el
				break
			}
		}
		if targetEdgeLease != nil {
			break
		}
	}
	s.edgeLeasesMu.Unlock()

	if targetEdgeLease != nil {
		// Found on Edge node! Look up node config to get the token hash
		var nodeHash string
		for _, node := range s.cfg.EdgeNodes {
			if node.ID == targetEdgeLease.NodeID {
				nodeHash = node.TokenHash
				break
			}
		}

		if nodeHash == "" {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Edge node config not found"})
			return
		}

		// Parse the edge base domain from FullHost
		edgeBaseDomain := targetEdgeLease.FullHost
		prefix := targetEdgeLease.Subdomain + "."
		edgeBaseDomain = strings.TrimPrefix(edgeBaseDomain, prefix)

		// Send proxy kick request to Edge server
		if s.sendEdgeWSKick(targetEdgeLease.NodeID, subdomain) {
			s.writeAudit(actor, "lease.kicked", "lease", subdomain, "Proxied kick to edge server via WebSocket: "+targetEdgeLease.NodeID, r)
			respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
			return
		}

		client := &http.Client{Timeout: 5 * time.Second}
		payload, _ := json.Marshal(map[string]string{"subdomain": subdomain})
		scheme := "https"
		if strings.HasPrefix(edgeBaseDomain, "127.0.0.1") || strings.HasPrefix(edgeBaseDomain, "localhost") {
			scheme = "http"
		}
		proxyReq, err := http.NewRequest("POST", scheme+"://"+edgeBaseDomain+"/api/internal/edge-kick", bytes.NewReader(payload))
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create proxy kick request"})
			return
		}
		proxyReq.Header.Set("Content-Type", "application/json")
		proxyReq.Header.Set("X-Edge-Token-Hash", nodeHash)

		resp, err := client.Do(proxyReq)
		if err != nil {
			respondJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to contact Edge server: " + err.Error()})
			return
		}
		defer resp.Body.Close() //nolint:errcheck

		if resp.StatusCode != http.StatusOK {
			respondJSON(w, resp.StatusCode, map[string]string{"error": "Edge server failed to kick lease"})
			return
		}

		s.writeAudit(actor, "lease.kicked", "lease", subdomain, "Proxied kick to edge server "+targetEdgeLease.NodeID, r)
		respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
		return
	}

	// 2. Otherwise fall back to local registry kick (control plane)
	found := s.registry.KickLease(subdomain)
	if !found {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "success", "message": "Lease not found or already gone"}) //nolint:errcheck
		return
	}

	s.writeAudit(actor, "lease.kicked", "lease", subdomain, "", r)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"}) //nolint:errcheck
}

// handleAdminOverrideRateLimit handles dynamic rate limit overrides for active tunnel leases.
func (s *Server) handleAdminOverrideRateLimit(w http.ResponseWriter, r *http.Request, actor string) {
	var req struct {
		Host      string `json:"host"`
		RateLimit int    `json:"rate_limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid payload"}`, http.StatusBadRequest)
		return
	}

	if req.Host == "" {
		http.Error(w, `{"error":"Host is required"}`, http.StatusBadRequest)
		return
	}

	if req.RateLimit < 0 {
		http.Error(w, `{"error":"Rate limit cannot be negative"}`, http.StatusBadRequest)
		return
	}

	// Apply override dynamically to active lease in memory!
	if err := s.registry.UpdateLeaseRateLimit(req.Host, req.RateLimit); err != nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	go s.BroadcastTelemetry()

	// Log audit event
	s.writeAudit(actor, "tunnel.rate_limit_overridden", "subdomain", req.Host, fmt.Sprintf("Overrode rate limit to %d RPS", req.RateLimit), r)

	respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

// handleAdminAuditExport streams audit log as CSV.
func (s *Server) handleAdminAuditExport(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}

	entries, err := s.db.ListAuditEntries(db.AuditFilter{})
	if err != nil {
		http.Error(w, `{"error":"Failed to list audit entries"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment;filename=audit_log.csv")

	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"ID", "Actor", "Action", "TargetType", "TargetID", "Details", "IP", "Timestamp"}) //nolint:errcheck

	for _, e := range entries {
		_ = writer.Write([]string{ //nolint:errcheck
			strconv.FormatInt(e.ID, 10),
			e.ActorID,
			e.Action,
			e.TargetType,
			e.TargetID,
			e.Details,
			e.IPAddress,
			e.CreatedAt.Format(time.RFC3339),
		})
	}
	writer.Flush()
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
	_ = json.NewEncoder(w).Encode(entries) //nolint:errcheck
}

// BackupInfo describes a single backup file available for restore.
type BackupInfo struct {
	Filename  string    `json:"filename"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// handleAdminListBackups returns a JSON list of available backup files.
func (s *Server) handleAdminListBackups(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}

	backupsDir := filepath.Join(filepath.Dir(s.cfg.DBPath), "backups")
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		if os.IsNotExist(err) {
			// No backups yet — return empty list, not an error
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte("[]")); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
			return
		}
		http.Error(w, `{"error":"Failed to read backups directory"}`, http.StatusInternalServerError)
		return
	}

	var backups []BackupInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		backups = append(backups, BackupInfo{
			Filename:  entry.Name(),
			SizeBytes: info.Size(),
			CreatedAt: info.ModTime().UTC(),
		})
	}
	// Newest first
	for i, j := 0, len(backups)-1; i < j; i, j = i+1, j-1 {
		backups[i], backups[j] = backups[j], backups[i]
	}

	if backups == nil {
		backups = []BackupInfo{}
	}
	respondJSON(w, http.StatusOK, backups)
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
		_ = json.NewEncoder(w).Encode(list) //nolint:errcheck
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
		s.BroadcastBlacklistUpdate("add", payload.IPAddress)
		s.writeAudit(actor, "ip.blacklisted", "ip", payload.IPAddress, payload.Reason, r)
		body, _ := s.renderNotificationTemplate("en", "admin_ip_banned.txt", map[string]interface{}{"IP": payload.IPAddress, "Actor": actor}) //nolint:errcheck
		s.notifications.SendAdminAlert("alert_notify_blacklist", "LFR Tunnel Alert: IP Banned", body)
		s.webhooks.SendIPBlacklistAlert(payload.IPAddress, fmt.Sprintf("%s (Banned by admin: %s)", payload.Reason, actor))
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"}) //nolint:errcheck
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
		s.BroadcastBlacklistUpdate("remove", ip)
		s.writeAudit(actor, "ip.unblacklisted", "ip", ip, "", r)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"}) //nolint:errcheck
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
		notifyReg, _ := s.db.GetAdminSetting("alert_notify_registration")       //nolint:errcheck
		notifyBan, _ := s.db.GetAdminSetting("alert_notify_blacklist")          //nolint:errcheck
		notifyOffline, _ := s.db.GetAdminSetting("alert_notify_tunnel_offline") //nolint:errcheck

		// Default values if not set
		if notifyReg == "" {
			notifyReg = "true"
		}
		if notifyBan == "" {
			notifyBan = "true"
		}
		if notifyOffline == "" {
			notifyOffline = "false"
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
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
					slog.Info(fmt.Sprintf("[Admin] Failed to save setting %s: %v", key, err))
				}
			}
		}

		s.writeAudit(actor, "admin.settings_updated", "system", "email_alerts", "Admin updated email alert configuration", r)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Settings updated"}) //nolint:errcheck
		return
	}

	http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
}

func (s *Server) handleAdminTestWebhook(w http.ResponseWriter, r *http.Request, actor string) {
	w.Header().Set("Content-Type", "application/json")

	// 1. Enforce sliding rate limiter per admin user
	s.testLimiterMu.Lock()
	lastTest, exists := s.lastTestTimes[actor]
	if exists && time.Since(lastTest) < 30*time.Second {
		remaining := int(30 - time.Since(lastTest).Seconds())
		if remaining < 1 {
			remaining = 1
		}
		s.testLimiterMu.Unlock()
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"error":       fmt.Sprintf("Rate limit exceeded. Please wait %d seconds before testing again.", remaining),
			"retry_after": remaining,
		})
		return
	}
	s.lastTestTimes[actor] = time.Now()
	s.testLimiterMu.Unlock()

	// 2. Assemble test alert parameters
	timestamp := time.Now().UTC().Format("2006-01-02 15:04:05 UTC")
	version := config.Version

	// 3. Dispatch webhooks
	if s.webhooks != nil {
		s.webhooks.SendTestAlert(actor, timestamp, version)
	}

	// 4. Dispatch Email alert
	if s.notifications != nil && s.cfg.AdminNotificationEmail != "" {
		body, _ := s.renderNotificationTemplate("en", "admin_test_integration.txt", map[string]interface{}{ //nolint:errcheck
			"Actor":     actor,
			"Timestamp": timestamp,
			"Version":   version,
		})
		s.notifications.SendAdminAlert(
			"alert_notify_test",
			"Liferay Tunnel Integration Test",
			body,
		)
	}

	// 5. Record Audit log
	s.writeAudit(actor, "admin.test_notification_dispatched", "system", "webhook_test", "Admin triggered an integration test notification", r)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"status":  "success",
		"message": "Test notifications dispatched successfully.",
	})
}

// respondJSON is a DRY helper for sending JSON API responses
func respondJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Info(fmt.Sprintf("[Server] Failed to encode JSON response: %v", err))
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

	s.PushUserTelemetryByID(req.UserID)

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

	s.PushUserTelemetryByID(user.ID)

	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handlePrivacyFallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	lang := s.ResolveLocale(r)
	dir := GetDirection(lang)
	title := s.GetTranslation(lang, "privacy_title")

	// Render dynamic localized Privacy Policy template
	body, err := s.renderEmailTemplate(lang, "privacy.html", map[string]interface{}{
		"Lang":  lang,
		"Dir":   dir,
		"Title": title,
	})
	if err != nil {
		slog.Info(fmt.Sprintf("[Server] Failed to render privacy policy template: %v", err))
		// Hardcoded basic fallback
		if _, err := w.Write([]byte(`<html><body><h1>Privacy Policy</h1><p>Under maintenance.</p></body></html>`)); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
		return
	}

	if _, err := w.Write([]byte(body)); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
}

func (s *Server) handleCookiesFallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	lang := s.ResolveLocale(r)
	dir := GetDirection(lang)
	title := s.GetTranslation(lang, "cookie_title")

	// Render dynamic localized Cookies Disclosure template
	body, err := s.renderEmailTemplate(lang, "cookies.html", map[string]interface{}{
		"Lang":  lang,
		"Dir":   dir,
		"Title": title,
	})
	if err != nil {
		slog.Info(fmt.Sprintf("[Server] Failed to render cookie disclosure template: %v", err))
		// Hardcoded basic fallback
		if _, err := w.Write([]byte(`<html><body><h1>Cookie Disclosure</h1><p>Under maintenance.</p></body></html>`)); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
		return
	}

	if _, err := w.Write([]byte(body)); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
}

// renderEmailTemplate loads and compiles the requested localized HTML template.
func (s *Server) renderEmailTemplate(lang, templateName string, data interface{}) (string, error) {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if len(lang) > 2 {
		lang = lang[:2]
	}

	externalDir := "/etc/lfr-tunneld/templates"
	var htmlContent string
	loadedExternal := false

	// 1. Try loading from external directory first (Runtime customization!)
	extPath := filepath.Join(externalDir, lang, templateName)
	if _, err := os.Stat(extPath); err == nil {
		data, err := os.ReadFile(extPath)
		if err == nil {
			htmlContent = string(data)
			loadedExternal = true
		}
	}
	if !loadedExternal && lang != "en" {
		// Try default en external override
		extPathEn := filepath.Join(externalDir, "en", templateName)
		if _, err := os.Stat(extPathEn); err == nil {
			data, err := os.ReadFile(extPathEn)
			if err == nil {
				htmlContent = string(data)
				loadedExternal = true
			}
		}
	}

	// 2. Fall back to Go-embedded assets second
	if !loadedExternal {
		// Try localized embedded template first
		data, err := templatesFS.ReadFile(fmt.Sprintf("templates/%s/%s", lang, templateName))
		if err != nil {
			// Fall back to default English embedded template
			data, err = templatesFS.ReadFile(fmt.Sprintf("templates/en/%s", templateName))
			if err != nil {
				return "", fmt.Errorf("failed to load embedded template %s: %w", templateName, err)
			}
		}
		htmlContent = string(data)
	}

	// 3. Compile and execute using html/template
	t, err := template.New(templateName).Parse(htmlContent)
	if err != nil {
		return "", fmt.Errorf("failed to parse template %s: %w", templateName, err)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template %s: %w", templateName, err)
	}

	renderedHTML := buf.String()

	// Append English fallback version at the bottom of non-English transactional emails
	if lang != "en" && templateName != "privacy.html" && templateName != "cookies.html" {
		engHTML, err := s.renderEmailTemplate("en", templateName, data)
		if err == nil && engHTML != "" {
			renderedHTML += `
<br/><br/>
<hr style="border: none; border-top: 1px solid rgba(0,0,0,0.1); margin: 24px 0;" />
<p style="font-size: 11px; color: #8b949e; margin-bottom: 16px; font-family: sans-serif;">[ English Translation / Fallback ]</p>
` + engHTML
		}
	}

	return renderedHTML, nil
}

func (s *Server) renderNotificationTemplate(lang, templateName string, data interface{}) (string, error) {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if len(lang) > 2 {
		lang = lang[:2]
	}

	externalDir := "/etc/lfr-tunneld/templates"
	var textContent string
	loadedExternal := false

	// 1. Try loading from external directory first (Runtime customization!)
	extPath := filepath.Join(externalDir, lang, templateName)
	if _, err := os.Stat(extPath); err == nil {
		data, err := os.ReadFile(extPath)
		if err == nil {
			textContent = string(data)
			loadedExternal = true
		}
	}
	if !loadedExternal && lang != "en" {
		// Try default en external override
		extPathEn := filepath.Join(externalDir, "en", templateName)
		if _, err := os.Stat(extPathEn); err == nil {
			data, err := os.ReadFile(extPathEn)
			if err == nil {
				textContent = string(data)
				loadedExternal = true
			}
		}
	}

	// 2. Fall back to Go-embedded assets second
	if !loadedExternal {
		// Try localized embedded template first
		data, err := templatesFS.ReadFile(fmt.Sprintf("templates/%s/%s", lang, templateName))
		if err != nil {
			// Fall back to default English embedded template
			data, err = templatesFS.ReadFile(fmt.Sprintf("templates/en/%s", templateName))
			if err != nil {
				return "", fmt.Errorf("failed to load embedded template %s: %w", templateName, err)
			}
		}
		textContent = string(data)
	}

	// 3. Compile and execute using text/template
	t, err := texttemplate.New(templateName).Parse(textContent)
	if err != nil {
		return "", fmt.Errorf("failed to parse template %s: %w", templateName, err)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template %s: %w", templateName, err)
	}

	return buf.String(), nil
}

var generatorAdjectives = []string{"clever", "dancing", "silent", "flying", "golden", "brave", "swift", "gentle", "happy", "bright", "cool", "smart", "bold", "wild", "ocean", "forest", "mountain", "cloud"}
var generatorNouns = []string{"rabbit", "tiger", "fox", "owl", "hawk", "lion", "bear", "wolf", "deer", "panda", "koala", "otter", "badger", "falcon", "eagle", "dolphin", "whale", "shark"}
var generatorTechAdjectives = []string{"hybrid", "headless", "cloud", "dynamic", "static", "micro", "agile", "secure", "elastic", "native", "client", "custom", "remote", "shared"}
var generatorLiferayNouns = []string{"portal", "tomcat", "extension", "object", "bundle", "theme", "layout", "site", "depot", "asset", "schema", "widget", "module", "service"}
var generatorWords = []string{"apple", "banana", "cherry", "dragon", "falcon", "guitar", "jungle", "monkey", "orange", "potato", "rocket", "shadow", "tomato", "violin", "wizard", "yellow", "zebra"}

func (s *Server) generateRandomSubdomainPrefix(style string) string {
	randInt := func(max int) int {
		b := make([]byte, 4)
		_, _ = rand.Read(b) //nolint:errcheck
		val := int(uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]))
		if val < 0 {
			val = -val
		}
		return val % max
	}

	switch style {
	case "words":
		return fmt.Sprintf("%s-%s-%s", generatorWords[randInt(len(generatorWords))], generatorWords[randInt(len(generatorWords))], generatorWords[randInt(len(generatorWords))])
	case "heroku":
		return fmt.Sprintf("%s-%s-%d", generatorAdjectives[randInt(len(generatorAdjectives))], generatorNouns[randInt(len(generatorNouns))], randInt(9000)+1000)
	case "liferay":
		return fmt.Sprintf("%s-%s-%d", generatorTechAdjectives[randInt(len(generatorTechAdjectives))], generatorLiferayNouns[randInt(len(generatorLiferayNouns))], randInt(900)+100)
	default: // Completely Random (Alphanumeric) [a-z0-9]{8}
		const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
		b := make([]byte, 8)
		_, _ = rand.Read(b) //nolint:errcheck
		for i := range b {
			b[i] = chars[int(b[i])%len(chars)]
		}
		return string(b)
	}
}

func (s *Server) checkQuarantineStatus(host string) (bool, string, string) {
	if s.db == nil {
		return false, "", ""
	}
	for _, domain := range s.cfg.Domains {
		if strings.HasSuffix(host, "."+domain) {
			subdomain := strings.TrimSuffix(host, "."+domain)
			existing, err := s.db.GetSubdomainReservationByName(subdomain, domain)
			if err == nil && existing != nil {
				if existing.ExpiresAt != nil && existing.ExpiresAt.Before(time.Now()) {
					quarantineCutoff := existing.ExpiresAt.AddDate(0, 0, s.cfg.SubdomainQuarantineDays)
					if time.Now().Before(quarantineCutoff) {
						return true, host, quarantineCutoff.Format("2006-01-02 15:04:05 MST")
					}
				}
			}
		}
	}
	return false, "", ""
}

func (s *Server) handleVisitorGonePage(w http.ResponseWriter, r *http.Request, host, releaseDate string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusGone)

	htmlBytes, err := staticFS.ReadFile("static/gone.html")
	if err != nil {
		if _, err := w.Write([]byte(`<h1>Subdomain Discontinued</h1><p>The subdomain is in quarantine.</p>`)); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
		return
	}

	portalURL := s.getPortalBaseURL(r) + "/portal"

	htmlContent := string(htmlBytes)
	htmlContent = strings.ReplaceAll(htmlContent, "{{.Host}}", html.EscapeString(host))
	htmlContent = strings.ReplaceAll(htmlContent, "{{.ReleaseDate}}", html.EscapeString(releaseDate))
	htmlContent = strings.ReplaceAll(htmlContent, "{{.PortalURL}}", html.EscapeString(portalURL))

	finalBytes := s.injectBaseTag([]byte(htmlContent), r)
	if _, err := w.Write(finalBytes); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
}

func (s *Server) injectBaseTag(htmlBytes []byte, r *http.Request) []byte {
	baseURL := s.getPortalBaseURL(r)
	baseTag := []byte(fmt.Sprintf("<head>\n    <base href=\"%s/\">", baseURL))
	return bytes.Replace(htmlBytes, []byte("<head>"), baseTag, 1)
}

type EdgeLease struct {
	NodeID        string    `json:"node_id"`
	Subdomain     string    `json:"subdomain_prefix"`
	UserID        string    `json:"user_id"`
	FullHost      string    `json:"full_host"`
	LocalPort     int       `json:"local_port"`
	ClientIP      string    `json:"client_ip"`
	ClientVersion string    `json:"client_version,omitempty"`
	ClientOS      string    `json:"client_os,omitempty"`
	BytesIn       uint64    `json:"bytes_in"`
	BytesOut      uint64    `json:"bytes_out"`
	CreatedAt     time.Time `json:"created_at"`
}

func (s *Server) handleEdgeRegisterProxy(w http.ResponseWriter, r *http.Request, req RegisterRequest) {
	activeDomains := s.getActiveDomainsForRequest(r, nil)
	edgeReqPayload := struct {
		RegisterRequest
		Domains  []string `json:"domains"`
		ClientIP string   `json:"client_ip"`
	}{
		RegisterRequest: req,
		Domains:         activeDomains,
		ClientIP:        getClientIP(r),
	}

	payloadBytes, err := json.Marshal(edgeReqPayload)
	if err != nil {
		s.respondRegisterResponse(w, http.StatusInternalServerError, r, RegisterResponse{Status: "error", Error: "failed to marshal proxy request"})
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	proxyReq, err := http.NewRequest("POST", s.cfg.ControlPlaneURL+"/api/internal/edge-register", bytes.NewReader(payloadBytes))
	if err != nil {
		s.respondRegisterResponse(w, http.StatusInternalServerError, r, RegisterResponse{Status: "error", Error: "failed to create proxy request"})
		return
	}
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set("X-Edge-Token", s.cfg.EdgeToken)

	resp, err := client.Do(proxyReq)
	if err != nil {
		s.respondRegisterResponse(w, http.StatusBadGateway, r, RegisterResponse{Status: "error", Error: "control plane connection failed: " + err.Error()})
		return
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errResp) //nolint:errcheck
		if errResp.Error == "" {
			errResp.Error = fmt.Sprintf("control plane rejected request with status %d", resp.StatusCode)
		}
		s.respondRegisterResponse(w, resp.StatusCode, r, RegisterResponse{Status: "error", Error: errResp.Error})
		return
	}

	var valResp struct {
		UserID          string `json:"user_id"`
		SubdomainPrefix string `json:"subdomain_prefix"`
		RateLimit       int    `json:"rate_limit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&valResp); err != nil {
		s.respondRegisterResponse(w, http.StatusInternalServerError, r, RegisterResponse{Status: "error", Error: "invalid response from control plane"})
		return
	}

	clientIP := getClientIP(r)
	sessionToken, remotes, err := s.registry.Register(valResp.UserID, valResp.SubdomainPrefix, req.Ports, activeDomains, valResp.RateLimit, clientIP, req.BasicAuth, req.AddedHeaders)
	if err != nil {
		s.respondRegisterResponse(w, http.StatusConflict, r, RegisterResponse{Status: "error", Error: err.Error()})
		return
	}

	var warning string
	if req.ClientVersion != "" && req.ClientVersion != config.Version {
		warning = fmt.Sprintf("Version mismatch! Server is running %s but client is %s. Please consider upgrading using 'lfr-tunnel -upgrade'", config.Version, req.ClientVersion)
	}

	s.respondRegisterResponse(w, http.StatusOK, r, RegisterResponse{
		Status:          "success",
		SessionToken:    sessionToken,
		SubdomainPrefix: valResp.SubdomainPrefix,
		Remotes:         remotes,
		Domains:         activeDomains,
		Warning:         warning,
		ServerVersion:   config.Version,
	})
}

func (s *Server) notifyControlPlaneDeregister(userID, subdomain string) {
	client := &http.Client{Timeout: 5 * time.Second}
	payloadBytes, err := json.Marshal(map[string]string{
		"user_id":   userID,
		"subdomain": subdomain,
	})
	if err != nil {
		return
	}

	req, err := http.NewRequest("POST", s.cfg.ControlPlaneURL+"/api/internal/edge-deregister", bytes.NewReader(payloadBytes))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Edge-Token", s.cfg.EdgeToken)

	resp, err := client.Do(req)
	if err != nil {
		slog.Info(fmt.Sprintf("[Server Edge] Failed to notify control plane deregister: %v", err))
		return
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		slog.Info(fmt.Sprintf("[Server Edge] Control plane deregister returned status: %d", resp.StatusCode))
	}
}

func (s *Server) handleEdgeRegister(w http.ResponseWriter, r *http.Request) {
	edgeToken := r.Header.Get("X-Edge-Token")
	if edgeToken == "" {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing edge token"})
		return
	}

	hash := sha256.Sum256([]byte(edgeToken))
	hashStr := hex.EncodeToString(hash[:])

	authorized := false
	var edgeNodeID string
	for _, node := range s.cfg.EdgeNodes {
		if node.TokenHash == hashStr {
			authorized = true
			edgeNodeID = node.ID
			break
		}
	}

	if !authorized {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid edge token"})
		return
	}

	var edgeReq struct {
		RegisterRequest
		Domains  []string `json:"domains"`
		ClientIP string   `json:"client_ip"`
	}

	if err := json.NewDecoder(r.Body).Decode(&edgeReq); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON payload"})
		return
	}

	user, ok := s.isValidToken(edgeReq.AuthToken)
	if !ok {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var userRec *db.User
	if s.db != nil {
		userRec, _ = s.db.GetUser(user.ID) //nolint:errcheck
	}

	finalSubdomain := edgeReq.SubdomainPrefix
	requestedRandom := finalSubdomain == "" || finalSubdomain == "random"

	if requestedRandom {
		found := false
		for attempt := 0; attempt < 10; attempt++ {
			candidate := s.generateRandomSubdomainPrefix("liferay")
			dbOk := true
			if s.db != nil {
				for _, d := range edgeReq.Domains {
					existing, err := s.db.GetSubdomainReservationByName(candidate, d)
					if err == nil && existing != nil {
						dbOk = false
						break
					}
				}
			}
			if dbOk {
				finalSubdomain = candidate
				found = true
				break
			}
		}
		if !found {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate unique random subdomain"})
			return
		}
	} else {
		if s.db != nil {
			var domainsToReserve []string
			for _, d := range edgeReq.Domains {
				existing, err := s.db.GetSubdomainReservationByName(finalSubdomain, d)
				if err == nil && existing != nil {
					if existing.ExpiresAt != nil && existing.ExpiresAt.Before(time.Now()) {
						quarantineCutoff := existing.ExpiresAt.AddDate(0, 0, s.cfg.SubdomainQuarantineDays)
						if time.Now().Before(quarantineCutoff) {
							if existing.UserID != user.ID {
								respondJSON(w, http.StatusConflict, map[string]string{"error": "Subdomain is currently in quarantine"})
								return
							}
							domainsToReserve = append(domainsToReserve, d)
						} else {
							_ = s.db.DeleteSubdomainReservation(existing.ID) //nolint:errcheck
							domainsToReserve = append(domainsToReserve, d)
						}
					} else {
						if existing.UserID != user.ID {
							respondJSON(w, http.StatusConflict, map[string]string{"error": "Subdomain is reserved by another user"})
							return
						}
					}
				} else {
					if s.canUserAutoReserve(userRec) {
						domainsToReserve = append(domainsToReserve, d)
					} else {
						respondJSON(w, http.StatusForbidden, map[string]string{"error": "Custom subdomains must be reserved in the portal prior to connecting"})
						return
					}
				}
			}

			if len(domainsToReserve) > 0 {
				limit := s.cfg.DefaultMaxReservations
				if userRec != nil {
					limit = s.getUserMaxReservations(userRec)
				}

				list, err := s.db.ListSubdomainReservationsByUserID(user.ID)
				activeCount := 0
				if err == nil {
					for _, res := range list {
						if res.ExpiresAt == nil || res.ExpiresAt.After(time.Now()) {
							activeCount++
						}
					}
				}

				needed := len(domainsToReserve)
				if limit >= 0 && activeCount+needed > limit {
					respondJSON(w, http.StatusForbidden, map[string]string{"error": "Subdomain reservation quota limit reached"})
					return
				}

				for _, d := range domainsToReserve {
					if existing, err := s.db.GetSubdomainReservationByName(finalSubdomain, d); err == nil && existing != nil {
						_ = s.db.DeleteSubdomainReservation(existing.ID) //nolint:errcheck
					}
					res := &db.SubdomainReservation{
						UserID:    user.ID,
						Subdomain: finalSubdomain,
						Domain:    d,
						ExpiresAt: s.getUserSubdomainExpiry(user),
					}
					_ = s.db.CreateSubdomainReservation(res) //nolint:errcheck
				}
			}
		}
	}

	if s.db != nil {
		for _, d := range edgeReq.Domains {
			existing, err := s.db.GetSubdomainReservationByName(finalSubdomain, d)
			if err == nil && existing != nil && existing.UserID == user.ID {
				updated := false
				if edgeReq.Passcode != "" {
					existing.Passcode = edgeReq.Passcode
					updated = true
				}
				if edgeReq.WhitelistIPs != "" {
					existing.WhitelistIPs = edgeReq.WhitelistIPs
					updated = true
				}
				if updated {
					_ = s.db.UpdateSubdomainReservation(existing) //nolint:errcheck
				}
			}
		}
	}

	effectiveLimit := edgeReq.RateLimit
	if userRec != nil && userRec.RateLimit > 0 {
		if effectiveLimit <= 0 || effectiveLimit > userRec.RateLimit {
			effectiveLimit = userRec.RateLimit
		}
	}
	if s.cfg.MaxTunnelRateLimit > 0 {
		if effectiveLimit <= 0 || effectiveLimit > s.cfg.MaxTunnelRateLimit {
			effectiveLimit = s.cfg.MaxTunnelRateLimit
		}
	} else if effectiveLimit <= 0 {
		effectiveLimit = 0
	}

	if (edgeReq.ClientVersion != "" || edgeReq.ClientOS != "") && userRec != nil {
		changed := false
		if edgeReq.ClientVersion != "" && userRec.LastClientVersion != edgeReq.ClientVersion {
			userRec.LastClientVersion = edgeReq.ClientVersion
			changed = true
		}
		if edgeReq.ClientOS != "" && userRec.LastClientOS != edgeReq.ClientOS {
			userRec.LastClientOS = edgeReq.ClientOS
			changed = true
		}
		if changed {
			_ = s.db.UpdateUser(userRec) //nolint:errcheck
		}
	}

	s.edgeLeasesMu.Lock()
	activeEdgeCount := len(s.edgeLeases[user.ID])
	s.edgeLeasesMu.Unlock()

	localCount := 0
	isReconnecting := false
	if s.registry != nil {
		leases := s.registry.ListLeases()
		uniqueSubs := make(map[string]bool)
		for _, l := range leases {
			if l.UserID == user.ID {
				uniqueSubs[l.SubdomainPrefix] = true
			}
		}
		localCount = len(uniqueSubs)
		isReconnecting = uniqueSubs[finalSubdomain]
	}

	s.edgeLeasesMu.Lock()
	for _, l := range s.edgeLeases[user.ID] {
		if l.Subdomain == finalSubdomain {
			isReconnecting = true
			break
		}
	}
	s.edgeLeasesMu.Unlock()

	maxTunnels := s.cfg.DefaultMaxActiveTunnels
	if user.Role == "admin" && s.cfg.AdminMaxActiveTunnels != nil {
		maxTunnels = *s.cfg.AdminMaxActiveTunnels
	} else if user.Role == "owner" && s.cfg.OwnerMaxActiveTunnels != nil {
		maxTunnels = *s.cfg.OwnerMaxActiveTunnels
	}

	if userRec != nil && userRec.MaxTunnels != nil {
		maxTunnels = *userRec.MaxTunnels
	}

	if maxTunnels > 0 && (activeEdgeCount+localCount) >= maxTunnels && !isReconnecting {
		respondJSON(w, http.StatusForbidden, map[string]string{
			"error": fmt.Sprintf("Active tunnels concurrency limit reached (%d). Stop another active tunnel or ask an administrator to increase your limit.", maxTunnels),
		})
		return
	}

	s.edgeLeasesMu.Lock()
	if leases, ok := s.edgeLeases[user.ID]; ok {
		var filtered []EdgeLease
		for _, el := range leases {
			if el.Subdomain != finalSubdomain || el.NodeID != edgeNodeID {
				filtered = append(filtered, el)
			}
		}
		s.edgeLeases[user.ID] = filtered
	}

	fullHost := finalSubdomain
	if len(edgeReq.Domains) > 0 {
		fullHost = fmt.Sprintf("%s.%s", finalSubdomain, edgeReq.Domains[0])
	}

	localPort := 0
	if len(edgeReq.Ports) > 0 {
		localPort = edgeReq.Ports[0].LocalPort
	}

	s.edgeLeases[user.ID] = append(s.edgeLeases[user.ID], EdgeLease{
		NodeID:        edgeNodeID,
		Subdomain:     finalSubdomain,
		UserID:        user.ID,
		FullHost:      fullHost,
		LocalPort:     localPort,
		ClientIP:      edgeReq.ClientIP,
		ClientVersion: edgeReq.ClientVersion,
		ClientOS:      edgeReq.ClientOS,
		BytesIn:       0,
		BytesOut:      0,
		CreatedAt:     time.Now(),
	})
	s.edgeLeasesMu.Unlock()

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":          user.ID,
		"subdomain_prefix": finalSubdomain,
		"rate_limit":       effectiveLimit,
	})
}

func (s *Server) handleEdgeDeregister(w http.ResponseWriter, r *http.Request) {
	edgeToken := r.Header.Get("X-Edge-Token")
	if edgeToken == "" {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing edge token"})
		return
	}

	hash := sha256.Sum256([]byte(edgeToken))
	hashStr := hex.EncodeToString(hash[:])

	authorized := false
	for _, node := range s.cfg.EdgeNodes {
		if node.TokenHash == hashStr {
			authorized = true
			break
		}
	}

	if !authorized {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid edge token"})
		return
	}

	var edgeReq struct {
		UserID    string `json:"user_id"`
		Subdomain string `json:"subdomain"`
	}

	if err := json.NewDecoder(r.Body).Decode(&edgeReq); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON payload"})
		return
	}

	s.edgeLeasesMu.Lock()
	defer s.edgeLeasesMu.Unlock()

	leases, ok := s.edgeLeases[edgeReq.UserID]
	if ok {
		var newLeases []EdgeLease
		for _, l := range leases {
			if l.Subdomain != edgeReq.Subdomain {
				newLeases = append(newLeases, l)
			}
		}
		if len(newLeases) == 0 {
			delete(s.edgeLeases, edgeReq.UserID)
		} else {
			s.edgeLeases[edgeReq.UserID] = newLeases
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) forwardAuditToControlPlane(actorID, action, targetType, targetID, details, ip string) {
	client := &http.Client{Timeout: 5 * time.Second}
	payloadBytes, err := json.Marshal(map[string]string{
		"actor_id":    actorID,
		"action":      action,
		"target_type": targetType,
		"target_id":   targetID,
		"details":     details,
		"ip_address":  ip,
	})
	if err != nil {
		return
	}

	req, err := http.NewRequest("POST", s.cfg.ControlPlaneURL+"/api/internal/edge-audit-log", bytes.NewReader(payloadBytes))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Edge-Token", s.cfg.EdgeToken)

	resp, err := client.Do(req)
	if err != nil {
		slog.Info(fmt.Sprintf("[Server Edge] Failed to forward audit log to control plane: %v", err))
		return
	}
	defer resp.Body.Close() //nolint:errcheck
}

func (s *Server) handleEdgeAuditLog(w http.ResponseWriter, r *http.Request) {
	edgeToken := r.Header.Get("X-Edge-Token")
	if edgeToken == "" {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing edge token"})
		return
	}

	hash := sha256.Sum256([]byte(edgeToken))
	hashStr := hex.EncodeToString(hash[:])

	authorized := false
	for _, node := range s.cfg.EdgeNodes {
		if node.TokenHash == hashStr {
			authorized = true
			break
		}
	}

	if !authorized {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid edge token"})
		return
	}

	var edgeReq struct {
		ActorID    string `json:"actor_id"`
		Action     string `json:"action"`
		TargetType string `json:"target_type"`
		TargetID   string `json:"target_id"`
		Details    string `json:"details"`
		IPAddress  string `json:"ip_address"`
	}

	if err := json.NewDecoder(r.Body).Decode(&edgeReq); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON payload"})
		return
	}

	if s.db != nil {
		entry := &db.AuditEntry{
			ActorID:    edgeReq.ActorID,
			Action:     edgeReq.Action,
			TargetType: edgeReq.TargetType,
			TargetID:   edgeReq.TargetID,
			Details:    edgeReq.Details,
			IPAddress:  edgeReq.IPAddress,
		}
		if err := s.db.WriteAuditEntry(entry); err != nil {
			slog.Info(fmt.Sprintf("[Server Control] Failed to write forwarded audit entry: %v", err))
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleEdgeMetrics(w http.ResponseWriter, r *http.Request) {
	edgeToken := r.Header.Get("X-Edge-Token")
	if edgeToken == "" {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing edge token"})
		return
	}

	hash := sha256.Sum256([]byte(edgeToken))
	hashStr := hex.EncodeToString(hash[:])

	authorized := false
	var edgeNodeID string
	for _, node := range s.cfg.EdgeNodes {
		if node.TokenHash == hashStr {
			authorized = true
			edgeNodeID = node.ID
			break
		}
	}

	if !authorized {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid edge token"})
		return
	}

	var edgeMetrics []db.TunnelMetric
	if err := json.NewDecoder(r.Body).Decode(&edgeMetrics); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON payload"})
		return
	}

	if s.db != nil {
		for _, m := range edgeMetrics {
			m.NodeID = edgeNodeID
			if err := s.db.RecordTunnelMetric(&m); err != nil {
				slog.Info(fmt.Sprintf("[Server Control] Failed to write forwarded metric: %v", err))
			}
		}
	}

	// Update the in-memory edgeLeases statistics on the Control Plane so the dashboard gets the latest bytes transferred on-the-fly!
	s.edgeLeasesMu.Lock()
	for _, m := range edgeMetrics {
		if leases, ok := s.edgeLeases[m.UserID]; ok {
			for idx, el := range leases {
				if el.Subdomain == m.SubdomainPrefix && el.NodeID == edgeNodeID {
					leases[idx].BytesIn += uint64(m.BytesIn)
					leases[idx].BytesOut += uint64(m.BytesOut)
				}
			}
		}
	}
	s.edgeLeasesMu.Unlock()

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleEdgeKick(w http.ResponseWriter, r *http.Request) {
	// First check the signature header from control plane (X-Edge-Token-Hash)
	tokenHash := r.Header.Get("X-Edge-Token-Hash")
	authorized := false

	if tokenHash != "" {
		hash := sha256.Sum256([]byte(s.cfg.EdgeToken))
		expectedHash := hex.EncodeToString(hash[:])
		if tokenHash == expectedHash {
			authorized = true
		}
	} else {
		// Fallback to plaintext header check if needed
		edgeToken := r.Header.Get("X-Edge-Token")
		if edgeToken == s.cfg.EdgeToken && s.cfg.EdgeToken != "" {
			authorized = true
		}
	}

	if !authorized {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var kickReq struct {
		Subdomain string `json:"subdomain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&kickReq); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON payload"})
		return
	}

	if s.registry != nil {
		found := s.registry.KickLease(kickReq.Subdomain)
		if found {
			respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
			return
		}
	}

	respondJSON(w, http.StatusNotFound, map[string]string{"error": "lease not found"})
}

// checkOutboundConnectivity checks outbound internet access by hitting highly available public endpoints.
func (s *Server) checkOutboundConnectivity() bool {
	targets := []string{"https://1.1.1.1", "https://www.google.com"}
	client := &http.Client{Timeout: 2 * time.Second}
	for _, target := range targets {
		req, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close() //nolint:errcheck
			return true
		}
	}
	return false
}

func (s *Server) monitorEdgeHealth() {
	for {
		outboundOk := s.checkOutboundConnectivity()
		s.outboundMutex.Lock()
		s.outboundConnected = outboundOk
		s.outboundMutex.Unlock()

		for _, edge := range s.cfg.EdgeNodes {
			if edge.URL == "" {
				continue
			}

			if !outboundOk {
				s.updateEdgeHealth(edge.ID, "Unknown", 0, "Gateway outbound connectivity check failed", "")
				continue
			}

			client := &http.Client{Timeout: 5 * time.Second}
			start := time.Now()
			req, err := http.NewRequest(http.MethodGet, edge.URL+"/api/version", nil)
			if err != nil {
				s.updateEdgeHealth(edge.ID, "Offline", 0, err.Error(), "")
				continue
			}

			req.Header.Set("User-Agent", "lfr-tunnel-health-monitor")

			resp, err := client.Do(req)
			latency := time.Since(start).Milliseconds()

			if err != nil {
				s.updateEdgeHealth(edge.ID, "Offline", latency, err.Error(), "")
				continue
			}

			var version string
			if resp.StatusCode == http.StatusOK {
				var versionResp struct {
					ServerVersion string `json:"server_version"`
				}
				if bodyBytes, readErr := io.ReadAll(resp.Body); readErr == nil {
					_ = json.Unmarshal(bodyBytes, &versionResp) //nolint:errcheck
					version = versionResp.ServerVersion
				}
			}
			_ = resp.Body.Close() //nolint:errcheck

			if resp.StatusCode == http.StatusOK {
				s.updateEdgeHealth(edge.ID, "Online", latency, "", version)
			} else {
				s.updateEdgeHealth(edge.ID, "Offline", latency, fmt.Sprintf("HTTP %d", resp.StatusCode), "")
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(60 * time.Second):
		}
	}
}

func (s *Server) updateEdgeHealth(id, status string, latency int64, errMsg string, version string) {
	s.edgeHealthMu.Lock()
	defer s.edgeHealthMu.Unlock()

	var resolvedIP string
	for _, edge := range s.cfg.EdgeNodes {
		if edge.ID == id && edge.URL != "" {
			if u, err := url.Parse(edge.URL); err == nil {
				host := u.Hostname()
				if ips, err := net.LookupHost(host); err == nil && len(ips) > 0 {
					resolvedIP = ips[0]
				}
			}
			break
		}
	}

	s.edgeHealth[id] = EdgeHealthStatus{
		Status:       status,
		LatencyMs:    latency,
		LastCheckAt:  time.Now().Unix(),
		ErrorMessage: errMsg,
		ResolvedIP:   resolvedIP,
		Version:      version,
	}
}

func (s *Server) handleEdgeHealth(w http.ResponseWriter, r *http.Request) {
	s.edgeHealthMu.RLock()
	nodes := make(map[string]EdgeHealthStatus)
	for k, v := range s.edgeHealth {
		nodes[k] = v
	}
	s.edgeHealthMu.RUnlock()

	s.edgeClientsMu.RLock()
	for nodeID, conn := range s.edgeClients {
		h, exists := nodes[nodeID]
		if !exists {
			h = EdgeHealthStatus{
				Status:      "Online",
				LastCheckAt: time.Now().Unix(),
			}
		} else {
			h.Status = "Online"
			h.ErrorMessage = ""
		}
		if h.Version == "" {
			h.Version = s.edgeVersions[nodeID]
		}
		if h.ResolvedIP == "" {
			h.ResolvedIP = s.edgeIPs[nodeID]
		}
		if h.ResolvedIP == "" && conn != nil {
			if remoteAddr := conn.RemoteAddr(); remoteAddr != nil {
				host, _, _ := net.SplitHostPort(remoteAddr.String())
				h.ResolvedIP = host
			}
		}
		nodes[nodeID] = h
	}
	s.edgeClientsMu.RUnlock()

	s.outboundMutex.RLock()
	outboundOk := s.outboundConnected
	s.outboundMutex.RUnlock()

	response := map[string]interface{}{
		"outbound_ok": outboundOk,
		"nodes":       nodes,
	}
	respondJSON(w, http.StatusOK, response)
}

// isValidCustomDomain validates a custom domain FQDN.
var customDomainRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*\.[a-z]{2,}$`)

func isValidCustomDomain(domain string) bool {
	domain = strings.ToLower(domain)
	if len(domain) < 3 || len(domain) > 253 {
		return false
	}
	return customDomainRegex.MatchString(domain)
}

// isCustomDomain checks if a host does not belong to configured root domains.
func (s *Server) isCustomDomain(host string) bool {
	for _, d := range s.cfg.Domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return false
		}
	}
	return true
}

// runVanityDomainHook runs the external script with action ("add"/"remove") and domain.
func (s *Server) runVanityDomainHook(action, domain string) {
	if s.cfg.VanityDomainHook == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	slog.Info(fmt.Sprintf("[Server] Executing vanity domain hook: %s %s %s", s.cfg.VanityDomainHook, action, domain))
	cmd := exec.CommandContext(ctx, s.cfg.VanityDomainHook, action, domain)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Info(fmt.Sprintf("[Server] Vanity domain hook error running %s for %s: %v. Output: %s", action, domain, err, string(output)))
	} else {
		slog.Info(fmt.Sprintf("[Server] Vanity domain hook ran successfully for %s %s", action, domain))
	}
}

// checkExpiringReservations scans for expiring or expired subdomain reservations and triggers email notifications.
func (s *Server) checkExpiringReservations() {
	if s.notifications == nil || s.notifications.Sender() == nil {
		return
	}

	now := time.Now()
	// Warning window of 48 hours
	warningThreshold := now.Add(48 * time.Hour)

	expiring, err := s.db.GetExpiringSubdomainReservations(now, warningThreshold)
	if err != nil {
		slog.Info(fmt.Sprintf("[Server] Failed to fetch expiring subdomain reservations: %v", err))
		return
	}

	for _, res := range expiring {
		user, err := s.db.GetUser(res.UserID)
		if err != nil {
			slog.Info(fmt.Sprintf("[Server] Failed to retrieve user %s for expiring reservation: %v", res.UserID, err))
			continue
		}

		if user.NotificationPrefs == "disabled" {
			continue
		}

		lang := user.LanguagePreference
		baseURL := s.getPortalBaseURL(nil)
		portalLink := baseURL + "/portal"

		if res.ExpiresAt != nil && res.ExpiresAt.Before(now) {
			// Stage 2: Already expired and quarantined
			releasedAt := res.ExpiresAt.AddDate(0, 0, s.cfg.SubdomainQuarantineDays)
			releasedStr := releasedAt.Format("2006-01-02 15:04:05 MST")

			body, err := s.renderEmailTemplate(lang, "subdomain_expired.html", map[string]interface{}{
				"Name":       user.FirstName,
				"Subdomain":  res.Subdomain,
				"Domain":     res.Domain,
				"ReleasedAt": releasedStr,
				"PortalLink": portalLink,
			})
			if err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to render subdomain_expired email template: %v", err))
				body = fmt.Sprintf("<p>Hi %s,</p>"+
					"<p>Your subdomain reservation <strong>%s.%s</strong> has expired and entered a %d-day quarantine period.</p>"+
					"<p>If you take no action, it will be released to the public pool on <strong>%s</strong>.</p>"+
					"<p><a href=\"%s\">Go to Portal</a></p>",
					html.EscapeString(user.FirstName), html.EscapeString(res.Subdomain), html.EscapeString(res.Domain),
					s.cfg.SubdomainQuarantineDays, releasedStr, portalLink)
			}

			plainBody := fmt.Sprintf("Hi %s,\n\nYour subdomain reservation %s.%s has expired and entered quarantine. It will be released to the public pool on %s.\n\nGo to the portal to manage it:\n%s",
				user.FirstName, res.Subdomain, res.Domain, releasedStr, portalLink)

			subject := s.GetTranslation(lang, "subdomain_expired_subject")
			if subject == "" {
				subject = fmt.Sprintf("Subdomain Expired & Quarantined: %s.%s", res.Subdomain, res.Domain)
			}

			if err := s.notifications.Sender().Send(user.Email, subject, body, plainBody); err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to send subdomain expired email to %s: %v", user.Email, err))
				continue
			}

			res.ExpiryWarningSent = 2
			if err := s.db.UpdateSubdomainReservation(res); err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to update expiry warning state for reservation %d: %v", res.ID, err))
			}
		} else if res.ExpiresAt != nil {
			// Stage 1: Expiring soon (< 48 hours remaining)
			expiresStr := res.ExpiresAt.Format("2006-01-02 15:04:05 MST")

			body, err := s.renderEmailTemplate(lang, "subdomain_expiring.html", map[string]interface{}{
				"Name":       user.FirstName,
				"Subdomain":  res.Subdomain,
				"Domain":     res.Domain,
				"ExpiresAt":  expiresStr,
				"PortalLink": portalLink,
			})
			if err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to render subdomain_expiring email template: %v", err))
				body = fmt.Sprintf("<p>Hi %s,</p>"+
					"<p>Your subdomain reservation <strong>%s.%s</strong> is expiring soon on <strong>%s</strong>.</p>"+
					"<p>To avoid service disruption, please renew your reservation or request an extension in the Liferay Tunnel Portal.</p>"+
					"<p><a href=\"%s\">Go to Portal</a></p>",
					html.EscapeString(user.FirstName), html.EscapeString(res.Subdomain), html.EscapeString(res.Domain),
					expiresStr, portalLink)
			}

			plainBody := fmt.Sprintf("Hi %s,\n\nYour subdomain reservation %s.%s is expiring soon on %s.\n\nPlease renew or request an extension in the portal:\n%s",
				user.FirstName, res.Subdomain, res.Domain, expiresStr, portalLink)

			subject := s.GetTranslation(lang, "subdomain_expiring_subject")
			if subject == "" {
				subject = fmt.Sprintf("Subdomain Expiring Soon: %s.%s", res.Subdomain, res.Domain)
			}

			if err := s.notifications.Sender().Send(user.Email, subject, body, plainBody); err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to send subdomain expiring email to %s: %v", user.Email, err))
				continue
			}

			res.ExpiryWarningSent = 1
			if err := s.db.UpdateSubdomainReservation(res); err != nil {
				slog.Info(fmt.Sprintf("[Server] Failed to update expiry warning state for reservation %d: %v", res.ID, err))
			}
		}
	}
}

func (s *Server) handleAdminConfigView(w http.ResponseWriter, r *http.Request, actor, role string) {
	allowedRole := strings.ToLower(s.cfg.ViewConfigAllowedRole)
	if allowedRole == "" {
		allowedRole = "owner"
	}

	isAuthorized := false
	switch allowedRole {
	case "owner":
		if role == "owner" {
			isAuthorized = true
		}
	case "admin":
		if role == "owner" || role == "admin" {
			isAuthorized = true
		}
	}

	if !isAuthorized {
		http.Error(w, `{"error":"You do not have the required role to view the server configuration"}`, http.StatusForbidden)
		return
	}

	bodyBytes, err := json.Marshal(s.cfg)
	if err != nil {
		http.Error(w, `{"error":"Failed to copy configuration details"}`, http.StatusInternalServerError)
		return
	}

	var configMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &configMap); err != nil {
		http.Error(w, `{"error":"Failed to parse configuration structure"}`, http.StatusInternalServerError)
		return
	}

	maskConfigMap(configMap)

	respondJSON(w, http.StatusOK, configMap)
}

func maskConfigMap(m map[string]interface{}) {
	for k, v := range m {
		if subMap, ok := v.(map[string]interface{}); ok {
			maskConfigMap(subMap)
		} else if arr, ok := v.([]interface{}); ok {
			for _, item := range arr {
				if itemMap, ok := item.(map[string]interface{}); ok {
					maskConfigMap(itemMap)
				}
			}
		} else {
			kl := strings.ToLower(k)
			cleanKey := strings.ReplaceAll(strings.ReplaceAll(kl, "_", ""), "-", "")
			if strings.Contains(cleanKey, "password") ||
				strings.Contains(cleanKey, "clientsecret") ||
				strings.Contains(cleanKey, "slackurl") ||
				strings.Contains(cleanKey, "teamsurl") ||
				strings.Contains(cleanKey, "edgetoken") ||
				strings.Contains(cleanKey, "tokenhash") {
				if _, ok := v.(string); ok {
					m[k] = "[MASKED]"
				}
			}
		}
	}
}
