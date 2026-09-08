package config

import (
	"os"
	"testing"

	"lfr-tunnel/internal/testhome"
)

// homeAtStartup is the real home directory of whoever is running this binary, captured before
// TestMain replaces it -- package-level variables are initialised before TestMain is called,
// which is the only window in which it is still visible.
var homeAtStartup, homeAtStartupErr = os.UserHomeDir()

// Tests in this package resolve paths through os.UserHomeDir(), so without this they read the
// home directory of whoever runs them (#1798). Installed here rather than in each test because
// per-test isolation only protects the tests that remember to ask for it -- which is precisely
// how two tests in this package came to pass in CI and fail on a developer's machine.
func TestMain(m *testing.M) {
	restore := testhome.Isolate()
	code := m.Run()
	restore()
	os.Exit(code)
}

// The mutation guard for the TestMain above. Deleting it, or an Isolate() that silently did
// nothing, would restore the original defect in the direction that is hardest to notice: the
// tests would still pass in CI, whose home is empty, and fail only on the machines of the
// people who have actually configured the product.
//
// Asserted as "not the home we started in" rather than "contains no ~/.lfr-tunnel", because
// the second is satisfied by CI's empty home whether isolation happened or not -- a green run
// proving nothing is what this whole issue is about.
func TestPackageTestsCannotReachTheRealHome(t *testing.T) {
	if homeAtStartupErr != nil {
		t.Skipf("the real home was not resolvable at startup, so there is nothing to compare against: %v", homeAtStartupErr)
	}

	current, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir() during tests: %v", err)
	}
	if current == homeAtStartup {
		t.Fatalf("tests are resolving the real home %q.\n"+
			"Anything this package reads through os.UserHomeDir() -- LoadClientConfig(\"\"), the "+
			"token-file and LDM-secrets fallbacks -- is then whatever the developer happens to "+
			"have, and the assertions become environment-dependent (#1798).", current)
	}
}
