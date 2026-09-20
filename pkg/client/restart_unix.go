//go:build !windows

package client

import (
	"fmt"
	"os"
	"syscall"
)

// restartProcess replaces this process image in place.
//
// exec(2) rather than spawn-and-exit: the PID, the controlling terminal and the shell's
// foreground job all survive, so a client running in a terminal restarts without the user's
// shell deciding the job finished, and a client under a service manager is not seen to exit.
func restartProcess(exe string, args []string) error {
	if err := syscall.Exec(exe, args, os.Environ()); err != nil {
		return fmt.Errorf("exec %s: %w", exe, err)
	}
	return nil // unreachable: a successful Exec never returns
}
