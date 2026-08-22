package ops

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

// powerHook is an operator-supplied script that knows how to read and change the power
// state of the machine behind a host.
//
// It exists so this package can start a stopped node for a deploy without knowing anything
// about the cloud it runs on. The AWS CLI calls that used to live here are now in
// scripts/common/lfr-power-hook-aws.sh; supporting a different provider means writing a
// sibling of that script and pointing power_hook at it, not patching lfr-tunnel (#1187).
// Same shape as the vanity domain hook: `<hook> <action> <target>`, everything else through
// the environment, and no defaults of its own.
//
// Contract:
//
//	<hook> status <host>  prints "<state> [id]" on stdout, exit 0
//	<hook> start  <host>  starts it and does not return until it is running, exit 0
//	<hook> stop   <host>  requests a stop, exit 0 once accepted
//
// state is the provider's own vocabulary; only "running", "stopped" and "stopping" mean
// anything here. id is optional and used purely to make failure messages actionable.
type powerHook struct {
	// Path is the hook script. Empty means power management is not configured, which is
	// the same as never setting it up -- deploys simply don't touch power state.
	Path string
	// Env is passed to the hook on top of the current environment, carrying whatever the
	// operator's chosen script needs (AWS_REGION and LFT_INSTANCE_TAG for the bundled one).
	Env []string
}

func (h powerHook) configured() bool { return h.Path != "" }

// checkPowerConfig rejects a target that asks for power management without saying how.
//
// Before #1187 aws_region alone switched the feature on. Now it is only a value passed to
// a hook, so a config that still carries it by itself would leave power unmanaged without
// a word -- and a deploy that starts a node and never stops it is precisely the failure
// #1183 exists to make loud. Better to stop with the missing line than to succeed quietly.
func checkPowerConfig(target DeployTarget) error {
	if target.AWSRegion == "" || target.PowerHook != "" {
		return nil
	}
	return fmt.Errorf(
		"aws_region is set but power_hook is not, so power management would be skipped silently.\n"+
			"It is no longer built in -- lfr-tunnel calls a script, so it works on any provider.\n"+
			"Add the bundled AWS implementation to your lfr-tunnel-ops.yaml:\n\n"+
			"  central:\n"+
			"    power_hook: %s\n\n"+
			"or set LFT_POWER_HOOK. To turn power management off instead, remove aws_region",
		bundledAWSPowerHook)
}

// bundledAWSPowerHook is the reference implementation shipped in this repo. Named here only
// so the error above can point at it -- nothing defaults to it, since which provider (and
// which script) an operator uses is theirs to declare.
const bundledAWSPowerHook = "scripts/common/lfr-power-hook-aws.sh"

// run invokes the hook and returns its trimmed stdout. The hook's stderr is passed through
// to ours, so whatever it has to say about a failure reaches the operator unedited.
func (h powerHook) run(action, host string) (string, error) {
	cmd := exec.Command(h.Path, action, host)
	cmd.Env = append(os.Environ(), h.Env...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("power hook %q %s %s: %w", h.Path, action, host, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// status asks the hook for the current power state, and an optional provider identifier
// used only to make messages actionable.
func (h powerHook) status(host string) (state, id string, err error) {
	out, err := h.run("status", host)
	if err != nil {
		return "", "", err
	}
	state, id, err = parsePowerStatus(out)
	if err != nil {
		return "", "", fmt.Errorf("power hook %q for %s: %w", h.Path, host, err)
	}
	return state, id, nil
}

// parsePowerStatus reads a hook's status line: a state, optionally followed by a provider
// identifier. Split out from status so the contract is testable without executing anything.
//
// Anything beyond the first two fields is ignored rather than rejected, so a hook that adds
// detail to its output doesn't break against an older lfr-tunnel.
func parsePowerStatus(out string) (state, id string, err error) {
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", "", fmt.Errorf("reported no power state")
	}
	if len(fields) > 1 {
		id = fields[1]
	}
	return fields[0], id, nil
}

// ensureInstanceRunning checks whether the machine behind host is currently stopped and, if
// so, starts it and waits until it is reachable over SSH before returning. The returned
// restore func puts it back into whatever power state it was in before this call -- callers
// should `defer restore()` immediately after a nil error, so a deploy that lands during a
// scheduled power-off window (e.g. edge-us/edge-apac's deliberately-wrong midnight-8am
// schedule, kept as a live test case for #885) doesn't leave the box running outside its
// schedule, whether the deploy itself succeeds or fails.
//
// A no-op restore (and nil error) is returned when no hook is configured -- this whole
// feature is opt-in (#1050), so a deploy that never sets it up behaves exactly as it did
// before it existed.
func ensureInstanceRunning(host string, hook powerHook) (restore func(), err error) {
	noop := func() {}
	if !hook.configured() {
		return noop, nil
	}

	state, id, err := hook.status(host)
	if err != nil {
		return noop, fmt.Errorf("reading the power state of %s: %w", host, err)
	}
	label := describeTarget(host, id)

	switch state {
	case "running":
		fmt.Printf("%s is already running.\n", label)
		return noop, nil
	case "stopped":
		fmt.Printf("%s is stopped -- starting it for this deploy...\n", label)
		if _, err := hook.run("start", host); err != nil {
			return noop, fmt.Errorf("starting %s: %w", label, err)
		}
		if err := waitForSSH(host, 2*time.Minute); err != nil {
			return noop, fmt.Errorf("%s is running but SSH never came up: %w", label, err)
		}
		restore = func() {
			fmt.Printf("Restoring %s to its previous (stopped) state...\n", label)
			_, stopErr := hook.run("stop", host)

			// Don't take the stop's word for it. Credentials on this account are
			// short-lived and their refresh is unreliable, so the window between starting
			// an instance and stopping it can outlive the credential -- which is exactly
			// how a node was left running overnight after a deploy to Tokyo (#1183).
			// Confirm the state actually reached.
			state, checkErr := confirmStopped(func() (string, error) {
				s, _, err := hook.status(host)
				return s, err
			}, stopConfirmAttempts, stopConfirmDelay)
			switch {
			case checkErr == nil && (state == "stopping" || state == "stopped"):
				fmt.Printf("%s is %s.\n", label, state)
				return
			case stopErr == nil && checkErr != nil:
				// The stop was accepted but we cannot confirm it. Say so rather than
				// implying either outcome.
				powerRestoreFailed(label, host, hook.Path,
					fmt.Sprintf("stop was accepted but its result could not be confirmed: %v", checkErr))
			case stopErr != nil:
				powerRestoreFailed(label, host, hook.Path, stopErr.Error())
			default:
				powerRestoreFailed(label, host, hook.Path,
					fmt.Sprintf("it is still %q after the stop", state))
			}
		}
		return restore, nil
	default:
		return noop, fmt.Errorf("%s is in state %q, not running or stopped -- refusing to guess, deploy manually once it settles", label, state)
	}
}

// describeTarget names the machine in operator-facing messages. The hook may or may not
// report a provider identifier; when it does, it is far more useful than the hostname for
// acting on the problem, so both are shown.
func describeTarget(host, id string) string {
	if id == "" {
		return host
	}
	return fmt.Sprintf("%s (%s)", id, host)
}

// How long the restore gives a stop to become visible. A provider's read API may be
// eventually consistent, so a status taken immediately after a stop can still report
// "running" even though the stop was accepted -- and since #1184 that wrongly fails the
// whole deploy (#1191).
//
// Deliberately short: the hook's stop returns as soon as the request is accepted rather
// than waiting for the machine to finish stopping, because "stopping" already proves the
// stop took effect and waiting for "stopped" would add 30-90s to every deploy teardown.
const (
	stopConfirmAttempts = 5
	stopConfirmDelay    = 2 * time.Second
)

// confirmStopped re-reads the power state until it proves the stop took effect, or the
// attempts run out. Returns the last state and error seen, so the caller can distinguish
// "still running" from "could not be read at all" -- those mean different things to an
// operator and get reported differently.
//
// Takes readState rather than calling the hook directly so the retry behaviour is testable
// without a provider account or a real delay.
func confirmStopped(readState func() (string, error), attempts int, delay time.Duration) (string, error) {
	var (
		state string
		err   error
	)
	for attempt := 1; attempt <= attempts; attempt++ {
		state, err = readState()
		if err == nil && (state == "stopping" || state == "stopped") {
			return state, nil
		}
		if attempt < attempts {
			time.Sleep(delay)
		}
	}
	return state, err
}

// PowerRestoreFailed reports whether a deploy left a node running that it had started.
// Deploy checks this so an unattended run cannot exit 0 having stranded a node outside its
// schedule, quietly costing money (#1183).
var powerRestoreFailure string

// PowerRestoreFailure returns the description of a failed power restore, or "" if none.
func PowerRestoreFailure() string { return powerRestoreFailure }

// powerRestoreFailed records the failure and makes it loud. The previous behaviour was a
// single Printf at the tail of a long, noisy deploy, which affected nothing and was easy
// to miss entirely.
//
// The remediation it prints is the hook itself rather than any provider's CLI -- that is
// the one command guaranteed to work for whoever is reading it.
func powerRestoreFailed(label, host, hookPath, reason string) {
	powerRestoreFailure = fmt.Sprintf("%s may still be RUNNING: %s", label, reason)
	rule := strings.Repeat("!", 78)
	fmt.Fprintf(os.Stderr, "\n%s\n", rule)
	fmt.Fprintf(os.Stderr, "WARNING: %s\n", powerRestoreFailure)
	fmt.Fprint(os.Stderr, "It was started for this deploy and must be stopped, or it runs outside its\n")
	fmt.Fprint(os.Stderr, "schedule and keeps costing money. Stop it with:\n\n")
	fmt.Fprintf(os.Stderr, "  %s stop %s\n", hookPath, host)
	fmt.Fprintf(os.Stderr, "%s\n\n", rule)
}

// waitForSSH polls host's SSH port (22) until it accepts a TCP connection or timeout
// elapses. Reaching "running" is not the same as being able to log in.
func waitForSSH(host string, timeout time.Duration) error {
	return waitForTCP(net.JoinHostPort(host, "22"), timeout, 5*time.Second)
}

// waitForTCP polls addr until it accepts a TCP connection, timeout elapses, or interval
// between attempts -- split out from waitForSSH so tests can use a short interval instead
// of the real 5s cadence.
func waitForTCP(addr string, timeout, interval time.Duration) error {
	dialTimeout := interval
	if dialTimeout > 5*time.Second {
		dialTimeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, dialTimeout)
		if err == nil {
			_ = conn.Close() //nolint:errcheck
			return nil
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("timed out after %s waiting for %s", timeout, addr)
}
