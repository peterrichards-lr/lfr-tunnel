package server

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"
)

// An ABORTED periodic rotation must be tried again when its own cause has cleared, not a full
// day later (#2210).
//
// The guarantee that must survive: the due instant is still advanced and persisted BEFORE the
// attempt, so a control plane crash-looping inside a rotation still defers rather than burning a
// generation per restart. Only an attempt that RETURNED may move the instant, and only ever
// closer -- never past the cadence the schedule would have had with none of this.
//
// Every assertion below names the outcome it is about. "The schedule moved" is true of a commit,
// of each abort reason and of a failure to persist, so it is not an assertion about any of them
// (§5c).

// stageOverdueRotation makes the next periodic rotation due, so one sweep runs exactly one
// attempt, and returns the state as it stood before it.
func stageOverdueRotation(t *testing.T, central *Server, now time.Time) visitorSessionRotationState {
	t.Helper()
	state := loadVisitorSessionRotationState(central.db)
	state.NextRotationAt = now.Add(-time.Minute)
	if err := saveVisitorSessionRotationState(central.db, state); err != nil {
		t.Fatalf("could not stage an overdue rotation: %v", err)
	}
	return state
}

// rotateToTheAcceptedBound fills the accepted set so the next attempt cannot mint into it.
func rotateToTheAcceptedBound(t *testing.T, central *Server) {
	t.Helper()
	var last visitorSessionRotationOutcome
	for i := 0; i < maxAcceptedVisitorSessionSecrets+1; i++ {
		last = central.RotateVisitorSessionSecret(context.Background(), central.db, visitorSessionTriggerManual, "peter@example.com", nil)
		if !last.committed() {
			break
		}
	}
	if last.committed() {
		t.Fatalf("rotations kept committing past the %d-key bound, so this fixture no longer reaches it",
			maxAcceptedVisitorSessionSecrets)
	}
}

// An abort AT THE BOUND clears by itself, at a knowable instant: the earliest scheduled
// retirement. The scheduler must use that instant rather than wait a day for a slot that frees
// in hours.
//
// The retirement schedule is rewritten to distinct instants first, on purpose. With every
// generation retiring at the same moment, "the schedule moved to a retirement" is satisfied by
// naming any of them, and only the FIRST is the answer to when the set has room.
func TestAnAbortAtTheAcceptedBoundIsRetriedAtTheEarliestRetirement(t *testing.T) {
	central, _ := aControlPlane(t)
	rotateToTheAcceptedBound(t, central)

	now := time.Now().UTC()
	state := loadVisitorSessionRotationState(central.db)
	ids := make([]string, 0, len(state.Retirements))
	for id := range state.Retirements {
		ids = append(ids, id)
	}
	if len(ids) < 2 {
		t.Fatalf("only %d generation(s) are awaiting retirement, so this test cannot tell the earliest from the latest", len(ids))
	}
	sort.Strings(ids) // map order is randomised; the staged schedule must not be
	earliest := now.Add(3 * time.Hour).Truncate(time.Second)
	for i, id := range ids {
		state.Retirements[id] = earliest.Add(time.Duration(i) * 10 * time.Hour)
	}
	state.NextRotationAt = now.Add(-time.Minute)
	if err := saveVisitorSessionRotationState(central.db, state); err != nil {
		t.Fatalf("could not stage the retirement schedule: %v", err)
	}

	central.sweepVisitorSessionRotation(context.Background(), central.db, now)

	after := loadVisitorSessionRotationState(central.db)
	// WHICH abort. Several guards abort, and each has a different right answer here, so the
	// instant below is only evidence if this attempt failed at the bound.
	if after.Last == nil || after.Last.committed() || !strings.Contains(after.Last.Reason, "more than the") {
		t.Fatalf("the sweep did not abort at the accepted bound, so the schedule it left says nothing about #2210: %+v", after.Last)
	}
	if !after.NextRotationAt.Equal(earliest) {
		t.Errorf("the next periodic rotation is due %s, want %s -- the earliest scheduled retirement.\n\n"+
			"The accepted set has room again the moment the first generation retires, and the "+
			"retirement sweep runs before rotation on every tick. Waiting %s instead costs the "+
			"fleet a day-old key for a reason that clears in hours (#2210).",
			after.NextRotationAt.Format(time.RFC3339), earliest.Format(time.RFC3339),
			now.Add(visitorSessionRotationInterval).Format(time.RFC3339))
	}
}

// An abort because a connected node did not ACKNOWLEDGE is a ten-second fault, so the retry is
// minutes away -- and doubles on each consecutive abort, so a fleet that is genuinely
// unreachable settles back to the normal cadence rather than attempting every quarter hour
// forever.
func TestAnUnacknowledgedAbortIsRetriedOnABackoffAndNotADayLater(t *testing.T) {
	withShortVisitorSessionAckTimeout(t, 750*time.Millisecond)

	central, ts := aControlPlane(t, "usedge", "wedged")
	aConnectedEdge(t, central, ts, "usedge")
	// Authenticated, connected and silent: it is pushed the new generation and never says
	// whether it applied it.
	aSilentEdgeNode(t, central, ts, "wedged")

	now := time.Now().UTC()
	stageOverdueRotation(t, central, now)
	central.sweepVisitorSessionRotation(context.Background(), central.db, now)

	after := loadVisitorSessionRotationState(central.db)
	if after.Last == nil || after.Last.committed() ||
		!strings.Contains(after.Last.Reason, "did not confirm they hold generation") {
		t.Fatalf("the sweep did not abort on an unacknowledged node, so the schedule it left says nothing about #2210: %+v", after.Last)
	}
	if after.ConsecutiveAborts != 1 {
		t.Errorf("consecutive aborts = %d after one aborted sweep, want 1; without the count the backoff cannot escalate", after.ConsecutiveAborts)
	}

	// The attempt itself takes a moment, so the retry is measured as a window rather than an
	// instant -- but a window nowhere near the flat interval the abort used to cost.
	waited := after.NextRotationAt.Sub(now)
	if waited < visitorSessionRotationUnackedBackoff || waited > visitorSessionRotationUnackedBackoff+time.Minute {
		t.Errorf("the next attempt is %s away, want about %s.\n\n"+
			"A node that did not answer inside the acknowledgement timeout is usually a node "+
			"that was busy for those few seconds; before #2210 that cost a full %s, and the "+
			"fleet's key was a day older than intended for a reason with a ten-second lifetime.",
			waited, visitorSessionRotationUnackedBackoff, visitorSessionRotationInterval)
	}
}

// The escalation itself, asserted as a property of the function rather than by staging seven
// consecutive real rotations.
func TestTheUnacknowledgedBackoffDoublesAndSaturatesAtTheNormalInterval(t *testing.T) {
	if got := visitorSessionUnackedRetryDelay(0); got != visitorSessionRotationUnackedBackoff {
		t.Errorf("the first retry waits %s, want %s", got, visitorSessionRotationUnackedBackoff)
	}
	previous := visitorSessionUnackedRetryDelay(0)
	saturated := false
	for aborts := 1; aborts < 64; aborts++ {
		got := visitorSessionUnackedRetryDelay(aborts)
		if got > visitorSessionRotationInterval {
			t.Fatalf("after %d consecutive aborts the retry waits %s, past the %s a rotation would have waited with no retry logic at all -- this may only make rotation more timely",
				aborts, got, visitorSessionRotationInterval)
		}
		if got < previous {
			t.Fatalf("after %d consecutive aborts the retry waits %s, less than the %s after %d -- the backoff must not shrink",
				aborts, got, previous, aborts-1)
		}
		if got == visitorSessionRotationInterval {
			saturated = true
			break
		}
		if got != previous*2 {
			t.Fatalf("after %d consecutive aborts the retry waits %s, want %s -- double the previous", aborts, got, previous*2)
		}
		previous = got
	}
	if !saturated {
		t.Errorf("the backoff never reaches %s, so a fleet that is genuinely unreachable goes on retrying faster than the normal cadence forever",
			visitorSessionRotationInterval)
	}
}

// THE PROPERTY, over every reason an abort could name rather than the two that name one today:
// no computed instant may fall outside the window, whatever the attempt asked for.
//
// The upper bound is what makes this change unable to make anything worse; the lower bound is
// the crash-storm guard -- an instant already past would have the sweep firing on every tick,
// which is what the pre-attempt write exists to prevent.
func TestNoAbortCanScheduleIntoThePastOrPastTheNormalInterval(t *testing.T) {
	now := time.Date(2026, 9, 24, 9, 14, 0, 0, time.UTC)
	floor := now.Add(visitorSessionRotationRetryFloor)
	ceiling := now.Add(visitorSessionRotationInterval)

	cases := []struct {
		name  string
		cause time.Time
		want  time.Time
	}{
		{"a retirement that is due but unswept", now.Add(-time.Hour), floor},
		{"a clock that moved backwards a year", now.AddDate(-1, 0, 0), floor},
		{"one tick away, which would fire on the next sweep", now.Add(visitorSessionRotationCheckInterval), floor},
		{"exactly now", now, floor},
		{"inside the window", now.Add(3 * time.Hour), now.Add(3 * time.Hour)},
		{"the interval itself", ceiling, ceiling},
		{"a retirement a week out", now.Add(7 * 24 * time.Hour), ceiling},
		{"a nonsense instant in the next century", now.AddDate(100, 0, 0), ceiling},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := visitorSessionRetryInstant(now, tc.cause)
			if !ok {
				t.Fatalf("an abort naming %s produced no instant at all", tc.cause.Format(time.RFC3339))
			}
			if !got.Equal(tc.want) {
				t.Errorf("got %s, want %s", got.Format(time.RFC3339), tc.want.Format(time.RFC3339))
			}
			if got.Before(floor) {
				t.Errorf("%s is sooner than the %s floor, so the sweep would fire on the next tick -- the storm the pre-attempt write exists to prevent",
					got.Format(time.RFC3339), visitorSessionRotationRetryFloor)
			}
			if got.After(ceiling) {
				t.Errorf("%s is later than %s, the instant the schedule would have carried with no retry logic at all; this mechanism may only make rotation more timely",
					got.Format(time.RFC3339), ceiling.Format(time.RFC3339))
			}
		})
	}

	// An attempt that named nothing keeps the flat interval: an unknown cause must not retry
	// faster than a known one.
	if _, ok := visitorSessionRetryInstant(now, time.Time{}); ok {
		t.Error("an abort that named no clearing instant was given one anyway, so a cause nobody understands retries faster than the ones that are understood")
	}
}

// A COMMITTED rotation keeps the normal interval, and clears the abort count.
//
// Stated separately because "the schedule is a day out" is the answer for a commit and the wrong
// answer for the aborts above; a change that brought every outcome forward would pass those and
// fail here.
func TestACommittedRotationKeepsTheNormalIntervalAndClearsTheAbortCount(t *testing.T) {
	central, _ := aControlPlane(t)

	now := time.Now().UTC()
	state := stageOverdueRotation(t, central, now)
	state.ConsecutiveAborts = 3 // as a run of earlier failures would have left it
	state.NextRotationAt = now.Add(-time.Minute)
	if err := saveVisitorSessionRotationState(central.db, state); err != nil {
		t.Fatalf("could not stage the abort count: %v", err)
	}

	before := central.visitorSessionSecrets.get().CurrentID
	central.sweepVisitorSessionRotation(context.Background(), central.db, now)

	after := loadVisitorSessionRotationState(central.db)
	if after.Last == nil || !after.Last.committed() {
		t.Fatalf("the sweep did not commit, so this says nothing about what a commit schedules: %+v", after.Last)
	}
	if central.visitorSessionSecrets.get().CurrentID == before {
		t.Fatal("the control plane is still minting with the previous generation after a committed rotation")
	}
	if want := now.Add(visitorSessionRotationInterval); !after.NextRotationAt.Equal(want) {
		t.Errorf("after a commit the next rotation is due %s, want %s -- a rotation that worked must not be rescheduled early",
			after.NextRotationAt.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	if after.ConsecutiveAborts != 0 {
		t.Errorf("consecutive aborts = %d after a commit, want 0 -- a stale count would leave the next unacknowledged abort waiting a backoff it has not earned",
			after.ConsecutiveAborts)
	}
}

// THE CRASH-STORM REGRESSION. An abort whose cause clears sooner than the floor must still not
// bring the next attempt inside it, or the sweep runs a rotation on every tick.
//
// Driven through two real sweeps: the second is the tick the cause said was enough, and nothing
// may happen on it.
func TestAnAbortWhoseCauseClearsImmediatelyStillWaitsTheRetryFloor(t *testing.T) {
	central, _ := aControlPlane(t)
	rotateToTheAcceptedBound(t, central)

	now := time.Now().UTC()
	state := loadVisitorSessionRotationState(central.db)
	// Every generation retiring a minute from now: due sooner than the floor, and not yet due,
	// so the retirement sweep at the top of the tick leaves them alone.
	soon := now.Add(time.Minute).Truncate(time.Second)
	for id := range state.Retirements {
		state.Retirements[id] = soon
	}
	state.NextRotationAt = now.Add(-time.Minute)
	if err := saveVisitorSessionRotationState(central.db, state); err != nil {
		t.Fatalf("could not stage the retirement schedule: %v", err)
	}

	central.sweepVisitorSessionRotation(context.Background(), central.db, now)

	after := loadVisitorSessionRotationState(central.db)
	if after.Last == nil || after.Last.committed() || !strings.Contains(after.Last.Reason, "more than the") {
		t.Fatalf("the sweep did not abort at the accepted bound, so this says nothing about the floor: %+v", after.Last)
	}
	if want := now.Add(visitorSessionRotationRetryFloor); !after.NextRotationAt.Equal(want) {
		t.Fatalf("the next attempt is due %s, want %s -- the retry floor.\n\n"+
			"An abort whose cause clears in a minute must still not schedule inside %s, or the "+
			"sweep attempts a rotation on every tick, which is the storm the pre-attempt write "+
			"exists to prevent.",
			after.NextRotationAt.Format(time.RFC3339), want.Format(time.RFC3339), visitorSessionRotationRetryFloor)
	}

	// The next tick: the cause has cleared by the abort's own reckoning, and nothing runs.
	attempts := after.Last.At
	central.sweepVisitorSessionRotation(context.Background(), central.db, now.Add(visitorSessionRotationCheckInterval))
	next := loadVisitorSessionRotationState(central.db)
	if next.Last == nil || !next.Last.At.Equal(attempts) {
		t.Errorf("a second attempt ran on the very next tick (%s, was %s); an abort must not be able to turn the sweep into a rotation storm",
			next.Last.At.Format(time.RFC3339Nano), attempts.Format(time.RFC3339Nano))
	}
}
