//go:build windows

package osutil

import (
	"os/exec"
	"syscall"
)

// BackgroundCommand creates an exec.Cmd that runs hidden on Windows to prevent console flashing.
func BackgroundCommand(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
