package server

import (
	"testing"
	"time"

	chserver "github.com/jpillora/chisel/server"
)

// newTestRegistry builds a Registry with a real chisel server, matching auth_test.go --
// CleanLease calls through to chiselServer.DeleteUser, so a nil one panics.
func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	chiselServer, err := chserver.NewServer(&chserver.Config{Reverse: true})
	if err != nil {
		t.Fatalf("failed to create chisel server: %v", err)
	}
	return NewRegistry(chiselServer)
}

// TestCleanLeaseKeepsHostOwnedByAnotherSession is the regression test for #1147.
//
// r.leases is keyed by host, so re-registering the same subdomain replaces the entry
// rather than adding one. Cleaning up an older session for that host must not remove the
// routing entry a newer session now owns. During a live failover this was routine, not
// exotic: the client re-registered four times in under a minute, and the cleanup of a
// stale session deleted the live session's route. The tunnel stayed connected while the
// public URL returned 502 indefinitely.
func TestCleanLeaseKeepsHostOwnedByAnotherSession(t *testing.T) {
	const host = "peters.lfr-demo.se"

	r := newTestRegistry(t)

	stale := &TunnelLease{FullHost: host, SessionToken: "stale", LocalPort: 36973, CreatedAt: time.Now().Add(-time.Minute)}
	live := &TunnelLease{FullHost: host, SessionToken: "live", LocalPort: 39955, CreatedAt: time.Now()}

	r.sessionLeases["stale"] = []*TunnelLease{stale}
	r.usedPorts[stale.LocalPort] = true
	r.leases[host] = stale

	// The re-registration: same host, new session takes ownership of the routing entry.
	r.sessionLeases["live"] = []*TunnelLease{live}
	r.usedPorts[live.LocalPort] = true
	r.leases[host] = live

	r.CleanLease("stale")

	current, ok := r.leases[host]
	if !ok {
		t.Fatal("cleaning up a stale session deleted the live session's routing entry -- the tunnel stays connected but the public URL 502s")
	}
	if current != live {
		t.Errorf("expected the host to still resolve to the live lease, got session %q", current.SessionToken)
	}

	// The stale session's own bookkeeping must still be released.
	if _, ok := r.sessionLeases["stale"]; ok {
		t.Error("expected the stale session's leases to be removed")
	}
	if r.usedPorts[stale.LocalPort] {
		t.Error("expected the stale session's port to be released")
	}
	if !r.usedPorts[live.LocalPort] {
		t.Error("the live session's port must not be released by another session's cleanup")
	}
}

// TestCleanLeaseRemovesHostItStillOwns confirms the guard does not prevent ordinary
// cleanup: a session that still owns its host must have the entry removed.
func TestCleanLeaseRemovesHostItStillOwns(t *testing.T) {
	const host = "solo.lfr-demo.se"

	r := newTestRegistry(t)

	only := &TunnelLease{FullHost: host, SessionToken: "only", LocalPort: 40001, CreatedAt: time.Now()}
	r.sessionLeases["only"] = []*TunnelLease{only}
	r.usedPorts[only.LocalPort] = true
	r.leases[host] = only

	r.CleanLease("only")

	if _, ok := r.leases[host]; ok {
		t.Error("expected the host entry to be removed when the cleaned session still owned it")
	}
	if r.usedPorts[only.LocalPort] {
		t.Error("expected the port to be released")
	}
}
