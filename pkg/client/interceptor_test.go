package client

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestInterceptorEngine_HeaderInjection(t *testing.T) {
	// 1. Setup Dummy Target Server
	targetHit := false
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHit = true
		// Verify Header Injection
		if r.Header.Get("X-Injected") != "true" {
			t.Errorf("Expected X-Injected header to be 'true'")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Target Response"))
	}))
	defer targetServer.Close()

	// Extract target port
	targetPort, _ := strconv.Atoi(targetServer.URL[len("http://127.0.0.1:"):])

	// 2. Setup Interceptor Engine
	engine := NewInterceptorEngine([]string{"X-Injected: true"})
	interceptPort, err := engine.InterceptPort(targetPort)
	if err != nil {
		t.Fatalf("Failed to intercept port: %v", err)
	}

	// 3. Make Request to Interceptor
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", interceptPort)
	resp, err := http.Get(proxyURL)
	if err != nil {
		t.Fatalf("Failed to request proxy: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "Target Response" {
		t.Errorf("Expected 'Target Response', got %s", string(body))
	}

	if !targetHit {
		t.Errorf("Target server was never hit")
	}

	// 4. Verify History Buffer
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if len(engine.History) != 1 {
		t.Fatalf("Expected 1 history record, got %d", len(engine.History))
	}
	rec := engine.History[0]
	if rec.Status != http.StatusOK {
		t.Errorf("Expected status 200 in history, got %d", rec.Status)
	}
}

func TestInterceptorEngine_MaintenanceMode(t *testing.T) {
	// 1. Setup Dummy Target Server
	targetHit := false
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer targetServer.Close()

	targetPort, _ := strconv.Atoi(targetServer.URL[len("http://127.0.0.1:"):])

	// 2. Setup Interceptor Engine
	engine := NewInterceptorEngine(nil)

	// Enable Maintenance Mode
	engine.mu.Lock()
	engine.MaintenanceMode = true
	engine.mu.Unlock()

	interceptPort, err := engine.InterceptPort(targetPort)
	if err != nil {
		t.Fatalf("Failed to intercept port: %v", err)
	}

	// 3. Make Request to Interceptor
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", interceptPort)
	resp, err := http.Get(proxyURL)
	if err != nil {
		t.Fatalf("Failed to request proxy: %v", err)
	}
	defer resp.Body.Close()

	// 4. Assertions
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 Maintenance Mode, got %d", resp.StatusCode)
	}

	if targetHit {
		t.Errorf("Target server should not have been hit while in maintenance mode")
	}

	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("Maintenance Mode")) {
		t.Errorf("Expected maintenance offline HTML")
	}
}
