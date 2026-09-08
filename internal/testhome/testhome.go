// Package testhome redirects the user's home directory for the duration of a test binary.
//
// Tests that resolve a path through os.UserHomeDir() -- LoadClientConfig(""),
// ResolveDefaultConfigPath(), the token-file and LDM-secrets fallbacks -- otherwise read
// whatever the developer running them happens to have in their own home. That is not a
// hypothetical: TestLoadClientConfig_TokenFile and TestInsecurePermissionWarning both failed
// on this machine and passed in CI, because CI's home is empty and a developer's is not
// (#1798). The tests were correct; the environment they asserted against was not theirs.
//
// It fails in the worse of the two directions. CI is the environment with no files, so the
// green run is the one that proves least, and the resolution ladder these tests exist to pin
// is exactly the thing a stray ~/.lfr-tunnel/config.yaml short-circuits.
package testhome

import (
	"fmt"
	"os"
	"path/filepath"
)

// Isolate points HOME and USERPROFILE at a fresh empty directory and returns a function that
// puts both back. Call it from TestMain so it covers every test in the package, including ones
// added later by someone who never reads this file -- per-test isolation is the arrangement
// that let #1798 through, since it protects the tests that remember and no others.
//
// USERPROFILE as well as HOME because os.UserHomeDir() reads USERPROFILE on Windows, and the
// packages this guards are built there.
func Isolate() func() {
	dir, err := os.MkdirTemp("", "lft-testhome-")
	if err != nil {
		// Nothing to restore, and no way to signal a failure from here without a *testing.T.
		// Returning a no-op leaves the tests reading the real home, which is what they did
		// before -- TestPackageTestsCannotReachTheRealHome is what notices.
		return func() {}
	}

	restore := make([]func(), 0, 2)
	for _, key := range []string{"HOME", "USERPROFILE"} {
		previous, had := os.LookupEnv(key)
		restore = append(restore, func() {
			var err error
			if had {
				err = os.Setenv(key, previous)
			} else {
				err = os.Unsetenv(key)
			}
			// Reported rather than discarded: a failure here leaves the process pointing at a
			// directory that is about to be removed, and the next thing to read it would fail
			// somewhere far less obvious than this line.
			if err != nil {
				fmt.Fprintf(os.Stderr, "testhome: could not restore %s: %v\n", key, err)
			}
		})
		if err := os.Setenv(key, dir); err != nil {
			fmt.Fprintf(os.Stderr, "testhome: could not redirect %s to %s: %v\n", key, dir, err)
		}
	}

	return func() {
		for _, put := range restore {
			put()
		}
		_ = os.RemoveAll(dir)
	}
}

// Dir reports the directory Isolate installed, for a test that needs to seed a file into the
// fake home rather than merely be protected from the real one.
func Dir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Clean(home)
}
