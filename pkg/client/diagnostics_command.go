package client

import (
	"encoding/json"
	"sync"
)

// Receiving a diagnostic-collection request from the gateway (#1763, part 2 of #1696).
//
// The gateway cannot open a connection to this process -- the client sits behind NAT and a
// firewall, which is what the tunnel is for. So the request arrives on the one channel that
// already runs the other way: the /api/tunnel-status heartbeat's response body, which this
// client already reads and already acts on for the node-shutdown warning (#1238).
//
// What this file does NOT do, deliberately: read, redact or upload anything. It recognises the
// request and acknowledges it. Reading the log files, redacting them and uploading them is the
// next part of #1763, and shipping an acknowledgement that arrives without an upload is a
// smaller lie than shipping an upload whose redaction has not been tested (#1696's own
// constraint -- "redact before upload, and verify it").
//
// Delivery is at-least-once, so the same command id can arrive on several heartbeats until an
// ack gets through. Dedupe is by id and it is this side's job.

// DiagnosticsCommand is one instruction from the gateway. The field names are short because the
// client reads only the first 512 bytes of the heartbeat body, and this shares that budget with
// the shutdown warning.
type DiagnosticsCommand struct {
	ID   string `json:"id"`
	Type string `json:"t"`
}

// DiagnosticsCommandCollectLogs is the only command that exists, and the only one this will act
// on. Anything else is ignored rather than dispatched: a general "run what the gateway says"
// path in a client that runs on a developer's machine is a foothold, not a feature.
const DiagnosticsCommandCollectLogs = "collect_logs"

// ParseDiagnosticsCommands pulls any commands out of a heartbeat response body.
//
// Tolerant by design. The body legitimately carries nothing, a shutdown warning, commands, or
// both, and an older gateway sends none of it. Anything unparseable yields no commands rather
// than an error -- this runs every five seconds and a malformed body must not become a log flood.
func ParseDiagnosticsCommands(data []byte) []DiagnosticsCommand {
	if len(data) == 0 {
		return nil
	}
	var payload struct {
		Commands []DiagnosticsCommand `json:"commands"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	out := make([]DiagnosticsCommand, 0, len(payload.Commands))
	for _, c := range payload.Commands {
		if c.ID == "" || c.Type != DiagnosticsCommandCollectLogs {
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// diagnosticsAckState remembers which command ids have been seen, so that at-least-once delivery
// produces one action and one log line rather than one per heartbeat.
type diagnosticsAckState struct {
	mu      sync.Mutex
	seen    map[string]bool
	pending []string
}

// noteDiagnosticsCommands records newly-arrived commands and reports which of them are new.
//
// The ack is queued rather than sent here: the heartbeat that delivered the command has already
// been answered, so the acknowledgement rides the next one. That is a five-second delay on a
// support action and it keeps the client from opening a request of its own.
func (e *InterceptorEngine) noteDiagnosticsCommands(cmds []DiagnosticsCommand) []DiagnosticsCommand {
	if len(cmds) == 0 {
		return nil
	}
	e.diagAcks.mu.Lock()
	defer e.diagAcks.mu.Unlock()
	if e.diagAcks.seen == nil {
		e.diagAcks.seen = make(map[string]bool)
	}

	var fresh []DiagnosticsCommand
	for _, c := range cmds {
		// The ack is queued on every arrival, not only the first: a repeat means the previous
		// ack did not reach the gateway, which is exactly when it needs sending again.
		e.diagAcks.pending = append(e.diagAcks.pending, c.ID)
		if e.diagAcks.seen[c.ID] {
			continue
		}
		e.diagAcks.seen[c.ID] = true
		fresh = append(fresh, c)
	}
	return fresh
}

// takeDiagnosticsAcks returns and clears the ids waiting to be acknowledged.
func (e *InterceptorEngine) takeDiagnosticsAcks() []string {
	e.diagAcks.mu.Lock()
	defer e.diagAcks.mu.Unlock()
	if len(e.diagAcks.pending) == 0 {
		return nil
	}
	// Deduplicated: several heartbeats can queue the same id before one gets through, and
	// there is no value in sending it four times in one request.
	seen := make(map[string]bool, len(e.diagAcks.pending))
	out := make([]string, 0, len(e.diagAcks.pending))
	for _, id := range e.diagAcks.pending {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	e.diagAcks.pending = nil
	return out
}
