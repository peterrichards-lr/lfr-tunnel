package client

import (
	"encoding/json"
	"strings"
	"testing"
)

// The client half of #1763's transport. The defect these guard is a command that arrives and is
// not noticed, which on a support call is indistinguishable from a gateway that never sent one.

func TestParseDiagnosticsCommands(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"a real command", `{"commands":[{"id":"abc123","t":"collect_logs"}]}`, 1},
		// The body legitimately carries these, and an older gateway sends none of it. None of
		// them may produce a command, and none may produce an error.
		{"an empty body", ``, 0},
		{"an acknowledgement with no body content", `{}`, 0},
		{"a shutdown warning alone", `{"type":"node_shutdown_warning","seconds":300}`, 0},
		{"the no-lease reply", `{"status":"ok"}`, 0},
		{"malformed JSON is not an error", `{"commands":[{"id":`, 0},
		// A command with no id cannot be acked, so acting on it would produce an action the
		// gateway never learns about and re-sends forever.
		{"a command with no id", `{"commands":[{"t":"collect_logs"}]}`, 0},
		// The whole point of naming one command: an unknown verb must not dispatch.
		{"an unknown command type", `{"commands":[{"id":"abc123","t":"rm_rf"}]}`, 0},
		{"a known and an unknown together", `{"commands":[{"id":"a","t":"rm_rf"},{"id":"b","t":"collect_logs"}]}`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseDiagnosticsCommands([]byte(tc.body))
			if len(got) != tc.want {
				t.Fatalf("got %d command(s), want %d: %+v", len(got), tc.want, got)
			}
		})
	}
}

func TestParseDiagnosticsCommandsAlongsideAShutdownWarning(t *testing.T) {
	// Both ride one body because there is only one body. Each parser must still find its own.
	body := []byte(`{"type":"node_shutdown_warning","seconds":300,"commands":[{"id":"abc123","t":"collect_logs"}]}`)

	if _, ok := ParseNodeShutdownWarning(body); !ok {
		t.Error("the shutdown warning stopped parsing once a command shared the body")
	}
	if got := ParseDiagnosticsCommands(body); len(got) != 1 {
		t.Errorf("the command did not parse alongside a shutdown warning: %+v", got)
	}
}

func TestDiagnosticsCommandsAreDedupedButAlwaysReacked(t *testing.T) {
	e := &InterceptorEngine{}
	cmd := []DiagnosticsCommand{{ID: "abc123", Type: DiagnosticsCommandCollectLogs}}

	if fresh := e.noteDiagnosticsCommands(cmd); len(fresh) != 1 {
		t.Fatalf("the first arrival should be fresh, got %d", len(fresh))
	}
	// At-least-once delivery: the same command rides every heartbeat until an ack lands. Acting
	// on it twice would upload the logs twice.
	if fresh := e.noteDiagnosticsCommands(cmd); len(fresh) != 0 {
		t.Fatalf("a repeat arrival should not be fresh, got %d", len(fresh))
	}

	// ...but it must still be acked. A repeat means the previous ack did not arrive, which is
	// precisely when it needs sending again -- deduping the ack as well would deadlock the
	// exchange: the gateway re-sends forever and the client never answers.
	acks := e.takeDiagnosticsAcks()
	if len(acks) != 1 || acks[0] != "abc123" {
		t.Fatalf("acks = %v, want one entry for abc123", acks)
	}
	if again := e.takeDiagnosticsAcks(); len(again) != 0 {
		t.Errorf("acks were not cleared after being taken: %v", again)
	}
}

func TestDiagnosticsAcksAreDedupedWithinOneHeartbeat(t *testing.T) {
	e := &InterceptorEngine{}
	cmd := []DiagnosticsCommand{{ID: "abc123", Type: DiagnosticsCommandCollectLogs}}
	for i := 0; i < 4; i++ {
		e.noteDiagnosticsCommands(cmd)
	}
	if acks := e.takeDiagnosticsAcks(); len(acks) != 1 {
		t.Fatalf("four arrivals before one heartbeat should ack once, got %v", acks)
	}
}

// The heartbeat body is read through io.LimitReader(resp.Body, 512). A command plus a shutdown
// warning has to fit, or the JSON truncates and NEITHER parses -- so this bounds the wire format
// rather than trusting that it stays small.
func TestHeartbeatBodyFitsTheClientReadLimit(t *testing.T) {
	const clientReadLimit = 512

	body, err := json.Marshal(map[string]any{
		"type":    "node_shutdown_warning",
		"seconds": 300,
		"reason":  strings.Repeat("scheduled maintenance ", 5),
		"node_id": "edge-apac-1",
		"commands": []DiagnosticsCommand{
			{ID: "0123456789ab", Type: DiagnosticsCommandCollectLogs},
		},
	})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if len(body) > clientReadLimit {
		t.Fatalf("a shutdown warning plus one command is %d bytes, over the %d-byte read limit -- "+
			"the body would truncate and neither would parse", len(body), clientReadLimit)
	}

	// And the parsers must still work on it at that size, not merely fit.
	if _, ok := ParseNodeShutdownWarning(body); !ok {
		t.Error("the shutdown warning did not parse at the size limit")
	}
	if len(ParseDiagnosticsCommands(body)) != 1 {
		t.Error("the command did not parse at the size limit")
	}
}
