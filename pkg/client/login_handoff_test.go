package client

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// #2480: a login nobody finished held the handoff port until its window was closed, so the next
// login could not receive the handoff and was told only "Could not listen".

// stdinNeverYields gives RunLogin a paste prompt that waits until the test ends, so only the
// handoff (or the timeout) can end the login.
func stdinNeverYields(t *testing.T) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating stdin pipe: %v", err)
	}
	old := stdinReader
	stdinReader = r
	t.Cleanup(func() {
		stdinReader = old
		if err := w.Close(); err != nil {
			t.Logf("closing stdin pipe: %v", err)
		}
	})
}

// startWaitingLogin starts a login on an ephemeral handoff port and returns that address and the
// channel RunLogin's result arrives on.
func startWaitingLogin(t *testing.T) (string, <-chan error) {
	t.Helper()
	stubLoginBrowser(t)
	addrChan := handoffEphemeral(t)
	stdinNeverYields(t)
	fakeHome(t)
	errChan := make(chan error, 1)
	go func() { errChan <- RunLogin("https://tunnel.example.com") }()
	return <-addrChan, errChan
}

func TestRunLogin_TimesOutAndReleasesTheHandoffPort(t *testing.T) {
	old := loginTimeout
	loginTimeout = 300 * time.Millisecond
	t.Cleanup(func() { loginTimeout = old })

	addr, errChan := startWaitingLogin(t)

	select {
	case err := <-errChan:
		if err == nil || !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "again") {
			t.Fatalf("RunLogin = %v, want a timeout that says to run login again", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunLogin never timed out: a forgotten login still holds the handoff port")
	}

	// The port is free again: the next login can bind it.
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the handoff port %s is still held after the timeout: %v", addr, err)
	}
	if err := l.Close(); err != nil {
		t.Logf("closing: %v", err)
	}
}

func TestHandoffIdentifiesItselfToALocalProbeOnly(t *testing.T) {
	addr, _ := startWaitingLogin(t)

	resp, err := http.Get("http://" + addr + "/handoff")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Logf("closing: %v", err)
		}
	}()
	var id handoffIdentity
	if err := json.NewDecoder(resp.Body).Decode(&id); err != nil {
		t.Fatalf("GET /handoff did not answer JSON: %v", err)
	}
	if id.Service != handoffService || id.PID != os.Getpid() || id.Started == "" {
		t.Errorf("identity = %+v, want service %q, pid %d and a start time", id, handoffService, os.Getpid())
	}
	// No CORS header: a web page must not be able to read which process this is.
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("GET /handoff carries Access-Control-Allow-Origin %q; the identity is for local processes only", got)
	}
}

func TestProbeHandoffHolder(t *testing.T) {
	probeAt := func(t *testing.T, addr string) string {
		t.Helper()
		old := handoffProbeAddr
		handoffProbeAddr = addr
		t.Cleanup(func() { handoffProbeAddr = old })
		return probeHandoffHolder()
	}

	t.Run("a login from this version is named with its PID", func(t *testing.T) {
		addr, _ := startWaitingLogin(t)
		got := probeAt(t, addr)
		if !strings.Contains(got, fmt.Sprintf("PID %d", os.Getpid())) || !strings.Contains(got, "waiting since") {
			t.Errorf("probe = %q, want the waiting login named by PID and start time", got)
		}
	})

	t.Run("an older login is recognised by how its handoff answers", func(t *testing.T) {
		// What v1.52.7's handler does: 400 to GET, 200 with its CORS headers to OPTIONS.
		old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			if r.URL.Path == "/handoff" && r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
		}))
		t.Cleanup(old.Close)
		if got := probeAt(t, strings.TrimPrefix(old.URL, "http://")); !strings.Contains(got, "older version") {
			t.Errorf("probe = %q, want the older login recognised", got)
		}
	})

	t.Run("anything else is not claimed to be a login", func(t *testing.T) {
		other := httptest.NewServer(http.NotFoundHandler())
		t.Cleanup(other.Close)
		if got := probeAt(t, strings.TrimPrefix(other.URL, "http://")); got != "" {
			t.Errorf("probe = %q for an unrelated service, want nothing", got)
		}
	})
}

func TestHandoffUnavailableMessage(t *testing.T) {
	// A real "address in use" error, as the operating system reports it.
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := held.Close(); err != nil {
			t.Logf("closing: %v", err)
		}
	})
	_, inUse := net.Listen("tcp", held.Addr().String())
	if inUse == nil || !isAddrInUse(inUse) {
		t.Fatalf("fixture: binding a held port gave %v, not an address-in-use error", inUse)
	}

	named := handoffUnavailableMessage(inUse, "another `lfr-tunnel login` (PID 4242, waiting since 09:15)")
	for _, want := range []string{"is in use", "PID 4242", "Ctrl+C", "again", handoffPortHint(), "paste it below"} {
		if !strings.Contains(named, want) {
			t.Errorf("message for an identified holder lacks %q:\n%s", want, named)
		}
	}

	unknown := handoffUnavailableMessage(inUse, "")
	if !strings.Contains(unknown, "Usually another") || strings.Contains(unknown, "PID") {
		t.Errorf("message for an unidentified holder should say what usually holds it, naming no PID:\n%s", unknown)
	}

	other := handoffUnavailableMessage(fmt.Errorf("listen tcp: permission denied"), "")
	if !strings.Contains(other, "permission denied") || strings.Contains(other, "is in use") {
		t.Errorf("a non-in-use failure must say what failed, not blame another login:\n%s", other)
	}
}
