package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	chserver "github.com/jpillora/chisel/server"
)

// PortMapping defines a local-to-remote port mapping request.
type PortMapping struct {
	LocalPort  int    `json:"local_port"`
	NameSuffix string `json:"name_suffix,omitempty"`
}

// TunnelLease represents a single active subdomain tunnel allocation.
type TunnelLease struct {
	UserID          string `json:"user_id"`
	SubdomainPrefix string `json:"subdomain_prefix"`
	FullHost        string `json:"full_host"`
	SessionToken    string `json:"session_token"`
	LocalPort       int    `json:"local_port"`
	TargetPort      int    `json:"target_port"`
	RateLimit       int    `json:"rate_limit"`
	ClientIP        string `json:"client_ip"`
	BasicAuth       string `json:"basic_auth"`
	// Access control carried on the lease rather than read from the database per request
	// (#1329), which also makes it available on an edge, where there is no database to read
	// (#1367).
	//
	// Guarded because a portal edit changes these while requests are reading them. BasicAuth
	// above needs no guard only because it is fixed for the life of the lease.
	accessMu     sync.RWMutex `json:"-"`
	passcode     string
	whitelistIPs string
	accessMode   string
	AddedHeaders map[string]string    `json:"added_headers"`
	Status       string               `json:"status"` // e.g., "up", "maintenance", "down"
	BytesIn      uint64               `json:"bytes_in"`
	BytesOut     uint64               `json:"bytes_out"`
	LastBytesIn  uint64               `json:"-"`
	LastBytesOut uint64               `json:"-"`
	CreatedAt    time.Time            `json:"created_at"`
	NodeID       string               `json:"node_id,omitempty"`
	VisitorIPsMu sync.Mutex           `json:"-"`
	VisitorIPs   map[string]time.Time `json:"-"`
}

// SetAccessControlsForHost applies rules to one lease. Per host rather than per session because
// a reservation is keyed on (subdomain, domain): the same subdomain registered across two
// domains can carry different rules, and applying one domain's answer to both would be wrong in
// whichever direction the loop happened to finish.
func (r *Registry) SetAccessControlsForHost(fullHost, passcode, whitelistIPs, accessMode string) bool {
	r.RLock()
	defer r.RUnlock()
	lease, ok := r.leases[fullHost]
	if !ok {
		return false
	}
	lease.SetAccessControls(passcode, whitelistIPs, accessMode)
	return true
}

// SetAccessControlsForSubdomain applies access-control rules to every live lease for a
// subdomain, so a portal edit takes effect immediately rather than on the client's next
// reconnect (#1329). Returns how many leases were updated, which lets a caller log whether the
// edit reached anything.
func (r *Registry) SetAccessControlsForSubdomain(subdomainPrefix, passcode, whitelistIPs, accessMode string) int {
	r.RLock()
	defer r.RUnlock()
	updated := 0
	for _, lease := range r.leases {
		if lease.SubdomainPrefix == subdomainPrefix {
			lease.SetAccessControls(passcode, whitelistIPs, accessMode)
			updated++
		}
	}
	return updated
}

// SetAccessControlsForSession applies rules to the leases created by one registration. Used at
// registration time, where the session token is what the caller has to hand.
func (r *Registry) SetAccessControlsForSession(sessionToken, passcode, whitelistIPs, accessMode string) {
	r.RLock()
	defer r.RUnlock()
	for _, lease := range r.sessionLeases[sessionToken] {
		lease.SetAccessControls(passcode, whitelistIPs, accessMode)
	}
}

// AccessControls returns the rules this tunnel is currently subject to. accessMode is "and" or
// "or"; empty passcode and whitelist mean the tunnel is open, which is the common case and the
// reason this is worth reading from memory.
func (l *TunnelLease) AccessControls() (passcode, whitelistIPs, accessMode string) {
	l.accessMu.RLock()
	defer l.accessMu.RUnlock()
	mode := l.accessMode
	if mode == "" {
		mode = "or"
	}
	return l.passcode, l.whitelistIPs, mode
}

// SetAccessControls records the rules for this tunnel. Called at registration, and again
// whenever the portal changes them, so an edit takes effect on the next request rather than on
// the next reconnect.
func (l *TunnelLease) SetAccessControls(passcode, whitelistIPs, accessMode string) {
	l.accessMu.Lock()
	defer l.accessMu.Unlock()
	l.passcode = passcode
	l.whitelistIPs = whitelistIPs
	l.accessMode = accessMode
}

var (
	subdomainRegex     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	reservedSubdomains = map[string]bool{
		"www":     true,
		"tunnel":  true,
		"admin":   true,
		"api":     true,
		"mail":    true,
		"blog":    true,
		"git":     true,
		"auth":    true,
		"portal":  true,
		"billing": true,
		"root":    true,
		"assets":  true,
		"static":  true,
		"dev":     true,
		"test":    true,
		"prod":    true,
		"status":  true,
	}
)

func isValidSubdomain(sub string) bool {
	sub = strings.ToLower(sub)
	if len(sub) < 3 || len(sub) > 63 {
		return false
	}
	if reservedSubdomains[sub] {
		return false
	}
	return subdomainRegex.MatchString(sub)
}

// Registry manages the mapping between subdomains, dynamic ports, and Chisel credentials.
type Registry struct {
	sync.RWMutex
	leases         map[string]*TunnelLease   // Key: FullHost -> Lease
	sessionLeases  map[string][]*TunnelLease // Key: SessionToken -> list of leases
	usedPorts      map[int]bool
	chiselServer   *chserver.Server
	OnLeaseCleanup func(*TunnelLease)

	// nodeID identifies the gateway this registry belongs to, and is stamped on every
	// lease it creates. It used to be the constant "control" regardless of which gateway
	// was serving, so the portal reported every tunnel on the control plane even when the
	// client's own TUI correctly showed a regional edge (issue #1167).
	nodeID string

	// cleanedSessions records tokens this gateway has reaped, so a client still
	// heartbeating against a session we destroyed can be told so explicitly rather
	// than left to infer it. Bounded by pruning on insert (issue #1146).
	cleanedSessions map[string]time.Time

	// releasedHosts records hosts whose lease this gateway tore down, so a visitor
	// arriving in the gap can be told the tunnel is briefly unavailable rather than that
	// it never existed. Without it the two are indistinguishable and both answered 502
	// (issue #1251). Bounded by pruning on insert, same as cleanedSessions.
	releasedHosts map[string]time.Time
}

// releasedHostTTL is how long a torn-down host keeps being treated as briefly unavailable
// rather than gone. Long enough to cover a failover and the client's reconnect; short
// enough that a subdomain nobody is coming back to stops promising it will return.
const releasedHostTTL = 5 * time.Minute

// cleanedSessionTTL is how long a reaped session token is remembered. It only has to
// outlast the client's 5s heartbeat by a wide margin; past that the client has either
// re-registered or gone away.
const cleanedSessionTTL = 10 * time.Minute

// NewRegistry initializes and returns a new Registry instance.
func NewRegistry(chiselServer *chserver.Server) *Registry {
	return &Registry{
		leases:          make(map[string]*TunnelLease),
		sessionLeases:   make(map[string][]*TunnelLease),
		usedPorts:       make(map[int]bool),
		chiselServer:    chiselServer,
		cleanedSessions: make(map[string]time.Time),
		releasedHosts:   make(map[string]time.Time),
	}
}

// SetNodeID records which gateway this registry belongs to. Called once at startup;
// unset means the control plane.
func (r *Registry) SetNodeID(id string) {
	r.Lock()
	defer r.Unlock()
	r.nodeID = strings.TrimSpace(id)
}

// localNodeID returns the identity stamped on leases this registry creates, defaulting to
// the control plane so an unconfigured gateway behaves as it always has.
func (r *Registry) localNodeID() string {
	if r.nodeID == "" {
		return "control"
	}
	return r.nodeID
}

// SessionWasCleaned reports whether this gateway reaped the given session token. It is
// deliberately specific to *this* gateway: a token it has never held is not evidence of
// anything, because a client reports its status to both its connected gateway and
// central, and central legitimately holds no lease for an edge-hosted session.
func (r *Registry) SessionWasCleaned(sessionToken string) bool {
	r.RLock()
	defer r.RUnlock()
	cleanedAt, ok := r.cleanedSessions[sessionToken]
	return ok && time.Since(cleanedAt) < cleanedSessionTTL
}

// rememberCleanedSession records a reaped token and prunes expired entries. Caller holds
// the write lock.
func (r *Registry) rememberCleanedSession(sessionToken string) {
	if r.cleanedSessions == nil {
		r.cleanedSessions = make(map[string]time.Time)
	}
	now := time.Now()
	for token, cleanedAt := range r.cleanedSessions {
		if now.Sub(cleanedAt) >= cleanedSessionTTL {
			delete(r.cleanedSessions, token)
		}
	}
	r.cleanedSessions[sessionToken] = now
}

// rememberReleasedHost records that this gateway has just torn down host's lease. Caller
// must hold the write lock, matching rememberCleanedSession.
func (r *Registry) rememberReleasedHost(host string) {
	if r.releasedHosts == nil {
		r.releasedHosts = make(map[string]time.Time)
	}
	now := time.Now()
	for h, releasedAt := range r.releasedHosts {
		if now.Sub(releasedAt) >= releasedHostTTL {
			delete(r.releasedHosts, h)
		}
	}
	r.releasedHosts[host] = now
}

// RecentlyReleased reports whether this gateway tore down a lease for host within
// releasedHostTTL. A true answer means "briefly unavailable" -- a failover, a reconnect, or
// a scheduled node stop -- rather than "no such tunnel".
func (r *Registry) RecentlyReleased(host string) bool {
	r.RLock()
	defer r.RUnlock()
	releasedAt, ok := r.releasedHosts[host]
	return ok && time.Since(releasedAt) < releasedHostTTL
}

// generateSessionToken generates a secure random token.
func generateSessionToken() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// getFreePort allocates a free TCP port dynamically on localhost.
func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close() //nolint:errcheck
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Register allocates ports and subdomains for a client.
func (r *Registry) Register(userID string, subdomainPrefix string, ports []PortMapping, domains []string, rateLimit int, clientIP string, basicAuth string, addedHeaders map[string]string) (string, []string, error) {
	r.Lock()
	defer r.Unlock()

	if subdomainPrefix == "" && (len(domains) != 1 || domains[0] == "") {
		return "", nil, fmt.Errorf("subdomain prefix cannot be empty")
	}
	if subdomainPrefix != "" && !isValidSubdomain(subdomainPrefix) {
		return "", nil, fmt.Errorf("invalid or reserved subdomain prefix: %s", subdomainPrefix)
	}
	if len(ports) == 0 {
		return "", nil, fmt.Errorf("at least one port mapping is required")
	}
	if len(domains) == 0 {
		return "", nil, fmt.Errorf("at least one host domain is required")
	}

	// 1. Check if any of the requested subdomains are already taken
	for idx, pm := range ports {
		suffix := pm.NameSuffix
		if suffix == "" && idx > 0 {
			// No leading dash: the separator is added when this is joined to the
			// subdomain below. Carrying one here produced hosts like sub--8080 (#1154).
			suffix = strconv.Itoa(pm.LocalPort)
		}
		// Deliberately not normalising a leading dash away here. A client from before
		// this fix sends "-8080" and prints sub--8080 for itself; rewriting it server-side
		// would serve sub-8080 while the client advertised sub--8080. Leaving the value
		// alone keeps every client consistent with the host it tells the user about,
		// whatever version it is.

		var subdomain string
		if suffix == "" {
			subdomain = subdomainPrefix
		} else {
			if subdomainPrefix == "" {
				subdomain = strings.TrimPrefix(suffix, "-")
			} else {
				subdomain = fmt.Sprintf("%s-%s", subdomainPrefix, suffix)
			}
		}

		for _, domain := range domains {
			var fullHost string
			if subdomain == "" {
				fullHost = domain
			} else {
				fullHost = fmt.Sprintf("%s.%s", subdomain, domain)
			}
			if existing, exists := r.leases[fullHost]; exists {
				if existing.UserID == userID {
					delete(r.leases, fullHost)
					delete(r.usedPorts, existing.LocalPort)
				} else {
					return "", nil, fmt.Errorf("host %s is already registered", fullHost)
				}
			}
		}
	}

	// 2. Generate credentials
	sessionToken, err := generateSessionToken()
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate session token: %v", err)
	}

	var allowedRemotes []string
	var allocatedLeases []*TunnelLease

	// 3. Allocate ports and create leases
	for idx, pm := range ports {
		suffix := pm.NameSuffix
		if suffix == "" && idx > 0 {
			// No leading dash: the separator is added when this is joined to the
			// subdomain below. Carrying one here produced hosts like sub--8080 (#1154).
			suffix = strconv.Itoa(pm.LocalPort)
		}
		// Deliberately not normalising a leading dash away here. A client from before
		// this fix sends "-8080" and prints sub--8080 for itself; rewriting it server-side
		// would serve sub-8080 while the client advertised sub--8080. Leaving the value
		// alone keeps every client consistent with the host it tells the user about,
		// whatever version it is.

		var subdomain string
		if suffix == "" {
			subdomain = subdomainPrefix
		} else {
			if subdomainPrefix == "" {
				subdomain = strings.TrimPrefix(suffix, "-")
			} else {
				subdomain = fmt.Sprintf("%s-%s", subdomainPrefix, suffix)
			}
		}

		// Find a free local port
		localPort, err := getFreePort()
		if err != nil {
			return "", nil, fmt.Errorf("failed to allocate free port: %v", err)
		}

		for _, domain := range domains {
			var fullHost string
			if subdomain == "" {
				fullHost = domain
			} else {
				fullHost = fmt.Sprintf("%s.%s", subdomain, domain)
			}
			lease := &TunnelLease{
				UserID:          userID,
				SubdomainPrefix: subdomainPrefix,
				FullHost:        fullHost,
				SessionToken:    sessionToken,
				LocalPort:       localPort,
				TargetPort:      pm.LocalPort,
				RateLimit:       rateLimit,
				ClientIP:        clientIP,
				BasicAuth:       basicAuth,
				AddedHeaders:    addedHeaders,
				Status:          "up",
				CreatedAt:       time.Now(),
				NodeID:          r.localNodeID(),
				VisitorIPs:      make(map[string]time.Time),
			}
			r.leases[fullHost] = lease
			allocatedLeases = append(allocatedLeases, lease)
		}

		r.usedPorts[localPort] = true
		allowedRemotes = append(allowedRemotes, fmt.Sprintf("^R:.*:%d(:.*)?$", localPort))
	}

	r.sessionLeases[sessionToken] = allocatedLeases

	// 4. Register credentials in Chisel server
	// username = sessionToken, password = sessionToken
	if err := r.chiselServer.AddUser(sessionToken, sessionToken, allowedRemotes...); err != nil {
		// Rollback on error
		for _, lease := range allocatedLeases {
			delete(r.leases, lease.FullHost)
			delete(r.usedPorts, lease.LocalPort)
		}
		delete(r.sessionLeases, sessionToken)
		return "", nil, fmt.Errorf("failed to register user in chisel server: %v", err)
	}

	// Format remotes list for client CLI (e.g. "R:10001:localhost:8080")
	var clientRemotes []string
	seenLocalPorts := make(map[int]bool)
	for _, lease := range allocatedLeases {
		if seenLocalPorts[lease.LocalPort] {
			continue
		}
		seenLocalPorts[lease.LocalPort] = true
		clientRemotes = append(clientRemotes, fmt.Sprintf("R:127.0.0.1:%d:localhost:%d", lease.LocalPort, lease.TargetPort))
	}

	return sessionToken, clientRemotes, nil
}

// GetLease retrieves the allocated lease for a host.
func (r *Registry) GetLease(host string) (*TunnelLease, bool) {
	r.RLock()
	defer r.RUnlock()

	lease, exists := r.leases[host]
	if !exists {
		return nil, false
	}
	return lease, true
}

// UpdateLeaseHeaders dynamically overrides the AddedHeaders of an active host.
func (r *Registry) UpdateLeaseHeaders(fullHost string, newHeaders map[string]string) error {
	r.Lock()
	defer r.Unlock()

	lease, ok := r.leases[fullHost]
	if !ok {
		return fmt.Errorf("active tunnel host %q not found", fullHost)
	}

	lease.AddedHeaders = newHeaders
	return nil
}

// UpdateLeaseRateLimit dynamically overrides the requests rate limit of an active host.
func (r *Registry) UpdateLeaseRateLimit(fullHost string, newLimit int) error {
	r.Lock()
	defer r.Unlock()

	lease, ok := r.leases[fullHost]
	if !ok {
		return fmt.Errorf("active tunnel host %q not found", fullHost)
	}
	lease.RateLimit = newLimit
	return nil
}

// CheckSubdomain checks a subdomain prefix availability and returns availability, reason if unavailable.
func (r *Registry) CheckSubdomain(subdomainPrefix string, domains []string) (bool, string) {
	return r.CheckSubdomainForUser(subdomainPrefix, domains, "")
}

// CheckSubdomainForUser checks a subdomain prefix availability for a specific user ID.
// If an existing lease belongs to the same user, it is considered available for re-registration / failover.
func (r *Registry) CheckSubdomainForUser(subdomainPrefix string, domains []string, userID string) (bool, string) {
	r.RLock()
	defer r.RUnlock()

	if subdomainPrefix == "" {
		return false, "empty subdomain"
	}

	subdomainPrefix = strings.ToLower(strings.TrimSpace(subdomainPrefix))

	if len(subdomainPrefix) < 3 || len(subdomainPrefix) > 63 {
		return false, "length must be between 3 and 63 characters"
	}

	if reservedSubdomains[subdomainPrefix] {
		return false, "reserved subdomain name"
	}

	if !subdomainRegex.MatchString(subdomainPrefix) {
		return false, "invalid characters (only alphanumeric and hyphens allowed)"
	}

	for _, domain := range domains {
		fullHost := fmt.Sprintf("%s.%s", subdomainPrefix, domain)
		if lease, exists := r.leases[fullHost]; exists {
			if userID != "" && lease.UserID == userID {
				continue
			}
			return false, "subdomain is already taken"
		}
	}

	return true, ""
}

// GenerateSuggestions produces a list of alternative subdomains that are currently available.
func (r *Registry) GenerateSuggestions(subdomainPrefix string, domains []string) []string {
	// Clean the prefix
	cleanPrefix := strings.ToLower(strings.TrimSpace(subdomainPrefix))
	// Remove invalid characters to form a base prefix
	var sb strings.Builder
	for _, ch := range cleanPrefix {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
			sb.WriteRune(ch)
		}
	}
	base := sb.String()
	if len(base) < 3 {
		base = "dev-" + base
		if len(base) < 3 {
			base = "dev-tunnel"
		}
	}

	suffixes := []string{"-dev", "-app", "-tunnel", "-se", "-1", "-2", "-3", "-hub", "-node", "-local"}
	var suggestions []string

	for _, suffix := range suffixes {
		candidate := base + suffix
		if len(candidate) > 63 {
			candidate = candidate[:63]
			if strings.HasSuffix(candidate, "-") {
				candidate = candidate[:62]
			}
		}

		available, _ := r.CheckSubdomain(candidate, domains)
		if available {
			suggestions = append(suggestions, candidate)
			if len(suggestions) >= 3 {
				break
			}
		}
	}

	// If we still don't have enough, generate with numeric suffix
	for i := 1; len(suggestions) < 3 && i < 20; i++ {
		candidate := fmt.Sprintf("%s-%d", base, time.Now().UnixNano()%1000+int64(i))
		available, _ := r.CheckSubdomain(candidate, domains)
		if available {
			suggestions = append(suggestions, candidate)
		}
	}

	return suggestions
}

// CleanLease removes a lease and its Chisel user registration.
func (r *Registry) CleanLease(sessionToken string) {
	r.Lock()
	defer r.Unlock()

	leases, exists := r.sessionLeases[sessionToken]
	if !exists {
		return
	}

	for _, lease := range leases {
		if r.OnLeaseCleanup != nil {
			r.OnLeaseCleanup(lease)
		}

		// r.leases is keyed by host, so a re-registration for the same subdomain
		// replaces the entry rather than adding one. Deleting unconditionally therefore
		// tore down whichever session currently owns the host, which after a failover is
		// a different, live one -- the routing entry vanished while its chisel tunnel
		// stayed up, so the public URL returned 502 indefinitely and nothing swept it
		// afterwards because only this token's sessionLeases entry was removed
		// (issue #1147). Any client re-registering for the same host inside the sweep's
		// 30s grace period hits this, which failover makes routine.
		if current, ok := r.leases[lease.FullHost]; ok && current == lease {
			delete(r.leases, lease.FullHost)
			// Only on the branch that actually removed the routing entry. When the guard
			// above fails, a different live session owns the host, so nothing was
			// released and a visitor should be served normally rather than told the
			// tunnel is briefly away (#1251).
			r.rememberReleasedHost(lease.FullHost)
		}
		// usedPorts is keyed by the port, which is unique per lease, so it needs no guard.
		delete(r.usedPorts, lease.LocalPort)
		slog.Info(fmt.Sprintf("[Server] Cleaned up lease for host %s (local port %d)", lease.FullHost, lease.LocalPort))
	}

	delete(r.sessionLeases, sessionToken)
	r.rememberCleanedSession(sessionToken)
	r.chiselServer.DeleteUser(sessionToken)
}

// StartCleanupRoutine periodically checks leases and garbage-collects offline tunnels.
func (r *Registry) StartCleanupRoutine(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		for range ticker.C {
			r.cleanupOrphanLeases()
		}
	}()
}

// cleanupOrphanLeases performs the actual check and cleanup of dead leases.
func (r *Registry) cleanupOrphanLeases() {
	r.RLock()
	// Copy session tokens to check outside read lock to prevent deadlocks
	var tokens []string
	for token := range r.sessionLeases {
		tokens = append(tokens, token)
	}
	r.RUnlock()

	for _, token := range tokens {
		r.RLock()
		leases, exists := r.sessionLeases[token]
		if !exists || len(leases) == 0 {
			r.RUnlock()
			continue
		}
		// Check the first lease's creation time and port
		firstLease := leases[0]
		createdAt := firstLease.CreatedAt
		localPort := firstLease.LocalPort
		r.RUnlock()

		// Allow grace period of 30 seconds for initial connection
		if time.Since(createdAt) < 30*time.Second {
			continue
		}

		// Dial localhost:localPort to see if Chisel listener is active
		address := fmt.Sprintf("127.0.0.1:%d", localPort)
		conn, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err != nil {
			// TCP port is not listening. The tunnel has disconnected!
			slog.Info(fmt.Sprintf("[Server] Tunnel session %s appears offline, cleaning up...", token))
			r.CleanLease(token)
		} else {
			conn.Close() //nolint:errcheck
		}
	}
}

// ListLeases returns a snapshot of all active leases.
func (r *Registry) ListLeases() []*TunnelLease {
	r.RLock()
	defer r.RUnlock()

	var snapshot []*TunnelLease
	for _, lease := range r.leases {
		// Copy fields manually to safely load atomic fields for JSON serialization and avoid copying sync.Mutex
		lCopy := &TunnelLease{
			UserID:          lease.UserID,
			SubdomainPrefix: lease.SubdomainPrefix,
			FullHost:        lease.FullHost,
			SessionToken:    lease.SessionToken,
			LocalPort:       lease.LocalPort,
			TargetPort:      lease.TargetPort,
			RateLimit:       lease.RateLimit,
			ClientIP:        lease.ClientIP,
			BasicAuth:       lease.BasicAuth,
			AddedHeaders:    lease.AddedHeaders,
			Status:          lease.Status,
			BytesIn:         atomic.LoadUint64(&lease.BytesIn),
			BytesOut:        atomic.LoadUint64(&lease.BytesOut),
			CreatedAt:       lease.CreatedAt,
			NodeID:          lease.NodeID,
		}
		snapshot = append(snapshot, lCopy)
	}
	return snapshot
}

// KickLease terminates an active lease by its subdomain prefix and cleans up resources.
// It returns true if a lease was found and kicked, false otherwise.
func (r *Registry) KickLease(subdomainPrefix string) bool {
	r.RLock()
	var targetSessionToken string
	// Find the session token associated with this subdomain prefix.
	// We search across all full hosts because a single prefix might be registered on multiple domains,
	// but they share the same session token.
	for fullHost, lease := range r.leases {
		if strings.HasPrefix(fullHost, subdomainPrefix+".") {
			targetSessionToken = lease.SessionToken
			break
		}
	}
	r.RUnlock()

	if targetSessionToken != "" {
		r.CleanLease(targetSessionToken)
		return true
	}
	return false
}

// UpdateLeaseStatus updates the status string for all leases associated with a session token.
func (r *Registry) UpdateLeaseStatus(sessionToken, status string) bool {
	r.Lock()
	defer r.Unlock()

	leases, exists := r.sessionLeases[sessionToken]
	if !exists {
		return false
	}

	for _, lease := range leases {
		lease.Status = status
	}
	return true
}

// GetSessionLeases returns a copy of all leases associated with a session token.
func (r *Registry) GetSessionLeases(sessionToken string) []*TunnelLease {
	r.RLock()
	defer r.RUnlock()

	leases, exists := r.sessionLeases[sessionToken]
	if !exists {
		return nil
	}
	res := make([]*TunnelLease, len(leases))
	copy(res, leases)
	return res
}

// GetActiveVisitorIPs returns a slice of active visitor IPs that have made a request within the timeout.
// It also prunes expired entries to prevent memory leaks.
func (l *TunnelLease) GetActiveVisitorIPs(timeout time.Duration) []string {
	l.VisitorIPsMu.Lock()
	defer l.VisitorIPsMu.Unlock()

	var active []string
	now := time.Now()
	for ip, lastActive := range l.VisitorIPs {
		if now.Sub(lastActive) <= timeout {
			active = append(active, ip)
		} else {
			delete(l.VisitorIPs, ip)
		}
	}
	return active
}

// applyAccessControlsToLeases stamps per-domain rules onto the leases a registration produced.
//
// Shared by the control plane and by an edge, which resolve the values differently -- one reads
// its own database, the other is told by central -- but apply them identically. Keeping the
// host construction in one place matters because it has to match Register's own, or the rules
// land on nothing and the tunnel is served unprotected.
func applyAccessControlsToLeases(r *Registry, subdomainPrefix string, domains []string, accessByDomain map[string][3]string) {
	if r == nil || len(accessByDomain) == 0 {
		return
	}
	for _, d := range domains {
		ac, ok := accessByDomain[d]
		if !ok {
			continue
		}
		fullHost := d
		if subdomainPrefix != "" {
			fullHost = subdomainPrefix + "." + d
		}
		if !r.SetAccessControlsForHost(fullHost, ac[0], ac[1], ac[2]) {
			slog.Info(fmt.Sprintf("[Server] Access controls for %s matched no live lease", fullHost))
		}
	}
}

// reservationAccessControls reads the stored rules for a reservation, so a caller applying them
// to live leases uses exactly what the database now holds rather than what the request asked
// for -- the two differ whenever the service normalises or rejects part of the input.
func (s *Server) reservationAccessControls(subdomain, domain string) ([3]string, bool) {
	if s.db == nil {
		return [3]string{}, false
	}
	res, err := s.db.GetSubdomainReservationByName(subdomain, domain)
	if err != nil || res == nil {
		return [3]string{}, false
	}
	return [3]string{res.Passcode, res.WhitelistIPs, res.AccessMode}, true
}
