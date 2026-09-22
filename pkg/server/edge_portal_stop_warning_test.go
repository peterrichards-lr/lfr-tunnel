package server

import (
	"os"
	"strings"
	"testing"
)

// stripLineComments removes // comments so a source assertion cannot match prose.
func stripLineComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// The portal's stop button must warn a node's clients before the instance goes away (#2167).
//
// The client has two cooldowns for leaving a gateway and they differ by forty times:
// regionFailoverCooldown is 90 seconds, plannedShutdownCooldown is an hour. The hour exists
// because a deliberately stopped gateway "is about to be unreachable for hours" and a 90-second
// hold-off would "re-elect it while it is still powered off, fail to register, and start the
// cycle again".
//
// This path never triggered it. It called client.Stop() FIRST and kicked the sessions
// afterwards, so there was no moment at which a warning could reach anyone, and clients
// experienced an administrative stop as an ordinary fault. Observed on 2026-09-22: three
// clients left edge-us and returned after 90 seconds rather than an hour. Harmless that day --
// the edge came back in three minutes -- but edge-us and apac both have scheduled overnight
// stops, where the same behaviour means churning every 90 seconds until morning.
//
// A source assertion, deliberately. Exercising the real handler needs a provisioner client, a
// live control channel to an edge and a clock to wait out the window, which is a great deal of
// machinery around a fact that is plain in the text: the warning is sent, and it is sent BEFORE
// the stop.
func TestThePortalStopWarnsClientsBeforeStopping(t *testing.T) {
	src, err := os.ReadFile("server_edge_provisioner.go")
	if err != nil {
		t.Fatalf("read server_edge_provisioner.go: %v", err)
	}
	text := string(src)

	stopHandler := text[strings.Index(text, "func (s *Server) handleAdminEdgeStop("):]
	if end := strings.Index(stopHandler, "\nfunc "); end > 0 {
		stopHandler = stopHandler[:end]
	}
	// Comments stripped before anything is located. The handler's own comment explains what it
	// "used to call client.Stop() first" -- and the first version of this test found THAT,
	// concluded the warning came after the stop, and failed on correct code. A check that
	// cannot tell an explanation from an instruction reports the opposite of the truth.
	stopHandler = stripLineComments(stopHandler)
	if stopHandler == "" {
		t.Fatal("handleAdminEdgeStop is gone; point this check at whatever replaced it")
	}

	warnAt := strings.Index(stopHandler, "BroadcastNodeShutdownWarning(")
	if warnAt < 0 {
		t.Fatal("the portal stop sends no shutdown warning. Its clients will treat the stop as " +
			"a fault and re-elect the node every 90s while it is powered off, instead of " +
			"holding off for plannedShutdownCooldown.")
	}

	stopAt := strings.Index(stopHandler, "client.Stop(")
	if stopAt < 0 {
		t.Fatal("the portal stop no longer stops anything")
	}

	// Order is the whole point. A warning sent after the instance is already going reaches
	// nobody, which is exactly the shipped behaviour this replaces.
	if warnAt > stopAt {
		t.Error("the shutdown warning is sent AFTER client.Stop(). The instance is already " +
			"going away, so no client can receive it -- which is the defect, not the fix.")
	}
}

// The stop is deferred, not immediate: the window is worth nothing if the instance goes down
// during it.
func TestThePortalStopWaitsOutTheWarningWindow(t *testing.T) {
	src, err := os.ReadFile("server_edge_provisioner.go")
	if err != nil {
		t.Fatalf("read server_edge_provisioner.go: %v", err)
	}
	text := string(src)

	if !strings.Contains(stripLineComments(text), "time.Sleep(edgePortalStopWarningSeconds * time.Second)") {
		t.Error("nothing waits out the warning window before stopping. Warning the clients and " +
			"then stopping immediately gives them no time to move, which is the same outcome " +
			"as not warning them at all.")
	}

	// Long enough to ride several heartbeats, which arrive about every five seconds. One
	// heartbeat's grace would depend on when in the cycle the button was pressed.
	if edgePortalStopWarningSeconds < 10 {
		t.Errorf("edgePortalStopWarningSeconds is %d, which may not span a heartbeat cycle",
			edgePortalStopWarningSeconds)
	}
}
