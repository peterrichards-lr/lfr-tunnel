package gui

import (
	"strings"
	"testing"
)

// The tray's Connect must honour the flags the GUI was started with (#2074).
//
// It spawned the client with exactly []string{"-background"}, so
// `lfr-tunnel -gui -prefer-region apac` launched a tray that knew the user wanted apac and then
// connected a client that did not. An empty cfg.Region is the condition that hands the choice to
// the region cache (main.go:2320), so a cached election silently won: the user asked for apac by
// name, got eu, and nothing said so.

func joined(args []string) string { return strings.Join(args, " ") }

// TestConnectForwardsTheRegionTheGUIWasStartedWith is the reported defect.
func TestConnectForwardsTheRegionTheGUIWasStartedWith(t *testing.T) {
	got := clientArgsForConnect([]string{"-gui", "-prefer-region", "apac"})

	if !strings.Contains(joined(got), "-prefer-region apac") {
		t.Errorf("the region the user named was dropped: %v\n"+
			"Without it the client has an empty cfg.Region, which is exactly what lets a cached "+
			"election choose a different region silently", got)
	}
	if strings.Contains(joined(got), "-gui") {
		t.Errorf("-gui was forwarded to the child, which is not a tray: %v", got)
	}
	if !strings.Contains(joined(got), "-background") {
		t.Errorf("the child was not started in background mode: %v", got)
	}
}

// A flag's value is a separate argv element. Filtering tokens must not orphan one.
func TestConnectKeepsFlagValuesWithTheirFlags(t *testing.T) {
	got := clientArgsForConnect([]string{
		"-gui", "-prefer-region", "apac", "-subdomain", "demo", "-port", "8080",
	})

	for _, want := range []string{"-prefer-region apac", "-subdomain demo", "-port 8080"} {
		if !strings.Contains(joined(got), want) {
			t.Errorf("%q did not survive: %v", want, got)
		}
	}
}

func TestConnectDoesNotPassBackgroundTwice(t *testing.T) {
	got := clientArgsForConnect([]string{"-gui", "-background", "-prefer-region", "us"})

	if n := strings.Count(joined(got), "-background"); n != 1 {
		t.Errorf("-background appears %d times, want exactly 1: %v", n, got)
	}
	if !strings.Contains(joined(got), "-prefer-region us") {
		t.Errorf("the region was lost while de-duplicating -background: %v", got)
	}
}

// Both spellings, and the =value form, since Go's flag package accepts all of them.
func TestConnectDropsEverySpellingOfTheGUIFlag(t *testing.T) {
	for _, spelling := range []string{"-gui", "--gui", "-gui=true", "--gui=true"} {
		got := joined(clientArgsForConnect([]string{spelling, "-prefer-region", "sa"}))
		if strings.Contains(got, "gui") {
			t.Errorf("%q survived into the child's argv: %s", spelling, got)
		}
		if !strings.Contains(got, "-prefer-region sa") {
			t.Errorf("dropping %q also lost the region: %s", spelling, got)
		}
	}
}

// PREMISE: with no flags at all the behaviour is unchanged from before the fix. Otherwise this
// could regress the ordinary case while fixing the reported one.
func TestConnectWithNoFlagsStillJustBackgrounds(t *testing.T) {
	if got := clientArgsForConnect(nil); joined(got) != "-background" {
		t.Errorf("a tray started with no flags should spawn exactly -background, got: %v", got)
	}
}
