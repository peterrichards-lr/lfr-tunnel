package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/jpillora/chisel/server"
)

// PortMapping defines a local-to-remote port mapping request.
type PortMapping struct {
	LocalPort  int    `json:"local_port"`
	NameSuffix string `json:"name_suffix,omitempty"`
}

// TunnelLease represents a single active subdomain tunnel allocation.
type TunnelLease struct {
	SubdomainPrefix string    `json:"subdomain_prefix"`
	FullHost        string    `json:"full_host"`
	SessionToken    string    `json:"session_token"`
	LocalPort       int       `json:"local_port"`
	TargetPort      int       `json:"target_port"`
	CreatedAt       time.Time `json:"created_at"`
}

// Registry manages the mapping between subdomains, dynamic ports, and Chisel credentials.
type Registry struct {
	sync.RWMutex
	leases        map[string]*TunnelLease   // Key: FullHost -> Lease
	sessionLeases map[string][]*TunnelLease // Key: SessionToken -> list of leases
	usedPorts     map[int]bool
	chiselServer  *chserver.Server
}

// NewRegistry initializes and returns a new Registry instance.
func NewRegistry(chiselServer *chserver.Server) *Registry {
	return &Registry{
		leases:        make(map[string]*TunnelLease),
		sessionLeases: make(map[string][]*TunnelLease),
		usedPorts:     make(map[int]bool),
		chiselServer:  chiselServer,
	}
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
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Register allocates ports and subdomains for a client.
func (r *Registry) Register(subdomainPrefix string, ports []PortMapping, domains []string) (string, []string, error) {
	r.Lock()
	defer r.Unlock()

	if subdomainPrefix == "" {
		return "", nil, fmt.Errorf("subdomain prefix cannot be empty")
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
			suffix = fmt.Sprintf("-%d", pm.LocalPort)
		}

		var subdomain string
		if suffix == "" {
			subdomain = subdomainPrefix
		} else {
			subdomain = fmt.Sprintf("%s-%s", subdomainPrefix, suffix)
		}

		for _, domain := range domains {
			fullHost := fmt.Sprintf("%s.%s", subdomain, domain)
			if _, exists := r.leases[fullHost]; exists {
				return "", nil, fmt.Errorf("host %s is already registered", fullHost)
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
			suffix = fmt.Sprintf("-%d", pm.LocalPort)
		}

		var subdomain string
		if suffix == "" {
			subdomain = subdomainPrefix
		} else {
			subdomain = fmt.Sprintf("%s-%s", subdomainPrefix, suffix)
		}

		// Find a free local port
		localPort, err := getFreePort()
		if err != nil {
			return "", nil, fmt.Errorf("failed to allocate free port: %v", err)
		}

		for _, domain := range domains {
			fullHost := fmt.Sprintf("%s.%s", subdomain, domain)
			lease := &TunnelLease{
				SubdomainPrefix: subdomainPrefix,
				FullHost:        fullHost,
				SessionToken:    sessionToken,
				LocalPort:       localPort,
				TargetPort:      pm.LocalPort,
				CreatedAt:       time.Now(),
			}
			r.leases[fullHost] = lease
			allocatedLeases = append(allocatedLeases, lease)
		}

		r.usedPorts[localPort] = true
		allowedRemotes = append(allowedRemotes, fmt.Sprintf("^R:.*:%d$", localPort))
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
		clientRemotes = append(clientRemotes, fmt.Sprintf("R:%d:localhost:%d", lease.LocalPort, lease.TargetPort))
	}

	return sessionToken, clientRemotes, nil
}

// GetBackendPort retrieves the allocated local port for a host.
func (r *Registry) GetBackendPort(host string) (int, bool) {
	r.RLock()
	defer r.RUnlock()

	lease, exists := r.leases[host]
	if !exists {
		return 0, false
	}
	return lease.LocalPort, true
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
		delete(r.leases, lease.FullHost)
		delete(r.usedPorts, lease.LocalPort)
		log.Printf("[Server] Cleaned up lease for host %s (local port %d)", lease.FullHost, lease.LocalPort)
	}

	delete(r.sessionLeases, sessionToken)
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
			log.Printf("[Server] Tunnel session %s appears offline, cleaning up...", token)
			r.CleanLease(token)
		} else {
			conn.Close()
		}
	}
}
