package mcp

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #2336: start_tunnel could never report success.
//
// `-background` is not the tunnel. handleBackground strips the flag and spawns a FURTHER process,
// then exits; that grandchild writes the state file with its own os.Getpid(). startTunnel matched
// `cs.PID == cmd.Process.Pid` -- the intermediate against the grandchild -- so the success branch
// was unreachable from v1.15.0 until this fix, and the `pid` on the pending path belonged to a
// process that had already exited.
//
// WHY THESE TESTS AND NOT AN END-TO-END ONE. Asserting the whole tool would mean starting a real
// background tunnel, which cannot be done on a developer machine here: running the client locally
// is forbidden and has cost three environment reinstalls (.agents/skills/edr-constraints). So the
// detection is tested as the pure function it now is, against planted state files. The end-to-end
// assertion belongs in the Docker-based suite.
//
// The load-bearing property is the FIRST case: a tunnel must be found even though the process
// that wrote its state file is NOT the one we spawned. That is precisely what the old comparison
// could not do, and no test asked.

func writeState(t *testing.T, dir, subdomain string, pid int) {
	t.Helper()
	state := ClientState{
		PID:          pid,
		Subdomain:    subdomain,
		PublicURLs:   []string{"https://" + subdomain + ".lfr-demo.se"},
		InspectorURL: "http://127.0.0.1:4040",
		StartTime:    "2026-10-01T09:14:02Z",
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	path := filepath.Join(dir, "lfr-tunnel-"+subdomain+".state")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

// stateDir points HOME at a temp tree and returns the state directory inside it.
func stateDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".lfr-tunnel")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return dir
}

// FIRING: the defect itself. The state file is written by a process that is not the one we
// spawned -- which is always true in production -- and it must still be found.
func TestNewStateSince_FindsATunnelWrittenByAnotherProcess(t *testing.T) {
	dir := stateDir(t)

	before := readStates()
	if len(before) != 0 {
		t.Fatalf("expected an empty snapshot, got %d", len(before))
	}

	// os.Getpid() stands in for the grandchild: a LIVE pid that is not the spawner's.
	writeState(t, dir, "peter-se", os.Getpid())

	found := newStateSince(before)
	if found == nil {
		t.Fatal("a newly registered tunnel was not found -- this is #2336: the old code compared " +
			"the state file's PID against the intermediate process it spawned, which never matches")
	}
	if found.Subdomain != "peter-se" {
		t.Errorf("found the wrong tunnel: %q", found.Subdomain)
	}
	if len(found.PublicURLs) == 0 {
		t.Error("the tunnel was found but carries no public URLs, which is what start_tunnel reports")
	}
}

// BOUNDING: a tunnel that was already running is not ours, or start_tunnel would report somebody
// else's tunnel as the one it just created.
func TestNewStateSince_IgnoresATunnelThatWasAlreadyRunning(t *testing.T) {
	dir := stateDir(t)
	writeState(t, dir, "someone-else", os.Getpid())

	before := readStates()
	if len(before) != 1 {
		t.Fatalf("expected the pre-existing tunnel in the snapshot, got %d", len(before))
	}

	if found := newStateSince(before); found != nil {
		t.Errorf("a pre-existing tunnel was reported as new: %q", found.Subdomain)
	}
}

// BOUNDING: restarting a tunnel on a subdomain that already had a state file must still register
// as new. Keying on the subdomain alone would make this indistinguishable from nothing happening.
func TestNewStateSince_DetectsAReplacementOnTheSameSubdomain(t *testing.T) {
	dir := stateDir(t)
	writeState(t, dir, "peter-se", 999001)

	before := readStates()

	writeState(t, dir, "peter-se", os.Getpid())

	found := newStateSince(before)
	if found == nil {
		t.Fatal("a tunnel restarted on an existing subdomain was not detected as new")
	}
	if found.PID != os.Getpid() {
		t.Errorf("found the stale PID %d rather than the replacement", found.PID)
	}
}

// BOUNDING: a state file left behind by a dead process is not a running tunnel. Without this,
// start_tunnel would report success on the corpse of a previous run.
func TestNewStateSince_IgnoresAStateFileWhoseProcessIsGone(t *testing.T) {
	dir := stateDir(t)

	before := readStates()

	// A PID that is not running. isPIDRunning is the same check getTunnelStatus uses to prune.
	writeState(t, dir, "dead-tunnel", 999002)

	if found := newStateSince(before); found != nil {
		t.Errorf("a state file with a dead PID was reported as a live tunnel: %q", found.Subdomain)
	}
}

// readStates must not prune, or the before/after comparison disagrees about a tunnel nobody
// started or stopped -- a stale entry would vanish between the two reads and look like a change.
func TestReadStates_DoesNotDeleteStaleStateFiles(t *testing.T) {
	dir := stateDir(t)
	writeState(t, dir, "dead-tunnel", 999003)

	_ = readStates()

	if _, err := os.Stat(filepath.Join(dir, "lfr-tunnel-dead-tunnel.state")); err != nil {
		t.Errorf("readStates removed a state file it should only have read: %v", err)
	}
}

// --- #2336, second pass: identify the tunnel by the PID the client reports ---
//
// Review of the first fix found that a snapshot diff alone is not enough. The MCP caller usually
// passes no subdomain, so repeated calls derive the SAME one; the second call then hits
// handleBackground's "already running" refusal and starts nothing -- and under a snapshot diff it
// would claim the FIRST call's tunnel and report success with its URLs. The client already prints
// the grandchild's PID, which is exactly the PID the state file will carry.

func TestParseBackgroundPID_ReadsTheClientsOwnReport(t *testing.T) {
	out := []byte("[Client] Tunnel started in background for subdomain 'peter-se' (PID: 51234).\n" +
		"[Client] Logs: /tmp/x.log\n")
	if got := parseBackgroundPID(out); got != 51234 {
		t.Errorf("got %d, want 51234", got)
	}
}

func TestParseBackgroundPID_ZeroWhenAbsentOrUnusable(t *testing.T) {
	for name, out := range map[string]string{
		"no line at all":    "[Client] something else entirely\n",
		"no digits":         "[Client] Tunnel started (PID: none).\n",
		"empty output":      "",
		"zero is not a pid": "[Client] Tunnel started (PID: 0).\n",
	} {
		if got := parseBackgroundPID([]byte(out)); got != 0 {
			t.Errorf("%s: got %d, want 0 so the caller falls back", name, got)
		}
	}
}

// FIRING: the misattribution the snapshot diff allowed. A tunnel registers during our window that
// is NOT the one we started -- findRegistered must not claim it.
func TestFindRegistered_DoesNotClaimAnotherTunnelThatRegisteredMeanwhile(t *testing.T) {
	dir := stateDir(t)

	before := readStates()

	// Somebody else's tunnel appears during our 2s window. It is live and it is new.
	writeState(t, dir, "someone-elses", os.Getpid())

	// Our client reported a different PID, which has not registered yet.
	const ourPID = 999101
	if found := findRegistered(before, ourPID); found != nil {
		t.Errorf("claimed a tunnel we did not start: %q (pid %d) -- a snapshot diff alone "+
			"reports success with somebody else's public URLs", found.Subdomain, found.PID)
	}
}

// BOUNDING: with the reported PID, our own tunnel is found exactly.
func TestFindRegistered_FindsOurTunnelByTheReportedPID(t *testing.T) {
	dir := stateDir(t)
	before := readStates()

	writeState(t, dir, "not-ours", 999102)
	writeState(t, dir, "ours", os.Getpid())

	found := findRegistered(before, os.Getpid())
	if found == nil {
		t.Fatal("our own tunnel was not found by the PID the client reported")
	}
	if found.Subdomain != "ours" {
		t.Errorf("found %q, want \"ours\"", found.Subdomain)
	}
}

// BOUNDING: with no usable PID the snapshot diff is the documented fallback, not an error.
func TestFindRegistered_FallsBackToTheSnapshotWhenThePIDIsUnknown(t *testing.T) {
	dir := stateDir(t)
	before := readStates()
	writeState(t, dir, "new-one", os.Getpid())

	if found := findRegistered(before, 0); found == nil {
		t.Error("with no reported PID the snapshot diff should still find a new tunnel")
	}
}

// FIRING: a client that refuses to start must surface as an ERROR, not as "pending".
//
// Every refusal in handleBackground is a log.Fatalf -- "already running", a bad log directory, a
// spawn failure. Before this, startTunnel used Start() and ignored the exit status, so all of
// them were reported as "pending": the agent was told to wait for a tunnel that would never come.
func TestStartTunnel_ANonZeroExitIsAnErrorNotPending(t *testing.T) {
	stateDir(t)

	falseBin, err := exec.LookPath("false")
	if err != nil {
		t.Skipf("no `false` on PATH to stand in for a refusing client: %v", err)
	}
	orig := osExecutable
	osExecutable = func() (string, error) { return falseBin, nil }
	t.Cleanup(func() { osExecutable = orig })

	res, err := startTunnel("", "", "")
	if err == nil {
		t.Fatalf("a client that exited non-zero was reported as %v, not as an error -- an agent "+
			"would wait for a tunnel that was never started", res)
	}
	if !strings.Contains(err.Error(), "refused to start") {
		t.Errorf("the error does not say what happened: %v", err)
	}
}
