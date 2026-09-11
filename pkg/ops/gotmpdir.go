package ops

import (
	"fmt"
	"os"
)

// GOTMPDIR, not -o, decides where the Go toolchain first writes an executable: it links inside
// GOTMPDIR and only then moves the result to the -o path (#1337). An unsigned binary appearing in
// a system temp directory is the shape the local EDR quarantines.
//
// The Makefile pins it (`export GOTMPDIR := $(LFT_TEST_DIR)`), but nothing outside make inherits
// that. This package shells out to the toolchain from a binary the operator runs directly, so it
// has to establish the pin itself -- before #1859 it passed only GOOS and GOARCH, and every
// `lfr-tunnel-ops build` linked five executables into /var/folders. On 2026-09-09 SentinelOne
// quarantined exactly those, and took 61 tracked scripts with them as remediation collateral.

// edrLinkDirEnv is the same variable the Makefile derives from, so the two controls cannot
// disagree about where the whitelist is.
const edrLinkDirEnv = "LFT_TEST_DIR"

// EDRLinkDir reports the directory the Go toolchain must link inside, mirroring the Makefile's
// derivation exactly: LFT_TEST_DIR when set, else /private/tmp where it exists (macOS, which is
// where the EDR runs), else /tmp.
func EDRLinkDir() string {
	if dir := os.Getenv(edrLinkDirEnv); dir != "" {
		return dir
	}
	if info, err := os.Stat("/private/tmp"); err == nil && info.IsDir() {
		return "/private/tmp"
	}
	return "/tmp"
}

// RunGoCommand runs the Go toolchain with GOTMPDIR pinned to EDRLinkDir().
//
// Every invocation of `go` from this package goes through here rather than through
// RunCommandWithEnv directly, so a new call site cannot forget the pin. scripts/check-edr-safety.sh
// enforces that: a literal "go" passed to a Run* helper in Go source is a finding unless the file
// establishes GOTMPDIR itself.
//
// GOTMPDIR is appended last on purpose. os/exec keeps the final occurrence of a duplicated key,
// so this wins over anything inherited from the caller's environment -- the same reasoning as the
// Makefile using := rather than ?= for it (#1335): an inherited value silently winning is how a
// build left the whitelist while reporting nothing.
func RunGoCommand(extraEnv []string, args ...string) error {
	env := append([]string{}, extraEnv...)
	env = append(env, fmt.Sprintf("GOTMPDIR=%s", EDRLinkDir()))
	return RunCommandWithEnv(env, "go", args...)
}
