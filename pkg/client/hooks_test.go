package client

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExecuteHook_Success(t *testing.T) {
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "hook_env.txt")

	var scriptCmd string
	if runtime.GOOS == "windows" {
		scriptCmd = "echo %LFT_EVENT% > " + outPath
	} else {
		scriptCmd = "echo $LFT_EVENT > " + outPath
	}

	env := map[string]string{
		"LFT_NODE_ID": "edge-us",
	}

	err := ExecuteHook("warning_received", scriptCmd, env)
	if err != nil {
		t.Fatalf("expected ExecuteHook to succeed, got %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	if len(data) == 0 {
		t.Errorf("expected non-empty output in hook_env.txt")
	}
}

func TestExecuteHook_EmptyCmdIgnored(t *testing.T) {
	err := ExecuteHook("starting", "", nil)
	if err != nil {
		t.Fatalf("expected empty command to be ignored without error, got %v", err)
	}
}
