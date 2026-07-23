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
	Passcode           string
	WhitelistIPs       string
	AccessMode         string
	PublicURLs         []string
	LanguagePreference string
	ThemePreference    string
	NavPlacement       string
	ServerVersion      string

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

// StartHealthChecks begins a background loop to verify Tomcat is responding and reports status to the Gateway.
func (e *InterceptorEngine) StartHealthChecks(ctx context.Context, serverURL, sessionToken string, targetPort int) {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Determine actual status
				e.mu.RLock()
				isMaint := e.MaintenanceMode
				e.mu.RUnlock()

				newStatus := "up"
				if isMaint {
					newStatus = "maintenance"
				} else {
					// Simple dial test to the local target port
					conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", e.TargetHost, targetPort), 2*time.Second)
					if err != nil {
						newStatus = "down"
					} else {
						conn.Close() //nolint:errcheck
					}
				}

				// Update internal state and notify server if changed
				e.mu.Lock()
				changed := e.Status != newStatus
				e.Status = newStatus
				e.mu.Unlock()

				if changed || newStatus == "maintenance" {
					// Send status update to Gateway
					payload, _ := json.Marshal(map[string]string{
						"session_token": sessionToken,
						"status":        newStatus,
					})
					req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/api/tunnel-status", serverURL), bytes.NewBuffer(payload))
					if err == nil {
						req.Header.Set("Content-Type", "application/json")
						resp, err := http.DefaultClient.Do(req)
						if err == nil {
							_ = resp.Body.Close() //nolint:errcheck
						}
					}
				}
			}
		}
	}()
}

// AddRecord safely appends a record to the history buffer.
func (e *InterceptorEngine) AddRecord(rec *RequestRecord) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.History = append([]*RequestRecord{rec}, e.History...) // Prepend
	if len(e.History) > e.MaxHistory {
		e.History = e.History[:e.MaxHistory]
	}
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
