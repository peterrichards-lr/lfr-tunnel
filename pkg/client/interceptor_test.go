package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
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
		if _, err := w.Write([]byte("Target Response")); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
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

// TestInterceptorEngine_DialFailurePage verifies the fix for #980: a dial failure (nothing
// listening on the local target port) used to fall through to httputil.ReverseProxy's stock
// error handling -- a bare 502 with no body. Now serves a styled page instead, naming the
// unreachable target so the developer running the client has something actionable.
func TestInterceptorEngine_DialFailurePage(t *testing.T) {
	// Grab a port and immediately release it, so it's a real "nothing listening" refusal
	// rather than a made-up number that might collide with something else.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a port: %v", err)
	}
	targetPort := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("failed to release the reserved port: %v", err)
	}

	engine := NewInterceptorEngine("", nil)
	interceptPort, err := engine.InterceptPort(targetPort)
	if err != nil {
		t.Fatalf("Failed to intercept port: %v", err)
	}

	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", interceptPort)
	resp, err := http.Get(proxyURL)
	if err != nil {
		t.Fatalf("Failed to request proxy: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("Expected 502 on dial failure, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("Local Application Unreachable")) {
		t.Errorf("Expected the styled dial-failure page, got: %s", body)
	}
	wantTarget := fmt.Sprintf("127.0.0.1:%d", targetPort)
	if !bytes.Contains(body, []byte(wantTarget)) {
		t.Errorf("Expected the page to name the unreachable target %q, got: %s", wantTarget, body)
	}

	// interceptorTransport.RoundTrip should still have recorded the failed request.
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if len(engine.History) != 1 {
		t.Fatalf("Expected 1 history record, got %d", len(engine.History))
	}
	if engine.History[0].Status != http.StatusBadGateway {
		t.Errorf("Expected status 502 in history, got %d", engine.History[0].Status)
	}
}

func TestInterceptorEngine_CustomTargetHost(t *testing.T) {
	// 1. Setup Dummy Target Server
	var receivedHost string
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("OK")); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
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
	_ = respCustom.Body.Close() //nolint:errcheck

	// The Host header should have been rewritten to the targetHost (my-project.local)
	// (Since port is not 80/443, it will be my-project.local:targetPort)
	expectedHost := fmt.Sprintf("my-project.local:%d", targetPort)
	if receivedHost != expectedHost {
		t.Errorf("Expected Host header to be %s, got %s", expectedHost, receivedHost)
	}

}

func TestInterceptorEngine_PreserveHost(t *testing.T) {
	// 1. Setup Dummy Target Server
	var receivedHost string
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("OK")); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
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
	_ = resp.Body.Close() //nolint:errcheck

	// The Host header should be preserved as the public domain name
	if receivedHost != "preserved-subdomain.lfr-demo.se" {
		t.Errorf("Expected Host header to be preserved as 'preserved-subdomain.lfr-demo.se', got %s", receivedHost)
	}
}

func TestInterceptorEngine_ResponseRewriting(t *testing.T) {
	// 1. Setup Dummy Target Server returning redirects and cookies
	var targetServer *httptest.Server
	targetServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Location", targetServer.URL+"/group/control_panel")
		w.Header().Add("Set-Cookie", "JSESSIONID=abc; Domain=localhost; Path=/")
		w.Header().Add("Set-Cookie", "ANOTHER=xyz; Domain=127.0.0.1; Secure")
		w.WriteHeader(http.StatusFound)
	}))
	defer targetServer.Close()

	// Extract target port
	targetPort, _ := strconv.Atoi(targetServer.URL[len("http://127.0.0.1:"):])

	// 2. Setup Interceptor Engine
	engine := NewInterceptorEngine("127.0.0.1", nil)
	interceptPort, err := engine.InterceptPort(targetPort)
	if err != nil {
		t.Fatalf("Failed to intercept port: %v", err)
	}

	// 3. Make Request to Interceptor with Forwarded headers
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", interceptPort)
	req, _ := http.NewRequest("GET", proxyURL, nil)
	req.Header.Set("X-Forwarded-Host", "pjrtest.lfr-demo.online")
	req.Header.Set("X-Forwarded-Proto", "https")

	// Prevent redirect following for testing Location header value directly
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to request proxy: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	// 4. Assertions on rewritten response headers
	newLoc := resp.Header.Get("Location")
	expectedLoc := "https://pjrtest.lfr-demo.online/group/control_panel"
	if newLoc != expectedLoc {
		t.Errorf("Expected Location to be rewritten to '%s', got '%s'", expectedLoc, newLoc)
	}

	cookies := resp.Header["Set-Cookie"]
	if len(cookies) != 2 {
		t.Fatalf("Expected 2 Set-Cookie headers, got %d", len(cookies))
	}

	// Cleaned domains checks
	if cookies[0] != "JSESSIONID=abc; Path=/" {
		t.Errorf("Expected first cookie domain to be stripped, got '%s'", cookies[0])
	}
	if cookies[1] != "ANOTHER=xyz; Secure" {
		t.Errorf("Expected second cookie domain to be stripped, got '%s'", cookies[1])
	}
}

func TestInterceptorEngine_LargePayloads(t *testing.T) {
	// Generate 50KB payload
	largePayload := bytes.Repeat([]byte("A"), 50000)

	// 1. Setup Dummy Target Server validating large request payload and returning large response payload
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("Failed to read request body: %v", err)
		}
		if len(reqBody) != len(largePayload) {
			t.Errorf("Expected request body to be %d bytes, got %d", len(largePayload), len(reqBody))
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(largePayload); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
	}))
	defer targetServer.Close()

	// Extract target port
	targetPort, _ := strconv.Atoi(targetServer.URL[len("http://127.0.0.1:"):])

	// 2. Setup Interceptor Engine
	engine := NewInterceptorEngine("127.0.0.1", nil)
	interceptPort, err := engine.InterceptPort(targetPort)
	if err != nil {
		t.Fatalf("Failed to intercept port: %v", err)
	}

	// 3. Make Request to Interceptor with 50KB payload
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", interceptPort)
	req, _ := http.NewRequest("POST", proxyURL, bytes.NewReader(largePayload))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to request proxy: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	// Verify response status
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// 4. Assert response body length is fully 50KB and not truncated
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}
	if len(respBody) != len(largePayload) {
		t.Errorf("Expected response body to be %d bytes, got %d (truncated!)", len(largePayload), len(respBody))
	}

	// 5. Assert history captured only the first 10KB (10240 bytes)
	engine.mu.RLock()
	defer engine.mu.RUnlock()

	if len(engine.History) != 1 {
		t.Fatalf("Expected 1 history record, got %d", len(engine.History))
	}

	rec := engine.History[0]
	if len(rec.ReqBody) != 10240 {
		t.Errorf("Expected captured request body to be 10240 bytes, got %d", len(rec.ReqBody))
	}
	if len(rec.RespBody) != 10240 {
		t.Errorf("Expected captured response body to be 10240 bytes, got %d", len(rec.RespBody))
	}
}

func TestParseBandwidth(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		hasErr   bool
	}{
		{"", 0, false},
		{"1024", 1024, false},
		{"512kbps", 64000, false},
		{"1mbps", 125000, false},
		{"1mb/sec", 1000000, false},
		{"10kb/s", 1250, false},
		{"invalid", 0, true},
	}

	for _, tc := range tests {
		res, err := ParseBandwidth(tc.input)
		if tc.hasErr {
			if err == nil {
				t.Errorf("expected error for %q, got nil", tc.input)
			}
		} else {
			if err != nil {
				t.Errorf("unexpected error for %q: %v", tc.input, err)
			}
			if res != tc.expected {
				t.Errorf("expected %d for %q, got %d", tc.expected, tc.input, res)
			}
		}
	}
}

func TestThrottledReader_And_Latency(t *testing.T) {
	// 1. Setup target server
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("Hello Throttled!")); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
	}))
	defer targetServer.Close()

	// Extract target port
	targetPort, _ := strconv.Atoi(targetServer.URL[len("http://127.0.0.1:"):])

	// 2. Setup Interceptor Engine with simulated latency & bandwidth limit
	engine := NewInterceptorEngine("127.0.0.1", nil)
	engine.Latency = 100 * time.Millisecond // 100ms latency simulation
	engine.BandwidthLimit = 100             // Throttled at 100 bytes/second

	interceptPort, err := engine.InterceptPort(targetPort)
	if err != nil {
		t.Fatalf("Failed to intercept port: %v", err)
	}

	// 3. Make Request to Interceptor
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", interceptPort)
	startTime := time.Now()

	resp, err := http.Get(proxyURL)
	if err != nil {
		t.Fatalf("Failed request: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed reading response: %v", err)
	}

	duration := time.Since(startTime)

	if string(body) != "Hello Throttled!" {
		t.Errorf("Expected 'Hello Throttled!', got %q", string(body))
	}

	// Latency is 100ms, so duration must be at least 100ms
	if duration < 100*time.Millisecond {
		t.Errorf("Expected request duration to be at least 100ms due to latency injection, got %v", duration)
	}
}

func TestStartFailbackProber_PrimaryRecovered(t *testing.T) {
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/healthz" {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`{"status":"healthy"}`)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer primaryServer.Close()

	engine := NewInterceptorEngine("", nil)
	engine.SelectedRegion = "central"
	engine.FailbackProbeInterval = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientCanceled := make(chan struct{})
	cancelClient := func() {
		close(clientCanceled)
	}

	engine.StartFailbackProber(ctx, cancelClient, primaryServer.URL, "in")

	select {
	case <-clientCanceled:
		if !engine.IsFailback {
			t.Error("expected IsFailback to be true when primary region recovers")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for failback prober to cancel client")
	}
}

func TestStartFailbackProber_PrimaryStillOffline(t *testing.T) {
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer primaryServer.Close()

	engine := NewInterceptorEngine("", nil)
	engine.SelectedRegion = "central"
	engine.FailbackProbeInterval = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientCanceled := make(chan struct{})
	cancelClient := func() {
		close(clientCanceled)
	}

	engine.StartFailbackProber(ctx, cancelClient, primaryServer.URL, "in")

	select {
	case <-clientCanceled:
		t.Fatal("expected client NOT to be canceled when primary region is still offline")
	case <-time.After(2 * time.Second):
		if engine.IsFailback {
			t.Error("expected IsFailback to remain false when primary region is offline")
		}
	}
}

// A control-plane blip used to tear down every edge-served tunnel in the fleet at once: the
// heartbeat pinged both the serving gateway and central, and acted on an eviction response
// from either. Central answering 503 during a restart says something about central, not about
// a tunnel it is not in the data path for (#1306).
func TestStartHealthChecks_CentralOutageDoesNotDropAnEdgeServedTunnel(t *testing.T) {
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The serving gateway is healthy and still holds the lease.
		w.WriteHeader(http.StatusOK)
	}))
	defer edge.Close()

	central := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Central is restarting.
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer central.Close()

	engine := NewInterceptorEngine("", nil)
	engine.SetCentralURL(central.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientCanceled := make(chan struct{})
	engine.StartHealthChecks(ctx, func() { close(clientCanceled) }, edge.URL, "us", "session-token", []int{})

	select {
	case <-clientCanceled:
		t.Fatal("a control plane restart tore down a tunnel the control plane does not serve")
	case <-time.After(7 * time.Second):
		// Long enough for at least one 5s heartbeat tick to have hit both endpoints.
	}
}

// And the failover that #1147 and #1246 depend on must still fire when the gateway actually
// serving the session says the lease is gone.
func TestStartHealthChecks_ServingGatewayEvictionStillFailsOver(t *testing.T) {
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer edge.Close()

	engine := NewInterceptorEngine("", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientCanceled := make(chan struct{})
	engine.StartHealthChecks(ctx, func() { close(clientCanceled) }, edge.URL, "us", "session-token", []int{})

	select {
	case <-clientCanceled:
	case <-time.After(10 * time.Second):
		t.Fatal("expected failover when the serving gateway reports the lease evicted")
	}
}

// A draining gateway is about to restart. Electing it means being moved again within seconds,
// so a client must decline it even though it is healthy in every other respect (#1238).
func TestGatewayCanCarrySession_DeclinesADrainingGateway(t *testing.T) {
	cases := map[string]struct {
		body string
		want bool
	}{
		"healthy":              {`{"status":"healthy","control_plane":"connected"}`, true},
		"draining":             {`{"status":"draining","control_plane":"connected"}`, false},
		"draining, no control": {`{"status":"draining"}`, false},
		"degraded":             {`{"status":"degraded","control_plane":"disconnected"}`, false},
		"healthy standalone":   {`{"status":"healthy"}`, true},
		"unparseable but 200":  {`not json`, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := GatewayCanCarrySession(http.StatusOK, []byte(tc.body)); got != tc.want {
				t.Errorf("GatewayCanCarrySession(%s) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

// The gate has to stop the prober BEFORE it cancels the session. Checking after the fact is
// too late: cancelling is itself the interruption, and the client then re-registers anyway
// via the ordinary failover path (#1310).
func TestStartFailbackProber_GateStopsItBeforeCancelling(t *testing.T) {
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer primaryServer.Close()

	engine := NewInterceptorEngine("", nil)
	engine.SelectedRegion = "us"
	engine.FailbackProbeInterval = 50 * time.Millisecond
	engine.SetFailbackGate(func(string) bool { return false })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientCanceled := make(chan struct{})
	engine.StartFailbackProber(ctx, func() { close(clientCanceled) }, primaryServer.URL, "eu")

	select {
	case <-clientCanceled:
		t.Fatal("the prober tore down the session for a failback the gate had refused")
	case <-time.After(1 * time.Second):
	}

	if engine.ConsumeFailback() {
		t.Error("a refused failback was still announced to the caller")
	}
}

// And with no gate, or an allowing one, failback works exactly as before.
func TestStartFailbackProber_AllowingGateStillFailsBack(t *testing.T) {
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer primaryServer.Close()

	engine := NewInterceptorEngine("", nil)
	engine.SelectedRegion = "us"
	engine.FailbackProbeInterval = 50 * time.Millisecond
	engine.SetFailbackGate(func(string) bool { return true })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientCanceled := make(chan struct{})
	engine.StartFailbackProber(ctx, func() { close(clientCanceled) }, primaryServer.URL, "eu")

	select {
	case <-clientCanceled:
	case <-time.After(3 * time.Second):
		t.Fatal("an allowed failback never happened")
	}
}
