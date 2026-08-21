package ops

import (
	"net"
	"testing"
	"time"
)

func TestParseInstanceDescribeOutput_Running(t *testing.T) {
	raw := `{
		"Reservations": [
			{"Instances": [{"InstanceId": "i-0123456789abcdef0", "State": {"Name": "running"}}]}
		]
	}`
	id, state, err := parseInstanceDescribeOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "i-0123456789abcdef0" || state != "running" {
		t.Errorf("got id=%q state=%q", id, state)
	}
}

func TestParseInstanceDescribeOutput_Stopped(t *testing.T) {
	raw := `{
		"Reservations": [
			{"Instances": [{"InstanceId": "i-04098b52e53499089", "State": {"Name": "stopped"}}]}
		]
	}`
	id, state, err := parseInstanceDescribeOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "i-04098b52e53499089" || state != "stopped" {
		t.Errorf("got id=%q state=%q", id, state)
	}
}

func TestParseInstanceDescribeOutput_NoMatch(t *testing.T) {
	raw := `{"Reservations": []}`
	id, state, err := parseInstanceDescribeOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "" || state != "" {
		t.Errorf("expected empty id/state for no match, got id=%q state=%q", id, state)
	}
}

func TestParseInstanceDescribeOutput_InvalidJSON(t *testing.T) {
	_, _, err := parseInstanceDescribeOutput("not json")
	if err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

func TestEnsureInstanceRunning_NoRegion_IsNoOp(t *testing.T) {
	restore, err := ensureInstanceRunning("host.example.com", "", "")
	if err != nil {
		t.Fatalf("expected no error when region is unset, got: %v", err)
	}
	// Must not panic and must not do anything observable.
	restore()
}

func TestResolveHostIPv4_Loopback(t *testing.T) {
	ip, err := resolveHostIPv4("localhost")
	if err != nil {
		t.Fatalf("unexpected error resolving localhost: %v", err)
	}
	if net.ParseIP(ip) == nil {
		t.Errorf("expected a valid IP, got %q", ip)
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
