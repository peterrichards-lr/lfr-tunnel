//go:build !windows

package main

import (
	"os"
	"syscall"
)

// SIGHUP is the reload signal everywhere the gateway actually runs (#1309).
//
// Split by platform because syscall.SIGHUP does not exist on Windows -- referring to it in
// main.go would stop the daemon compiling there, and the cross-platform build in
// lfr-tunnel-ops builds this command for every target.

func reloadSignals() []os.Signal {
	return []os.Signal{syscall.SIGHUP}
}

func isReloadSignal(sig os.Signal) bool {
	return sig == syscall.SIGHUP
}
