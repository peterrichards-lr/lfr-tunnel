package client

import (
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

// #2180. A gateway-shutdown warning describes one gateway. Nothing retired it, so it
// outlived the move it caused: observed in production for 65 minutes across a failover AND
// a failback, still reading "Gateway shutting down now" after the client had returned to
// the very gateway the warning was about -- which had restarted and was healthy.
//
// The class is state scoped to one gateway surviving onto another. `shutdownWarnNodeID`
// cannot be used to detect it: the carrier that reaches a client is the tunnel-status
// heartbeat, and Server.pendingShutdownWarning builds that body with no `node_id` field at
// all, so the recorded value is "" whatever the gateway. The retirement therefore hangs off
// the event that is always observable -- a session committed on a gateway.

// announceShutdown puts the engine in the state the bug reproduces from: a warning that has
// already elapsed, which is what renders as the fixed word "now".
func announceShutdown(t *testing.T, e *InterceptorEngine) {
	t.Helper()
	e.noteShutdownWarning(&NodeShutdownWarning{
		Type:             "node_shutdown_warning",
		NodeID:           "", // exactly what the heartbeat carrier sends -- see above.
		SecondsRemaining: 15,
		ShutdownAt:       time.Now().Add(-30 * time.Second).Unix(),
		Reason:           "Administrative stop requested from the portal",
	})
}

// The property, asserted on the engine rather than on any one reader of it: a warning
// announced while serving from apac reports nothing once a session is established on eu.
func TestShutdownWarningIsRetiredOnceASessionIsEstablished(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)
	e.SetRegionEndpoint("apac", "https://apac.example", nil)
	announceShutdown(t, e)

	if at, _, _ := e.ShutdownWarning(); at == 0 {
		t.Fatal("the warning was never recorded, so this test cannot observe it being " +
			"retired -- check noteShutdownWarning, not the clear")
	}

	// The move. This is the call cmd/lfr-tunnel's applySession makes for a failover, a
	// failback, a node-set change and a reconnect alike.
	e.SetRegionEndpoint("eu", "https://eu.example", []string{"https://peter.eu.example"})

	at, secs, reason := e.ShutdownWarning()
	if at != 0 || secs != 0 || reason != "" {
		t.Errorf("a shutdown announced by apac survived the session established on eu: "+
			"ShutdownWarning() = (at=%d, seconds=%d, reason=%q), want (0, 0, \"\"). "+
			"This is #2180: nothing clears the warning when the client moves.", at, secs, reason)
	}
}

// Returning to the gateway that announced the stop, after it has restarted, must retire the
// warning too. This is the half the production report was sharpest about -- the banner was
// still up 61 minutes later on a fresh session to that same node, uptime 00:47.
func TestShutdownWarningIsRetiredOnFailbackToTheAnnouncingGateway(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)
	e.SetRegionEndpoint("apac", "https://apac.example", nil)
	announceShutdown(t, e)
	e.SetRegionEndpoint("eu", "https://eu.example", nil)

	// ...and back, to the restarted apac.
	e.SetRegionEndpoint("apac", "https://apac.example", nil)

	if at, secs, reason := e.ShutdownWarning(); at != 0 || secs != 0 || reason != "" {
		t.Errorf("failing back to the restarted gateway left its old warning standing: "+
			"ShutdownWarning() = (at=%d, seconds=%d, reason=%q), want (0, 0, \"\")", at, secs, reason)
	}
}

// The reported instance: the TUI banner. Asserted through the real renderer, and only after
// confirming it renders while the warning stands -- an absence-only assertion would pass on
// a renderer that never produced the line at all.
func TestShutdownCountdownLineIsGoneAfterTheMoveItCaused(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)
	e.SetRegionEndpoint("apac", "https://apac.example", nil)
	announceShutdown(t, e)

	before := shutdownCountdownLine(e)
	if !strings.Contains(before, "shutting down") {
		t.Fatalf("the banner must be showing before the move for this test to mean "+
			"anything, got %q", before)
	}

	e.SetRegionEndpoint("eu", "https://eu.example", nil)

	if after := shutdownCountdownLine(e); after != "" {
		t.Errorf("the TUI still warns about a gateway this client has left: %q. "+
			"eu is not shutting down; apac was.", after)
	}
}

// The second member of the same class, and the reason the clear lives in the engine rather
// than in shutdownCountdownLine: RunHook assembles LFT_NODE_ID and LFT_SECONDS_REMAINING
// from the same fields for EVERY hook, so a `started` hook fired after a failover was being
// handed the departed gateway's countdown.
func TestSessionHooksDoNotInheritTheDepartedGatewaysShutdown(t *testing.T) {
	e, rec := engineWithRecorder(t, config.ClientHooksConfig{Started: "/usr/local/bin/on-started.sh"})
	e.SetRegionEndpoint("apac", "https://apac.example", nil)
	e.noteShutdownWarning(&NodeShutdownWarning{
		Type:             "node_shutdown_warning",
		NodeID:           "edge-apac",
		SecondsRemaining: 300,
		ShutdownAt:       time.Now().Add(5 * time.Minute).Unix(),
		Reason:           "Administrative stop requested from the portal",
	})

	e.SetRegionEndpoint("eu", "https://eu.example", nil)
	e.RunHook(HookStarted, map[string]string{"LFT_FAILOVER_REGION": "eu"})

	if got := rec.events(); len(got) != 1 {
		t.Fatalf("expected exactly one started hook, got %v", got)
	}
	env := rec.calls[0].env
	if env["LFT_NODE_ID"] != "" {
		t.Errorf("the started hook on eu was told LFT_NODE_ID=%q, which names the gateway "+
			"the client left", env["LFT_NODE_ID"])
	}
	if env["LFT_SECONDS_REMAINING"] != "0" {
		t.Errorf("the started hook on eu was told LFT_SECONDS_REMAINING=%q; eu has "+
			"announced no shutdown", env["LFT_SECONDS_REMAINING"])
	}
}

// A pending planned move belongs to the gateway that announced it. The session loop consumes
// it exactly once, but only on the branch it takes after a failure -- a session that ended
// for an unrelated reason first would carry the signal onto the new gateway, whose migrator
// would then tear down a healthy session.
func TestPendingPlannedMoveDoesNotFollowTheClientToTheNewGateway(t *testing.T) {
	e := NewInterceptorEngine("127.0.0.1", nil)
	e.SetRegionEndpoint("apac", "https://apac.example", nil)
	announceShutdown(t, e)

	if !e.PendingShutdownMigration() {
		t.Fatal("the migration signal was never raised, so this test cannot observe it " +
			"being retired")
	}

	e.SetRegionEndpoint("eu", "https://eu.example", nil)

	if e.PendingShutdownMigration() {
		at, reason, _ := e.ConsumeShutdownMigrationPeek()
		t.Errorf("apac's planned move followed the client to eu (at=%d, reason=%q); the "+
			"migrator on the new session would cancel it immediately", at, reason)
	}
}

// The wiring half. The four tests above prove the engine retires the warning when
// SetRegionEndpoint is called; none of them proves anything calls it when a session is
// established. That is the shape of defect #1708 was -- correct code with no call site --
// so it is asserted statically, against main.go's syntax tree rather than its text, because
// driving applySession needs a live gateway.
//
// It also pins the ORDER against the `started` hook, which is the only reason the hook test
// above holds in production: SetRegionEndpoint must run first.
func TestApplySessionRetiresTheWarningBeforeFiringStarted(t *testing.T) {
	const mainGo = "../../cmd/lfr-tunnel/main.go"

	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, mainGo, nil, 0)
	if err != nil {
		t.Fatalf("failed to parse %s: %v", mainGo, err)
	}

	body := applySessionBody(file)
	if body == nil {
		t.Fatalf("applySession is no longer a function literal in %s -- move this guard "+
			"with it rather than deleting it. It is what proves the engine's clear has a "+
			"caller at the moment a session is established.", mainGo)
	}

	regionEndpointAt, startedHookAt := -1, -1
	goast.Inspect(body, func(n goast.Node) bool {
		call, ok := n.(*goast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*goast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "SetRegionEndpoint":
			if regionEndpointAt < 0 {
				regionEndpointAt = int(call.Pos())
			}
		case "RunHook":
			if startedHookAt >= 0 || len(call.Args) == 0 {
				return true
			}
			if arg, ok := call.Args[0].(*goast.SelectorExpr); ok && arg.Sel.Name == "HookStarted" {
				startedHookAt = int(call.Pos())
			}
		}
		return true
	})

	if regionEndpointAt < 0 {
		t.Fatal("applySession no longer calls engine.SetRegionEndpoint, so a re-established " +
			"session never retires the previous gateway's shutdown warning (#2180). If the " +
			"commit point has moved, move the clear with it.")
	}
	if startedHookAt < 0 {
		t.Fatal("applySession no longer fires the started hook -- this guard's ordering " +
			"assertion has nothing to compare against")
	}
	if regionEndpointAt > startedHookAt {
		t.Error("applySession fires the started hook BEFORE SetRegionEndpoint, so that hook " +
			"is handed the departed gateway's LFT_NODE_ID and LFT_SECONDS_REMAINING (#2180)")
	}
}

// applySessionBody returns the body of main.go's `applySession := func(...)`, or nil.
func applySessionBody(file *goast.File) *goast.BlockStmt {
	var found *goast.BlockStmt
	goast.Inspect(file, func(n goast.Node) bool {
		assign, ok := n.(*goast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		name, ok := assign.Lhs[0].(*goast.Ident)
		if !ok || name.Name != "applySession" {
			return true
		}
		if lit, ok := assign.Rhs[0].(*goast.FuncLit); ok {
			found = lit.Body
			return false
		}
		return true
	})
	return found
}
