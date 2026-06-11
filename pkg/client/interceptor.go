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
}

// NewInterceptorEngine creates a new state engine for traffic inspection.
func NewInterceptorEngine(headers []string) *InterceptorEngine {
	headerMap := make(map[string]string)
	for _, h := range headers {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) == 2 {
			headerMap[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}

	return &InterceptorEngine{
		MaintenanceMode: false,
		Status:          "up",
		AddedHeaders:    headerMap,
		History:         make([]*RequestRecord, 0),
		MaxHistory:      100, // Keep last 100 requests
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
				conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", targetPort), 2*time.Second)
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

// InterceptPort creates a reverse proxy listening on a dynamic local port and forwarding to the targetPort.
func (e *InterceptorEngine) InterceptPort(targetPort int) (int, error) {
	// Start listener on dynamic port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}

	listenPort := listener.Addr().(*net.TCPAddr).Port

	targetURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", targetPort))
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
	for k, v := range req.Header {
		reqHeaders[k] = strings.Join(v, ", ")
	}

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
	for k, v := range res.Header {
		respHeaders[k] = strings.Join(v, ", ")
	}

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
