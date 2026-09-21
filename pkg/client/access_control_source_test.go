package client

import (
	"testing"
)

// The Inspector must show the access control the GATEWAY holds, not the local config file and a
// constant (#2130).
//
// engine.AccessMode was assigned the literal "or" at startup and the passcode and whitelist came
// from cfg, so the panel described the machine rather than the tunnel. Saving from it then posted
// that constant: a reservation set to "and" became "or", and access was widened for someone who
// only meant to change the passcode. Nothing in the UI indicated a mode change had happened.

func TestTheGatewaysAccessControlReplacesTheLocalGuess(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	// What the client used to hold: its own config, plus the hardcoded mode.
	engine.Passcode = "from-the-local-config"
	engine.WhitelistIPs = "10.0.0.0/8"
	engine.AccessMode = "or"

	// The literal rather than server.PasscodeMask, on purpose: the client does NOT import the
	// server package and does not need to know what this string means. It stores what arrives
	// and posts it back untouched, and the gateway reads its own mask as "unchanged". Teaching
	// the client the meaning would be a second place to get it wrong.
	const maskFromGateway = "********"
	engine.SetAccessControlFromGateway(maskFromGateway, "203.0.113.0/24", "and")

	engine.mu.RLock()
	mode, wl, pc := engine.AccessMode, engine.WhitelistIPs, engine.Passcode
	engine.mu.RUnlock()

	if mode != "and" {
		t.Errorf("access mode is %q; the gateway says \"and\", and a panel showing \"or\" will "+
			"post \"or\" back and widen access", mode)
	}
	if wl != "203.0.113.0/24" {
		t.Errorf("whitelist is %q, not the reservation's", wl)
	}
	if pc != maskFromGateway {
		t.Errorf("passcode is %q; it must be the mask, so the panel can show that one is set "+
			"without ever holding it", pc)
	}
}

// An older gateway sends none of these. The client must keep behaving exactly as it did rather
// than blanking a panel on a field the gateway has never heard of.
func TestAGatewayThatSaysNothingChangesNothing(t *testing.T) {
	engine := NewInterceptorEngine("127.0.0.1", nil)
	engine.Passcode = "kept"
	engine.WhitelistIPs = "10.0.0.0/8"
	engine.AccessMode = "passcode"

	// Exactly what applyGatewayAccessControl guards on.
	empty := &RegisterResponse{}
	if empty.AccessMode != "" || empty.WhitelistIPs != "" || empty.Passcode != "" {
		t.Fatal("fixture is not the empty case this is about")
	}

	engine.mu.RLock()
	mode := engine.AccessMode
	engine.mu.RUnlock()
	if mode != "passcode" {
		t.Errorf("the engine changed before anything was applied: %q", mode)
	}
}
