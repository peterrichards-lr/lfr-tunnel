package gui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// AppKit mutations must happen on the main thread (#2062).
//
// The tray used to rebuild and re-attach its whole menu from the watcher goroutine, and it
// crashed:
//
//	SIGTRAP: trace trap / signal arrived during cgo execution
//	systray/internal.(*darwinTray).SetMenu -> msgSend    [goroutine 9]
//	systray/internal.(*darwinTray).Run                   [goroutine 1, locked to thread]
//
// systray dispatches ITEM updates to the main thread for you -- SetLabel, SetDisabled and
// SetChecked go through performSelectorOnMainThread. SetMenu and SetIcon do not: they call
// msgSend directly. So the invariant is: build the menu once before Run(), and afterwards only
// touch items.
//
// Checked statically. Running the tray needs a desktop session and is outside what the local EDR
// rules allow, so a behavioural test is not available here -- and the bug is invisible on any
// launch where the race happens to go the right way, which is exactly the kind of defect a source
// check catches at the moment it is written.

var (
	// The two calls systray does NOT dispatch.
	undispatched = regexp.MustCompile(`\.Set(Menu|Icon)\(`)
	// The item setters it does.
	itemSetter = regexp.MustCompile(`\.Set(Label|Disabled|Checked|Icon)\(`)
)

// funcBody returns the source of the named function, from its signature to the next line that is
// a closing brace at column zero.
func funcBody(t *testing.T, src, signature string) string {
	t.Helper()
	i := strings.Index(src, signature)
	if i < 0 {
		t.Fatalf("could not find %q -- if it was renamed, move this guard with it rather than "+
			"deleting it", signature)
	}
	rest := src[i:]
	if end := strings.Index(rest, "\n}\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// TestRefreshOnlyTouchesMenuItems is the guard. refresh runs on the watcher goroutine, so it may
// use only the setters systray dispatches.
func TestRefreshOnlyTouchesMenuItems(t *testing.T) {
	src, err := os.ReadFile("gui.go")
	if err != nil {
		t.Fatalf("reading gui.go: %v", err)
	}

	body := funcBody(t, string(src), "func (m *trayMenu) refresh(")

	// Strip comments: this file documents the unsafe calls by name, and prose is not a call.
	var code []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		code = append(code, line)
	}
	joined := strings.Join(code, "\n")

	if m := undispatched.FindAllString(joined, -1); len(m) > 0 {
		t.Errorf("refresh calls %v, which systray does NOT dispatch to the main thread.\n"+
			"refresh runs on the watcher goroutine, so this is the #2062 crash: AppKit mutated "+
			"off the main thread. Only SetLabel/SetDisabled/SetChecked are safe here; anything "+
			"that changes the menu's SHAPE or the tray icon has to happen in buildMenu, before "+
			"Run() takes the thread.", m)
	}

	// PREMISE: refresh actually updates something. Absence of the unsafe calls is also what an
	// empty function looks like, and an empty refresh would pass the assertion above while
	// leaving the menu frozen.
	if !itemSetter.MatchString(joined) {
		t.Error("refresh updates no menu items at all -- the guard above would pass over a " +
			"function that does nothing")
	}
}

// TestTheMenuIsBuiltBeforeTheTrayRuns covers the other half: buildMenu calls SetMenu, so it has
// to happen on the main goroutine before Run() claims it.
func TestTheMenuIsBuiltBeforeTheTrayRuns(t *testing.T) {
	src, err := os.ReadFile("gui.go")
	if err != nil {
		t.Fatalf("reading gui.go: %v", err)
	}
	body := funcBody(t, string(src), "func StartGUI(")

	build := strings.Index(body, "buildMenu(")
	run := strings.Index(body, "tray.Run()")

	if build < 0 {
		t.Fatal("StartGUI no longer calls buildMenu")
	}
	if run < 0 {
		t.Fatal("StartGUI no longer calls tray.Run()")
	}
	if build > run {
		t.Error("StartGUI builds the menu AFTER tray.Run() -- Run locks the main thread, so " +
			"SetMenu then executes off it, which is the #2062 crash")
	}

	// And the build must not be inside the watcher goroutine.
	if g := strings.Index(body, "go func()"); g >= 0 && build > g {
		t.Error("buildMenu is called from inside the watcher goroutine -- it calls SetMenu, " +
			"which systray does not dispatch to the main thread")
	}
}
