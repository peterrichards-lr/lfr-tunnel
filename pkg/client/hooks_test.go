package client

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

func TestExecuteHook_Success(t *testing.T) {
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "hook_env.txt")

	var scriptCmd string
	if runtime.GOOS == "windows" {
		scriptCmd = "echo %LFT_EVENT% > " + outPath
	} else {
		scriptCmd = "echo $LFT_EVENT > " + outPath
	}

	env := map[string]string{
		"LFT_NODE_ID": "edge-us",
	}

	err := ExecuteHook("warning_received", scriptCmd, env)
	if err != nil {
		t.Fatalf("expected ExecuteHook to succeed, got %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	if len(data) == 0 {
		t.Errorf("expected non-empty output in hook_env.txt")
	}
}

func TestExecuteHook_EmptyCmdIgnored(t *testing.T) {
	err := ExecuteHook("starting", "", nil)
	if err != nil {
		t.Fatalf("expected empty command to be ignored without error, got %v", err)
	}
}

// A hook that exits non-zero is reported to the caller, never swallowed. RunHook is what
// decides to ignore it; ExecuteHook's job is to say what happened.
func TestExecuteHook_NonZeroExitReported(t *testing.T) {
	cmd := "exit 3"
	if runtime.GOOS == "windows" {
		cmd = "exit /b 3"
	}
	if err := ExecuteHook(HookStopping, cmd, nil); err == nil {
		t.Fatal("expected a non-zero exit to be reported as an error")
	}
}

// The timeout is the whole reason a user script cannot wedge the client. Exercised with a
// shortened bound so the assertion is about the mechanism, not about waiting 15 seconds.
func TestExecuteHook_TimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh and sleep")
	}
	defer shortenHookBounds(t, 100*time.Millisecond, 100*time.Millisecond)()

	start := time.Now()
	err := ExecuteHook(HookStarted, "sleep 3", nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a hook that outlives the timeout to return an error")
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("hook was not bounded: returned after %s", elapsed)
	}
}

// The timeout kills the hook process; it does not close the pipes CombinedOutput is reading.
// A hook that backgrounds a child inheriting stdout therefore holds those pipes open after
// its parent is dead, and without cmd.WaitDelay the client blocks in cmd.Wait for as long as
// the grandchild lives -- the "a hook hangs the client forever" defect, reached through a
// hook that did nothing unusual. "sleep 3 & sleep 3" is exactly that shape.
func TestExecuteHook_BackgroundedChildDoesNotHang(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh and sleep")
	}
	defer shortenHookBounds(t, 100*time.Millisecond, 100*time.Millisecond)()

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		err := ExecuteHook(HookStarted, "sleep 3 & sleep 3", nil)
		if err == nil {
			t.Error("expected the killed hook to be reported as a timeout")
		}
		done <- time.Since(start)
	}()

	select {
	case elapsed := <-done:
		if elapsed > 1500*time.Millisecond {
			t.Fatalf("hook was not bounded: returned after %s", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ExecuteHook never returned: the output pipes outlived the killed process")
	}
}

func shortenHookBounds(t *testing.T, timeout, waitDelay time.Duration) func() {
	t.Helper()
	prevTimeout, prevWait := hookTimeout, hookWaitDelay
	hookTimeout, hookWaitDelay = timeout, waitDelay
	return func() { hookTimeout, hookWaitDelay = prevTimeout, prevWait }
}

// hookRecorder stands in for the real executor so the wiring can be asserted without
// spawning anything.
type hookRecorder struct {
	mu     sync.Mutex
	calls  []hookCall
	fired  chan hookCall
	result error
}

type hookCall struct {
	event   string
	hookCmd string
	env     map[string]string
}

func newHookRecorder() *hookRecorder {
	return &hookRecorder{fired: make(chan hookCall, 8)}
}

func (r *hookRecorder) run(event, hookCmd string, env map[string]string) error {
	call := hookCall{event: event, hookCmd: hookCmd, env: env}
	r.mu.Lock()
	r.calls = append(r.calls, call)
	res := r.result
	r.mu.Unlock()
	r.fired <- call
	return res
}

func (r *hookRecorder) events() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.calls))
	for _, c := range r.calls {
		out = append(out, c.event)
	}
	return out
}

func engineWithRecorder(t *testing.T, hooks config.ClientHooksConfig) (*InterceptorEngine, *hookRecorder) {
	t.Helper()
	e := NewInterceptorEngine("127.0.0.1", nil)
	r := newHookRecorder()
	e.hookExec = r.run
	e.SetHooks(hooks)
	return e, r
}

func TestRunHook_FiresConfiguredHookWithDocumentedEnv(t *testing.T) {
	e, rec := engineWithRecorder(t, config.ClientHooksConfig{Started: "/usr/local/bin/on-started.sh"})
	e.SetSubdomainDetails("peter", "peter-se", true, false)

	e.RunHook(HookStarted, map[string]string{"LFT_FAILOVER_REGION": "apac"})

	if got := rec.events(); len(got) != 1 || got[0] != HookStarted {
		t.Fatalf("expected exactly one started hook, got %v", got)
	}
	call := rec.calls[0]
	if call.hookCmd != "/usr/local/bin/on-started.sh" {
		t.Errorf("wrong command: %q", call.hookCmd)
	}
	for _, key := range []string{"LFT_NODE_ID", "LFT_SECONDS_REMAINING", "LFT_FAILOVER_REGION", "LFT_SUBDOMAIN"} {
		if _, ok := call.env[key]; !ok {
			t.Errorf("every hook must receive %s; env was %v", key, call.env)
		}
	}
	if call.env["LFT_FAILOVER_REGION"] != "apac" {
		t.Errorf("caller-supplied LFT_FAILOVER_REGION was not applied: %v", call.env)
	}
	if call.env["LFT_SUBDOMAIN"] != "peter-se" {
		t.Errorf("LFT_SUBDOMAIN should be the leased prefix, got %q", call.env["LFT_SUBDOMAIN"])
	}
	if call.env["LFT_SECONDS_REMAINING"] != "0" {
		t.Errorf("with no shutdown announced LFT_SECONDS_REMAINING should be 0, got %q", call.env["LFT_SECONDS_REMAINING"])
	}
}

// The overwhelmingly common configuration is no hooks at all. That path must not reach the
// executor -- not merely execute an empty string.
func TestRunHook_UnconfiguredEventIsNoOp(t *testing.T) {
	e, rec := engineWithRecorder(t, config.ClientHooksConfig{Started: "echo only-started"})

	for _, event := range []string{HookWarningReceived, HookStopping, HookStopped, HookStarting} {
		e.RunHook(event, nil)
	}
	if got := rec.events(); len(got) != 0 {
		t.Fatalf("unconfigured events must not execute anything, got %v", got)
	}

	e.RunHook("not_a_real_event", nil)
	if got := rec.events(); len(got) != 0 {
		t.Fatalf("an unknown event name must be inert, got %v", got)
	}
}

// The zero ClientHooksConfig is the default for every user who has never opened the config
// file, so it is the path that must cost nothing.
func TestRunHook_ZeroConfigIsNoOp(t *testing.T) {
	e, rec := engineWithRecorder(t, config.ClientHooksConfig{})
	for _, event := range []string{HookWarningReceived, HookStopping, HookStopped, HookStarting, HookStarted} {
		e.RunHook(event, nil)
	}
	if got := rec.events(); len(got) != 0 {
		t.Fatalf("a zero hooks config must execute nothing, got %v", got)
	}
}

// Documented contract: a non-zero exit is reported and discarded. It must not propagate, and
// it must not stop the next transition's hook from firing.
func TestRunHook_FailureDoesNotBlockTheNextTransition(t *testing.T) {
	e, rec := engineWithRecorder(t, config.ClientHooksConfig{Stopping: "false", Stopped: "true"})
	rec.result = errors.New("hook exited 1")

	e.RunHook(HookStopping, nil)
	e.RunHook(HookStopped, nil)

	if got := rec.events(); len(got) != 2 || got[0] != HookStopping || got[1] != HookStopped {
		t.Fatalf("a failing hook must not suppress the following one, got %v", got)
	}
}

// End to end through the real executor, with harmless shell builtins, to prove RunHook's
// default path really executes and really ignores the exit status.
func TestRunHook_RealExecutorIgnoresExitStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh builtins")
	}
	marker := filepath.Join(t.TempDir(), "ran")

	e := NewInterceptorEngine("127.0.0.1", nil)
	e.SetHooks(config.ClientHooksConfig{Stopping: "touch " + marker + "; false"})
	e.RunHook(HookStopping, nil) // must return, despite the hook exiting non-zero

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the configured hook did not actually run: %v", err)
	}
}

// The one lifecycle point that lives inside pkg/client: a gateway shutdown announcement.
func TestNoteShutdownWarning_FiresWarningReceivedHook(t *testing.T) {
	e, rec := engineWithRecorder(t, config.ClientHooksConfig{WarningReceived: "/usr/local/bin/on-warning.sh"})
	e.SetSubdomainDetails("peter", "peter-se", true, false)

	e.noteShutdownWarning(&NodeShutdownWarning{
		Type:             "node_shutdown_warning",
		NodeID:           "edge-in",
		SecondsRemaining: 300,
		ShutdownAt:       time.Now().Add(5 * time.Minute).Unix(),
		Reason:           "scheduled stop",
	})

	select {
	case call := <-rec.fired:
		if call.event != HookWarningReceived {
			t.Fatalf("expected warning_received, got %q", call.event)
		}
		if call.env["LFT_NODE_ID"] != "edge-in" {
			t.Errorf("LFT_NODE_ID should name the announcing gateway, got %q", call.env["LFT_NODE_ID"])
		}
		if call.env["LFT_SECONDS_REMAINING"] != "300" {
			t.Errorf("LFT_SECONDS_REMAINING should be the announced countdown, got %q", call.env["LFT_SECONDS_REMAINING"])
		}
		if call.env["LFT_SUBDOMAIN"] != "peter-se" {
			t.Errorf("LFT_SUBDOMAIN should be the leased prefix, got %q", call.env["LFT_SUBDOMAIN"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("warning_received hook never fired for an announced gateway shutdown")
	}
}

// Warnings repeat on every heartbeat for the whole countdown. The hook is per announcement,
// not per heartbeat -- otherwise a five-minute warning runs the user's script ~150 times.
func TestNoteShutdownWarning_HookFiresOncePerAnnouncement(t *testing.T) {
	e, rec := engineWithRecorder(t, config.ClientHooksConfig{WarningReceived: "/usr/local/bin/on-warning.sh"})
	at := time.Now().Add(5 * time.Minute).Unix()

	for i := 0; i < 3; i++ {
		e.noteShutdownWarning(&NodeShutdownWarning{
			Type:             "node_shutdown_warning",
			NodeID:           "edge-in",
			SecondsRemaining: 300 - i,
			ShutdownAt:       at,
		})
	}

	select {
	case <-rec.fired:
	case <-time.After(5 * time.Second):
		t.Fatal("warning_received hook never fired")
	}
	select {
	case call := <-rec.fired:
		t.Fatalf("repeat heartbeats must not re-fire the hook, but %s fired again", call.event)
	case <-time.After(500 * time.Millisecond):
	}
}

// LFT_NODE_ID and LFT_SECONDS_REMAINING default from the pending announcement, so the hooks
// fired later in the move still know which gateway is being left and how long it has.
func TestRunHook_ShutdownContextCarriesThroughTheMove(t *testing.T) {
	e, rec := engineWithRecorder(t, config.ClientHooksConfig{Stopping: "/usr/local/bin/on-stopping.sh"})
	e.noteShutdownWarning(&NodeShutdownWarning{
		Type:             "node_shutdown_warning",
		NodeID:           "edge-in",
		SecondsRemaining: 300,
		ShutdownAt:       time.Now().Add(5 * time.Minute).Unix(),
	})

	e.RunHook(HookStopping, nil)

	var stopping *hookCall
	deadline := time.After(5 * time.Second)
	for stopping == nil {
		select {
		case call := <-rec.fired:
			if call.event == HookStopping {
				c := call
				stopping = &c
			}
		case <-deadline:
			t.Fatal("stopping hook never fired")
		}
	}
	if stopping.env["LFT_NODE_ID"] != "edge-in" {
		t.Errorf("LFT_NODE_ID should still name the gateway being left, got %q", stopping.env["LFT_NODE_ID"])
	}
	if stopping.env["LFT_SECONDS_REMAINING"] == "0" {
		t.Errorf("LFT_SECONDS_REMAINING should count down to the announced stop, got %q", stopping.env["LFT_SECONDS_REMAINING"])
	}
}
