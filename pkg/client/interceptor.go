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

// gatewayHasNoLease reports whether a 200 from /api/tunnel-status came from the branch
// where the gateway holds no lease for this session, rather than the one where it updated
// ours. handleTunnelStatus answers 200 either way and the two differ only by body: the
// update path writes nothing, the no-lease path writes a JSON object.
//
// Anything unreadable, unparseable or empty is treated as "lease present". A false
// negative merely delays recovery by one tick; a false positive would re-register a
// perfectly healthy tunnel, so the doubt goes that way deliberately.
func gatewayHasNoLease(resp *http.Response) bool {
	if resp == nil || resp.Body == nil {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	if err != nil {
		return false
	}
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

// SuppressFailback holds the failback prober off for d. Cooling the region down is not
// enough on its own -- the prober targets the primary region directly and never consults
// the cooldown set.
func (e *InterceptorEngine) SuppressFailback(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failbackSuppressedUntil = time.Now().Add(d)
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
					req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/api/tunnel-status", pingURL), bytes.NewBuffer(payload))
					if err == nil {
						req.Header.Set("Content-Type", "application/json")
						resp, err := healthReportClient.Do(req)
						if err == nil {
							if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusServiceUnavailable {
								slog.Info(fmt.Sprintf("[Client] Control plane reported region offline or lease evicted (HTTP %d). Triggering dynamic region failover...", resp.StatusCode))
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
							if pingURL == serverURL && resp.StatusCode == http.StatusOK && gatewayHasNoLease(resp) {
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
func (e *InterceptorEngine) StartFailbackProber(ctx context.Context, cancel context.CancelFunc, primaryServerURL, primaryRegion string) {
	if primaryServerURL == "" || primaryRegion == "" {
		return
	}
	go func() {
		interval := e.FailbackProbeInterval
		if interval <= 0 {
			interval = 15 * time.Second
		}
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

				// Probe primary region healthz endpoint
				probeURL := strings.TrimRight(primaryServerURL, "/") + "/api/healthz"
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
				if err != nil {
					continue
				}

				client := &http.Client{Timeout: 5 * time.Second}
				resp, err := client.Do(req)
				if err == nil {
					_ = resp.Body.Close() //nolint:errcheck
					if resp.StatusCode == http.StatusOK {
						slog.Info(fmt.Sprintf("[Client] Primary region '%s' (%s) is back online! Initiating automated failback...", primaryRegion, primaryServerURL))
						e.mu.Lock()
						e.IsFailback = true
						e.mu.Unlock()
						if cancel != nil {
							cancel()
						}
						return
					}
				}
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
