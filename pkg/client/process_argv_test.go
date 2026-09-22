package client

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// processArgv must return the real argument vector, exactly.
//
// Checked against this test binary's own os.Args, which is the only argv available here that is
// known independently. It exercises the platform reader for real -- /proc/<pid>/cmdline on
// Linux, KERN_PROCARGS2 on macOS -- rather than a fixture describing what those are believed to
// return.
//
// Reads a process; runs none. The EDR rules forbid executing a client binary locally, and
// nothing here does.
func TestProcessArgvMatchesOurOwnArguments(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("processArgv is deliberately unimplemented on %s", runtime.GOOS)
	}

	got, err := processArgv(os.Getpid())
	if err != nil {
		t.Fatalf("reading our own command line: %v", err)
	}

	if len(got) != len(os.Args) {
		t.Fatalf("got %d arguments, want %d\ngot:  %q\nwant: %q",
			len(got), len(os.Args), got, os.Args)
	}
	for i := range os.Args {
		if got[i] != os.Args[i] {
			t.Errorf("argument %d = %q, want %q", i, got[i], os.Args[i])
		}
	}
}

// An argument containing a space must survive.
//
// This is the reason the reader does not parse `ps` output: `ps` joins arguments with spaces
// and nothing can split them again, so `-header "X-Api: v2"` would come back as three arguments
// instead of two and the tunnel would restart misconfigured. Asserted through the test binary's
// own argv, since the test runner passes this flag value through unchanged.
func TestProcessArgvPreservesAnArgumentContainingSpaces(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("processArgv is deliberately unimplemented on %s", runtime.GOOS)
	}

	got, err := processArgv(os.Getpid())
	if err != nil {
		t.Fatalf("reading our own command line: %v", err)
	}

	// Find any argument the harness gave us that contains a space, and assert it came back as
	// ONE argument rather than having been split. When the harness supplies none, the identity
	// check above has already covered the general case.
	var withSpace string
	for _, a := range os.Args {
		if strings.Contains(a, " ") {
			withSpace = a
			break
		}
	}
	if withSpace == "" {
		t.Skip("no argument with a space in this run; the exact-match test covers the rest")
	}

	var found bool
	for _, a := range got {
		if a == withSpace {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("the argument %q was not returned intact; it has been split or mangled, which "+
			"would restart the tunnel with different configuration from the one the user had",
			withSpace)
	}
}

// A process whose command line cannot be read is not relaunched, and is reported.
//
// Guessing the arguments would start a tunnel configured differently from the one the user had
// -- a different subdomain, or no passcode -- which is worse than leaving it stopped, because
// it looks like it worked.
func TestAppendRelaunchTargetSkipsAProcessItCannotRead(t *testing.T) {
	// A pid that cannot be running: the kernel reserves 0, and no lookup can succeed for it.
	before := [][]string{{"/usr/local/bin/lfr-tunnel", "-subdomain", "demo"}}
	after := appendRelaunchTarget(before, 0, "background tunnel")

	if len(after) != len(before) {
		t.Errorf("a process whose command line could not be read was added to the relaunch "+
			"list anyway: %+v", after)
	}
}

// ...and one it can read is recorded verbatim.
func TestAppendRelaunchTargetRecordsWhatItRead(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("processArgv is deliberately unimplemented on %s", runtime.GOOS)
	}

	got := appendRelaunchTarget(nil, os.Getpid(), "test process")
	if len(got) != 1 {
		t.Fatalf("expected one relaunch target, got %d", len(got))
	}
	if len(got[0]) != len(os.Args) {
		t.Errorf("recorded %d arguments, want %d", len(got[0]), len(os.Args))
	}
}

// The upgrade must restart what it stopped -- all of it.
//
// stopActiveProcessesAndServices stops four kinds of thing and, before #2164, restarted two:
// launchd plists and the systemd service came back, while the GUI process and background
// tunnels it had killed were left down, with the upgrade reporting success either way. So
// whether a client survived an upgrade depended entirely on how it had been started.
//
// Read from the source because the alternative is an upgrade that downloads a release, swaps a
// binary and spawns clients -- none of which may happen on this machine: the EDR rules forbid
// executing a client binary locally, and doing it from a test is how the environment was lost
// three times. The thing worth protecting is narrow and textual: that both capture sites exist
// and the restart consumes them.
func TestTheUpgradeRestartsWhatItStopped(t *testing.T) {
	src, err := os.ReadFile("upgrade.go")
	if err != nil {
		t.Fatalf("read upgrade.go: %v", err)
	}
	text := string(src)

	// Both kinds of directly-killed process: the GUI, and each background tunnel.
	if got := strings.Count(text, "relaunch = appendRelaunchTarget("); got != 2 {
		t.Errorf("found %d capture site(s), want 2 (the GUI process and background tunnels). "+
			"A process that is killed without being captured is one the upgrade silently "+
			"leaves down.", got)
	}

	// ...and something has to start them again.
	if !strings.Contains(text, "osutil.BackgroundCommand(argv[0], argv[1:]...)") {
		t.Error("nothing relaunches the captured processes. Capturing how they were started " +
			"and then not restarting them is the defect #2164 describes, with extra steps.")
	}

	// Detached, like handleBackground does. Running them in the foreground would attach the
	// tunnels to the upgrade process and kill them again when it exits.
	if strings.Contains(text, "exec.Command(argv[0]") {
		t.Error("the relaunch uses exec.Command rather than osutil.BackgroundCommand, so the " +
			"restarted tunnel dies with the upgrade process that started it")
	}
}
