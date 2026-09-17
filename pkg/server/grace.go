package server

import (
	"fmt"
	"time"
)

// The grace-window vocabulary, shared by every "you have N days before this stops working"
// deadline this server enforces.
//
// It was invented for policy re-consent (#1707) and is used verbatim by the min_version
// floor (#1988). These strings cross the wire to both portals and to the client, so they are
// part of the API surface: ConsentPhase* below are aliases of these, not copies, so the two
// cannot drift into meaning different things.
const (
	// GracePhaseNone means there is nothing outstanding.
	GracePhaseNone = ""
	// GracePhaseGrace means outstanding, deadline not close. Notice, not enforcement.
	GracePhaseGrace = "grace"
	// GracePhaseWarning means outstanding and inside the warning window: an escalated
	// banner in the portal, and a startup warning from the client.
	GracePhaseWarning = "warning"
	// GracePhaseExpired means the window has run out and the obligation is now enforced.
	GracePhaseExpired = "expired"
)

// gracePhase is the whole deadline decision, as a pure function of four values so it can be
// tested without a database or a clock.
//
// firstSeen is when this user first had the obligation put in front of them; a zero value
// means never, which is not expired -- it is "the window has not started". Somebody who has
// not logged in or run a client since the obligation was published has not been asked yet,
// and cutting them off for not answering a question nobody put to them is exactly the failure
// the first-sight model was chosen to avoid.
//
// This is deliberately ONE function serving two unrelated obligations. Consent expiry is a
// legal gate resolved in the portal; a version floor is a support decision resolved with
// `lfr-tunnel -upgrade`. The two deadlines are independent -- a user can be inside one and
// outside the other -- and nothing here couples them: each caller passes its own firstSeen and
// its own configured windows. What IS shared is how a window is computed, so a change to that
// arithmetic cannot apply to one and silently not the other (#1948).
func gracePhase(firstSeen, now time.Time, graceDays, warningDays int) (string, time.Time) {
	if firstSeen.IsZero() {
		return GracePhaseGrace, time.Time{}
	}
	deadline := firstSeen.Add(time.Duration(graceDays) * 24 * time.Hour)
	if !now.Before(deadline) {
		return GracePhaseExpired, deadline
	}
	if !now.Before(deadline.Add(-time.Duration(warningDays) * 24 * time.Hour)) {
		return GracePhaseWarning, deadline
	}
	return GracePhaseGrace, deadline
}

// clampWarningDays resolves a configured warning window against the grace window it sits
// inside, falling back to fallback when unset or nonsensical.
//
// A warning that starts before the window it warns about would be permanently on, which is
// the same as having no warning at all -- so the result never exceeds graceDays.
func clampWarningDays(graceDays, configured, fallback int) int {
	if configured <= 0 {
		if graceDays < fallback {
			return graceDays
		}
		return fallback
	}
	if configured > graceDays {
		return graceDays
	}
	return configured
}

// formatGraceRemaining renders a coarse duration -- days and hours, or hours and minutes
// inside the last day. Coarse on purpose: a deadline days away rendered to the second reads
// as machine output rather than as something to act on.
func formatGraceRemaining(seconds int64) string {
	if seconds <= 0 {
		return "no time"
	}
	d := time.Duration(seconds) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		if hours > 0 {
			return fmt.Sprintf("%dd %dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
