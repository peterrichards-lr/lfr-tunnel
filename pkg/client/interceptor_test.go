package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
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
		_, _ = w.Write([]byte("Target Response"))
	}))
	defer targetServer.Close()

	// Extract target port
	targetPort, _ := strconv.Atoi(targetServer.URL[len("http://127.0.0.1:"):])

	// 2. Setup Interceptor Engine
	engine := NewInterceptorEngine("", []string{"X-Injected: true"})
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
	defer resp.Body.Close() //nolint:errcheck

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
	engine := NewInterceptorEngine("", nil)

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
	defer resp.Body.Close() //nolint:errcheck

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

func TestInterceptorEngine_CustomTargetHost(t *testing.T) {
	// 1. Setup Dummy Target Server
	var receivedHost string
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))
	defer targetServer.Close()

	// Extract target port
	targetPort, _ := strconv.Atoi(targetServer.URL[len("http://127.0.0.1:"):])

	// 2. Mock DNS resolution by overriding DefaultTransport.DialContext
	originalDial := http.DefaultTransport.(*http.Transport).DialContext
	defer func() {
		http.DefaultTransport.(*http.Transport).DialContext = originalDial
	}()

	http.DefaultTransport.(*http.Transport).DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		if host == "my-project.local" || host == "host.docker.internal" {
			return (&net.Dialer{}).DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", targetPort))
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}

	// Test case 1: Custom target domain name (should rewrite Host header)
	engineCustom := NewInterceptorEngine("my-project.local", nil)
	interceptPortCustom, err := engineCustom.InterceptPort(targetPort)
	if err != nil {
		t.Fatalf("Failed to intercept port: %v", err)
	}

	reqCustom, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d", interceptPortCustom), nil)
	reqCustom.Host = "public-subdomain.lfr-demo.se"
	respCustom, err := http.DefaultClient.Do(reqCustom)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	_ = respCustom.Body.Close()

	// The Host header should have been rewritten to the targetHost (my-project.local)
	// (Since port is not 80/443, it will be my-project.local:targetPort)
	expectedHost := fmt.Sprintf("my-project.local:%d", targetPort)
	if receivedHost != expectedHost {
		t.Errorf("Expected Host header to be %s, got %s", expectedHost, receivedHost)
	}

	// Test case 2: Loopback/docker target (should NOT rewrite Host header, preserving public Host)
	engineLoopback := NewInterceptorEngine("host.docker.internal", nil)
	interceptPortLoopback, err := engineLoopback.InterceptPort(targetPort)
	if err != nil {
		t.Fatalf("Failed to intercept port: %v", err)
	}

	reqLoopback, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d", interceptPortLoopback), nil)
	reqLoopback.Host = "public-subdomain.lfr-demo.se"
	respLoopback, err := http.DefaultClient.Do(reqLoopback)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	_ = respLoopback.Body.Close()

	// The Host header should be preserved as the public domain name
	if receivedHost != "public-subdomain.lfr-demo.se" {
		t.Errorf("Expected Host header to be preserved as 'public-subdomain.lfr-demo.se', got %s", receivedHost)
	}
}

func TestInterceptorEngine_PreserveHost(t *testing.T) {
	// 1. Setup Dummy Target Server
	var receivedHost string
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))
	defer targetServer.Close()

	// Extract target port
	targetPort, _ := strconv.Atoi(targetServer.URL[len("http://127.0.0.1:"):])

	// 2. Mock DNS resolution by overriding DefaultTransport.DialContext
	originalDial := http.DefaultTransport.(*http.Transport).DialContext
	defer func() {
		http.DefaultTransport.(*http.Transport).DialContext = originalDial
	}()

	http.DefaultTransport.(*http.Transport).DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		if host == "custom-target.local" {
			return (&net.Dialer{}).DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", targetPort))
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}

	// Set env var to true
	t.Setenv("LFT_PRESERVE_HOST", "true")

	// Custom target domain name (with PreserveHost=true, should NOT rewrite Host header)
	engine := NewInterceptorEngine("custom-target.local", nil)
	if !engine.PreserveHost {
		t.Errorf("Expected PreserveHost to be true")
	}

	interceptPort, err := engine.InterceptPort(targetPort)
	if err != nil {
		t.Fatalf("Failed to intercept port: %v", err)
	}

	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d", interceptPort), nil)
	req.Host = "preserved-subdomain.lfr-demo.se"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	_ = resp.Body.Close()

	// The Host header should be preserved as the public domain name
	if receivedHost != "preserved-subdomain.lfr-demo.se" {
		t.Errorf("Expected Host header to be preserved as 'preserved-subdomain.lfr-demo.se', got %s", receivedHost)
	}
}
