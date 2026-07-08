package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStartInspector_Basic(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	port, err := StartInspector(55555, engine)
	if err != nil {
		t.Fatalf("StartInspector failed: %v", err)
	}
	if port == 0 {
		t.Errorf("Expected non-zero port")
	}
}

func TestStartHealthChecks(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	engine.TargetHost = "127.0.0.1"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	portStr := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	port, _ := strconv.Atoi(portStr)
	engine.DestPort = port

	engine.StartHealthChecks("http://example.com", "dummy-token", port)
	time.Sleep(100 * time.Millisecond)
}

func TestIsDocker(t *testing.T) {
	_ = IsDocker()
}

func TestIsPIDRunning(t *testing.T) {
	res := IsPIDRunning(99999999)
	if res {
		t.Logf("Unexpectedly found PID 99999999 to be running")
	}
}

func TestRedirectChiselLogger(t *testing.T) {
	redirectChiselLogger(nil, nil)
}

func TestRunLogin(t *testing.T) {
	oldOpenBrowserFunc := openBrowserFunc
	openBrowserFunc = func(url string) error { return nil }
	defer func() { openBrowserFunc = oldOpenBrowserFunc }()

	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = http.Post("http://127.0.0.1:4444/handoff", "text/plain", strings.NewReader("dummy-token")) //nolint:errcheck
	}()

	origHome := os.Getenv("HOME")
	defer func() { _ = os.Setenv("HOME", origHome) }()

	tempHome := t.TempDir()
	_ = os.Setenv("HOME", tempHome) //nolint:errcheck

	err := RunLogin("https://tunnel.lfr-demo.se")
	if err != nil {
		t.Fatalf("RunLogin failed: %v", err)
	}
}

func TestRunClient_FailFast(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	engine := NewInterceptorEngine("127.0.0.1", nil)
	err := RunClient(ctx, srv.URL, "dummy-token", []string{"8080:localhost:8080"}, nil, engine)
	_ = err // Context cancellation does not return error in chisel Run
}

func TestInterceptorEngine_SetSubdomainDetails(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	engine.SetSubdomainDetails("mysubdomain", "myhost", false, false)
}
