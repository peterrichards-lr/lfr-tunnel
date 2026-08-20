package client

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ExecuteHook runs a user-configured lifecycle hook command with contextual environment variables.
// A 15-second timeout safeguard is enforced to prevent hanging scripts from blocking connection loops.
func ExecuteHook(event string, hookCmd string, env map[string]string) error {
	if strings.TrimSpace(hookCmd) == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	slog.Info(fmt.Sprintf("[Client Hook] Executing %s hook: %s...", event, hookCmd))

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd.exe", "/c", hookCmd)
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", hookCmd)
	}

	// Inherit system environment variables and append LFT_* context
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("LFT_EVENT=%s", event))
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			slog.Info(fmt.Sprintf("[Client Hook Error] %s hook timed out after 15s: %s", event, hookCmd))
			return fmt.Errorf("%s hook timed out after 15 seconds", event)
		}
		slog.Info(fmt.Sprintf("[Client Hook Error] %s hook failed: %v (output: %s)", event, err, strings.TrimSpace(string(out))))
		return err
	}

	slog.Info(fmt.Sprintf("[Client Hook] Successfully completed %s hook.", event))
	return nil
}
