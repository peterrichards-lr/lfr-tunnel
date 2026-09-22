//go:build linux

package client

import (
	"fmt"
	"os"
	"strings"
)

// processArgv returns the full argument vector of a running process (#2164).
//
// Read from the OS rather than from anything the client persists. argv carries -passcode,
// -basic-auth and -token VALUES, so writing it to disk would put credentials at rest -- the
// class of leak closed in #2137, and the reason #2148 records flag NAMES only. Reading it from
// the kernel adds no exposure: it is already visible to this user through `ps`.
//
// /proc/<pid>/cmdline is NUL-separated, so the arguments come back exactly as they were passed.
// Splitting `ps` output on whitespace would corrupt any argument containing a space -- an
// injected header like `-header "X-Api: v2"` being the obvious one -- and a silently mangled
// restart is worse than no restart at all.
func processArgv(pid int) ([]string, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil, fmt.Errorf("reading /proc/%d/cmdline: %w", pid, err)
	}

	// A trailing NUL terminates the last argument rather than starting an empty one.
	parts := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("process %d reported an empty command line", pid)
	}
	return parts, nil
}
