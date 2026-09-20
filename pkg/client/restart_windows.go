//go:build windows

package client

import (
	"fmt"
	"os"
	"os/exec"
)

// restartProcess starts a replacement and exits, because Windows has no exec(2).
//
// A process cannot replace its own image there, so the only way to restart is to start another
// and stop being one. The gap is real -- a new PID, and a moment with two processes -- but the
// alternative is telling Windows users to restart by hand for a setting the panel just saved.
func restartProcess(exe string, args []string) error {
	cmd := exec.Command(exe, args[1:]...) //nolint:gosec // our own path and our own argv
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting replacement client: %w", err)
	}
	// Released rather than waited on: the point is for this process to go away.
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("releasing replacement client: %w", err)
	}
	os.Exit(0)
	return nil // unreachable
}
