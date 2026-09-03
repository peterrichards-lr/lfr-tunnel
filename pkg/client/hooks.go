package client

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"lfr-tunnel/pkg/config"
)

// Lifecycle event names. Each is both a YAML key under `hooks:` in the client config
// (config.ClientHooksConfig) and the value the hook sees in LFT_EVENT, so the two cannot
// drift apart -- hookCommandFor below is the only place that maps between them.
const (
	HookWarningReceived = "warning_received"
	HookStopping        = "stopping"
	HookStopped         = "stopped"
	HookStarting        = "starting"
	HookStarted         = "started"
)

// hookTimeout bounds a single hook run. A hook is arbitrary user-supplied shell, so it is
// assumed to be capable of hanging; the tunnel's own lifecycle must never depend on one
// returning.
//
// A var rather than a const only so the bound itself can be tested without a 15s test --
// same pattern and same reason as shutdownMigrationPollInterval in interceptor.go. Nothing
// in the product writes it.
var hookTimeout = 15 * time.Second

// hookWaitDelay bounds the *read* of the hook's output, which the timeout above does not.
// CommandContext kills the process on deadline, but CombinedOutput then keeps waiting on the
// output pipes -- and a hook that backgrounds a child inheriting stdout leaves those pipes
// open after their parent is dead, so cmd.Wait blocks forever. WaitDelay force-closes them
// (see exec.Cmd.WaitDelay), which is what actually makes "bounded at 15s" true.
var hookWaitDelay = 5 * time.Second

// ExecuteHook runs a user-configured lifecycle hook command with contextual environment variables.
// A 15-second timeout safeguard is enforced to prevent hanging scripts from blocking connection loops.
func ExecuteHook(event string, hookCmd string, env map[string]string) error {
	if strings.TrimSpace(hookCmd) == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
	defer cancel()

	slog.Info(fmt.Sprintf("[Client Hook] Executing %s hook: %s...", event, hookCmd))

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd.exe", "/c", hookCmd)
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", hookCmd)
	}
	cmd.WaitDelay = hookWaitDelay

	// Inherit system environment variables and append LFT_* context
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("LFT_EVENT=%s", event))
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			slog.Info(fmt.Sprintf("[Client Hook Error] %s hook timed out after %s: %s", event, hookTimeout, hookCmd))
			return fmt.Errorf("%s hook timed out after %s", event, hookTimeout)
		}
		slog.Info(fmt.Sprintf("[Client Hook Error] %s hook failed: %v (output: %s)", event, err, strings.TrimSpace(string(out))))
		return err
	}

	slog.Info(fmt.Sprintf("[Client Hook] Successfully completed %s hook.", event))
	return nil
}

// hookCommandFor returns the command configured for event, or "" when that event has no
// hook -- which is also what an unknown event name yields, so a typo is inert rather than
// an error.
func hookCommandFor(h config.ClientHooksConfig, event string) string {
	switch event {
	case HookWarningReceived:
		return h.WarningReceived
	case HookStopping:
		return h.Stopping
	case HookStopped:
		return h.Stopped
	case HookStarting:
		return h.Starting
	case HookStarted:
		return h.Started
	default:
		return ""
	}
}

// SetHooks records the user's lifecycle hook commands on the engine, which is what the
// session loop and the shutdown-warning path reach for when a transition happens. Calling it
// with a zero ClientHooksConfig disables every hook.
func (e *InterceptorEngine) SetHooks(h config.ClientHooksConfig) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hooks = h
}

// RunHook fires the hook configured for event and blocks until it finishes or is bounded
// out, so the documented ordering (stopping before stopped, starting before started) is a
// property of the code rather than of goroutine scheduling.
//
// A hook that is not configured is a no-op: nothing is executed and no shell is spawned.
//
// A hook's exit status never changes what the client does. It is reported and discarded --
// a lifecycle hook observes a transition that has already been decided, and letting a user
// script veto a failover would leave the tunnel down with no way to recover it.
//
// The LFT_* environment is assembled here rather than at each call site, so every event
// gets the same set of variables. extra overrides the defaults for the values only the
// caller knows.
func (e *InterceptorEngine) RunHook(event string, extra map[string]string) {
	e.mu.RLock()
	hookCmd := hookCommandFor(e.hooks, event)
	env := map[string]string{
		"LFT_NODE_ID":           e.shutdownWarnNodeID,
		"LFT_SECONDS_REMAINING": strconv.Itoa(secondsUntil(e.shutdownWarnedAt)),
		"LFT_FAILOVER_REGION":   "",
		"LFT_SUBDOMAIN":         e.SubdomainAss,
	}
	runner := e.hookExec
	e.mu.RUnlock()

	if strings.TrimSpace(hookCmd) == "" {
		return
	}
	for k, v := range extra {
		env[k] = v
	}
	if runner == nil {
		runner = ExecuteHook
	}
	if err := runner(event, hookCmd, env); err != nil {
		// Recorded, not returned. The transition has already been decided, so there is
		// nothing here to abort -- but a hook that quietly fails every failover is worth
		// finding in error-<subdomain>.log next to the failover it belongs to.
		e.LogEvent("warn", "client_hook_failed", map[string]any{
			"hook_event": event,
			"error":      err.Error(),
		})
	}
}

// secondsUntil is the countdown to an announced gateway stop, floored at zero. Zero also
// means "nothing announced", since a hook cannot usefully act on a stop that is already due
// either way.
func secondsUntil(at int64) int {
	if at == 0 {
		return 0
	}
	if d := time.Until(time.Unix(at, 0)); d > 0 {
		return int(d.Seconds())
	}
	return 0
}
