package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"lfr-tunnel/pkg/regionvocab"
)

// A failover used to arrive at the gateway describing itself as an ordinary probe (#2086).
//
// reregisterAcrossRegions clears cfg.Region and calls resolveServerURL, which records `probe`
// or `cache` for the election it just ran. That token was then reported by the registration,
// so central wrote an audit row identical to one from a client that had just launched and
// probed its way to the same region. The server could not tell a failover from a cold start,
// which is why no completed failback has ever been observed in production.
//
// These are source-level assertions because the alternative is standing up two gateways and
// killing one, which the edge E2E already does; what is worth guarding cheaply here is that
// the token is recorded on the right side of the call that reports it.

func mainSource(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	return string(body)
}

// funcSource returns the body of the named function, so ordering assertions cannot be
// satisfied by an unrelated occurrence elsewhere in the file.
func funcSource(t *testing.T, src, signature string) string {
	t.Helper()
	start := strings.Index(src, signature)
	if start < 0 {
		t.Fatalf("function %q not found -- this test is asserting about code that no longer exists", signature)
	}
	rest := src[start:]
	if end := strings.Index(rest, "\nfunc "); end > 0 {
		return rest[:end]
	}
	return rest
}

func TestFailoverIsRecordedBeforeTheRegistrationThatReportsIt(t *testing.T) {
	body := funcSource(t, mainSource(t), "func reregisterAcrossRegions(")

	record := strings.Index(body, "RecordRegionSource(regionvocab.SourceFailover)")
	resolve := strings.Index(body, "resolveServerURL(")
	register := strings.Index(body, "attemptRegistration(")

	if record < 0 {
		t.Fatal("the failover path records no region source, so a failover still reports " +
			"itself as an ordinary probe and is indistinguishable from a cold start")
	}
	if resolve >= 0 && record < resolve {
		t.Error("the failover token is recorded BEFORE resolveServerURL, which then overwrites " +
			"it with the probe/cache token for the election it runs")
	}
	if register >= 0 && record > register {
		t.Error("the failover token is recorded AFTER the registration that would have " +
			"reported it -- the gateway is told nothing, and the next registration inherits " +
			"a token describing the wrong event")
	}
}

func TestFailbackIsRecordedOnTheFailbackPath(t *testing.T) {
	src := mainSource(t)

	if !strings.Contains(src, "RecordRegionSource(regionvocab.SourceFailback)") {
		t.Fatal("nothing records a failback, so a client returning home is reported the same " +
			"as one that never left")
	}

	// The prober-driven failback: recorded before the registration that carries it.
	window := src
	if i := strings.Index(src, `applySession(newResp, "failback")`); i > 0 {
		window = src[max0(i-1500):i]
	}
	if !strings.Contains(window, "RecordRegionSource(regionvocab.SourceFailback)") {
		t.Error("the prober-driven failback registers without recording the failback token")
	}
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// CONTROL, and the one the issue asked for: the tokens must mean what they say. A path that is
// neither a failover nor a failback must not record either -- otherwise "failover" degrades
// into "a registration happened", which is what the audit row already told us.
func TestAnOrdinaryElectionRecordsNeitherToken(t *testing.T) {
	body := funcSource(t, mainSource(t), "func resolveServerURL(")

	for _, token := range []string{"SourceFailover", "SourceFailback"} {
		if strings.Contains(body, token) {
			t.Errorf("resolveServerURL records %s. That function runs at startup, so every "+
				"cold start would be reported as a failover and the token would mean nothing",
				token)
		}
	}

	// PREMISE: it records SOMETHING, or the assertion above passes over a function that
	// stopped recording sources at all.
	if !strings.Contains(body, "RecordRegionSource(") {
		t.Error("resolveServerURL no longer records any region source -- the check above " +
			"would pass over a function that reports nothing")
	}
}

// A latency-driven move to a region the user did not ask for is neither event, so the
// re-election branch must gate the failback token on the requested region.
func TestTheReelectionMoveOnlyCountsAsFailbackWhenItIsTheRequestedRegion(t *testing.T) {
	src := mainSource(t)

	i := strings.Index(src, "reelection.consume()")
	if i < 0 {
		t.Fatal("the re-election branch is gone; this test asserts about code that no longer exists")
	}
	branch := src[i:min(len(src), i+1800)]

	if !strings.Contains(branch, "SourceFailback") {
		t.Fatal("the re-election branch never records a failback, so the node-set watcher " +
			"bringing a client home to the region it asked for is reported as an ordinary move")
	}

	gated := regexp.MustCompile(`requestedRegion != ""\s*&&\s*target\.Region == requestedRegion`)
	if !gated.MatchString(branch) {
		t.Error("the re-election branch records a failback without checking the destination is " +
			"the region the user asked for. A move to a merely-closer region would then be " +
			"reported as a failback to somewhere the user never named.")
	}
}

// The vocabulary is sent to gateways that may predate it, so the tokens have to be declared
// members of it -- GetRegionSources reports declared sources in order and appends unknown ones.
func TestTheNewTokensAreInTheDeclaredVocabulary(t *testing.T) {
	for _, want := range []string{regionvocab.SourceFailover, regionvocab.SourceFailback} {
		found := false
		for _, s := range regionvocab.Sources {
			if s == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is not in regionvocab.Sources, so a report will not present it as a "+
				"declared bucket and a zero will read as 'not measured'", want)
		}
		if regionvocab.IsPinned(want) {
			t.Errorf("%q is classified as pinned. A client that moved between gateways is the "+
				"opposite of one that cannot move", want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
