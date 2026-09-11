package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
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

// uploadDiagnosticsBundle reads this client's logs, redacts them, and sends them to the gateway
// (#1894, completing #1763).
//
// Everything it sends comes from #1885's CollectRedactedLogs: application request and response
// bodies are excluded entirely rather than filtered, secrets are removed from what remains, and
// the whole thing is capped. Nothing here chooses what to read -- the paths come from the
// client's own LogDir, never from the gateway, so a collection request cannot name a file.
//
// Runs on its own goroutine. The heartbeat that delivered the command has already been answered,
// and reading and redacting several megabytes must not sit in the middle of a health check.
func (e *InterceptorEngine) uploadDiagnosticsBundle(serverURL, sessionToken, subdomain, requestID string) {
	logs, err := CollectRedactedLogs("", subdomain, DefaultCollectionMaxBytes)
	if err != nil {
		slog.Info(fmt.Sprintf("[Client] Could not read the logs for collection %s: %v", requestID, err))
		e.LogEvent("warn", "diagnostics_collect_failed", map[string]any{
			"request_id": requestID,
			"reason":     "read_failed",
		})
		return
	}
	if len(logs) == 0 {
		slog.Info(fmt.Sprintf("[Client] Collection %s found no logs to send.", requestID))
		return
	}

	type wireLog struct {
		Kind         string `json:"kind"`
		Content      string `json:"content"`
		Truncated    bool   `json:"truncated"`
		DroppedLines int    `json:"dropped_lines"`
	}
	payload := struct {
		RequestID string    `json:"request_id"`
		Logs      []wireLog `json:"logs"`
	}{RequestID: requestID}
	total := 0
	for _, l := range logs {
		payload.Logs = append(payload.Logs, wireLog{
			Kind: l.Kind, Content: string(l.Content),
			Truncated: l.Truncated, DroppedLines: l.DroppedLines,
		})
		total += l.Bytes
	}

	body, err := json.Marshal(payload)
	if err != nil {
		slog.Info(fmt.Sprintf("[Client] Could not encode collection %s: %v", requestID, err))
		return
	}

	req, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(serverURL, "/")+"/api/client/diagnostics/upload", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// The session token is what proves who this is; the request id is what proves the gateway
	// asked. Neither alone is enough, which is why both travel.
	req.Header.Set("X-Session-Token", sessionToken)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		slog.Info(fmt.Sprintf("[Client] Could not send collection %s: %v", requestID, err))
		e.LogEvent("warn", "diagnostics_collect_failed", map[string]any{
			"request_id": requestID,
			"reason":     "upload_failed",
		})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		slog.Info(fmt.Sprintf("[Client] The gateway refused collection %s (HTTP %d).", requestID, resp.StatusCode))
		e.LogEvent("warn", "diagnostics_collect_failed", map[string]any{
			"request_id":  requestID,
			"status_code": resp.StatusCode,
		})
		return
	}

	// Said out loud, and recorded in the user's own log. A collection they cannot see is
	// indistinguishable from one that did not happen, and this is their machine.
	slog.Info(fmt.Sprintf("[Client] Sent %d diagnostic log(s) (%d bytes) for collection %s, as requested by an administrator.",
		len(logs), total, requestID))
	e.LogEvent("info", "diagnostics_collect_sent", map[string]any{
		"request_id": requestID,
		"logs":       len(logs),
		"bytes":      total,
	})
}
