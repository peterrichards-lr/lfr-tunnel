package main

import (
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"testing"

	"lfr-tunnel/pkg/client"
)

// #1708 was not a broken hook. It was a hook with no caller: pkg/client.ExecuteHook, its
// config struct and its documentation all shipped in #1088, the call sites did not, and
// every unit test still passed because they tested the executor in isolation. Nothing in
// the suite could tell a wired feature from an unwired one.
//
// The four session-lifecycle hooks fire from inside main()'s failover loop, around a
// blocking RunClient and a live tunnel, so they cannot be driven from a unit test. What can
// be checked is the thing that was actually missing: that a call site exists at all. This
// asserts against main.go's syntax tree rather than its text, so a commented-out call or a
// renamed constant fails it.
//
// It deliberately proves less than it looks like it does -- it says the calls are there, not
// that they are in the right place. Correct placement is a review question, and the
// ordering guarantee comes from RunHook being synchronous (see pkg/client/hooks.go).
func TestSessionLifecycleHooksHaveCallSites(t *testing.T) {
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("failed to parse main.go: %v", err)
	}

	// warning_received is wired in pkg/client (noteShutdownWarning) and covered by a real
	// behavioural test there; the other four belong to the session loop in this file.
	want := map[string]bool{
		client.HookStopping: false,
		client.HookStopped:  false,
		client.HookStarting: false,
		client.HookStarted:  false,
	}

	goast.Inspect(file, func(n goast.Node) bool {
		call, ok := n.(*goast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*goast.SelectorExpr)
		if !ok || sel.Sel.Name != "RunHook" {
			return true
		}
		arg, ok := call.Args[0].(*goast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := arg.X.(*goast.Ident)
		if !ok || pkg.Name != "client" {
			return true
		}
		switch arg.Sel.Name {
		case "HookStopping":
			want[client.HookStopping] = true
		case "HookStopped":
			want[client.HookStopped] = true
		case "HookStarting":
			want[client.HookStarting] = true
		case "HookStarted":
			want[client.HookStarted] = true
		}
		return true
	})

	for event, found := range want {
		if !found {
			t.Errorf("the %q lifecycle hook is configurable and documented but has no engine.RunHook call site in main.go -- this is #1708 exactly", event)
		}
	}
}

// The hooks are useless if nothing hands the engine what the user configured.
func TestClientHooksConfigIsHandedToTheEngine(t *testing.T) {
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("failed to parse main.go: %v", err)
	}

	found := false
	goast.Inspect(file, func(n goast.Node) bool {
		call, ok := n.(*goast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		sel, ok := call.Fun.(*goast.SelectorExpr)
		if !ok || sel.Sel.Name != "SetHooks" {
			return true
		}
		arg, ok := call.Args[0].(*goast.SelectorExpr)
		if ok && arg.Sel.Name == "Hooks" {
			found = true
		}
		return true
	})

	if !found {
		t.Error("main.go never calls engine.SetHooks(cfg.Hooks): the parsed `hooks:` config would reach nothing")
	}
}
