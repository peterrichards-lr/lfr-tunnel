package client

import (
	"encoding/json"
	"log/slog"
)

// Noticing that the gateway's node set changed, mid-session (#1937).
//
// This file recognises the signal and records it. It does NOT decide anything: whether to move,
// where to, and whether a move is worth the interruption are election questions, and election
// lives in cmd/lfr-tunnel where the prober and the cooldowns are. Keeping the split here means
// the gateway's influence ends at "something changed" -- it never names a destination, and a
// client that disagrees with it simply stays put.
//
// The comparison is against the FIRST fingerprint seen this session, not against anything this
// client computes. Two consequences, both wanted:
//
//   - The client and the gateway never have to agree on how the hash is built, so the wire
//     contract is only "this value changes when the roster changes".
//   - The baseline is reset at the start of every session (ResetNodeSet), because a session on a
//     different gateway is a different publisher. Without the reset, moving to a gateway that
//     reports a different value -- or none -- would read as a topology change and could bounce
//     the client straight back.

// ParseNodeSetFingerprint pulls the advertised node-set fingerprint out of a heartbeat response
// body.
//
// Tolerant in exactly the way ParseDiagnosticsCommands is, and for the same reason: the body
// legitimately carries nothing, a shutdown warning, commands, a fingerprint, or any combination,
// and an older gateway sends none of it. This runs every five seconds, so an unparseable body
// must produce no fingerprint rather than a log line.
func ParseNodeSetFingerprint(data []byte) (string, bool) {
	if len(data) == 0 {
		return "", false
	}
	var payload struct {
		Nodes string `json:"nodes"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || payload.Nodes == "" {
		return "", false
	}
	return payload.Nodes, true
}

// ResetNodeSet forgets the fingerprint baseline and any unconsumed change.
//
// Called at the start of each session, before the heartbeat that establishes the new baseline.
// A pending change from the previous gateway must not survive into a session that has just
// re-elected -- acting on it would move a client that has this second finished moving.
func (e *InterceptorEngine) ResetNodeSet() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nodeSetFingerprint = ""
	e.nodeSetChanged = false
}

// NoteNodeSetFingerprint records what the serving gateway says its roster hashes to, and raises
// the change signal when that differs from the first value seen this session.
//
// Exported, unlike noteShutdownWarning beside it, because this is one half of a contract whose
// other half (ConsumeNodeSetChange) lives in cmd/lfr-tunnel: the value arrives here and the
// decision is taken there, and a package that can only read the signal cannot exercise the pair.
//
// The baseline is moved forward on a change, so a roster that changes twice raises the signal
// twice rather than latching on the first. Logged once per distinct value, not per heartbeat --
// these arrive every five seconds.
func (e *InterceptorEngine) NoteNodeSetFingerprint(fingerprint string) {
	if fingerprint == "" {
		return
	}

	e.mu.Lock()
	previous := e.nodeSetFingerprint
	e.nodeSetFingerprint = fingerprint
	changed := previous != "" && previous != fingerprint
	if changed {
		e.nodeSetChanged = true
	}
	e.mu.Unlock()

	if !changed {
		return
	}
	slog.Info("[Client] The gateway reports that the set of available gateways has changed; reconsidering which one to use.")
	e.LogEvent("info", "node_set_changed", map[string]any{
		"was": previous,
		"now": fingerprint,
	})
}

// ConsumeNodeSetChange reports whether the node set has changed since it was last asked, and
// clears the signal.
//
// Read-and-clear rather than a level, so one change produces one reconsideration however long
// the reader takes to notice -- the same shape as ConsumeShutdownMigration.
func (e *InterceptorEngine) ConsumeNodeSetChange() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	changed := e.nodeSetChanged
	e.nodeSetChanged = false
	return changed
}

// NodeSetFingerprint returns the last fingerprint the serving gateway advertised, or "" when it
// has advertised none. Exported for the diagnostics/TUI surface and for tests.
func (e *InterceptorEngine) NodeSetFingerprint() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.nodeSetFingerprint
}
