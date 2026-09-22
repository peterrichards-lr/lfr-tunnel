//go:build !linux && !darwin

package client

import (
	"fmt"
	"runtime"
)

// processArgv is not implemented on this platform (#2164).
//
// Windows has no /proc and no KERN_PROCARGS2, and its restart story is different anyway: there
// is no syscall.Exec, and relaunching a -gui process detached needs its own handling. Returning
// an error rather than a best guess keeps the caller honest -- an upgrade that cannot read how a
// process was started must leave it stopped and SAY so, not relaunch it with arguments it
// invented.
func processArgv(pid int) ([]string, error) {
	return nil, fmt.Errorf("reading a process command line is not supported on %s", runtime.GOOS)
}
