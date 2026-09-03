package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"lfr-tunnel/pkg/config"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	chclient "github.com/jpillora/chisel/client"
	"gopkg.in/yaml.v3"
)

// PortMapping matches the server DTO for port allocations.
type PortMapping struct {
	LocalPort  int    `json:"local_port"`
	NameSuffix string `json:"name_suffix,omitempty"`
}

// RegionProbe is one measurement of one advertised region, reported at registration (#1151).
//
// The client already probes every region to choose one and then discards all but the winner.
// Those discarded numbers are the best available answer to "do we need an edge here" -- better
// than geography, because they carry the VPN, tethering and routing penalties a country code
// cannot show.
type RegionProbe struct {
	Region string `json:"region"`
	// RTTMs is the round trip in milliseconds. Omitted when the region did not answer, which
	// Unreachable then states -- an edge nobody can reach is a placement fact, not missing data.
	RTTMs       int  `json:"rtt_ms,omitempty"`
	Unreachable bool `json:"unreachable,omitempty"`
}

// regionProbes holds this process's startup measurement.
//
// Package-level because RegisterTunnel already takes eleven positional parameters and a twelfth
// would make every call site worse to read. It models a genuine singleton -- the probe runs once
// per process, at startup, before any registration -- and the mutex is there because
// registration retries happen on other goroutines.
var (
	regionProbesMu sync.Mutex
	regionProbes   []RegionProbe
)

// RecordRegionProbes stores the startup probe results for the next registration to report.
// Passing nil clears them, which is how the opt-out is expressed: nothing to send.
func RecordRegionProbes(probes []RegionProbe) {
	regionProbesMu.Lock()
	defer regionProbesMu.Unlock()
	regionProbes = probes
}

// reportableRegionProbes returns the probes to attach to a registration.
func reportableRegionProbes() []RegionProbe {
	regionProbesMu.Lock()
	defer regionProbesMu.Unlock()
	return regionProbes
}

// RegisterRequest matches the server's registration payload format.
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
	RegionProbes    []RegionProbe     `json:"region_probes,omitempty"`
}

// RegisterResponse matches the server DTO for response.
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
	// The serving gateway's own scheduled downtime, when it has one (#1275). Zero means
	// this gateway is not scheduled to stop -- which is always true of the control plane.
	// PolicyConsent is this user's standing against the current privacy/cookie policy
	// version (#1707). It comes back on the registration exchange, which is the only
	// AUTHENTICATED thing the client does before a tunnel exists -- /api/version carries
	// enforce_policy_consent but is unauthenticated, and this is per-user.
	//
	// Nil from a gateway that predates this, which reads as "nothing to say".
	PolicyConsent      *PolicyConsentState `json:"policy_consent,omitempty"`
	NodeStopsInSeconds int                 `json:"node_stops_in_seconds,omitempty"`
	NodeStopTime       string              `json:"node_stop_time,omitempty"`
	NodeTimezone       string              `json:"node_timezone,omitempty"`
}

// PinnedShutdownNotice returns the warning a client pinned to a specific gateway should be
// shown at startup, or "" when there is nothing to say (#1275).
//
// A client started with -server never fails over. That is deliberate -- the flag means "use
// this and nothing else", and a user wanting regional preference with resilience has -region
// -- but it was never disclosed, so a pinned client simply dropped when its gateway hit a
// scheduled stop and stayed down for the whole window. Measured at 24m36s against edge-in.
//
// Silent unless all three conditions hold: the client is pinned, the gateway is scheduled,
// and the stop is still ahead. An unpinned client needs no warning because it will fail over.
func PinnedShutdownNotice(resp *RegisterResponse, isExplicitServer bool) string {
	if resp == nil || !isExplicitServer || resp.NodeStopsInSeconds <= 0 {
		return ""
	}

	when := resp.NodeStopTime
	if resp.NodeTimezone != "" {
		when = fmt.Sprintf("%s %s", resp.NodeStopTime, resp.NodeTimezone)
	}
	return fmt.Sprintf(
		"This gateway is scheduled to stop at %s, in %s. Because it was named with -server, "+
			"this client will not move to another gateway -- the tunnel will drop and stay down "+
			"until the gateway returns. Use -region instead of -server to allow failover.",
		when, formatTimeUntil(resp.NodeStopsInSeconds))
}

// PinnedRoutingNotice warns that naming a gateway has opted this client out of region
// selection and failover (#1691).
//
// PinnedShutdownNotice above covers the case where the pinned gateway is about to stop. This
// covers the cost that applies even when nothing stops: a user in the US who names the control
// plane stays on the control plane, however close an edge is, for the life of the tunnel.
//
// Both behaviours are intended -- naming a gateway should use that gateway (#1275). What was
// missing is that the three documented ways to supply one are not equivalent: -server and the
// environment variables pin, while server_url in the config file does not, and nothing said so
// at the point of choosing.
//
// Silent when only one region exists: there is nothing to elect between, so the advice would be
// noise on a single-gateway deployment.
func PinnedRoutingNotice(isExplicitServer bool, regionCount int) string {
	if !isExplicitServer || regionCount < 2 {
		return ""
	}
	return fmt.Sprintf(
		"This client is pinned to the gateway you named, so it will not pick the closest of the "+
			"%d available and will not fail over if that gateway goes away. To keep both, remove "+
			"-server (and LFT_SERVER_URL / LFT_CLIENT_SERVER / LFT_SERVER) and put "+
			"`server_url: \"<url>\"` in your client config file instead -- that supplies a "+
			"starting point without pinning. Use -region <name> to prefer one region while "+
			"keeping failover.",
		regionCount)
}

// formatTimeUntil renders a span as coarse human units, e.g. "6h 12m".
//
// Distinct from tui.go's formatCountdown, which renders minutes and seconds because it ticks
// down a five-minute warning. The same treatment here would print "372m 30s" for a stop
// six hours away.
// PolicyConsentState mirrors the server's ConsentState (pkg/server/policy_consent.go).
// Like RegisterResponse itself, it is duplicated by hand rather than shared: there is no
// package both sides import, so a field added on one side must be added on the other.
type PolicyConsentState struct {
	Required         bool   `json:"required"`
	DocumentID       string `json:"document_id,omitempty"`
	Version          string `json:"version,omitempty"`
	Phase            string `json:"phase,omitempty"`
	Deadline         string `json:"deadline,omitempty"`
	SecondsRemaining int64  `json:"seconds_remaining,omitempty"`
	PolicyURL        string `json:"policy_url,omitempty"`
	CookieURL        string `json:"cookie_url,omitempty"`
	PortalURL        string `json:"portal_url,omitempty"`
	AcceptedAt       string `json:"accepted_at,omitempty"`
}

// Consent phases, matching the server's strings.
const (
	ConsentPhaseGrace   = "grace"
	ConsentPhaseWarning = "warning"
	ConsentPhaseExpired = "expired"
)

// PolicyConsentNotice renders the startup warning for an outstanding policy acceptance,
// or "" when there is nothing to say.
//
// This is load-bearing rather than polish. Plenty of people here register once and then
// live entirely on the command line; if clients stop at the deadline and the only warning
// was a portal banner, the first they would learn of it is a tunnel refusing to start --
// most likely at the worst possible moment, since that is when people reach for it.
//
// Silent during the grace phase on purpose. A message on every tunnel start for two weeks
// is noise, and noise is exactly what stops the message that matters from being read.
// Same shape as PinnedShutdownNotice above, so the call site stays a two-line if.
func PolicyConsentNotice(resp *RegisterResponse) string {
	if resp == nil {
		return ""
	}
	return policyConsentNoticeFrom(resp.PolicyConsent)
}

func policyConsentNoticeFrom(c *PolicyConsentState) string {
	if c == nil || !c.Required {
		return ""
	}
	where := c.PortalURL
	if where == "" {
		where = "the Liferay Tunnel portal"
	}
	switch c.Phase {
	case ConsentPhaseWarning:
		return fmt.Sprintf(
			"The Privacy Policy and Cookie Disclosure have changed. Accept the update at %s within %s, or new tunnels will stop being accepted.",
			where, formatConsentRemaining(c.SecondsRemaining),
		)
	case ConsentPhaseExpired:
		return fmt.Sprintf(
			"Your acceptance of the updated Privacy Policy and Cookie Disclosure is overdue. Accept it at %s -- new tunnels are being refused until you do.",
			where,
		)
	default:
		return ""
	}
}

// formatConsentRemaining renders days and hours, or hours and minutes inside the last
// day. Coarse on purpose: a deadline days away rendered to the second reads as machine
// output rather than as something to act on.
func formatConsentRemaining(seconds int64) string {
	if seconds <= 0 {
		return "no time"
	}
	d := time.Duration(seconds) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		if hours > 0 {
			return fmt.Sprintf("%dd %dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func formatTimeUntil(seconds int) string {
	d := time.Duration(seconds) * time.Second
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%ds", seconds)
}

// NodeShutdownWarning represents a shutdown notification message from a tunnel gateway.
type NodeShutdownWarning struct {
	Type             string `json:"type"`
	NodeID           string `json:"node_id,omitempty"`
	Action           string `json:"action,omitempty"`
	SecondsRemaining int    `json:"seconds_remaining,omitempty"`
	ShutdownAt       int64  `json:"shutdown_at,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

// ParseNodeShutdownWarning attempts to parse a raw JSON frame into a NodeShutdownWarning.
func ParseNodeShutdownWarning(data []byte) (*NodeShutdownWarning, bool) {
	var msg NodeShutdownWarning
	if err := json.Unmarshal(data, &msg); err == nil && msg.Type == "node_shutdown_warning" {
		return &msg, true
	}
	return nil, false
}

// ClearRegionCacheFile purges the local 24h region cache file to force re-probing on failover.
func ClearRegionCacheFile() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".lfr-tunnel", "region_cache.json")
	if _, err := os.Stat(path); err == nil {
		return os.Remove(path)
	}
	return nil
}

type RegistrationError struct {
	StatusCode int
	Message    string
	PortalURL  string
	// PolicyConsent is set when the gateway refused because this user's policy acceptance
	// is overdue (#1707). Without it a consent 403 is indistinguishable from the
	// reservation/quota 403 the client already knows about, and would be reported with
	// advice that sends the user looking for a problem they do not have.
	PolicyConsent *PolicyConsentState
}

func (e *RegistrationError) Error() string {
	if e.PortalURL != "" {
		return fmt.Sprintf("gateway error (%d): %s (Portal: %s)", e.StatusCode, e.Message, e.PortalURL)
	}
	return fmt.Sprintf("gateway error (%d): %s", e.StatusCode, e.Message)
}

// DetectWorkspacePorts walks the filesystem looking for client-extension.yaml files
// and extracts active developer ports.
func DetectWorkspacePorts(rootDir string) ([]PortMapping, error) {
	var mappings []PortMapping
	seenPorts := make(map[int]bool)

	// Always default to including local Liferay instance port 8080 as primary
	mappings = append(mappings, PortMapping{LocalPort: 8080})
	seenPorts[8080] = true

	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip directory read errors
		}

		if d.IsDir() {
			// Skip common large development / build / configuration directories
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "build" ||
				name == "dist" || name == ".gradle" || name == "platform" ||
				name == "configs" || name == "osgi" {
				return filepath.SkipDir
			}
			return nil
		}

		// Look for Liferay Client Extension configurations
		if d.Name() == "client-extension.yaml" || d.Name() == "client-extension.yml" {
			file, err := os.Open(path)
			if err != nil {
				return nil // Skip files we cannot read
			}
			defer file.Close() //nolint:errcheck

			var data map[string]interface{}
			dec := yaml.NewDecoder(file)
			if err := dec.Decode(&data); err == nil {
				for extKey, extVal := range data {
					m, ok := extVal.(map[string]interface{})
					if !ok {
						continue
					}

					portVal, exists := m["port"]
					if !exists {
						continue
					}

					var port int
					switch v := portVal.(type) {
					case int:
						port = v
					case float64:
						port = int(v)
					case string:
						port, _ = strconv.Atoi(v)
					}

					if port > 0 && !seenPorts[port] {
						seenPorts[port] = true
						// Use the client-extension key as the subdomain suffix
						mappings = append(mappings, PortMapping{
							LocalPort:  port,
							NameSuffix: extKey,
						})
						slog.Info(fmt.Sprintf("[Client] Detected Liferay Client Extension port %d from: %s", port, path))
					}
				}
			}
		}
		return nil
	})

	return mappings, err
}

// registerTimeout bounds the registration handshake. Generous enough for a slow link to a
// cold gateway, short enough that a user learns something is wrong long before they
// conclude the client is hung.
const registerTimeout = 20 * time.Second

// registerClient bounds the registration POST. http.DefaultClient has no timeout, and a
// gateway whose host is powered off drops packets rather than refusing the connection, so
// this call used to sit in the OS TCP retry cycle for over a minute. Nothing is printed in
// that window and the TUI has not started yet -- it only runs once registration has
// succeeded (cmd/lfr-tunnel/main.go) -- so the client looked hung with no output at all
// (#1257). Edge nodes here power off nightly, which makes an unreachable gateway a routine
// state rather than an exceptional one.
//
// Same reasoning as healthReportClient in interceptor.go; registration was the one call in
// pkg/ and cmd/ that never got it.
var registerClient = &http.Client{Timeout: registerTimeout}

// RegisterTunnel performs the handshake with the server's registration endpoint.
func RegisterTunnel(serverURL string, authToken string, subdomain string, customDomain string, ports []PortMapping, rateLimit int, basicAuth string, addedHeaders map[string]string, clientOS string, passcode string, whitelistIPs string) (*RegisterResponse, error) {
	// Normalize server URL
	if !strings.HasPrefix(serverURL, "http") {
		serverURL = "http://" + serverURL
	}
	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL: %v", err)
	}

	registerURL := fmt.Sprintf("%s://%s/api/register", parsedURL.Scheme, parsedURL.Host)

	payload, err := json.Marshal(RegisterRequest{
		SubdomainPrefix: subdomain,
		CustomDomain:    customDomain,
		Ports:           ports,
		AuthToken:       authToken,
		RateLimit:       rateLimit,
		BasicAuth:       basicAuth,
		AddedHeaders:    addedHeaders,
		ClientVersion:   config.Version,
		ClientOS:        clientOS,
		Passcode:        passcode,
		WhitelistIPs:    whitelistIPs,
		RegionProbes:    reportableRegionProbes(),
	})
	if err != nil {
		return nil, err
	}

	// Say which gateway is being contacted before blocking on it, so a slow or failing
	// registration reports what it is trying rather than printing nothing at all.
	slog.Info(fmt.Sprintf("[Client] Registering with gateway %s...", parsedURL.Host))

	resp, err := registerClient.Post(registerURL, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		// The "registration request failed" prefix is load-bearing: attemptRegistration in
		// cmd/lfr-tunnel classifies this as a retry-elsewhere failure by matching on that
		// substring, so a different region gets tried. Keep it when adding detail.
		return nil, fmt.Errorf("registration request failed (%s): %v", parsedURL.Host, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var regResp RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		return nil, fmt.Errorf("failed to decode server response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		if regResp.Error != "" {
			return nil, &RegistrationError{
				StatusCode:    resp.StatusCode,
				Message:       regResp.Error,
				PortalURL:     regResp.PortalURL,
				PolicyConsent: regResp.PolicyConsent,
			}
		}
		return nil, &RegistrationError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("gateway returned status %d", resp.StatusCode),
		}
	}

	if regResp.Status != "success" {
		return nil, fmt.Errorf("registration status: %s, error: %s", regResp.Status, regResp.Error)
	}

	return &regResp, nil
}

// RunClient runs the embedded Chisel client.
func RunClient(ctx context.Context, serverURL string, token string, remotes []string, publicURLs []string, engine *InterceptorEngine) error {
	// Intercept logger to monitor connection state
	cleanup, err := redirectChiselLogger(engine)
	if err != nil {
		return err
	}
	defer cleanup()

	// 1. Ensure server URL starts with http/https
	if !strings.HasPrefix(serverURL, "http") {
		serverURL = "http://" + serverURL
	}

	// 2. Setup Chisel client config
	chiselCfg := &chclient.Config{
		Server:           serverURL + "/tunnel",
		Auth:             fmt.Sprintf("%s:%s", token, token),
		Remotes:          remotes,
		MaxRetryInterval: 3 * time.Second,
		MaxRetryCount:    3, // Allow client to return control on edge shutdown for dynamic region failover
	}

	// 3. Initialize Chisel client
	c, err := chclient.NewClient(chiselCfg)
	if err != nil {
		return fmt.Errorf("failed to initialize chisel client: %v", err)
	}

	// Log client status
	slog.Info(fmt.Sprintf("[Client] Establised lease. Connecting tunnels to %s...", serverURL))
	for _, remote := range remotes {
		slog.Info(fmt.Sprintf("[Client] Forwarding remote port: %s", remote))
	}

	// Start background latency tracker
	go func() {
		parsed, err := url.Parse(serverURL)
		if err != nil {
			return
		}
		host := parsed.Host
		if !strings.Contains(host, ":") {
			if parsed.Scheme == "https" {
				host = host + ":443"
			} else {
				host = host + ":80"
			}
		}

		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				engine.mu.RLock()
				state := engine.ConnState
				engine.mu.RUnlock()

				if state == "connected" {
					t0 := time.Now()
					conn, err := net.DialTimeout("tcp", host, 3*time.Second)
					if err == nil {
						rtt := time.Since(t0).Milliseconds()
						_ = conn.Close() //nolint:errcheck

						engine.mu.Lock()
						engine.LatencyLast = rtt
						engine.LatencyHistory = append(engine.LatencyHistory, rtt)
						if len(engine.LatencyHistory) > 60 {
							engine.LatencyHistory = engine.LatencyHistory[1:]
						}
						engine.mu.Unlock()
					}
				}
			}
		}
	}()

	// 4. Start the client
	if err := c.Start(ctx); err != nil {
		return fmt.Errorf("chisel client error: %v", err)
	}

	// Print a clean, auto-clickable URL block, but only once the tunnel is genuinely up.
	// This used to fire on a 500ms timer guarded by ctx.Err() == nil, which asks whether
	// the context was cancelled -- not whether anything connected. Start() returns as soon
	// as the chisel client has been started, so every failed attempt announced success:
	// during a node's scheduled stop the log filled with "fully online" while the client
	// was attached to nothing and the TUI correctly showed OFFLINE (#1258). The log is the
	// artefact someone greps during an incident, so it is the one that must not lie.
	go func() {
		if !waitForConnected(ctx, engine, connectAnnounceTimeout) {
			return
		}
		slog.Info("[Client] ========================================================")
		slog.Info("[Client] Tunnel is active and fully online!")
		slog.Info("[Client] You can access your local environment at:")
		for _, u := range publicURLs {
			slog.Info(fmt.Sprintf("  %s", u))
		}
		slog.Info("[Client] ========================================================")
	}()

	// 5. Block until context done or wait error
	return c.Wait()
}

// connectAnnounceTimeout bounds how long the "fully online" announcement waits for the
// tunnel before giving up. Chisel reports "Connected" within about a second on a healthy
// link; this is generous enough to absorb a slow one without parking a goroutine for the
// life of the process on a connection that is never going to succeed.
const connectAnnounceTimeout = 30 * time.Second

// connectPollInterval is how often waitForConnected re-reads the engine's state. The state
// is set from a log line rather than pushed, so there is nothing to select on.
const connectPollInterval = 100 * time.Millisecond

// waitForConnected reports whether the tunnel actually came up, blocking until it does,
// until ctx is cancelled, or until timeout elapses.
//
// "Connected" means the engine's ConnState, which logParserWriter.parseMessage sets from
// chisel's own "Connected (Latency ...)" line -- not anything inferred from Start()
// returning, which happens whether or not a connection follows.
//
// Deliberately silent on the failure paths. Announcing a failure is #1257's job; doing it
// here would print on every reconnect attempt, which is the same mistake as #1258 in the
// opposite direction.
func waitForConnected(ctx context.Context, engine *InterceptorEngine, timeout time.Duration) bool {
	ticker := time.NewTicker(connectPollInterval)
	defer ticker.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		engine.mu.RLock()
		connected := engine.ConnState == "connected"
		engine.mu.RUnlock()
		if connected {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

var latencyRegex = regexp.MustCompile(`Latency\s+([^)]+)`)

type logParserWriter struct {
	original io.Writer
	engine   *InterceptorEngine
}

func (w *logParserWriter) Write(p []byte) (n int, err error) {
	msg := string(p)
	w.parseMessage(msg)
	return w.original.Write(p)
}

func (w *logParserWriter) parseMessage(msg string) {
	w.engine.mu.Lock()
	defer w.engine.mu.Unlock()

	// Track transitions
	oldState := w.engine.ConnState

	if strings.Contains(msg, "Connecting to") {
		w.engine.ConnState = "connecting"
	} else if strings.Contains(msg, "Connected (Latency") {
		w.engine.ConnState = "connected"
		w.engine.UptimeStart = time.Now()
		w.engine.AuthValid = true
		w.engine.AuthErrorMessage = ""
		matches := latencyRegex.FindStringSubmatch(msg)
		if len(matches) > 1 {
			durStr := matches[1]
			dur, err := time.ParseDuration(durStr)
			if err == nil {
				ms := dur.Milliseconds()
				w.engine.LatencyLast = ms
				w.engine.LatencyHistory = append(w.engine.LatencyHistory, ms)
				if len(w.engine.LatencyHistory) > 60 {
					w.engine.LatencyHistory = w.engine.LatencyHistory[1:]
				}
			}
		}
	} else if strings.Contains(msg, "Disconnected") {
		w.engine.ConnState = "disconnected"
		w.engine.UptimeStart = time.Time{}
		if oldState == "connected" {
			w.engine.ReconnectCount++
		}
	} else if strings.Contains(msg, "Retrying in") {
		w.engine.ConnState = "reconnecting"
	} else if strings.Contains(msg, "Authentication failed") {
		w.engine.AuthValid = false
		w.engine.AuthErrorMessage = "Authentication failed"
		w.engine.ConnState = "disconnected"
	}
}

func redirectChiselLogger(engine *InterceptorEngine) (func(), error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create pipe for stderr redirect: %w", err)
	}

	originalStderr := os.Stderr
	os.Stderr = w

	// Start a background goroutine to parse messages from pipe and write to originalStderr
	go func() {
		parser := &logParserWriter{
			original: originalStderr,
			engine:   engine,
		}
		_, _ = io.Copy(parser, r) //nolint:errcheck
	}()

	cleanup := func() {
		os.Stderr = originalStderr
		_ = w.Close() //nolint:errcheck
		_ = r.Close() //nolint:errcheck
	}

	return cleanup, nil
}

// IsLiferayWorkspace checks if a directory contains structural signals of a Liferay workspace
// (such as client-extensions directory, gradlew, or gradle.properties).
func IsLiferayWorkspace(dir string) bool {
	// Check for client-extensions folder
	if fi, err := os.Stat(filepath.Join(dir, "client-extensions")); err == nil && fi.IsDir() {
		return true
	}
	// Check for gradlew file
	if _, err := os.Stat(filepath.Join(dir, "gradlew")); err == nil {
		return true
	}
	// Check for gradle.properties file
	if _, err := os.Stat(filepath.Join(dir, "gradle.properties")); err == nil {
		return true
	}
	return false
}

// ProbeLocalPorts scans the specified localhost ports and returns the ports that are active.
func ProbeLocalPorts(ports []int) []int {
	var active []int
	for _, port := range ports {
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		conn, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err == nil {
			active = append(active, port)
			_ = conn.Close() //nolint:errcheck
		}
	}
	return active
}
