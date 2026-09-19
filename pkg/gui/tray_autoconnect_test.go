package gui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The tray used to start idle and wait for a click (#2076).
//
// `lfr-tunnel -gui` built the menu, started the settings server and started the state watcher,
// and never brought a tunnel up -- StartGUI had no path that started a client at all. Launch on
// Login therefore produced an icon and nothing else, and after #2074 it produced something
// worse: a tray that knew which region the user had named on the command line and did nothing
// with it until someone noticed the menu.

func TestTheTrayConnectsWhenNothingIsRunning(t *testing.T) {
	if !autoConnectOnStart(true, false) {
		t.Error("the GUI started with nothing running and did not connect -- this is the " +
			"reported defect: the tray comes up idle and waits to be asked")
	}
}

// Attaching to a running client is the one case where connecting is wrong: a second client
// would race the first for the same subdomain.
func TestTheTrayDoesNotStartASecondClient(t *testing.T) {
	if autoConnectOnStart(true, true) {
		t.Error("a client was already running and the tray started another one")
	}
}

// -no-autoconnect has to actually opt out. Launch on Login plus auto-connect means a tunnel
// comes up as soon as the user logs in, and that must be refusable.
func TestTheOptOutIsHonoured(t *testing.T) {
	if autoConnectOnStart(false, false) {
		t.Error("-no-autoconnect was given and the tray connected anyway")
	}
	if autoConnectOnStart(false, true) {
		t.Error("-no-autoconnect was given and the tray connected anyway")
	}
}

// The opt-out describes how the TRAY starts. Forwarding it to the spawned client would be
// passing a flag about the GUI to a process that is not one.
func TestConnectArgsDropTheTrayOnlyOptOut(t *testing.T) {
	for _, spelling := range []string{
		"-no-autoconnect", "--no-autoconnect", "-no-autoconnect=true", "--no-autoconnect=false",
	} {
		got := clientArgsForConnect([]string{"-gui", spelling, "-prefer-region", "apac"})

		if strings.Contains(joined(got), "no-autoconnect") {
			t.Errorf("%s was forwarded to the client: %v", spelling, got)
		}
		// PREMISE: the filter removed the opt-out and not the argument list.
		if !strings.Contains(joined(got), "-prefer-region apac") {
			t.Errorf("filtering %s also lost the region: %v", spelling, got)
		}
	}
}

// StartGUI has to call the decision and act on it. The three assertions above are about a pure
// function, and a pure function nothing calls changes no behaviour whatsoever.
func TestStartGUIActuallyConnectsOnStartup(t *testing.T) {
	src, err := os.ReadFile("gui.go")
	if err != nil {
		t.Fatalf("reading gui.go: %v", err)
	}
	body := stripComments(funcBody(t, string(src), "func StartGUI("))

	decide := strings.Index(body, "autoConnectOnStart(")
	spawn := strings.Index(body, "startClient()")
	run := strings.Index(body, "tray.Run()")

	if decide < 0 {
		t.Fatal("StartGUI never calls autoConnectOnStart -- the tray still starts idle, which " +
			"is the whole of #2076")
	}
	if spawn < 0 {
		t.Fatal("StartGUI decides to connect and then never starts a client")
	}
	if spawn < decide {
		t.Error("StartGUI starts a client before deciding whether it should -- -no-autoconnect " +
			"cannot work from there")
	}
	if run >= 0 && spawn > run {
		t.Error("the auto-connect is after tray.Run(), which blocks on the main thread for the " +
			"life of the process -- it would never execute")
	}
}

// Connect and auto-connect must launch the same thing. They were one call site when only
// Connect existed; the moment there were two, "Connect passes the CLI flags and startup does
// not" became a bug waiting to be written -- and that bug is #2074, already paid for once.
func TestThereIsOnlyOneWayToLaunchTheClient(t *testing.T) {
	src, err := os.ReadFile("gui.go")
	if err != nil {
		t.Fatalf("reading gui.go: %v", err)
	}
	code := stripComments(string(src))

	if n := strings.Count(code, "clientArgsForConnect(os.Args"); n != 1 {
		t.Errorf("clientArgsForConnect is applied at %d call sites, want exactly 1 "+
			"(startClient).\nTwo places building the client's argv is how they drift: one gains "+
			"a flag, the other does not, and the tray connects differently depending on whether "+
			"you clicked", n)
	}
}

var commentLine = regexp.MustCompile(`(?m)^\s*//.*$`)

func stripComments(s string) string { return commentLine.ReplaceAllString(s, "") }
