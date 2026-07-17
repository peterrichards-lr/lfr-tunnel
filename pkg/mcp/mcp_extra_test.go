package mcp

import (
	"testing"
)

func TestMCPServer_Tools(t *testing.T) {
	sendToolError("123", "some tool error")

	handleToolCall("123", "unknown_tool", []byte(`{}`))

	_, _ = startTunnel("sub", "8080", "localhost") //nolint:errcheck
	_, _ = stopTunnel("sub")                       //nolint:errcheck
	_, _ = replayRequest("req-1")                  //nolint:errcheck
}

func TestMCPServer_queryInspectorInfo_Error(t *testing.T) {
	_ = queryInspectorInfo("http://invalid-url-that-fails") //nolint:errcheck
}
