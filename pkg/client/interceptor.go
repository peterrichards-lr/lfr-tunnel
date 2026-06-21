package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
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
	mu              sync.RWMutex
	MaintenanceMode bool
	Status          string
	AddedHeaders    map[string]string
	History         []*RequestRecord
	MaxHistory      int
	TargetHost      string
	PreserveHost    bool

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
	SubdomainLeased   bool
	SubdomainConflict bool
	DestPort          int

	// Traffic stats
	RequestsTotal int64
	BytesIn       int64
	BytesOut      int64
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
		MaintenanceMode: false,
		Status:          "up",
		AddedHeaders:    headerMap,
		History:         make([]*RequestRecord, 0),
		MaxHistory:      100, // Keep last 100 requests
		TargetHost:      targetHost,
		PreserveHost:    preserveHost,
		ConnState:       "disconnected",
		AuthValid:       true,
		DestPort:        8080, // Default Liferay port
		LatencyHistory:  make([]int64, 0),
	}
}

// StartHealthChecks begins a background loop to verify Tomcat is responding and reports status to the Gateway.
func (e *InterceptorEngine) StartHealthChecks(serverURL, sessionToken string, targetPort int) {
	go func() {
		for {
			time.Sleep(5 * time.Second)

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
				resp, err := http.Post(fmt.Sprintf("%s/api/tunnel-status", serverURL), "application/json", bytes.NewBuffer(payload))
				if err == nil {
					_ = resp.Body.Close()
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

	targetURL, _ := url.Parse(fmt.Sprintf("http://%s:%d", e.TargetHost, targetPort))
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Custom Transport to capture response and duration
	proxy.Transport = &interceptorTransport{
		engine:     e,
		targetPort: targetPort,
		transport:  http.DefaultTransport,
	}

	// Custom Director to inject headers
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)

		// Rewrite Host header if target is a custom virtual host
		if !e.PreserveHost && e.TargetHost != "localhost" && e.TargetHost != "127.0.0.1" && e.TargetHost != "host.docker.internal" {
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
			serveMaintenancePage(w)
			return
		}

		proxy.ServeHTTP(w, r)
	})

	// Run in background
	go func() {
		if err := http.Serve(listener, handler); err != nil {
			log.Printf("[Interceptor] Proxy on port %d crashed: %v", listenPort, err)
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
	startTime := time.Now()

	// Capture request body (up to 10KB)
	var reqBodyStr string
	if req.Body != nil {
		bodyBytes, _ := io.ReadAll(io.LimitReader(req.Body, 10240))
		reqBodyStr = string(bodyBytes)
		req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
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
	duration := time.Since(startTime).Milliseconds()

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
		bodyBytes, _ := io.ReadAll(io.LimitReader(res.Body, 10240))
		respBodyStr = string(bodyBytes)
		res.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
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

func serveMaintenancePage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`
	<!DOCTYPE html>
	<html>
	<head>
		<title>Maintenance Mode</title>
		<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;600;700&display=swap" rel="stylesheet">
		<style>
			body { font-family: 'Inter', sans-serif; background: #0f172a; color: white; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; }
			.card { background: rgba(30,41,59,0.8); padding: 40px; border-radius: 12px; text-align: center; border: 1px solid rgba(255,255,255,0.1); max-width: 500px; }
			h1 { color: #f59e0b; margin-top: 0; }
			p { color: #94a3b8; line-height: 1.6; }
		</style>
	</head>
	<body>
		<div class="card">
			<h1>Down for Maintenance</h1>
			<p>The developer has temporarily paused this tunnel for maintenance. Please check back shortly.</p>
		</div>
	</body>
	</html>
	`))
}

func getHostHeaderValue(host string, port int) string {
	if port == 80 || port == 443 {
		return host
	}
	return fmt.Sprintf("%s:%d", host, port)
}
