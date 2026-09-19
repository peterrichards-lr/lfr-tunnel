package client

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A planned move must let in-flight requests finish (#2059).
//
// StartShutdownMigrator used to cancel the session within a second of the warning. That killed
// whatever was mid-flight -- measured in production at 945ms and 357ms against a steady-state
// 1245ms for the same path -- for no reason: the gateway announces the stop MINUTES ahead (282
// seconds in that capture) and is still serving throughout.
//
// waitForInFlight is tested directly rather than through the migrator: the migrator's job is to
// cancel, and driving it needs a live session. The property worth protecting is that the wait
// ends when the last request lands, and does not outlive its budget.

func TestTheDrainEndsAsSoonAsTheLastRequestLands(t *testing.T) {
	e := &InterceptorEngine{}
	atomic.StoreInt32(&e.ActiveConnections, 2)

	// Land them after a short delay; the drain must notice promptly rather than sitting out
	// its whole budget.
	go func() {
		time.Sleep(60 * time.Millisecond)
		atomic.AddInt32(&e.ActiveConnections, -1)
		time.Sleep(60 * time.Millisecond)
		atomic.AddInt32(&e.ActiveConnections, -1)
	}()

	start := time.Now()
	left := e.waitForInFlight(context.Background(), 5*time.Second)
	elapsed := time.Since(start)

	if left != 0 {
		t.Errorf("drain gave up with %d still in flight, want 0", left)
	}
	if elapsed > 2*time.Second {
		t.Errorf("drain took %s -- it should return when the count reaches zero, not sit out "+
			"the whole budget", elapsed.Round(time.Millisecond))
	}
	// PREMISE: it actually waited. A drain that returned instantly would satisfy both
	// assertions above while proving nothing, and that is precisely the defect -- cancelling
	// without waiting is what this replaces.
	if elapsed < 100*time.Millisecond {
		t.Errorf("drain returned after %s, before the in-flight requests could land -- it is "+
			"not waiting at all", elapsed.Round(time.Millisecond))
	}
}

func TestTheDrainGivesUpAtItsBudget(t *testing.T) {
	e := &InterceptorEngine{}
	atomic.StoreInt32(&e.ActiveConnections, 1) // never lands

	start := time.Now()
	left := e.waitForInFlight(context.Background(), 150*time.Millisecond)
	elapsed := time.Since(start)

	if left != 1 {
		t.Errorf("drain reported %d in flight, want 1 -- the request never completed", left)
	}
	if elapsed > time.Second {
		t.Errorf("drain took %s for a 150ms budget -- a hung request must not hold the move "+
			"open", elapsed.Round(time.Millisecond))
	}
}

// A zero or negative budget means "do not wait" -- the caller clamps it to the time remaining
// before the announced shutdown, which can already have passed.
func TestADrainWithNoBudgetDoesNotWait(t *testing.T) {
	e := &InterceptorEngine{}
	atomic.StoreInt32(&e.ActiveConnections, 3)

	start := time.Now()
	left := e.waitForInFlight(context.Background(), 0)

	if left != 3 {
		t.Errorf("reported %d in flight, want 3", left)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("waited %s on a zero budget", elapsed.Round(time.Millisecond))
	}
}

// TestTheMigratorDrainsBeforeCancelling is the integration half.
//
// The three tests above prove waitForInFlight behaves; none of them proves anything CALLS it.
// A correct helper nobody invokes is the shape of defect this session kept turning up, so the
// wiring is asserted too -- statically, because driving StartShutdownMigrator to the cancel
// needs a live session and a gateway announcing a shutdown.
func TestTheMigratorDrainsBeforeCancelling(t *testing.T) {
	src, err := os.ReadFile("interceptor.go")
	if err != nil {
		t.Fatalf("reading interceptor.go: %v", err)
	}

	const marker = "func (e *InterceptorEngine) StartShutdownMigrator("
	i := strings.Index(string(src), marker)
	if i < 0 {
		t.Fatal("StartShutdownMigrator has moved -- move this guard with it rather than deleting it")
	}
	body := string(src)[i:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}

	drain := strings.Index(body, "waitForInFlight(")
	cancelAt := strings.Index(body, "cancel()")

	if drain < 0 {
		t.Error("StartShutdownMigrator does not drain in-flight requests before cancelling " +
			"(#2059). Cancelling immediately kills whatever is mid-flight, and the gateway " +
			"announces its stop minutes ahead -- there is no reason to be in a hurry.")
	}
	if cancelAt < 0 {
		t.Fatal("StartShutdownMigrator no longer cancels -- the move depends on it")
	}
	if drain >= 0 && drain > cancelAt {
		t.Error("StartShutdownMigrator cancels BEFORE draining, so the drain cannot save " +
			"anything that was in flight")
	}
}
