package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lfr-tunnel/pkg/client"
)

// startAfter returns a findState that reports nothing for the first n polls, then st.
func startAfter(n int32, st *client.ClientState) func(int) *client.ClientState {
	var calls atomic.Int32
	return func(int) *client.ClientState {
		if calls.Add(1) > n {
			return st
		}
		return nil
	}
}

func never(int) *client.ClientState { return nil }

func TestAwaitRegistration_RegisteredWhenTheStateFileAppears(t *testing.T) {
	st := &client.ClientState{PID: 42, PublicURLs: []string{"https://peter.lfr-demo.se"}}
	res := backgroundStart{
		pid: 42, exited: make(chan error), findState: startAfter(3, st),
		wait: 5 * time.Second, interval: 5 * time.Millisecond,
	}.awaitRegistration()
	if res.outcome != backgroundRegistered || res.state != st {
		t.Fatalf("outcome = %v, state = %v; want registered with the state file", res.outcome, res.state)
	}
}

// The #2573 case: the gateway refused the tunnel and it exited 1. Before the fix the wrapper got
// exit 0 and nothing else.
func TestAwaitRegistration_RefusedWhenTheTunnelExitsWithAnError(t *testing.T) {
	exited := make(chan error, 1)
	exited <- errors.New("exit status 1")
	res := backgroundStart{
		pid: 42, exited: exited, findState: never,
		wait: 5 * time.Second, interval: 5 * time.Millisecond,
	}.awaitRegistration()
	if res.outcome != backgroundRefused || res.exitErr == nil {
		t.Fatalf("outcome = %v, err = %v; want refused with the exit error", res.outcome, res.exitErr)
	}
}

// A clean exit before registering is a hand-over (a restart into an upgrade), not a refusal.
func TestAwaitRegistration_CleanExitIsNotARefusal(t *testing.T) {
	exited := make(chan error, 1)
	exited <- nil
	res := backgroundStart{
		pid: 42, exited: exited, findState: never,
		wait: 5 * time.Second, interval: 5 * time.Millisecond,
	}.awaitRegistration()
	if res.outcome != backgroundPending {
		t.Fatalf("outcome = %v; want pending", res.outcome)
	}
}

// A slow network is not a failure: the wait ends, the tunnel keeps trying.
func TestAwaitRegistration_PendingWhenTheWaitRunsOut(t *testing.T) {
	start := time.Now()
	res := backgroundStart{
		pid: 42, exited: make(chan error), findState: never,
		wait: 50 * time.Millisecond, interval: 5 * time.Millisecond,
	}.awaitRegistration()
	if res.outcome != backgroundPending {
		t.Fatalf("outcome = %v; want pending", res.outcome)
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Fatalf("returned after %s, before the wait ran out", time.Since(start))
	}
}

// Registered then exited (stopped at once): the state file decides, not the exit.
func TestAwaitRegistration_StateWrittenBeforeExitCountsAsRegistered(t *testing.T) {
	st := &client.ClientState{PID: 42}
	exited := make(chan error, 1)
	exited <- errors.New("signal: terminated")
	res := backgroundStart{
		pid: 42, exited: exited, findState: startAfter(1, st),
		wait: 5 * time.Second, interval: 5 * time.Millisecond,
	}.awaitRegistration()
	if res.outcome != backgroundRegistered {
		t.Fatalf("outcome = %v; want registered", res.outcome)
	}
}

// What the child prints on a token refusal: performRegistrationHandshake's [Error] line and advice,
// after a page of startup lines that must not drown it.
const refusedLog = `2026/11/24 10:00:00 [Client] Starting lfr-tunnel v1.52.10
2026/11/24 10:00:00 [Client] Ports: 8080
2026/11/24 10:00:01 [Client] Region: eu
[Error] Failed to register: gateway returned status 401: invalid or expired token
[Client] Your access token was refused. Generate a new one in the User Portal:
         👉 https://tunnel.lfr-demo.se/portal
`

func TestRefusalExcerpt_StartsAtTheLastErrorLine(t *testing.T) {
	got := refusalExcerpt(refusedLog)
	if len(got) != 3 || !strings.HasPrefix(got[0], "[Error] Failed to register") || !strings.Contains(got[2], "/portal") {
		t.Fatalf("excerpt = %q; want the [Error] line and its advice only", got)
	}
}

func TestRefusalExcerpt_NoErrorLineGivesTheTail(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 30; i++ {
		b.WriteString("line\n")
	}
	b.WriteString("panic: boom\n")
	got := refusalExcerpt(b.String())
	if len(got) != refusalExcerptTailLines || got[len(got)-1] != "panic: boom" {
		t.Fatalf("excerpt has %d lines ending %q; want the last %d", len(got), got[len(got)-1], refusalExcerptTailLines)
	}
}

func TestReportBackgroundRefusal_QuotesTheLogOnStderr(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "client-peter.log")
	if err := os.WriteFile(logPath, []byte(refusedLog), 0600); err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	reportBackgroundRefusal(&w, "peter", logPath, errors.New("exit status 1"))
	out := w.String()
	for _, want := range []string{"did not start", "invalid or expired token", "/portal", "Full log: " + logPath} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Region: eu") {
		t.Errorf("report quotes startup noise:\n%s", out)
	}
}

func TestRemovePIDFileIfOwned(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	if err := writePID("peter", 42); err != nil {
		t.Fatal(err)
	}
	removePIDFileIfOwned("peter", 7) // someone else's: kept
	if pid, err := readPID("peter"); err != nil || pid != 42 {
		t.Fatalf("pidfile for another PID was touched: pid=%d err=%v", pid, err)
	}
	removePIDFileIfOwned("peter", 42)
	if _, err := readPID("peter"); err == nil {
		t.Fatal("pidfile of the refused tunnel was left behind")
	}
}
