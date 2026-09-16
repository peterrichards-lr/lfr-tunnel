package main

import (
	"context"
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/client"
)

// resetReelectionState clears the process-wide throttle and pending move, and the cooldowns,
// so one test cannot decide the outcome of the next. Both are package-level singletons because
// they outlive any one session.
func resetReelectionState(t *testing.T) {
	t.Helper()
	reelection.mu.Lock()
	reelection.pending = nil
	reelection.lastAt = time.Time{}
	reelection.mu.Unlock()

	cooldowns.mu.Lock()
	cooldowns.until = make(map[string]time.Time)
	cooldowns.mu.Unlock()
}

// The threshold, stated as cases rather than as a comment. A move is a re-registration plus a
// chisel reconnect, so "any improvement" is the wrong bar -- see reelectionMinGain for why both
// floors exist and where the numbers come from.
func TestMateriallyCloserRequiresBothFloors(t *testing.T) {
	ms := time.Millisecond
	cases := []struct {
		name      string
		current   time.Duration
		candidate time.Duration
		want      bool
	}{
		{"the #1947 Orlando measurement: Ireland against Ohio", 200 * ms, 47 * ms, true},
		{"no improvement at all", 120 * ms, 120 * ms, false},
		{"the candidate is worse", 60 * ms, 140 * ms, false},
		{"a 30ms gain on a short path is jitter, not geography", 100 * ms, 70 * ms, false},
		{"41ms on a short path clears both floors", 100 * ms, 59 * ms, true},
		{"50ms off a 400ms long haul is the same continent", 400 * ms, 350 * ms, false},
		{"exactly at the absolute floor is enough, when the fraction also clears", 100 * ms, 60 * ms, true},
		{"one millisecond under the absolute floor is not", 100 * ms, 61 * ms, false},
		{"a large gain that is a small fraction still fails", 1000 * ms, 900 * ms, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := materiallyCloser(tc.current, tc.candidate); got != tc.want {
				t.Errorf("materiallyCloser(%v, %v) = %v, want %v", tc.current, tc.candidate, got, tc.want)
			}
		})
	}
}

// fastestRegion has to be a function of the measurement alone. It used to depend on which probe
// goroutine finished first, so a tie was unreproducible and two clients on the same network could
// disagree about the same numbers.
func TestFastestRegionBreaksTiesOnTheName(t *testing.T) {
	rtts := map[string]time.Duration{
		"us": 40 * time.Millisecond,
		"eu": 40 * time.Millisecond,
		"in": 90 * time.Millisecond,
	}
	for i := 0; i < 50; i++ {
		if got := fastestRegion(rtts); got != "eu" {
			t.Fatalf("fastestRegion returned %q on iteration %d; a tie must resolve the same way every time", got, i)
		}
	}
	if got := fastestRegion(map[string]time.Duration{}); got != "" {
		t.Errorf("fastestRegion of nothing returned %q", got)
	}
}

// gateway is a stand-in that answers the health probe after a chosen delay, and optionally
// advertises a roster on /api/version.
func gateway(t *testing.T, delay time.Duration, roster func() map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/healthz":
			time.Sleep(delay)
			w.Header().Set("Content-Type", "application/json")
			// control_plane connected, not draining: fit to carry a session (#1165, #1238).
			fmt.Fprintf(w, `{"status":"healthy","control_plane":"connected"}`)
		case "/api/version":
			w.Header().Set("Content-Type", "application/json")
			if roster == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			regions := roster()
			body := `{"regions":{`
			first := true
			for name, u := range regions {
				if !first {
					body += ","
				}
				first = false
				body += fmt.Sprintf("%q:%q", name, u)
			}
			body += `},"regions_unavailable":{}}`
			fmt.Fprintf(w, "%s", body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The decision this issue is about: an edge woke up, it is materially closer, so move.
//
// The CONTROL is the second half, which differs only in how far away the new gateway is. If the
// threshold were dropped, or reversed, or if the current gateway's own measurement were not
// consulted, the two halves would give the same answer -- and this test would be satisfied by a
// function that always says "move".
func TestChooseReelectionTargetMovesOnlyForAMateriallyCloserGateway(t *testing.T) {
	// A round trip long enough that the gain clears both floors by the margin the real case
	// does (#1947 measured ~200ms to Ireland against ~47ms to Ohio).
	const farAway = 150 * time.Millisecond

	t.Run("a materially closer gateway appears", func(t *testing.T) {
		resetReelectionState(t)
		near := gateway(t, 0, nil)
		var far *httptest.Server
		far = gateway(t, farAway, func() map[string]string {
			return map[string]string{"eu": far.URL, "us": near.URL}
		})

		target := chooseReelectionTarget(far.URL, "", client.NewInterceptorEngine("", nil))
		if target == nil {
			t.Fatal("a gateway 150ms closer appeared and the client stayed put")
		}
		if target.Region != "us" || target.URL != near.URL {
			t.Fatalf("moved to %q (%s), want the near gateway", target.Region, target.URL)
		}
		if target.Regions["us"] != near.URL {
			t.Errorf("the target carries no roster for the session loop to register against: %v", target.Regions)
		}
	})

	t.Run("CONTROL: the new gateway is no closer", func(t *testing.T) {
		resetReelectionState(t)
		// Same shape, same code path, one difference: the newcomer is the same distance away.
		sameDistance := gateway(t, farAway, nil)
		var current *httptest.Server
		current = gateway(t, farAway, func() map[string]string {
			return map[string]string{"eu": current.URL, "us": sameDistance.URL}
		})

		if target := chooseReelectionTarget(current.URL, "", client.NewInterceptorEngine("", nil)); target != nil {
			t.Fatalf("the client tore down a working session to move to %q, which is no closer", target.Region)
		}
	})
}

// A client that named a region is not asking for the fastest gateway, it is asking for that
// region. When the named region is asleep at startup the client falls back to a probe (#1690);
// this is the point at which the request can finally be honoured, and latency is not the
// question -- so this deliberately uses a named region that is FURTHER away than the incumbent.
func TestChooseReelectionTargetHonoursTheRegionTheUserNamed(t *testing.T) {
	resetReelectionState(t)

	asked := gateway(t, 120*time.Millisecond, nil)
	var current *httptest.Server
	current = gateway(t, 0, func() map[string]string {
		return map[string]string{"eu": current.URL, "us": asked.URL}
	})

	target := chooseReelectionTarget(current.URL, "us", client.NewInterceptorEngine("", nil))
	if target == nil {
		t.Fatal("the region the user asked for came back and the client ignored it")
	}
	if target.Region != "us" || target.URL != asked.URL {
		t.Fatalf("moved to %q (%s), want the region the user named", target.Region, target.URL)
	}

	// And it stays put when the named region is the gateway it is already on -- otherwise every
	// roster change would tear down the session of every client that named its own region.
	resetReelectionState(t)
	if target := chooseReelectionTarget(current.URL, "eu", client.NewInterceptorEngine("", nil)); target != nil {
		t.Fatalf("a client already on the region it asked for was moved to %q", target.Region)
	}
}

// A flapping node changes the fingerprint every time it flaps. Without the throttle each flap is
// an interruption, which is worse than the problem being solved.
func TestChooseReelectionTargetIsThrottledAfterAMove(t *testing.T) {
	resetReelectionState(t)

	near := gateway(t, 0, nil)
	var far *httptest.Server
	far = gateway(t, 150*time.Millisecond, func() map[string]string {
		return map[string]string{"eu": far.URL, "us": near.URL}
	})

	// PREMISE: without the throttle this move is taken, so the assertion below is about the
	// throttle rather than about the decision.
	first := chooseReelectionTarget(far.URL, "", client.NewInterceptorEngine("", nil))
	if first == nil {
		t.Fatal("the premise failed: the move this test throttles was not taken in the first place")
	}
	reelection.propose(first)

	if second := chooseReelectionTarget(far.URL, "", client.NewInterceptorEngine("", nil)); second != nil {
		t.Fatalf("a second move was allowed immediately after the first, to %q", second.Region)
	}

	// The throttle lapses rather than latching: a roster that genuinely changes again later must
	// still be acted on.
	reelection.mu.Lock()
	reelection.lastAt = time.Now().Add(-2 * reelectionMinInterval)
	reelection.mu.Unlock()
	if third := chooseReelectionTarget(far.URL, "", client.NewInterceptorEngine("", nil)); third == nil {
		t.Error("the throttle never lapsed, so the client can move for this reason exactly once")
	}
}

// A gateway we deliberately left is not one to move back to just because it is advertised again
// (#1310). The cooldown failover honours has to be honoured here too, or the two mechanisms
// disagree about the same gateway.
func TestChooseReelectionTargetSkipsAGatewayInCooldown(t *testing.T) {
	resetReelectionState(t)

	near := gateway(t, 0, nil)
	var far *httptest.Server
	far = gateway(t, 150*time.Millisecond, func() map[string]string {
		return map[string]string{"eu": far.URL, "us": near.URL}
	})

	// PREMISE: this is a move that would otherwise be taken.
	if first := chooseReelectionTarget(far.URL, "", client.NewInterceptorEngine("", nil)); first == nil {
		t.Fatal("the premise failed: the move this test suppresses was not taken in the first place")
	}

	resetReelectionState(t)
	cooldowns.exclude(near.URL, time.Minute)
	if target := chooseReelectionTarget(far.URL, "", client.NewInterceptorEngine("", nil)); target != nil {
		t.Fatalf("moved to %q despite that gateway being in cooldown", target.Region)
	}
}

// Our own gateway not answering its probe is failover's business, not this one's. Acting on it
// here would move a client on the strength of a missed measurement -- and #1947 is the standing
// proof that this measurement can be wrong.
func TestChooseReelectionTargetStaysPutWhenTheCurrentGatewayDoesNotAnswer(t *testing.T) {
	resetReelectionState(t)

	near := gateway(t, 0, nil)
	var silent *httptest.Server
	silent = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/version" {
			fmt.Fprintf(w, `{"regions":{"eu":%q,"us":%q},"regions_unavailable":{}}`, silent.URL, near.URL)
			return
		}
		// Healthz refuses: up enough to answer /api/version, not fit to carry a session.
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer silent.Close()

	if target := chooseReelectionTarget(silent.URL, "", client.NewInterceptorEngine("", nil)); target != nil {
		t.Fatalf("moved to %q on the strength of our own gateway missing its probe", target.Region)
	}
}

// The watcher's whole job: notice the signal, decide, and end the session so the loop can move.
// Nothing else in this package's tests drives it, and a watcher that decides correctly and never
// cancels is a feature that does nothing.
func TestNodeSetWatcherEndsTheSessionForAMoveAndLeavesTheTarget(t *testing.T) {
	resetReelectionState(t)

	restore := nodeSetPollInterval
	nodeSetPollInterval = 10 * time.Millisecond
	defer func() { nodeSetPollInterval = restore }()

	near := gateway(t, 0, nil)
	var far *httptest.Server
	far = gateway(t, 150*time.Millisecond, func() map[string]string {
		return map[string]string{"eu": far.URL, "us": near.URL}
	})

	engine := client.NewInterceptorEngine("", nil)
	ctx, cancelOuter := context.WithCancel(context.Background())
	defer cancelOuter()

	cancelled := make(chan struct{})
	startNodeSetWatcher(ctx, func() { close(cancelled) }, engine, far.URL, "")

	// PREMISE: with no roster change the session must be left alone, or the assertion below is
	// satisfied by a watcher that cancels unconditionally.
	select {
	case <-cancelled:
		t.Fatal("the session was ended with no roster change at all")
	case <-time.After(200 * time.Millisecond):
	}

	// Now the roster moves, which is what the gateway's fingerprint reports.
	engine.NoteNodeSetFingerprint("aaaaaaaaaaaa")
	engine.NoteNodeSetFingerprint("bbbbbbbbbbbb")

	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the roster changed, a materially closer gateway was available, and the session was never ended")
	}

	target := reelection.consume()
	if target == nil {
		t.Fatal("the session was ended with no target recorded -- the loop would re-elect from scratch instead of moving")
	}
	if target.URL != near.URL {
		t.Errorf("the recorded target is %s, want the near gateway %s", target.URL, near.URL)
	}
}

// #1708's lesson, applied to this change: a feature with no call site passes every unit test in
// the package. The session loop cannot be driven from a unit test -- it wraps a blocking
// RunClient around a live tunnel -- so this asserts against main.go's syntax tree that the two
// halves are actually wired: the watcher is started, and the loop consumes what it leaves.
//
// It deliberately proves less than it looks like it does: that the calls exist, not that they
// are in the right place. Placement is a review question.
func TestNodeSetReelectionIsWiredIntoTheSessionLoop(t *testing.T) {
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("failed to parse main.go: %v", err)
	}

	want := map[string]bool{
		"startNodeSetWatcher": false, // the signal is watched for
		"consume":             false, // and what it leaves behind is acted on
	}

	goast.Inspect(file, func(n goast.Node) bool {
		call, ok := n.(*goast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *goast.Ident:
			if _, tracked := want[fun.Name]; tracked {
				want[fun.Name] = true
			}
		case *goast.SelectorExpr:
			recv, ok := fun.X.(*goast.Ident)
			if ok && recv.Name == "reelection" {
				if _, tracked := want[fun.Sel.Name]; tracked {
					want[fun.Sel.Name] = true
				}
			}
		}
		return true
	})

	for name, found := range want {
		if !found {
			t.Errorf("main.go never calls %s -- the node-set re-election is unreachable from the "+
				"session loop, which is exactly how #1708 shipped a feature nothing could run", name)
		}
	}
}
