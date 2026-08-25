package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lfr-tunnel/pkg/config"

	"golang.org/x/time/rate"
)

// RequestRecord stores the captured HTTP traffic for the inspector.
type RequestRecord struct {
	ID          string            `json:"id"`
	Time        time.Time         `json:"time"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	ReqHeaders  map[string]string `json:"req_headers"`
	ReqBody     string            `json:"req_body"`
	Status      int               `json:"status"`
	RespHeaders map[string]string `json:"resp_headers"`
	RespBody    string            `json:"resp_body"`
	DurationMs  int64             `json:"duration_ms"`
	TargetPort  int               `json:"target_port"`
}

// InterceptorEngine manages the traffic routing, modification, and capture.
type InterceptorEngine struct {
	mu                 sync.RWMutex
	MaintenanceMode    bool
	Status             string
	AddedHeaders       map[string]string
	History            []*RequestRecord
	MaxHistory         int
	TargetHost         string
	PreserveHost       bool
	InsecureSkipVerify bool

	// Connection status and statistics
	ConnState         string // "disconnected", "connecting", "connected", "reconnecting"
	UptimeStart       time.Time
	ReconnectCount    int
	LatencyLast       int64   // ms
	LatencyHistory    []int64 // to calculate 5m rolling average
	AuthValid         bool
	AuthErrorMessage  string
	SubdomainReq      string
	SubdomainAss      string
	ClientSubdomain   string
	SubdomainLeased   bool
	SubdomainConflict bool
	DestPort          int

	// Traffic stats
	RequestsTotal     int64
	BytesIn           int64
	BytesOut          int64
	ActiveConnections int32

	// Access Control & Server Settings
	Token              string
	ServerURL          string
	SelectedRegion     string
	Passcode           string
	WhitelistIPs       string
	AccessMode         string
	PublicURLs         []string
	LanguagePreference string
	ThemePreference    string
	NavPlacement       string
	ServerVersion      string

	PrimaryRegion         string
	PrimaryServerURL      string
	IsFailback            bool
	FailbackProbeInterval time.Duration

	// LeaseLost records that the connected gateway stopped holding a lease for this
	// session while the tunnel itself was healthy. Distinct from an eviction: nothing
	// is wrong with the region, so the recovery is to re-register, preferentially
	// where we already were (issue #1146).
	LeaseLost bool

	// failbackSuppressedUntil holds the failback prober off. A failback that is
	// immediately evicted means the primary answers /api/healthz but cannot actually
	// carry the session, and retrying it every 15s produces the flapping loop that
	// took a tunnel down on 2026-08-21 (issue #1145).
	failbackSuppressedUntil time.Time

	// failbackGate is asked, just before a failback would end the current session, whether
	// returning to that gateway is allowed at all. It exists because "the primary answers"
	// and "we should go back to the primary" are different questions, and only the caller
	// knows the second -- it holds the cooldowns, and knows whether we left deliberately.
	//
	// Consulted here rather than after the prober cancels, because cancelling IS the
	// interruption: by the time the caller could decline, the tunnel is already down and
	// re-registering (#1310).
	failbackGate func(regionURL string) bool

	// centralURL is the control plane an edge session also reports status to. Set from
	// configuration via SetCentralURL; empty means "report only to the connected
	// gateway". Unexported so it cannot be read without the lock.
	centralURL string

	// sessionLog persists proxied requests and diagnostic events. Nil is valid and
	// discards, so no call site needs a nil check.
	sessionLog *SessionLogger

	// inspectorPort is the port the Inspector actually bound, which is not necessarily
	// the one requested -- StartInspector walks upwards when the port is in use.
	inspectorPort int

	// shutdownWarnedAt is the Unix time the connected gateway says it is going down, with
	// the countdown and reason as last reported. Zero means none announced (#1238).
	shutdownWarnedAt    int64
	shutdownWarnSeconds int
	shutdownWarnReason  string
	// migrateOnShutdownAt is set when a warning arrives and the client should move off this
	// gateway before it stops, rather than waiting to be dropped (#1246). Separate from
	// shutdownWarnedAt because that one stays set so the TUI can keep rendering its
	// countdown, whereas this is read-and-cleared exactly once by the session loop.
	migrateOnShutdownAt     int64
	migrateOnShutdownReason string

	// latestVersion is the newest client version the gateway advertises. Refreshed while
	// running, not just at startup: a client left up for days would otherwise never learn
	// about a release, and those are exactly the users who do not revisit the portal
	// after registering (issue #1168).
	latestVersion string

	// Latency & Bandwidth Simulation Settings
	Latency         time.Duration
	BandwidthLimit  int64
	RateLimitKBPS   int64
	IsCustomDomain  bool
	MaintenancePath string
}

// NewInterceptorEngine creates a new state engine for traffic inspection.
func NewInterceptorEngine(targetHost string, headers []string) *InterceptorEngine {
	headerMap := make(map[string]string)
	for _, h := range headers {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) == 2 {
			headerMap[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}

	if targetHost == "" {
		targetHost = "127.0.0.1"
	}

	preserveHost := os.Getenv("LFT_PRESERVE_HOST") == "true"

	return &InterceptorEngine{
		MaintenanceMode:    false,
		Status:             "up",
		AddedHeaders:       headerMap,
		History:            make([]*RequestRecord, 0),
		MaxHistory:         100, // Keep last 100 requests
		TargetHost:         targetHost,
		PreserveHost:       preserveHost,
		InsecureSkipVerify: os.Getenv("LFT_INSECURE_SKIP_VERIFY") == "true",
		ConnState:          "disconnected",
		AuthValid:          true,
		DestPort:           8080, // Default Liferay port
		LatencyHistory:     make([]int64, 0),
	}
}

// sameGatewayHost reports whether two gateway URLs address the same host. Compared on
// the parsed host rather than by substring: a substring test matches any hostname that
// merely contains the other, and misses equivalent spellings such as a trailing slash
// or an explicit default port.
func sameGatewayHost(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	parsedA, errA := url.Parse(strings.TrimRight(a, "/"))
	parsedB, errB := url.Parse(strings.TrimRight(b, "/"))
	if errA != nil || errB != nil {
		return strings.EqualFold(a, b)
	}
	return strings.EqualFold(parsedA.Hostname(), parsedB.Hostname())
}

// statusReportTargets lists the gateways a tunnel-status heartbeat should go to. The
// connected gateway always gets one. The central control plane additionally gets one
// when the client is connected to a regional edge, so central keeps an accurate view
// of the session.
//
// centralURL must come from configuration. It was previously hardcoded to this
// project's own production gateway, which meant any self-hosted deployment shipped its
// session tokens to a third party every 5 seconds (issue #1124). When it is unknown,
// report only to the connected gateway.
func statusReportTargets(serverURL, centralURL string) []string {
	targets := []string{serverURL}
	if centralURL != "" && !sameGatewayHost(serverURL, centralURL) {
		targets = append(targets, centralURL)
	}
	return targets
}

// CentralURL returns the configured central control-plane URL, if any.
func (e *InterceptorEngine) CentralURL() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.centralURL
}

// SetCentralURL records the central control-plane URL that regional edge sessions
// should also report their status to.
func (e *InterceptorEngine) SetCentralURL(u string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.centralURL = u
}

// GatewayCanCarrySession reports whether a gateway is fit to serve a tunnel, given its
// /api/healthz response.
//
// A 200 alone is not enough. An edge whose control channel to central is down answers
// healthz perfectly well -- its HTTP listener is fine -- but central reports its region
// offline and evicts anything that lands there. Electing on the status code alone is what
// let a client fail back onto such an edge and be thrown off five seconds later, over and
// over (issue #1165).
//
// Gateways from before this field existed, and central itself, send no control_plane key.
// Their absence means healthy: an older gateway must keep working exactly as it does now.
func GatewayCanCarrySession(statusCode int, body []byte) bool {
	if statusCode != http.StatusOK {
		return false
	}
	var payload struct {
		ControlPlane string `json:"control_plane"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(body), &payload); err != nil {
		// Unparseable but 200: treat as healthy rather than stranding a client on a
		// gateway that is probably fine and merely returning something unexpected.
		return true
	}
	return payload.ControlPlane != "disconnected"
}

// probeGatewayHealth fetches /api/healthz and applies GatewayCanCarrySession.
// gatewayProbe records what a health probe actually saw.
//
// The verdict alone is not enough to explain a failback after the fact. A client failed
// back onto an edge that was mid-reboot and there was no way to tell, from the logs, what
// the prober had seen to justify it -- no status code, no control_plane value, nothing.
// Every other decision on this path writes a diagnostic event; this one did not, so the
// one question worth answering could not be (issue #1180).
type gatewayProbe struct {
	Usable       bool
	StatusCode   int
	ControlPlane string
	Body         string
	Err          string
}

// Reason renders the probe as a short phrase for a log line.
func (p gatewayProbe) Reason() string {
	switch {
	case p.Err != "":
		return "unreachable: " + p.Err
	case p.StatusCode != http.StatusOK:
		return fmt.Sprintf("http %d", p.StatusCode)
	case p.ControlPlane == "disconnected":
		return "control plane disconnected"
	case p.ControlPlane == "":
		return "healthy (no control_plane reported)"
	default:
		return "healthy (control_plane " + p.ControlPlane + ")"
	}
}

// probeGateway asks a gateway whether it can carry a session and reports what it saw.
func probeGateway(ctx context.Context, serverURL string, timeout time.Duration) gatewayProbe {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(serverURL, "/")+"/api/healthz", nil)
	if err != nil {
		return gatewayProbe{Err: err.Error()}
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return gatewayProbe{Err: err.Error()}
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	if err != nil {
		return gatewayProbe{StatusCode: resp.StatusCode, Err: err.Error()}
	}

	probe := gatewayProbe{
		StatusCode: resp.StatusCode,
		Body:       strings.TrimSpace(string(body)),
		Usable:     GatewayCanCarrySession(resp.StatusCode, body),
	}
	var payload struct {
		ControlPlane string `json:"control_plane"`
	}
	if json.Unmarshal(bytes.TrimSpace(body), &payload) == nil {
		probe.ControlPlane = payload.ControlPlane
	}
	// Only worth keeping the body when it did not parse -- that is the case the
	// "unparseable 200 counts as healthy" fallback covers, and the one most likely to be
	// wrong.
	if probe.ControlPlane != "" {
		probe.Body = ""
	}
	return probe
}

func probeGatewayHealth(ctx context.Context, serverURL string, timeout time.Duration) bool {
	return probeGateway(ctx, serverURL, timeout).Usable
}

// gatewayHasNoLease reports whether a 200 from /api/tunnel-status came from the branch
// where the gateway holds no lease for this session, rather than the one where it updated
// ours. handleTunnelStatus answers 200 either way and the two differ only by body: the
// update path writes nothing, the no-lease path writes a JSON object.
//
// Anything unreadable, unparseable or empty is treated as "lease present". A false
// negative merely delays recovery by one tick; a false positive would re-register a
// perfectly healthy tunnel, so the doubt goes that way deliberately.
func gatewayHasNoLease(body []byte) bool {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return false
	}
	var payload struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	return payload.Status == "ok"
}

// noteShutdownWarning records that the connected gateway has told us it is going down,
// and says so once per warning rather than on every heartbeat -- these arrive with the
// status ping, so an unguarded log would repeat every few seconds for the whole countdown.
//
// Recording it is all this does today. Acting on it -- bringing up a session on another
// gateway before this one goes, so the move costs nothing -- is the remaining half of
// #1238, and now has a signal to hang off.
func (e *InterceptorEngine) noteShutdownWarning(w *NodeShutdownWarning) {
	if w == nil || w.ShutdownAt == 0 {
		return
	}

	e.mu.Lock()
	already := e.shutdownWarnedAt == w.ShutdownAt
	e.shutdownWarnedAt = w.ShutdownAt
	e.shutdownWarnSeconds = w.SecondsRemaining
	e.shutdownWarnReason = w.Reason
	if !already {
		// Raised once per distinct shutdown, not on every heartbeat that repeats the
		// warning -- the session loop consumes it to move off this gateway before it
		// stops (#1246).
		e.migrateOnShutdownAt = w.ShutdownAt
		e.migrateOnShutdownReason = w.Reason
	}
	e.mu.Unlock()
	if already {
		return
	}

	slog.Info(fmt.Sprintf("[Client] Gateway reports it is shutting down in %ds: %s", w.SecondsRemaining, w.Reason))
	e.LogEvent("warn", "gateway_shutdown_warning", map[string]any{
		"seconds_remaining": w.SecondsRemaining,
		"shutdown_at":       w.ShutdownAt,
		"reason":            w.Reason,
		"node_id":           w.NodeID,
	})
}

// ShutdownWarning returns the pending gateway shutdown, if one has been announced: the
// Unix time it is expected at, the countdown as last reported, and the reason.
func (e *InterceptorEngine) ShutdownWarning() (at int64, secondsRemaining int, reason string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.shutdownWarnedAt, e.shutdownWarnSeconds, e.shutdownWarnReason
}

// ConsumeLeaseLost reports whether the connected gateway has stopped holding our lease,
// clearing the flag as it does. Read-and-clear under one lock, for the same reason as
// ConsumeFailback: the health-check goroutine sets it while the main loop reads it.
func (e *InterceptorEngine) ConsumeLeaseLost() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	lost := e.LeaseLost
	e.LeaseLost = false
	return lost
}

// ConsumeShutdownMigration reports whether the connected gateway has announced a shutdown
// this client should move ahead of, clearing the signal as it does. Read-and-clear under one
// lock, matching ConsumeLeaseLost: a watcher goroutine sets it while the session loop reads
// it.
//
// Returns the announced shutdown time and reason so the caller can say why it moved, and how
// long the gateway it is leaving will be away.
func (e *InterceptorEngine) ConsumeShutdownMigration() (at int64, reason string, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	at, reason = e.migrateOnShutdownAt, e.migrateOnShutdownReason
	e.migrateOnShutdownAt, e.migrateOnShutdownReason = 0, ""
	return at, reason, at != 0
}

// PendingShutdownMigration reports whether a move is pending without consuming it, so a
// watcher can poll cheaply and only the session loop takes the signal.
func (e *InterceptorEngine) PendingShutdownMigration() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.migrateOnShutdownAt != 0
}

// SuppressFailback holds the failback prober off for d. Cooling the region down is not
// enough on its own -- the prober targets the primary region directly and never consults
// the cooldown set.
func (e *InterceptorEngine) SuppressFailback(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failbackSuppressedUntil = time.Now().Add(d)
}

// SetFailbackGate installs the predicate consulted before a failback ends the session. Nil, the
// default, means every recovered primary is returned to -- the behaviour before #1310.
func (e *InterceptorEngine) SetFailbackGate(gate func(regionURL string) bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failbackGate = gate
}

// failbackAllowed asks the gate whether returning to regionURL is permitted.
func (e *InterceptorEngine) failbackAllowed(regionURL string) bool {
	e.mu.RLock()
	gate := e.failbackGate
	e.mu.RUnlock()
	if gate == nil {
		return true
	}
	return gate(regionURL)
}

// failbackSuppressed reports whether the prober is currently held off.
func (e *InterceptorEngine) failbackSuppressed() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return time.Now().Before(e.failbackSuppressedUntil)
}

// healthReportClient bounds the tunnel-status POST. http.DefaultClient has no timeout,
// so the only thing that could release a hung request is this loop's own context -- and
// that context is cancelled by this loop, on lease eviction. An edge that completes the
// TCP handshake and then never responds would therefore park the one goroutine
// responsible for noticing the eviction, and no failover would ever be triggered
// (issue #1137). The timeout is generous relative to the 5s tick because a slow reply is
// not itself a fault; only an indefinite one is.
var healthReportClient = &http.Client{Timeout: 10 * time.Second}

// localTargetStatus dials every mapped local port and reports one aggregate status for
// the session. A tunnel is only "up" when every port it advertises is reachable:
// reporting "up" while a client extension's port is dead would mean the gateway
// forwards traffic to a listener that isn't there.
func (e *InterceptorEngine) localTargetStatus(targetPorts []int) string {
	e.mu.RLock()
	isMaint := e.MaintenanceMode
	e.mu.RUnlock()
	if isMaint {
		return "maintenance"
	}

	for _, port := range targetPorts {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", e.TargetHost, port), 2*time.Second)
		if err != nil {
			return "down"
		}
		conn.Close() //nolint:errcheck
	}
	return "up"
}

// StartHealthChecks begins a background loop to verify the local targets are responding
// and reports one aggregate status for the session to the Gateway.
//
// One goroutine per session, not per port. It previously ran per mapped port, so every
// tick produced an identical status report per port per endpoint -- a portal plus three
// client extensions meant eight POSTs every five seconds carrying the same session
// state -- and each goroutine independently raced to trigger the same failover, while
// each overwrote the engine's single Status field with its own port's result
// (issue #1123).
func (e *InterceptorEngine) StartHealthChecks(ctx context.Context, cancel context.CancelFunc, serverURL, region, sessionToken string, targetPorts []int) {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				newStatus := e.localTargetStatus(targetPorts)

				// Update internal state and notify server if changed or heartbeat tick
				e.mu.Lock()
				e.Status = newStatus
				e.mu.Unlock()

				// Send status update/heartbeat to Gateway and Central Control Plane
				payload, _ := json.Marshal(map[string]string{
					"session_token": sessionToken,
					"region":        region,
					"status":        newStatus,
				})

				urlsToPing := statusReportTargets(serverURL, e.CentralURL())

				for _, pingURL := range urlsToPing {
					// Only the gateway actually serving this session can say the lease is
					// gone. The same reasoning the 200 path below already applies, applied
					// here: central legitimately knows nothing about an edge-hosted session,
					// so its answer describes central, not this tunnel (#1306).
					servingGateway := sameGatewayHost(pingURL, serverURL)

					req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/api/tunnel-status", pingURL), bytes.NewBuffer(payload))
					if err == nil {
						req.Header.Set("Content-Type", "application/json")
						resp, err := healthReportClient.Do(req)
						if err == nil {
							if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusServiceUnavailable {
								if !servingGateway {
									// Central having a moment -- a restart, a deploy, a blip.
									// Acting on it used to tear down every edge-served tunnel
									// in the fleet at once, which is the opposite of what the
									// regional edges are for: they are meant to keep serving
									// while central is unavailable.
									slog.Info(fmt.Sprintf("[Client] Control plane at %s answered HTTP %d; it does not serve this tunnel, so continuing.", pingURL, resp.StatusCode))
									e.LogEvent("warn", "control_plane_unavailable", map[string]any{
										"region":      region,
										"reported_by": pingURL,
										"status_code": resp.StatusCode,
									})
									_ = resp.Body.Close() //nolint:errcheck
									continue
								}
								slog.Info(fmt.Sprintf("[Client] Gateway reported region offline or lease evicted (HTTP %d). Triggering dynamic region failover...", resp.StatusCode))
								e.LogEvent("warn", "lease_evicted", map[string]any{
									"region":       region,
									"reported_by":  pingURL,
									"status_code":  resp.StatusCode,
									"local_status": newStatus,
								})
								_ = resp.Body.Close() //nolint:errcheck
								if cancel != nil {
									cancel()
								}
								return
							}

							// A 200 does not by itself mean the session is still served.
							// handleTunnelStatus answers 200 both when it updated our lease
							// and when it holds no lease for us at all; the two differ only
							// by body, the update path writing nothing and the no-lease path
							// writing {"status":"ok"}. Central legitimately takes the
							// no-lease path for every edge-hosted session, so only the
							// gateway we are actually connected to can tell us anything.
							//
							// Without this the client is told "ok" indefinitely after its
							// lease is swept, serves no traffic and never re-registers --
							// a dead tunnel that only a restart recovers.
							// One read, two questions: whether the gateway still holds our
							// lease, and whether it has told us it is going down. The body
							// can only be consumed once, so it is read here rather than
							// inside either check.
							body, _ := io.ReadAll(io.LimitReader(resp.Body, 512)) //nolint:errcheck

							if pingURL == serverURL {
								if warning, ok := ParseNodeShutdownWarning(body); ok {
									e.noteShutdownWarning(warning)
								}
							}

							if pingURL == serverURL && resp.StatusCode == http.StatusOK && gatewayHasNoLease(body) {
								slog.Info("[Client] Gateway no longer holds a lease for this session. Re-registering...")
								e.LogEvent("warn", "lease_missing", map[string]any{
									"region":       region,
									"reported_by":  pingURL,
									"local_status": newStatus,
								})
								_ = resp.Body.Close() //nolint:errcheck
								e.mu.Lock()
								e.LeaseLost = true
								e.mu.Unlock()
								if cancel != nil {
									cancel()
								}
								return
							}
							_ = resp.Body.Close() //nolint:errcheck
						}
					}
				}
			}
		}
	}()
}

// StartFailbackProber periodically checks if the primary region is back online when running in failover mode.
// shutdownMigrationPollInterval is how often the migrator checks for a pending shutdown. The
// signal is set from a heartbeat response rather than pushed, so there is nothing to select
// on; a second is far finer than the minute-granularity warnings it reacts to.
var shutdownMigrationPollInterval = time.Second

// StartShutdownMigrator ends the current session when the gateway announces it is stopping,
// so the client moves while that gateway is still up rather than being dropped by it (#1246).
//
// It does not perform the move itself. Cancelling the session hands control back to the
// session loop, which already knows how to exclude a gateway, re-elect and re-register --
// the same path a connection loss takes. Reusing it means a planned move and an unplanned one
// cannot drift apart, and the loop consumes ConsumeShutdownMigration to tell them apart.
//
// Measured baseline this replaces: a client dropped by a scheduled stop was down 24m36s,
// nearly all of it waiting for the node to return. Moving on the warning turns that into the
// cost of one reconnect.
//
// Deliberately not started for a client pinned with -server: that client will not fail over
// (see #1275), so cancelling its session would drop the tunnel with nothing to move to.
func (e *InterceptorEngine) StartShutdownMigrator(ctx context.Context, cancel context.CancelFunc) {
	go func() {
		ticker := time.NewTicker(shutdownMigrationPollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !e.PendingShutdownMigration() {
					continue
				}
				at, _, _ := e.ConsumeShutdownMigrationPeek()
				slog.Info(fmt.Sprintf("[Client] Gateway is stopping in %ds; moving to another gateway now rather than waiting to be dropped.",
					int(time.Until(time.Unix(at, 0)).Seconds())))
				cancel()
				return
			}
		}
	}()
}

// ConsumeShutdownMigrationPeek reads the pending migration without clearing it, so the
// migrator can log what it is reacting to while leaving the signal for the session loop to
// consume once.
func (e *InterceptorEngine) ConsumeShutdownMigrationPeek() (at int64, reason string, ok bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.migrateOnShutdownAt, e.migrateOnShutdownReason, e.migrateOnShutdownAt != 0
}

func (e *InterceptorEngine) StartFailbackProber(ctx context.Context, cancel context.CancelFunc, primaryServerURL, primaryRegion string) {
	if primaryServerURL == "" || primaryRegion == "" {
		return
	}
	go func() {
		interval := e.FailbackProbeInterval
		if interval <= 0 {
			interval = 15 * time.Second
		}
		// Tracks the last reason a failback was declined, so a long outage logs the
		// transitions rather than the same line every 15 seconds.
		lastDeclineReason := ""
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.mu.RLock()
				currentRegion := e.SelectedRegion
				e.mu.RUnlock()

				if strings.EqualFold(currentRegion, primaryRegion) {
					continue
				}

				// A failback that was immediately evicted means the primary answers
				// /api/healthz while being unable to carry the session -- its HTTP
				// listener is up but its control channel to central is not. Retrying
				// on the next tick just reproduces that, so back off (issue #1145).
				if e.failbackSuppressed() {
					continue
				}

				// Before probing, and long before cancelling: a gateway we deliberately
				// left is not one to go back to just because it answers again (#1310).
				// Checked here so the session is never torn down for a failback that was
				// going to be declined.
				if !e.failbackAllowed(primaryServerURL) {
					if lastDeclineReason != "cooldown" {
						lastDeclineReason = "cooldown"
						e.LogEvent("info", "failback_declined", map[string]any{
							"region": primaryRegion,
							"url":    primaryServerURL,
							"reason": "cooldown",
						})
					}
					continue
				}

				// Ask the primary whether it can actually carry a session, not merely
				// whether its HTTP listener is up. An edge that cannot reach central
				// answers healthz fine and then evicts us within seconds.
				probe := probeGateway(ctx, primaryServerURL, 5*time.Second)

				if !probe.Usable {
					// Declines are logged only when the reason changes, since this runs
					// every 15s and an edge can be down for a long time. The transition is
					// the informative part.
					if probe.Reason() != lastDeclineReason {
						lastDeclineReason = probe.Reason()
						e.LogEvent("info", "failback_declined", map[string]any{
							"region":        primaryRegion,
							"url":           primaryServerURL,
							"reason":        probe.Reason(),
							"status_code":   probe.StatusCode,
							"control_plane": probe.ControlPlane,
							"body":          probe.Body,
						})
					}
					continue
				}
				// No need to reset lastDeclineReason here -- a usable probe always ends
				// this goroutine a few lines below, so nothing would ever read it again.

				// Always recorded: a failback is rare, and this is the decision that
				// needs explaining when one turns out to have been wrong.
				e.LogEvent("info", "failback_probe", map[string]any{
					"region":        primaryRegion,
					"url":           primaryServerURL,
					"reason":        probe.Reason(),
					"status_code":   probe.StatusCode,
					"control_plane": probe.ControlPlane,
					"body":          probe.Body,
				})

				slog.Info(fmt.Sprintf("[Client] Primary region '%s' (%s) reports %s. Initiating automated failback...", primaryRegion, primaryServerURL, probe.Reason()))
				e.mu.Lock()
				e.IsFailback = true
				e.mu.Unlock()
				if cancel != nil {
					cancel()
				}
				return
			}
		}
	}()
}

// SetInspectorPort records the port the Inspector actually bound to.
func (e *InterceptorEngine) SetInspectorPort(port int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inspectorPort = port
}

// InspectorPort returns the port the Inspector bound to, or 0 if it is not running.
func (e *InterceptorEngine) InspectorPort() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.inspectorPort
}

// SetLatestVersion records the newest client version the gateway advertises.
func (e *InterceptorEngine) SetLatestVersion(v string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.latestVersion = v
}

// NewerVersion returns the advertised version when it is newer than the running one, and
// "" otherwise. A development build never reports an update, since its version does not
// compare meaningfully.
func (e *InterceptorEngine) NewerVersion() string {
	e.mu.RLock()
	latest := e.latestVersion
	e.mu.RUnlock()

	if latest == "" || config.Version == "dev" {
		return ""
	}
	if CompareVersions(config.Version, latest) < 0 {
		return latest
	}
	return ""
}

// StartVersionWatcher periodically re-asks the gateway what the newest client version is.
//
// The existing check ran once, before the tunnel opened, and wrote a single log line. In
// the TUI that line is pushed out of the five-line log box within seconds, and in
// background mode it goes to a file nobody reads -- so a client left running for days
// never learned about a release (issue #1168). This keeps the header honest instead.
func (e *InterceptorEngine) StartVersionWatcher(ctx context.Context, serverURL string, interval time.Duration) {
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if info, err := CheckServerCompatibility(serverURL); err == nil && info != nil {
					e.SetLatestVersion(info.LatestVersion)
				}
			}
		}
	}()
}

// SetSessionLogger attaches the persistent traffic/diagnostic logs to the engine.
func (e *InterceptorEngine) SetSessionLogger(l *SessionLogger) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sessionLog = l
}

// LogEvent records a diagnostic event to the persistent error log, if one is attached.
func (e *InterceptorEngine) LogEvent(level, event string, fields map[string]any) {
	e.mu.RLock()
	logger := e.sessionLog
	e.mu.RUnlock()
	logger.Event(level, event, fields)
}

// AddRecord safely appends a record to the history buffer and persists it.
func (e *InterceptorEngine) AddRecord(rec *RequestRecord) {
	e.mu.Lock()
	e.History = append([]*RequestRecord{rec}, e.History...) // Prepend
	if len(e.History) > e.MaxHistory {
		e.History = e.History[:e.MaxHistory]
	}
	logger, region := e.sessionLog, e.SelectedRegion
	e.mu.Unlock()

	// Written outside the lock: the in-memory ring is on the hot path of every proxied
	// request and must not wait on disk I/O.
	logger.Traffic(rec, region)
}

// ConsumeFailback reports whether the failback prober has asked for a switch to the
// primary region, clearing the flag as it does so. Read-and-clear is one operation
// under the lock: the prober goroutine sets the flag while the main loop reads it, and
// a failback must be acted on exactly once.
func (e *InterceptorEngine) ConsumeFailback() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	requested := e.IsFailback
	e.IsFailback = false
	return requested
}

// SetRegionEndpoint atomically updates the region, gateway URL and public URLs the
// client is currently serving from. These three always change together during a
// failover or failback, and the TUI renderer, the Inspector HTTP handlers and the
// failback prober all read them concurrently -- updating them under one lock keeps
// readers from observing a half-applied switch (e.g. the new region label next to
// the old edge host).
func (e *InterceptorEngine) SetRegionEndpoint(region, serverURL string, publicURLs []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.SelectedRegion = region
	e.ServerURL = serverURL
	// Copy rather than alias: the caller keeps using its slice after this returns.
	e.PublicURLs = append([]string(nil), publicURLs...)
}

// RegionEndpoint returns the current region, gateway URL and public URLs together.
func (e *InterceptorEngine) RegionEndpoint() (region, serverURL string, publicURLs []string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.SelectedRegion, e.ServerURL, append([]string(nil), e.PublicURLs...)
}

// SetSubdomainDetails updates the subdomain registration details safely.
func (e *InterceptorEngine) SetSubdomainDetails(req, ass string, leased, conflict bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.SubdomainReq = req
	e.SubdomainAss = ass
	if ass == "" {
		e.SubdomainAss = req
	}
	e.SubdomainLeased = leased
	e.SubdomainConflict = conflict
}

// InterceptPort creates a reverse proxy listening on a dynamic local port and forwarding to the targetPort.
func (e *InterceptorEngine) InterceptPort(targetPort int) (int, error) {
	// Start listener on dynamic port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}

	listenPort := listener.Addr().(*net.TCPAddr).Port

	scheme := "http"
	if targetPort == 443 || targetPort == 8443 {
		scheme = "https"
	}
	targetURL, _err := url.Parse(fmt.Sprintf("%s://%s:%d", scheme, e.TargetHost, targetPort))
	_ = _err //nolint:errcheck
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	customTransport := http.DefaultTransport.(*http.Transport).Clone()
	if scheme == "https" && e.InsecureSkipVerify {
		customTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	// Custom Transport to capture response and duration
	proxy.Transport = &interceptorTransport{
		engine:     e,
		targetPort: targetPort,
		transport:  customTransport,
	}

	// Custom Director to inject headers
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)

		// Rewrite Host header if PreserveHost is unchecked
		if !e.PreserveHost {
			req.Host = getHostHeaderValue(e.TargetHost, targetPort)
		}

		e.mu.RLock()
		defer e.mu.RUnlock()
		for k, v := range e.AddedHeaders {
			req.Header.Set(k, v)
		}
	}

	// Without this, a dial failure (nothing listening on the local target port) falls
	// through to httputil.ReverseProxy's stock error handling: a bare 502 with no body,
	// inconsistent with the polished "Environment Offline" page the central serves when
	// no client is connected at all (#980). interceptorTransport.RoundTrip above already
	// records the failed request (rec.Status = 502) before returning the error, so this
	// only replaces how the response itself gets written -- it also takes over the
	// default handler's log.Printf("http: proxy error: ...") call, which is why that's
	// replicated here rather than dropped (the TUI's SYSTEM LOGS panel captures it via
	// log.SetOutput, same as before).
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("http: proxy error: %v", err)
		serveDialFailurePage(w, e.TargetHost, targetPort)
	}

	// HTTP Handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.RLock()
		isMaint := e.MaintenanceMode
		e.mu.RUnlock()

		if isMaint {
			serveMaintenancePage(w, e.MaintenancePath)
			return
		}

		proxy.ServeHTTP(w, r)
	})

	// Run in background
	go func() {
		if err := http.Serve(listener, handler); err != nil {
			slog.Info(fmt.Sprintf("[Interceptor] Proxy on port %d crashed: %v", listenPort, err))
		}
	}()

	return listenPort, nil
}

// interceptorTransport intercepts roundtrips to capture request/response data.
type interceptorTransport struct {
	engine     *InterceptorEngine
	targetPort int
	transport  http.RoundTripper
}

func (t *interceptorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddInt32(&t.engine.ActiveConnections, 1)
	defer atomic.AddInt32(&t.engine.ActiveConnections, -1)

	startTime := time.Now()

	// Inject request-phase latency (half of total roundtrip delay)
	if t.engine.Latency > 0 {
		time.Sleep(t.engine.Latency / 2)
	}

	// Capture request body (up to 10KB)
	var reqBodyStr string
	if req.Body != nil {
		var bodyReader io.Reader = req.Body
		var remainingReader io.Reader = req.Body
		if t.engine.BandwidthLimit > 0 {
			limiter := rate.NewLimiter(rate.Limit(t.engine.BandwidthLimit), int(getHostBurst(t.engine.BandwidthLimit)))
			bodyReader = &throttledReader{r: req.Body, limiter: limiter, ctx: req.Context()}
			remainingReader = &throttledReader{r: req.Body, limiter: limiter, ctx: req.Context()}
		}
		bodyBytes, _ := io.ReadAll(io.LimitReader(bodyReader, 10240))
		reqBodyStr = string(bodyBytes)
		req.Body = struct {
			io.Reader
			io.Closer
		}{
			Reader: io.MultiReader(bytes.NewReader(bodyBytes), remainingReader),
			Closer: req.Body,
		}
	}

	// Extract Req Headers
	reqHeaders := make(map[string]string)
	var reqHeadersSize int64
	for k, v := range req.Header {
		joinVal := strings.Join(v, ", ")
		reqHeaders[k] = joinVal
		reqHeadersSize += int64(len(k) + len(joinVal) + 4) // key + ": " + value + "\r\n"
	}
	reqHeadersSize += int64(len(req.Method) + len(req.URL.RequestURI()) + len(req.Proto) + 4) // Request Line + "\r\n"

	var reqBodySize int64
	if req.ContentLength >= 0 {
		reqBodySize = req.ContentLength
	} else {
		reqBodySize = int64(len(reqBodyStr))
	}

	// Update requests total and bytes in
	t.engine.mu.Lock()
	t.engine.RequestsTotal++
	t.engine.BytesIn += (reqHeadersSize + reqBodySize)
	t.engine.mu.Unlock()

	// Forward Request
	res, err := t.transport.RoundTrip(req)

	// Inject response-phase latency (remaining half of total roundtrip delay)
	if t.engine.Latency > 0 {
		time.Sleep(t.engine.Latency / 2)
	}
	duration := time.Since(startTime).Milliseconds()

	if err == nil && res != nil {
		// 1. Rewrite Location redirect header if absolute and points to the target host/port
		if locStr := res.Header.Get("Location"); locStr != "" {
			if locURL, parseErr := url.Parse(locStr); parseErr == nil && locURL.IsAbs() {
				targetHostPort := fmt.Sprintf("%s:%d", t.engine.TargetHost, t.targetPort)
				isTarget := locURL.Host == targetHostPort ||
					locURL.Host == fmt.Sprintf("localhost:%d", t.targetPort) ||
					locURL.Host == fmt.Sprintf("127.0.0.1:%d", t.targetPort)

				if !isTarget && (strings.HasPrefix(locURL.Host, "localhost:") || strings.HasPrefix(locURL.Host, "127.0.0.1:")) {
					_, p, _ := net.SplitHostPort(locURL.Host)
					if portInt, _ := strconv.Atoi(p); portInt == t.targetPort {
						isTarget = true
					}
				}

				if isTarget {
					publicHost := req.Header.Get("X-Forwarded-Host")
					publicProto := req.Header.Get("X-Forwarded-Proto")
					if publicHost != "" {
						if publicProto == "" {
							publicProto = "https"
						}
						locURL.Scheme = publicProto
						locURL.Host = publicHost
						res.Header.Set("Location", locURL.String())
					}
				}
			}
		}

		// 2. Rewrite Set-Cookie domains (remove Domain=localhost, Domain=127.0.0.1, Domain=<TargetHost>)
		if cookies := res.Header["Set-Cookie"]; len(cookies) > 0 {
			var newCookies []string
			for _, cookieStr := range cookies {
				parts := strings.Split(cookieStr, ";")
				var newParts []string
				for _, part := range parts {
					trimmed := strings.TrimSpace(part)
					if strings.HasPrefix(strings.ToLower(trimmed), "domain=") {
						domVal := strings.TrimPrefix(strings.ToLower(trimmed), "domain=")
						isLocal := domVal == "localhost" || domVal == "127.0.0.1" || domVal == strings.ToLower(t.engine.TargetHost)
						if isLocal {
							continue
						}
					}
					newParts = append(newParts, part)
				}
				newCookies = append(newCookies, strings.Join(newParts, ";"))
			}
			res.Header["Set-Cookie"] = newCookies
		}
	}

	rec := &RequestRecord{
		ID:         fmt.Sprintf("%d", time.Now().UnixNano()),
		Time:       startTime,
		Method:     req.Method,
		Path:       req.URL.Path,
		ReqHeaders: reqHeaders,
		ReqBody:    reqBodyStr,
		DurationMs: duration,
		TargetPort: t.targetPort,
	}

	if err != nil {
		rec.Status = 502
		t.engine.AddRecord(rec)
		return res, err
	}

	// Capture response body (up to 10KB)
	var respBodyStr string
	if res.Body != nil {
		var bodyReader io.Reader = res.Body
		var remainingReader io.Reader = res.Body
		if t.engine.BandwidthLimit > 0 {
			limiter := rate.NewLimiter(rate.Limit(t.engine.BandwidthLimit), int(getHostBurst(t.engine.BandwidthLimit)))
			bodyReader = &throttledReader{r: res.Body, limiter: limiter, ctx: req.Context()}
			remainingReader = &throttledReader{r: res.Body, limiter: limiter, ctx: req.Context()}
		}
		bodyBytes, _ := io.ReadAll(io.LimitReader(bodyReader, 10240))
		respBodyStr = string(bodyBytes)
		res.Body = struct {
			io.Reader
			io.Closer
		}{
			Reader: io.MultiReader(bytes.NewReader(bodyBytes), remainingReader),
			Closer: res.Body,
		}
	}

	respHeaders := make(map[string]string)
	var respHeadersSize int64
	for k, v := range res.Header {
		joinVal := strings.Join(v, ", ")
		respHeaders[k] = joinVal
		respHeadersSize += int64(len(k) + len(joinVal) + 4) // key + ": " + value + "\r\n"
	}
	respHeadersSize += int64(len(res.Proto) + 15) // Status line e.g., "HTTP/1.1 200 OK\r\n"

	var respBodySize int64
	if res.ContentLength >= 0 {
		respBodySize = res.ContentLength
	} else {
		respBodySize = int64(len(respBodyStr))
	}

	t.engine.mu.Lock()
	t.engine.BytesOut += (respHeadersSize + respBodySize)
	t.engine.mu.Unlock()

	rec.Status = res.StatusCode
	rec.RespHeaders = respHeaders
	rec.RespBody = respBodyStr

	t.engine.AddRecord(rec)
	return res, err
}

func serveMaintenancePage(w http.ResponseWriter, path string) {
	if path != "" {
		if content, err := os.ReadFile(path); err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := w.Write(content); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
			return
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	if _, err := w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
	<title>Developer Maintenance Mode</title>
	<link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;600;800&display=swap" rel="stylesheet">
	<style>
		body { font-family: 'Outfit', sans-serif; background: linear-gradient(135deg, #0f172a 0%, #1e1b4b 50%, #311042 100%); color: white; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; }
		.card { background: rgba(30, 41, 59, 0.7); padding: 48px 32px; border-radius: 24px; border: 1px solid rgba(255,255,255,0.08); text-align: center; max-width: 520px; box-shadow: 0 20px 40px rgba(0, 0, 0, 0.3); }
		h1 { margin-top: 0; color: #38bdf8; font-size: 28px; font-weight: 800; }
		p { color: #94a3b8; font-size: 16px; line-height: 1.6; }
		.logo-container { margin-bottom: 24px; display: inline-flex; align-items: center; justify-content: center; width: 80px; height: 80px; border-radius: 20px; background: rgba(255, 255, 255, 0.03); border: 1px solid rgba(255, 255, 255, 0.05); }
	</style>
</head>
<body>
	<div class="card">
		<div class="logo-container">
			<svg width="44" height="44" viewBox="0 0 24 24" fill="white"><path d="M12 2L2 22h20L12 2zm0 3.8l7.5 14.2H4.5L12 5.8z"/></svg>
		</div>
		<h1>Developer Maintenance</h1>
		<p>The developer has temporarily paused this tunnel for maintenance. Please check back shortly.</p>
	</div>
</body>
</html>`)); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
}

// serveDialFailurePage renders a styled 502 page for the case where the tunnel itself is
// up but the local target (targetHost:targetPort) can't be dialed -- distinct from
// serveMaintenancePage's "developer paused this on purpose" case above, and from the
// central's "no client connected at all" page, but deliberately similar in visual style
// to both (dark gradient card, Outfit font) so a visitor sees one coherent "offline" look
// across all three (#980). Unlike the central's visitor-facing page, this one names the
// target host/port -- the audience here is the developer running the client, for whom
// that's exactly the actionable detail ("is anything even listening on :8080?").
func serveDialFailurePage(w http.ResponseWriter, targetHost string, targetPort int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	target := fmt.Sprintf("%s:%d", targetHost, targetPort)
	page := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<title>Local Application Unreachable</title>
	<link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;600;800&display=swap" rel="stylesheet">
	<style>
		body { font-family: 'Outfit', sans-serif; background: linear-gradient(135deg, #0f172a 0%%, #1e1b4b 50%%, #311042 100%%); color: white; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; }
		.card { background: rgba(30, 41, 59, 0.7); padding: 48px 32px; border-radius: 24px; border: 1px solid rgba(255,255,255,0.08); text-align: center; max-width: 520px; box-shadow: 0 20px 40px rgba(0, 0, 0, 0.3); }
		h1 { margin-top: 0; color: #f87171; font-size: 28px; font-weight: 800; }
		p { color: #94a3b8; font-size: 16px; line-height: 1.6; }
		.logo-container { margin-bottom: 24px; display: inline-flex; align-items: center; justify-content: center; width: 80px; height: 80px; border-radius: 20px; background: rgba(255, 255, 255, 0.03); border: 1px solid rgba(255, 255, 255, 0.05); }
		.target { font-family: monospace; color: #38bdf8; font-weight: 600; }
	</style>
</head>
<body>
	<div class="card">
		<div class="logo-container">
			<svg width="44" height="44" viewBox="0 0 24 24" fill="white"><path d="M12 2L2 22h20L12 2zm0 3.8l7.5 14.2H4.5L12 5.8z"/></svg>
		</div>
		<h1>Local Application Unreachable</h1>
		<p>The tunnel is connected, but nothing responded at <span class="target">%s</span> on this machine. Make sure your local application is running, then reload.</p>
	</div>
</body>
</html>`, target)
	if _, err := w.Write([]byte(page)); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
}

func getHostHeaderValue(host string, port int) string {
	if port == 80 || port == 443 {
		return host
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func getHostBurst(limit int64) int64 {
	if limit > 65536 {
		return limit
	}
	return 65536
}

type throttledReader struct {
	r       io.Reader
	limiter *rate.Limiter
	ctx     context.Context
}

func (tr *throttledReader) Read(p []byte) (n int, err error) {
	n, err = tr.r.Read(p)
	if n > 0 && tr.limiter != nil {
		burst := tr.limiter.Burst()
		if burst <= 0 {
			return n, err
		}

		rem := n
		for rem > 0 {
			chunk := rem
			if chunk > burst {
				chunk = burst
			}
			ctx := tr.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			if waitErr := tr.limiter.WaitN(ctx, chunk); waitErr != nil {
				return n - rem + chunk, waitErr
			}
			rem -= chunk
		}
	}
	return n, err
}

func ParseBandwidth(bwStr string) (int64, error) {
	bwStr = strings.ToLower(strings.TrimSpace(bwStr))
	if bwStr == "" {
		return 0, nil
	}

	if val, err := strconv.ParseInt(bwStr, 10, 64); err == nil {
		return val, nil
	}

	var numStr string
	var suffix string
	for i, c := range bwStr {
		if (c >= '0' && c <= '9') || c == '.' {
			numStr += string(c)
		} else {
			suffix = bwStr[i:]
			break
		}
	}

	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid numeric bandwidth: %s", numStr)
	}

	suffix = strings.TrimSpace(suffix)
	var multiplier float64
	switch suffix {
	case "b", "bps":
		multiplier = 1.0 / 8.0
	case "kb", "kbps", "kb/s":
		multiplier = 1000.0 / 8.0
	case "mb", "mbps", "mb/s":
		multiplier = 1000.0 * 1000.0 / 8.0
	case "gb", "gbps", "gb/s":
		multiplier = 1000.0 * 1000.0 * 1000.0 / 8.0
	case "b/s", "bytes/s":
		multiplier = 1.0
	case "kbytes/s", "kb/sec":
		multiplier = 1000.0
	case "mbytes/s", "mb/sec":
		multiplier = 1000.0 * 1000.0
	default:
		return 0, fmt.Errorf("unknown bandwidth suffix: %s", suffix)
	}

	bytesPerSec := int64(val * multiplier)
	if bytesPerSec <= 0 {
		bytesPerSec = 1
	}
	return bytesPerSec, nil
}
