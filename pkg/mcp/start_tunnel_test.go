package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
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
