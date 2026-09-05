package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// fakeHome points os.UserHomeDir at a temp directory so a login test writes its token
// there rather than into the real home directory. USERPROFILE matters too: the test suite
// runs on windows-latest, where os.UserHomeDir reads that and ignores HOME.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestRunLogin(t *testing.T) {
	oldOpenBrowserFunc := openBrowserFunc
	openBrowserFunc = func(url string) error { return nil }
	defer func() { openBrowserFunc = oldOpenBrowserFunc }()

	// Bind an ephemeral port rather than the product's fixed 4444, and post to whatever
	// it hands back. The old test hardcoded 4444 on both sides, so any unrelated process
	// on the machine -- Selenium Grid, a debugger, or a socket left behind by a SIGKILLed
	// test run -- failed it for reasons that had nothing to do with this code (#1718).
	//
	// Publishing the address also removes the sleep this test used to race on: the POST
	// now cannot be fired before the listener exists, rather than merely being unlikely to.
	addrChan := make(chan string, 1)
	oldListen := listenHandoff
	listenHandoff = func() (net.Listener, error) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		addrChan <- l.Addr().String()
		return l, nil
	}
	defer func() { listenHandoff = oldListen }()

	go func() {
		resp, err := http.Post("http://"+(<-addrChan)+"/handoff", "text/plain", strings.NewReader("dummy-token"))
		if err != nil {
			return
		}
		if err := resp.Body.Close(); err != nil {
			log.Printf("[Warning] Failed to close handoff response body: %v", err)
		}
	}()

	home := fakeHome(t)

	if err := RunLogin("https://tunnel.lfr-demo.se"); err != nil {
		t.Fatalf("RunLogin failed: %v", err)
	}

	saved, err := os.ReadFile(filepath.Join(home, ".lfr-tunnel", "token"))
	if err != nil {
		t.Fatalf("expected the handed-off token to be saved: %v", err)
	}
	if string(saved) != "dummy-token" {
		t.Errorf("saved token = %q, want %q", saved, "dummy-token")
	}
}

// TestRunLogin_PortBusyFallsBackToManualPaste is the #1718 regression: an occupied 4444
// must degrade to the paste-it-yourself path that already exists, not abort the login.
// The port cannot be moved -- the portal's browser JavaScript POSTs to the literal
// 127.0.0.1:4444 (pkg/server/static/dashboard.js) -- so degrading is the whole fix.
func TestRunLogin_PortBusyFallsBackToManualPaste(t *testing.T) {
	oldOpenBrowserFunc := openBrowserFunc
	openBrowserFunc = func(url string) error { return nil }
	defer func() { openBrowserFunc = oldOpenBrowserFunc }()

	oldListen := listenHandoff
	listenHandoff = func() (net.Listener, error) {
		return nil, errors.New("listen tcp 127.0.0.1:4444: bind: address already in use")
	}
	defer func() { listenHandoff = oldListen }()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdin pipe: %v", err)
	}
	origStdin := os.Stdin
	os.Stdin = stdinR
	defer func() { os.Stdin = origStdin }()
	go func() {
		if _, err := stdinW.WriteString("pasted-token\n"); err != nil {
			log.Printf("[Warning] Failed to write to stdin pipe: %v", err)
		}
		if err := stdinW.Close(); err != nil {
			log.Printf("[Warning] Failed to close stdin pipe: %v", err)
		}
	}()

	origStdout := os.Stdout
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	os.Stdout = outW
	done := make(chan struct{})
	var output string
	go func() {
		defer close(done)
		buf, _ := io.ReadAll(outR)
		output = string(buf)
	}()

	home := fakeHome(t)

	runErr := RunLogin("https://tunnel.lfr-demo.se")

	if err := outW.Close(); err != nil {
		t.Fatalf("failed to close stdout pipe: %v", err)
	}
	os.Stdout = origStdout
	<-done

	if runErr != nil {
		t.Fatalf("a busy handoff port must not fail the login, got: %v", runErr)
	}

	saved, err := os.ReadFile(filepath.Join(home, ".lfr-tunnel", "token"))
	if err != nil {
		t.Fatalf("expected the pasted token to be saved: %v", err)
	}
	if string(saved) != "pasted-token" {
		t.Errorf("saved token = %q, want %q", saved, "pasted-token")
	}

	// The warning has to be actionable, which means naming the port and how to find
	// whatever holds it -- a bare "handoff unavailable" leaves the user nothing to do.
	if !strings.Contains(output, handoffAddr) {
		t.Errorf("expected the warning to name %s, got:\n%s", handoffAddr, output)
	}
	if !strings.Contains(output, handoffPortHint()) {
		t.Errorf("expected the warning to suggest %q, got:\n%s", handoffPortHint(), output)
	}
}

// TestRunLogin_PortBusyAndNoStdinFailsFast guards the other side of the degradation: with
// no listener and no token on stdin, nothing can ever deliver one, so RunLogin must report
// the bind failure instead of blocking forever on the token channel. An unattended
// `lfr-tunnel login` in a script has to keep failing fast.
func TestRunLogin_PortBusyAndNoStdinFailsFast(t *testing.T) {
	oldOpenBrowserFunc := openBrowserFunc
	openBrowserFunc = func(url string) error { return nil }
	defer func() { openBrowserFunc = oldOpenBrowserFunc }()

	oldListen := listenHandoff
	listenHandoff = func() (net.Listener, error) {
		return nil, errors.New("listen tcp 127.0.0.1:4444: bind: address already in use")
	}
	defer func() { listenHandoff = oldListen }()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdin pipe: %v", err)
	}
	if err := stdinW.Close(); err != nil { // immediate EOF: a closed or /dev/null stdin
		t.Fatalf("failed to close stdin writer: %v", err)
	}
	origStdin := os.Stdin
	os.Stdin = stdinR
	defer func() { os.Stdin = origStdin }()

	fakeHome(t)

	errChan := make(chan error, 1)
	go func() { errChan <- RunLogin("https://tunnel.lfr-demo.se") }()

	select {
	case runErr := <-errChan:
		if runErr == nil {
			t.Fatal("expected an error when neither the listener nor stdin can deliver a token")
		}
		if !strings.Contains(runErr.Error(), handoffAddr) {
			t.Errorf("expected the error to name %s, got: %v", handoffAddr, runErr)
		}
		if !strings.Contains(runErr.Error(), "address already in use") {
			t.Errorf("expected the error to carry the bind failure, got: %v", runErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunLogin blocked instead of reporting that no token could be delivered")
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
