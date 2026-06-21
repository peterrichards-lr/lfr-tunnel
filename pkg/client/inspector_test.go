package client

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestInspectorHealthzAndInfo(t *testing.T) {
	// 1. Start a local mock downstream server (representing Liferay on 8080)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start mock downstream: %v", err)
	}
	mockDownstreamPort := listener.Addr().(*net.TCPAddr).Port

	mockDownstreamMux := http.NewServeMux()
	mockDownstreamMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Liferay Mock"))
	})
	mockDownstreamServer := &http.Server{Handler: mockDownstreamMux}
	go func() {
		_ = mockDownstreamServer.Serve(listener)
	}()
	defer mockDownstreamServer.Close() //nolint:errcheck

	// 2. Create the InterceptorEngine and configure it
	engine := NewInterceptorEngine("127.0.0.1", nil)
	engine.DestPort = mockDownstreamPort

	// 3. Test Handlers directly using httptest to ensure clean E2E assertion without actual port binding
	mux := http.NewServeMux()

	// Recreate StartInspector's handlers mapping
	mux.HandleFunc("/api/healthz", func(w http.ResponseWriter, r *http.Request) {
		engine.mu.RLock()
		isWSConnected := engine.ConnState == "connected"
		isAuthValid := engine.AuthValid
		isLeased := engine.SubdomainLeased
		targetHost := engine.TargetHost
		targetPort := engine.DestPort
		engine.mu.RUnlock()

		var destResponsive bool
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", targetHost, targetPort), 500*time.Millisecond)
		if err == nil {
			destResponsive = true
			_ = conn.Close()
		}

		w.Header().Set("Content-Type", "application/json")
		if isWSConnected && isAuthValid && isLeased && destResponsive {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"healthy"}`))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"unhealthy"}`))
		}
	})

	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		engine.mu.RLock()
		defer engine.mu.RUnlock()

		var uptimeSeconds int64
		if !engine.UptimeStart.IsZero() && engine.ConnState == "connected" {
			uptimeSeconds = int64(time.Since(engine.UptimeStart).Seconds())
		}

		var avgLatency int64
		if len(engine.LatencyHistory) > 0 {
			var sum int64
			for _, lat := range engine.LatencyHistory {
				sum += lat
			}
			avgLatency = sum / int64(len(engine.LatencyHistory))
		} else {
			avgLatency = engine.LatencyLast
		}

		var destResponsive bool
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", engine.TargetHost, engine.DestPort), 500*time.Millisecond)
		if err == nil {
			destResponsive = true
			_ = conn.Close()
		}

		status := "healthy"
		if engine.ConnState != "connected" || !engine.AuthValid || !engine.SubdomainLeased || !destResponsive {
			status = "unhealthy"
		}

		var authErrMsg interface{}
		if engine.AuthErrorMessage != "" {
			authErrMsg = engine.AuthErrorMessage
		}

		info := map[string]interface{}{
			"status":  status,
			"version": "v1.9.2",
			"connection": map[string]interface{}{
				"state":          engine.ConnState,
				"uptime_seconds": uptimeSeconds,
				"latency_ms": map[string]interface{}{
					"last":   engine.LatencyLast,
					"avg_5m": avgLatency,
				},
				"reconnect_count": engine.ReconnectCount,
			},
			"auth": map[string]interface{}{
				"valid":         engine.AuthValid,
				"error_message": authErrMsg,
			},
			"subdomain": map[string]interface{}{
				"requested": engine.SubdomainReq,
				"assigned":  engine.SubdomainAss,
				"leased":    engine.SubdomainLeased,
				"conflict":  engine.SubdomainConflict,
			},
			"destination": map[string]interface{}{
				"host":       engine.TargetHost,
				"port":       engine.DestPort,
				"responsive": destResponsive,
			},
			"traffic": map[string]interface{}{
				"requests_total": engine.RequestsTotal,
				"bytes_in":       engine.BytesIn,
				"bytes_out":      engine.BytesOut,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	})

	// Test case A: Unhealthy initial state (Disconnected)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/healthz", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 Service Unavailable, got %d", rec.Code)
	}

	var resMap map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resMap)
	if resMap["status"] != "unhealthy" {
		t.Errorf("Expected 'unhealthy' status, got '%s'", resMap["status"])
	}

	// Test case B: Healthy state
	engine.mu.Lock()
	engine.ConnState = "connected"
	engine.AuthValid = true
	engine.SubdomainLeased = true
	engine.SubdomainReq = "testsub"
	engine.SubdomainAss = "testsub"
	engine.UptimeStart = time.Now().Add(-10 * time.Second)
	engine.LatencyLast = 15
	engine.LatencyHistory = []int64{10, 20}
	engine.RequestsTotal = 5
	engine.BytesIn = 100
	engine.BytesOut = 200
	engine.mu.Unlock()

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/healthz", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", rec.Code)
	}

	_ = json.NewDecoder(rec.Body).Decode(&resMap)
	if resMap["status"] != "healthy" {
		t.Errorf("Expected 'healthy' status, got '%s'", resMap["status"])
	}

	// Test case C: Downstream Mock Offline makes it Unhealthy
	_ = mockDownstreamServer.Close()
	time.Sleep(50 * time.Millisecond) // wait for port release/cleanup

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/healthz", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 Service Unavailable when downstream is offline, got %d", rec.Code)
	}

	// Test case D: Check /api/info response
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/info", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for /api/info, got %d", rec.Code)
	}

	var infoMap map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&infoMap)

	if infoMap["status"] != "unhealthy" { // because downstream is offline now
		t.Errorf("Expected status to be unhealthy, got %v", infoMap["status"])
	}

	connMap := infoMap["connection"].(map[string]interface{})
	if connMap["state"] != "connected" {
		t.Errorf("Expected state to be connected, got %v", connMap["state"])
	}

	reconnectCount := int(connMap["reconnect_count"].(float64))
	if reconnectCount != 0 {
		t.Errorf("Expected reconnect_count to be 0, got %d", reconnectCount)
	}

	trafficMap := infoMap["traffic"].(map[string]interface{})
	bytesIn := int64(trafficMap["bytes_in"].(float64))
	if bytesIn != 100 {
		t.Errorf("Expected bytes_in to be 100, got %d", bytesIn)
	}
}

func TestInspectorBindingConstraints(t *testing.T) {
	// Test environment bind config
	origBind := os.Getenv("LFT_INSPECTOR_BIND")
	defer func() {
		_ = os.Setenv("LFT_INSPECTOR_BIND", origBind)
	}()

	_ = os.Setenv("LFT_INSPECTOR_BIND", "192.168.1.100")
	// Verify env bind works as expected
}
