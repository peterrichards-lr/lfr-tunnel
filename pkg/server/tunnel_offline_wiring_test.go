package server

import (
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"testing"
)

// The decision is tested exhaustively in tunnel_offline_alert_test.go, and the first version of
// #2270 still shipped a defect -- because it was in how the HANDLER combined the results, not in
// the results. A reviewer restored the twelve-mails-a-minute bug by hand at the call site and the
// whole suite stayed green.
//
// So this asserts the mapping itself: which action does what. Same technique, and the same
// stated limits, as cmd/lfr-tunnel/hooks_wiring_test.go -- it says the wiring is right, not that
// the decision behind it is, and the decision has its own tests.
func TestTheOfflineAlertIsWiredToTheRightAction(t *testing.T) {
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "server.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing server.go: %v", err)
	}

	// Which method each case of the switch on NoteHeartbeat's action is allowed to call.
	want := map[string]string{
		"offlineAlertSend": "sendTunnelOfflineAlert",
		"offlineAlertHeld": "logTunnelOfflineAlertHeld",
		"offlineAlertNone": "",
	}
	seen := map[string]string{}

	goast.Inspect(file, func(n goast.Node) bool {
		clause, ok := n.(*goast.CaseClause)
		if !ok || len(clause.List) != 1 {
			return true
		}
		label, ok := clause.List[0].(*goast.Ident)
		if !ok {
			return true
		}
		if _, governed := want[label.Name]; !governed {
			return true
		}

		called := ""
		goast.Inspect(clause, func(inner goast.Node) bool {
			call, ok := inner.(*goast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*goast.SelectorExpr); ok {
				switch sel.Sel.Name {
				case "sendTunnelOfflineAlert", "logTunnelOfflineAlertHeld", "sendAdminAlert":
					called = sel.Sel.Name
				}
			}
			return true
		})
		seen[label.Name] = called
		return true
	})

	// Anti-vacuity first: a renamed action constant, or the switch replaced by an if, would
	// otherwise leave every assertion below unreachable and this test green.
	if len(seen) != len(want) {
		t.Fatalf("found %d of the %d offline-alert cases in server.go (%v); the switch has been "+
			"restructured and this guard is watching nothing", len(seen), len(want), seen)
	}

	for label, wantCall := range want {
		got := seen[label]
		if got == wantCall {
			continue
		}
		switch {
		case label == "offlineAlertHeld" && got == "sendTunnelOfflineAlert":
			t.Errorf("a HELD alert is sent immediately, which defeats the traffic check entirely")
		case label == "offlineAlertSend" && got == "":
			t.Errorf("an alert the decision said to SEND does nothing; a tunnel that goes down " +
				"is never reported")
		case label == "offlineAlertNone" && got != "":
			t.Errorf("the no-op case calls %q; this is the twelve-mails-a-minute defect, which "+
				"is what happens when the steady state acts like an event", got)
		default:
			t.Errorf("case %s calls %q, want %q", label, got, wantCall)
		}
	}
}

// NoteHeartbeat must actually be the thing the handler asks. If the handler went back to setting
// the status itself, the decision above would be tested and unused -- #1708's shape, and the
// reason the client half of this change has the same guard.
func TestTheHeartbeatHandlerAsksNoteHeartbeat(t *testing.T) {
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "server.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing server.go: %v", err)
	}

	called := false
	goast.Inspect(file, func(n goast.Node) bool {
		call, ok := n.(*goast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*goast.SelectorExpr); ok && sel.Sel.Name == "NoteHeartbeat" {
			called = true
		}
		return true
	})

	if !called {
		t.Error("server.go never calls NoteHeartbeat; the offline-alert decision is unwired")
	}
}
