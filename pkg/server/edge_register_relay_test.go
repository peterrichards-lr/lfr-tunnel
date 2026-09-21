package server

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// An edge must relay the reservation's access control to the client, not only apply it to the
// lease (#2139).
//
// #2130 added access_mode, whitelist_ips and passcode to RegisterResponse so the Inspector could
// show the real values. handleEdgeRegisterProxy rebuilds the response from a narrow struct, so
// those fields were dropped for every edge-served client -- which is most of them. The fix worked
// on the one gateway people are least often on.
//
// Read from the source rather than exercised end to end: standing up an edge, a control plane and
// a reservation to observe three strings is a lot of machinery, and what is worth protecting is
// exact -- that this function's response carries the fields, and that the passcode is masked.
func TestTheEdgeProxyRelaysAccessControlToTheClient(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("reading server.go: %v", err)
	}

	fn := regexp.MustCompile(`(?s)func \(s \*Server\) handleEdgeRegisterProxy\(.*?\n\}`).
		FindString(string(src))
	if fn == "" {
		t.Fatal("handleEdgeRegisterProxy not found; this check is asserting about code that no " +
			"longer exists, which is worse than not checking")
	}

	// The success response the client receives, asserted on the VALUE and not merely on the
	// field being mentioned. A first version checked for "AccessMode:" alone, which a field
	// set to "" would satisfy -- it would have passed against a response that relays nothing.
	for _, assignment := range []string{
		"AccessMode:      acMode,",
		"WhitelistIPs:    acWhitelist,",
		"Passcode:        acPasscode,",
	} {
		if !strings.Contains(fn, assignment) {
			t.Errorf("the edge's registration response does not carry %q -- an edge-served "+
				"client gets nothing and falls back to its own config file and a hardcoded "+
				"mode, which is the whole defect #2130 set out to fix", assignment)
		}
	}

	if !strings.Contains(fn, "MaskPasscode(") {
		t.Error("the edge relays a passcode without masking it. Central sends an edge the real " +
			"stored hash on purpose, because the edge needs it to VERIFY (#2125) -- passing that " +
			"to a client is the leak #2135 closed on the telemetry path")
	}
}

// The edge must not invent a second source for this. It already holds the values, and a separate
// lookup would be one more place for the client's view and the enforcement to disagree -- the
// split that made a passcode unopenable on an edge (#2125).
func TestTheEdgeRelaysWhatItAlreadyEnforcesWith(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("reading server.go: %v", err)
	}
	fn := regexp.MustCompile(`(?s)func \(s \*Server\) handleEdgeRegisterProxy\(.*?\n\}`).
		FindString(string(src))

	// Specifically the LOOKUP, not any mention of the map. "edgeAccess[" alone also matches
	// the line that BUILDS it a few statements earlier, so swapping the relay's source for a
	// different map left the check green -- found by running the control.
	if !strings.Contains(fn, "edgeAccess[activeDomains[0]]") {
		t.Error("the relayed access control is not read from edgeAccess[activeDomains[0]] -- " +
			"the map this function already built to enforce with. Reading it from anywhere " +
			"else lets the client's view and the enforcement drift apart, which is the split " +
			"that made a passcode unopenable on an edge (#2125)")
	}
}
