package client

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// A tunnel that runs for days hears from the gateway about its minimum version only when it
// registers, so without this the warning window can open -- and the deadline pass -- while the
// client says nothing (#2523). The registration response carries the deadline and when the
// warning window opens; this schedules one line for each, so the user is told at the moment the
// state changes rather than at their next restart.
//
// Never disconnects anything. The gateway refuses NEW tunnels after the deadline; the one that
// is running keeps running, and the expiry line says so.

// defaultMinVersionWarningDays is the server's own default warning window, used only when a
// gateway that predates warning_at sends a deadline without one.
const defaultMinVersionWarningDays = 5

// minVersionWarningAt is when the warning window opens: the gateway's warning_at, or the
// default window before the deadline from a gateway too old to send one.
func minVersionWarningAt(m *MinVersionState, deadline time.Time) time.Time {
	if at, err := time.Parse(time.RFC3339, m.WarningAt); err == nil {
		return at
	}
	return deadline.Add(-defaultMinVersionWarningDays * 24 * time.Hour)
}

// NextMinVersionReminder returns when this client's standing next changes phase, and the state
// it will be in then. ok is false when nothing further is due: no floor, no deadline, or the
// deadline already passed.
//
// Pure, so every transition is testable without waiting for one.
func NextMinVersionReminder(m *MinVersionState, now time.Time) (at time.Time, next *MinVersionState, ok bool) {
	if m == nil || !m.Required {
		return time.Time{}, nil, false
	}
	deadline, err := time.Parse(time.RFC3339, m.Deadline)
	if err != nil {
		return time.Time{}, nil, false
	}
	following := *m
	switch warningAt := minVersionWarningAt(m, deadline); {
	case now.Before(warningAt):
		following.Phase = ConsentPhaseWarning
		following.SecondsRemaining = int64(deadline.Sub(warningAt).Seconds())
		return warningAt, &following, true
	case now.Before(deadline):
		following.Phase = ConsentPhaseExpired
		following.SecondsRemaining = 0
		return deadline, &following, true
	default:
		return time.Time{}, nil, false
	}
}

// minVersionReminder holds the one pending reminder. A new registration replaces it -- the
// gateway's answer is the current truth, and a reconnect to a gateway with a different floor
// must not leave the old one's timer behind.
type minVersionReminder struct {
	mu    sync.Mutex
	timer *time.Timer
	gen   int
}

// schedule replaces any pending reminder with the next one for m, calling say when it is due and
// then scheduling the one after it.
func (r *minVersionReminder) schedule(m *MinVersionState, now time.Time, say func(*MinVersionState)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	r.gen++
	at, next, ok := NextMinVersionReminder(m, now)
	if !ok {
		return
	}
	gen := r.gen
	r.timer = time.AfterFunc(at.Sub(now), func() {
		r.mu.Lock()
		current := gen == r.gen
		r.mu.Unlock()
		if !current {
			return // replaced by a newer registration after this one fired
		}
		say(next)
		r.schedule(next, at, say)
	})
}

// NoteMinVersion records this client's standing against the gateway's minimum version, for the
// Inspector, and (re)schedules the reminders for a long-running tunnel. Called on every
// successful registration, including reconnects and failovers; it prints nothing itself -- the
// startup notice does that -- only the later phase changes.
func (e *InterceptorEngine) NoteMinVersion(m *MinVersionState) {
	e.mu.Lock()
	e.MinVersion = m
	e.mu.Unlock()
	e.minVersionReminder.schedule(m, time.Now(), func(next *MinVersionState) {
		e.mu.Lock()
		e.MinVersion = next
		e.mu.Unlock()
		slog.Warn(fmt.Sprintf("[Client] %s", minVersionNoticeFrom(next)))
		e.LogEvent("warn", "min_version_phase", map[string]any{
			"phase":       next.Phase,
			"min_version": next.MinVersion,
			"deadline":    next.Deadline,
		})
	})
}
