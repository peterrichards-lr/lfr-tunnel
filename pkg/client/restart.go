package client

import (
	"fmt"
	"os"
)

// RestartSelf replaces this client with a fresh one running the same command line.
//
// Eight of the nine settings in the panel are written to config.yaml and read only at startup,
// so saving them changes nothing about the session in front of the user (#2088). Telling them
// to restart by hand is a poor answer when the process knows how to do it.
//
// The same argv, deliberately: a client started with -prefer-region apac must come back in
// apac. Dropping the arguments would silently change where the tunnel lands, which is the
// defect #2074 already cost a release to find in the tray.
//
// It does not return on success.
func RestartSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable: %w", err)
	}
	return restartProcess(exe, os.Args)
}
