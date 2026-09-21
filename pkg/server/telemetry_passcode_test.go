package server

import (
	"os"
	"strings"
	"testing"
)

// No payload that leaves this process may carry a stored passcode unmasked (#2135).
//
// The telemetry feed sent res.Passcode -- the bcrypt hash -- over a WebSocket to the browser,
// for every tunnel on the system when the viewer is an admin or owner. The reservations API has
// masked it since #2101; this path was missed, and went unnoticed because no UI renders the
// field. Not rendering it is not the same as not sending it.
//
// Asserted against the SOURCE deliberately. #2130 proved that testing MaskPasscode in isolation
// says nothing about whether a caller uses it: swapping the raw value back in failed no test.
// What is worth protecting here is exact and narrow -- that a bcrypt hash never crosses the
// process boundary -- so the assertion reads the assignment.
//
// Its limit, stated rather than discovered later: this cannot follow data flow. Reading the
// stored value into a local and masking that local would pass the eye and fail this check, and
// an indirection the other way would evade it entirely. It catches the DIRECT form, which is the
// form both leaks took, and reports per file when that form disappears.
func TestNoOutboundPayloadCarriesAStoredPasscode(t *testing.T) {
	// Files that build payloads for something outside this process, and that read a
	// reservation's stored passcode to do it.
	//
	// server.go is deliberately NOT here. It gains such a read in #2130, where the
	// registration response starts carrying access control -- and that change brings its own
	// guard on the assignment. Listing it before it has one would assert nothing while looking
	// like coverage, which is exactly what the per-file check below exists to catch.
	outbound := []string{"telemetry_ws.go", "api.go"}

	for _, file := range outbound {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		checked := 0
		for i, line := range strings.Split(string(src), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			// Any line that READS a reservation's stored passcode. Deliberately not "= res.
			// Passcode": matching only the broken spelling means the check reports marker rot
			// the moment it is fixed, and then guards nothing. Found by running it.
			if !strings.Contains(trimmed, "res.Passcode") &&
				!strings.Contains(trimmed, "existing.Passcode") {
				continue
			}
			// Writes INTO the reservation are the opposite direction and are not a leak.
			if strings.HasPrefix(trimmed, "res.Passcode =") ||
				strings.HasPrefix(trimmed, "existing.Passcode =") {
				continue
			}
			checked++
			if !strings.Contains(trimmed, "MaskPasscode(") {
				t.Errorf("%s:%d takes the stored passcode unmasked:\n    %s\n"+
					"That value is a bcrypt hash and this file builds payloads that leave the "+
					"process. Use MaskPasscode.", file, i+1, trimmed)
			}
		}

		// PER FILE, not across the set. A set-wide count stays non-zero while one file quietly
		// stops matching, so a rename in telemetry_ws.go alone would leave it unguarded with
		// everything still green. Found by running the control.
		if checked == 0 {
			t.Errorf("%s contains no reservation-passcode read at all. Either the payload moved "+
				"or this check no longer recognises it -- and an unguarded file is worse than "+
				"an unchecked one, because it looks covered.", file)
		}
	}
}

// The mask itself, so a failure above can be read as "the caller is wrong" rather than "the
// helper is wrong".
func TestMaskPasscodeHidesAStoredHash(t *testing.T) {
	hash := "$2a$10$D9Uqx96vvqLs8tKTnyibye2BIdkG5aNBn6hcbv34uRP6zM83TFUQ6"
	if got := MaskPasscode(hash); got == hash {
		t.Fatal("MaskPasscode returned the stored hash unchanged")
	} else if got != PasscodeMask {
		t.Errorf("MaskPasscode(hash) = %q, want %q", got, PasscodeMask)
	}
	if MaskPasscode("") != "" {
		t.Error("an unset passcode must mask to empty, or a viewer is told one exists")
	}
}
