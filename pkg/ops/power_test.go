package ops

import (
	"net"
	"strings"
	"testing"
	"time"
)

// The AWS-specific lookup this file used to cover -- describe-instances JSON parsing, the
// tag filter, IPv4 resolution -- moved into scripts/common/lfr-power-hook-aws.sh with
// #1187. What is left in Go is the hook contract and the policy around it.

func TestParsePowerStatus_StateOnly(t *testing.T) {
	state, id, err := parsePowerStatus("running")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "running" || id != "" {
		t.Errorf("got state=%q id=%q", state, id)
	}
}

func TestParsePowerStatus_StateAndIdentifier(t *testing.T) {
	state, id, err := parsePowerStatus("stopped i-0123456789abcdef0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "stopped" || id != "i-0123456789abcdef0" {
		t.Errorf("got state=%q id=%q", state, id)
	}
}

// Hooks are shell scripts, so trailing newlines and padding are the normal case rather
// than the exception.
func TestParsePowerStatus_ToleratesSurroundingWhitespace(t *testing.T) {
	state, id, err := parsePowerStatus("  stopping   i-abc \n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "stopping" || id != "i-abc" {
		t.Errorf("got state=%q id=%q", state, id)
	}
}

// A hook that adds detail to its output must not break an older lfr-tunnel.
func TestParsePowerStatus_IgnoresExtraFields(t *testing.T) {
	state, id, err := parsePowerStatus("running i-abc eu-west-1 extra")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "running" || id != "i-abc" {
		t.Errorf("got state=%q id=%q", state, id)
	}
}

// Empty output must be an error, not an empty state that silently falls through to the
// "refusing to guess" branch with a confusing message.
func TestParsePowerStatus_EmptyOutputIsAnError(t *testing.T) {
	if _, _, err := parsePowerStatus("   \n  "); err == nil {
		t.Fatal("expected an error for empty hook output")
	}
}

// Power management is opt-in. With no hook configured, deploys must behave exactly as they
// did before the feature existed -- and must not try to execute the empty string.
func TestEnsureInstanceRunning_NoHookIsANoOp(t *testing.T) {
	restore, err := ensureInstanceRunning("host.example.com", powerHook{})
	if err != nil {
		t.Fatalf("expected no error when no hook is configured, got: %v", err)
	}
	if restore == nil {
		t.Fatal("expected a non-nil restore func so callers can defer it unconditionally")
	}
	restore()
}

func TestPowerHookConfigured(t *testing.T) {
	if (powerHook{}).configured() {
		t.Error("an empty path must count as unconfigured")
	}
	if !(powerHook{Path: "/usr/local/bin/hook.sh"}).configured() {
		t.Error("a set path must count as configured")
	}
}

// describeTarget decides how the machine is named in every operator-facing message,
// including the one telling them to go stop a stranded node by hand.
func TestDescribeTarget(t *testing.T) {
	if got := describeTarget("apac.lfr-demo.se", "i-abc"); got != "i-abc (apac.lfr-demo.se)" {
		t.Errorf("expected the identifier to lead when the hook reports one, got %q", got)
	}
	if got := describeTarget("apac.lfr-demo.se", ""); got != "apac.lfr-demo.se" {
		t.Errorf("expected a bare host when the hook reports no identifier, got %q", got)
	}
}

// The migration case (#1187). aws_region used to switch power management on by itself, so
// a config that still carries it alone must fail loudly rather than quietly stop managing
// power -- a deploy that starts a node and never stops it is what #1183 exists to catch.
func TestCheckPowerConfig_AWSRegionWithoutHookErrorsActionably(t *testing.T) {
	err := checkPowerConfig(DeployTarget{Host: "edge.example.com", AWSRegion: "eu-west-1"})
	if err == nil {
		t.Fatal("expected an error when aws_region is set without a power_hook")
	}
	// The operator has to be able to fix this from the message alone.
	for _, want := range []string{"aws_region", "power_hook", bundledAWSPowerHook, "LFT_POWER_HOOK"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to mention %q, got: %v", want, err)
		}
	}
}

func TestCheckPowerConfig_AcceptsValidCombinations(t *testing.T) {
	cases := []struct {
		name   string
		target DeployTarget
	}{
		// Power management is opt-in; neither field set is the overwhelmingly common case.
		{"nothing configured", DeployTarget{}},
		{"hook and region", DeployTarget{AWSRegion: "eu-west-1", PowerHook: "/opt/hook.sh"}},
		// A hook for a provider that needs no region is perfectly legitimate -- the whole
		// point is that lfr-tunnel does not know what a given hook requires.
		{"hook without region", DeployTarget{PowerHook: "/opt/hook.sh"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkPowerConfig(tc.target); err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

func TestWaitForTCP_TimesOutOnClosedPort(t *testing.T) {
	// Port 0 on localhost never accepts a connection within the test's short timeout,
	// so this exercises the timeout path without needing a real SSH server.
	err := waitForTCP("127.0.0.1:0", 200*time.Millisecond, 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}

func TestWaitForTCP_SucceedsOnOpenPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start a test listener: %v", err)
	}
	defer func() { _ = ln.Close() }() //nolint:errcheck

	if err := waitForTCP(ln.Addr().String(), 2*time.Second, 50*time.Millisecond); err != nil {
		t.Fatalf("expected success against a real open port, got: %v", err)
	}
}
