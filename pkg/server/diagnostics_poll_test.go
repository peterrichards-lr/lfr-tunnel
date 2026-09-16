package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Requesting a user's diagnostic logs is asynchronous by design (#1763): the command is queued,
// the client picks it up on its next tunnel-status heartbeat -- up to 5s later -- and only then
// collects, redacts and uploads. Neither portal arm reacted to any of that. The bundle list was
// a snapshot taken when the dialog opened, so an admin got a toast and then nothing, forever,
// until they closed the dialog and opened it again (#1944).
//
// The class this belongs to is "a UI that starts an asynchronous server-side action and then
// never looks again". A plain re-fetch is NOT a fix for it -- fired immediately it reads the
// state from before the client has even been told, and shows the admin exactly what the bug
// showed them. What is required is a bounded wait, and the properties that make a wait honest
// are what this asserts, in BOTH arms, because a capability in one arm only is a defect (#1866):
//
//  1. it is bounded on both axes -- a gap between reads and a total cap;
//  2. both arms agree on those bounds, so V1 and V2 are the same feature, not two guesses;
//  3. it has something to say while waiting, and something to say when nothing arrived. A
//     spinner that never resolves is worse than the snapshot it replaced;
//  4. it cannot outlive the dialog it writes into.
//
// Source-level, because neither arm has a runtime harness: V1 is a browser script the Go tests
// read as a file (see analytics_empty_table_test.go for the same shape) and V2 is TSX with no
// unit-test runner in ui/. The e2e suite cannot cover this either -- it would need a real client
// to upload a real bundle mid-test.
const (
	diagPollIntervalConst = "DIAG_POLL_INTERVAL_MS"
	diagPollTimeoutConst  = "DIAG_POLL_TIMEOUT_MS"
)

// diagPollArm is one portal arm, and the symbol in it that starts the wait. The two arms spell
// that differently -- V1 schedules a timer, V2 sets a deadline React's effect keys on -- so the
// marker is named per arm rather than pretended to be shared.
type diagPollArm struct {
	name      string
	path      string
	pollStart string
}

func diagPollArms() []diagPollArm {
	return []diagPollArm{
		{
			name:      "Portal V1",
			path:      filepath.Join("static", "dashboard.js"),
			pollStart: "pollForDiagnosticsBundle(email);",
		},
		{
			name:      "Portal V2",
			path:      filepath.Join("..", "..", "ui", "src", "pages", "AdminUsers.tsx"),
			pollStart: "setDiagPollDeadline(Date.now() + " + diagPollTimeoutConst + ");",
		},
	}
}

// readDiagArm reads one arm's source and fails the test if it has moved, rather than reporting a
// clean pass over a file that is not there.
func readDiagArm(t *testing.T, arm diagPollArm) string {
	t.Helper()
	b, err := os.ReadFile(arm.path)
	if err != nil {
		t.Fatalf("%s: read %s: %v (if the portal moved, move this check with it rather than deleting it)", arm.name, arm.path, err)
	}
	src := string(b)
	// Anti-vacuity: every assertion below is about the diagnostics panel, and all of them would
	// pass trivially against a file that no longer contains one.
	if !strings.Contains(src, "/api/admin/diagnostics/collect") {
		t.Fatalf("%s: %s does not request a diagnostics collection at all; this check is scanning the wrong file", arm.name, arm.path)
	}
	return src
}

// TestDiagnosticsCollectionIsWaitedForInBothArms covers properties 1, 2 and 3.
func TestDiagnosticsCollectionIsWaitedForInBothArms(t *testing.T) {
	// Collected across arms so a disagreement between them is its own failure, not two
	// independently-passing halves.
	intervals := map[string]int{}
	timeouts := map[string]int{}

	for _, arm := range diagPollArms() {
		src := readDiagArm(t, arm)

		for _, c := range []struct {
			name string
			into map[string]int
			min  int
			max  int
		}{
			{diagPollIntervalConst, intervals, 1000, 10000},
			{diagPollTimeoutConst, timeouts, 15000, 120000},
		} {
			re := regexp.MustCompile(regexp.QuoteMeta(c.name) + `\s*=\s*(\d+)`)
			m := re.FindStringSubmatch(src)
			if m == nil {
				t.Errorf("%s (%s) does not define %s: the wait after a queued collection has to be bounded by a value somebody can read, not by whatever the code happens to do (#1944)", arm.name, arm.path, c.name)
				continue
			}
			v, err := strconv.Atoi(m[1])
			if err != nil {
				t.Errorf("%s: %s = %q is not a number", arm.name, c.name, m[1])
				continue
			}
			if v < c.min || v > c.max {
				t.Errorf("%s: %s is %dms, outside the sane range %d-%dms. The client answers on a heartbeat up to 5s away (#1763), so a shorter gap hammers the gateway for nothing and a longer cap leaves an admin watching a spinner (#1944)", arm.name, c.name, v, c.min, c.max)
			}
			c.into[arm.name] = v
		}

		// The poll must be started in exactly one place, and that place must be after the
		// check that the command was actually queued. The response separates authorisation
		// from delivery on purpose: a request that was permitted but reached nobody -- no
		// client connected, or the client served by another gateway -- will NEVER produce a
		// bundle, and waiting on one would be a lie dressed as patience.
		queued := strings.Index(src, `delivery === 'queued'`)
		if queued < 0 {
			t.Errorf("%s (%s) does not compare delivery to 'queued'. Authorisation and delivery are separate axes in the response (pkg/server/diagnostics_consent.go), and only a queued command can ever produce a bundle (#1944)", arm.name, arm.path)
			continue
		}
		if n := strings.Count(src, arm.pollStart); n != 1 {
			t.Errorf("%s (%s) starts its wait via %q %d times; expected exactly 1, so that the queued guard below cannot be bypassed by a second call site (#1944)", arm.name, arm.path, arm.pollStart, n)
			continue
		}
		if start := strings.Index(src, arm.pollStart); start < queued {
			t.Errorf("%s (%s) starts its wait at byte %d, before the delivery == 'queued' check at byte %d: an undeliverable request would be waited on and would never arrive (#1944)", arm.name, arm.path, start, queued)
		}
		if !regexp.MustCompile(`if\s*\(\s*queued\s*\)`).MatchString(src) {
			t.Errorf("%s (%s) has no `if (queued)` guard around starting the wait (#1944)", arm.name, arm.path)
		}

		// Both ends of the wait have to say something. "Waiting" and "nothing arrived" are
		// different statements, and the second one is the requirement -- a wait that just
		// stops leaves the admin exactly where the bug did.
		for _, key := range []string{"diag_waiting", "diag_timeout"} {
			if !strings.Contains(src, key) {
				t.Errorf("%s (%s) never renders %q. A bounded wait has to report both that it is waiting and that it gave up; without the second, an admin sees a spinner stop and learns nothing (#1944)", arm.name, arm.path, key)
			}
		}
	}

	// Property 2: the arms are the same feature. Portal V1 and V2 are an A/B test, so a user
	// moved between them must not find the same request behaving differently (#1866).
	for name, byArm := range map[string]map[string]int{
		diagPollIntervalConst: intervals,
		diagPollTimeoutConst:  timeouts,
	} {
		if len(byArm) < 2 {
			continue // already reported above
		}
		var first string
		for arm := range byArm {
			if first == "" || arm < first {
				first = arm
			}
		}
		for arm, v := range byArm {
			if v != byArm[first] {
				t.Errorf("%s disagrees between arms: %s=%d, %s=%d. V1 and V2 are an A/B test, so the same request must not wait for different lengths of time depending on which portal the admin happens to be in (#1866)", name, first, byArm[first], arm, v)
			}
		}
	}
}

// TestDiagnosticsPollCannotOutliveItsDialog covers property 4.
//
// Asserted separately per arm because the mechanism genuinely differs, and asserting a shared
// spelling would be asserting a coincidence. V1 holds a timer in module scope, so something has
// to cancel it on the way out; V2's poll is a useEffect keyed on the selected user, so React's
// own cleanup is the cancellation and what matters is that the key is really there.
func TestDiagnosticsPollCannotOutliveItsDialog(t *testing.T) {
	arms := diagPollArms()

	v1 := readDiagArm(t, arms[0])
	// The body of closeUserDetailsModal -- every close path in V1 routes through it (the
	// footer button, the overlay click, and the post-kick refresh).
	body := regexp.MustCompile(`(?s)function closeUserDetailsModal\(\) \{(.*?)\n\}`).FindStringSubmatch(v1)
	if body == nil {
		t.Fatalf("%s: closeUserDetailsModal() not found in %s; this check can no longer see how the dialog closes", arms[0].name, arms[0].path)
	}
	if !strings.Contains(body[1], "stopDiagnosticsPoll()") {
		t.Errorf("%s: closeUserDetailsModal() does not call stopDiagnosticsPoll(). The timer lives in module scope, so without this it keeps re-fetching and writing into a dialog that is no longer on screen (#1944)", arms[0].name)
	}
	// Selecting a different user is the other way the panel changes underneath a running
	// wait, and it does not go through the close path.
	if !strings.Contains(v1, "resetDiagnosticsPanel(u.email)") {
		t.Errorf("%s: showUserDetails() does not call resetDiagnosticsPanel(u.email); opening a second user would inherit the first one's wait (#1944)", arms[0].name)
	}

	v2 := readDiagArm(t, arms[1])
	// The dependency array is the cancellation. Whitespace-insensitive because prettier owns
	// how this wraps, and a reformat must not read as a regression.
	deps := regexp.MustCompile(`\}\s*,\s*\[\s*diagPollDeadline\s*,\s*selectedUser\?\.email\s*\]\s*\)`)
	if !deps.MatchString(v2) {
		t.Errorf("%s: the diagnostics poll effect is not keyed on [diagPollDeadline, selectedUser?.email] in %s. That dependency IS the cancellation -- closing the dialog or picking another user re-runs the effect and clears the interval; drop the user from it and the poll writes into whichever dialog is open next (#1944)", arms[1].name, arms[1].path)
	}
	if !strings.Contains(v2, "clearInterval(id)") {
		t.Errorf("%s: the diagnostics poll effect does not clear its interval on cleanup (#1944)", arms[1].name)
	}
}
