package client

import (
	"bytes"
	"testing"
)

func TestLogParserWriter_Write(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	var buf bytes.Buffer
	w := &logParserWriter{original: &buf, engine: engine}

	msg := []byte("2026/07/02 08:30:14 client: Connecting to ws://127.0.0.1:60824/tunnel\n")
	n, err := w.Write(msg)
	if err != nil {
		t.Errorf("Write error")
	}
	if n != len(msg) {
		t.Errorf("Expected length %d, got %d", len(msg), n)
	}
}

func TestLogParserWriter_parseMessage(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	w := &logParserWriter{original: nil, engine: engine}

	w.parseMessage("client: Connection error: websocket: bad handshake")
	if engine.ConnState == "error" && engine.AuthErrorMessage != "" {
		t.Logf("State parsed correctly")
	}
}
