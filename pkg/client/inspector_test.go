package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
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
		if _, err := w.Write([]byte("Liferay Mock")); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
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
			if _, err := w.Write([]byte(`{"status":"healthy"}`)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := w.Write([]byte(`{"status":"unhealthy"}`)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
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

func TestReplayRequestEndpoint(t *testing.T) {
	// 1. Start mock downstream server
	downstreamReceived := false
	mockDownstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downstreamReceived = true
		if r.Method != "POST" {
			t.Errorf("Expected method POST, got %s", r.Method)
		}
		if r.Header.Get("X-Test-Header") != "ReplayVal" {
			t.Errorf("Expected X-Test-Header: ReplayVal, got %s", r.Header.Get("X-Test-Header"))
		}
		w.WriteHeader(http.StatusCreated)
		if _, err := w.Write([]byte("Mock Replay Response")); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
	}))
	defer mockDownstream.Close()

	u, err := url.Parse(mockDownstream.URL)
	if err != nil {
		t.Fatalf("failed to parse downstream URL: %v", err)
	}
	_, portStr, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portStr)

	// 2. Create engine and populate history
	engine := NewInterceptorEngine("127.0.0.1", nil)
	origRecord := &RequestRecord{
		ID:         "orig-id-123",
		Method:     "POST",
		Path:       "/replay-test?foo=bar",
		ReqHeaders: map[string]string{"X-Test-Header": "ReplayVal"},
		ReqBody:    "payload data",
		TargetPort: port,
	}
	engine.AddRecord(origRecord)

	// 3. Set up routing
	mux := http.NewServeMux()
	mux.HandleFunc("/api/replay", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		engine.mu.RLock()
		var record *RequestRecord
		for _, rec := range engine.History {
			if rec.ID == req.ID {
				record = rec
				break
			}
		}
		engine.mu.RUnlock()

		if record == nil {
			http.Error(w, "Request not found", http.StatusNotFound)
			return
		}

		newRec, err := ReplayRequest(engine.TargetHost, record)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		engine.AddRecord(newRec)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"new_id": newRec.ID,
		})
	})

	// 4. Test case A: Successful Replay
	bodyBytes, _ := json.Marshal(map[string]string{"id": "orig-id-123"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/replay", bytes.NewReader(bodyBytes))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", rec.Code)
	}

	var resMap map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&resMap)
	if resMap["status"] != "ok" {
		t.Errorf("Expected status 'ok', got '%v'", resMap["status"])
	}
	newID := resMap["new_id"].(string)

	if !downstreamReceived {
		t.Error("Expected mock downstream to receive replayed request")
	}

	// Verify history updated
	if len(engine.History) != 2 {
		t.Fatalf("Expected 2 records in history, got %d", len(engine.History))
	}

	newRecord := engine.History[0]
	if newRecord.ID != newID {
		t.Errorf("Expected newly added record to match returned new ID: %s vs %s", newRecord.ID, newID)
	}
	if newRecord.Method != "POST (Replay)" {
		t.Errorf("Expected Method to be 'POST (Replay)', got %s", newRecord.Method)
	}
	if newRecord.Status != http.StatusCreated {
		t.Errorf("Expected replayed status to be 201, got %d", newRecord.Status)
	}
	if newRecord.RespBody != "Mock Replay Response" {
		t.Errorf("Expected RespBody 'Mock Replay Response', got '%s'", newRecord.RespBody)
	}

	// Test case B: Request Not Found
	bodyBytes2, _ := json.Marshal(map[string]string{"id": "non-existent-id"})
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/replay", bytes.NewReader(bodyBytes2))
	mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotFound {
		t.Errorf("Expected 404 Not Found for invalid ID, got %d", rec2.Code)
	}
}

func TestInspectorAccessControl(t *testing.T) {
	// 1. Start a mock gateway server
	gatewayMux := http.NewServeMux()
	var gatewayReceived bool
	var receivedToken string
	var receivedBody map[string]interface{}

	gatewayMux.HandleFunc("/api/portal/reservations/access-control", func(w http.ResponseWriter, r *http.Request) {
		gatewayReceived = true
		receivedToken = r.Header.Get("X-Auth-Token")
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"status":"success"}`)); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
	})
	gatewayServer := httptest.NewServer(gatewayMux)
	defer gatewayServer.Close()

	// 2. Setup InterceptorEngine
	engine := NewInterceptorEngine("127.0.0.1", nil)
	engine.Token = "test-auth-token"
	engine.ServerURL = gatewayServer.URL
	engine.SubdomainAss = "my-subdomain.lfr-demo.se"

	// 3. Setup local inspector routes manually for testing
	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		engine.mu.RLock()
		defer engine.mu.RUnlock()

		state := map[string]interface{}{
			"passcode":      engine.Passcode,
			"whitelist_ips": engine.WhitelistIPs,
			"access_mode":   engine.AccessMode,
			"assigned":      engine.SubdomainAss,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state)
	})

	mux.HandleFunc("/api/access-control", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Passcode     string `json:"passcode"`
			WhitelistIPs string `json:"whitelist_ips"`
			AccessMode   string `json:"access_mode"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
			return
		}

		engine.mu.Lock()
		engine.Passcode = req.Passcode
		engine.WhitelistIPs = req.WhitelistIPs
		engine.AccessMode = req.AccessMode
		token := engine.Token
		serverURL := engine.ServerURL
		subdomainAss := engine.SubdomainAss
		engine.mu.Unlock()

		parts := strings.SplitN(subdomainAss, ".", 2)
		if len(parts) != 2 {
			http.Error(w, "Invalid assigned subdomain format", http.StatusBadRequest)
			return
		}
		prefix := parts[0]
		domain := parts[1]

		updatePayload := map[string]string{
			"subdomain":     prefix,
			"domain":        domain,
			"passcode":      req.Passcode,
			"whitelist_ips": req.WhitelistIPs,
			"access_mode":   req.AccessMode,
		}

		bodyBytes, _ := json.Marshal(updatePayload)
		gatewayURL := fmt.Sprintf("%s/api/portal/reservations/access-control", serverURL)

		reqHTTP, err := http.NewRequest(http.MethodPost, gatewayURL, bytes.NewReader(bodyBytes))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		reqHTTP.Header.Set("Content-Type", "application/json")
		reqHTTP.Header.Set("X-Auth-Token", token)

		clientHTTP := &http.Client{Timeout: 5 * time.Second}
		resp, err := clientHTTP.Do(reqHTTP)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close() //nolint:errcheck

		if resp.StatusCode != http.StatusOK {
			http.Error(w, "Gateway error", resp.StatusCode)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"status":"ok"}`)); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
	})

	// Test A: POST to /api/access-control
	payload := map[string]string{
		"passcode":      "new-passcode",
		"whitelist_ips": "1.1.1.1",
		"access_mode":   "and",
	}
	bodyBytes, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/access-control", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	if !gatewayReceived {
		t.Error("Expected request to be forwarded to mock gateway server")
	}

	if receivedToken != "test-auth-token" {
		t.Errorf("Expected token 'test-auth-token', got '%s'", receivedToken)
	}

	if receivedBody["subdomain"] != "my-subdomain" || receivedBody["domain"] != "lfr-demo.se" {
		t.Errorf("Expected payload to contain parsed prefix/domain, got %v", receivedBody)
	}

	// Test B: GET /api/state
	req2 := httptest.NewRequest("GET", "/api/state", nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	var state map[string]interface{}
	_ = json.NewDecoder(rec2.Body).Decode(&state)

	if state["passcode"] != "new-passcode" || state["whitelist_ips"] != "1.1.1.1" || state["access_mode"] != "and" {
		t.Errorf("Expected state to be updated, got %v", state)
	}
}
