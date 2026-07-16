//go:build !windows

package osutil

import (
	"os/exec"
)

// BackgroundCommand creates an exec.Cmd. On Unix, no special flags are needed.
func BackgroundCommand(name string, arg ...string) *exec.Cmd {
	return exec.Command(name, arg...)
}
