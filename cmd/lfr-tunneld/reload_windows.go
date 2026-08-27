//go:build windows

package main

import "os"

// Windows has no SIGHUP, so there is nothing to reload on (#1309). The gateway runs on Linux in
// every deployment; this exists so the daemon still compiles for a Windows target.

func reloadSignals() []os.Signal {
	return nil
}

func isReloadSignal(os.Signal) bool {
	return false
}
