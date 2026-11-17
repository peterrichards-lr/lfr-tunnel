package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// Review of #2484: start_tunnel's ports arrived only as a string, so an agent sending 9999 or
// [9999, 3000] had it silently dropped and the tunnel published discovery's port instead.
func TestMCPPortsParamAcceptsEveryShapeAndRefusesTheRest(t *testing.T) {
	cases := map[string]string{
		`"9999,3000"`:  "9999,3000",
		`9999`:         "9999",
		`[9999, 3000]`: "9999,3000",
		`null`:         "",
		``:             "",
	}
	for raw, want := range cases {
		got, err := mcpPortsParam(json.RawMessage(raw))
		if err != nil || got != want {
			t.Errorf("ports %s: got %q (%v), want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{`{"port": 9999}`, `true`, `[{"port": 9999}]`} {
		if _, err := mcpPortsParam(json.RawMessage(raw)); err == nil || !strings.Contains(err.Error(), "ports must be") {
			t.Errorf("ports %s was accepted; want an error, not a silent fall-through", raw)
		}
	}
}
