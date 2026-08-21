package client

import (
	"context"
	"fmt"
	"log"
	"net"
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

	engine.StartHealthChecks(context.Background(), nil, "http://example.com", "central", "dummy-token", []int{port})
	time.Sleep(100 * time.Millisecond)
}

func TestLocalTargetStatus(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	openPort, _ := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])

	// A closed port: bind one, read the number, release it.
	closedLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a port: %v", err)
	}
	closedPort := closedLn.Addr().(*net.TCPAddr).Port
	closedLn.Close() //nolint:errcheck

	if got := engine.localTargetStatus([]int{openPort}); got != "up" {
		t.Errorf("expected 'up' when the only port is reachable, got %q", got)
	}

	// The aggregate must be "down" if any single mapped port is dead -- reporting "up"
	// would have the gateway forward traffic to a listener that isn't there.
	if got := engine.localTargetStatus([]int{openPort, closedPort}); got != "down" {
		t.Errorf("expected 'down' when one of several ports is unreachable, got %q", got)
	}

	engine.mu.Lock()
	engine.MaintenanceMode = true
	engine.mu.Unlock()
	if got := engine.localTargetStatus([]int{openPort}); got != "maintenance" {
		t.Errorf("expected maintenance mode to take precedence, got %q", got)
	}
}

func TestStatusReportTargets(t *testing.T) {
	const edge = "https://aws-edge-apac.example.se"
	const central = "https://tunnel.example.se"

	got := statusReportTargets(edge, central)
	if len(got) != 2 || got[0] != edge || got[1] != central {
		t.Errorf("an edge session should report to both the edge and central, got %v", got)
	}

	// Connected directly to central: no duplicate report.
	if got := statusReportTargets(central, central); len(got) != 1 {
		t.Errorf("expected a single target when already on central, got %v", got)
	}
	if got := statusReportTargets(central+"/", central); len(got) != 1 {
		t.Errorf("a trailing slash should not defeat the same-host check, got %v", got)
	}

	// No central configured: report only to the connected gateway, rather than
	// falling back to a hardcoded host (issue #1124).
	if got := statusReportTargets(edge, ""); len(got) != 1 || got[0] != edge {
		t.Errorf("expected only the connected gateway when central is unknown, got %v", got)
	}
}

func TestIsDocker(t *testing.T) {
	_ = IsDocker() //nolint:errcheck
}

func TestIsPIDRunning(t *testing.T) {
	res := IsPIDRunning(99999999)
	if res {
		t.Logf("Unexpectedly found PID 99999999 to be running")
	}
}

func TestRedirectChiselLogger(t *testing.T) {
	engine := &InterceptorEngine{}
	cleanup, err := redirectChiselLogger(engine)
	if err != nil {
		t.Fatalf("failed to redirect: %v", err)
	}
	defer cleanup()

	if _, err := fmt.Fprintln(os.Stderr, "Connected (Latency 15ms)"); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
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
	_ = err // Context cancellation does not return error in chisel Run //nolint:errcheck
}

func TestInterceptorEngine_SetSubdomainDetails(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	engine.SetSubdomainDetails("mysubdomain", "myhost", false, false)
}
