package server

import (
	"os"
	"strings"
	"testing"
)

// A stored passcode must never leave this process (#2130).
//
// The registration response now carries the reservation's access control so the client Inspector
// can show it. That is exactly the shape that destroyed passcodes on the portal side: it
// pre-filled the input with the stored bcrypt hash, and saving to change the WHITELIST re-hashed
// it, leaving no error and no way back to a value anyone knew (#2103).
func TestTheRegistrationResponseCanOnlyEverCarryTheMask(t *testing.T) {
	hash := "$2a$10$D9Uqx96vvqLs8tKTnyibye2BIdkG5aNBn6hcbv34uRP6zM83TFUQ6"

	if got := MaskPasscode(hash); got == hash {
		t.Fatal("MaskPasscode returned the stored hash; a client that round-trips it stores " +
			"HashPasscode(hash) and the real passcode stops working")
	} else if got != PasscodeMask {
		t.Errorf("MaskPasscode(hash) = %q, want %q", got, PasscodeMask)
	}

	if MaskPasscode("") != "" {
		t.Error("an unset passcode must mask to empty, or the client claims one exists")
	}
}

// ...and the registration path must actually USE it.
//
// The test above proves MaskPasscode masks. It says nothing about whether handleRegister calls
// it -- swapping in the raw stored value failed no test, which is the gap a control found. This
// closes it by reading the assignment itself.
//
// A source-level assertion, deliberately: the alternative is standing up a server with a
// database, a user and a reservation to exercise one field, and the thing worth protecting here
// is narrow and exact -- that a bcrypt hash is never what leaves this process.
func TestTheRegistrationResponsePasscodeIsAssignedFromTheMask(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("reading server.go: %v", err)
	}

	const field = "acPasscode"
	var assignments []string
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, field+" =") || strings.HasPrefix(trimmed, field+" :=") {
			assignments = append(assignments, trimmed)
		}
	}

	if len(assignments) == 0 {
		t.Fatal("found no assignment to the registration response's passcode at all; this check " +
			"has stopped recognising the code it guards, which is worse than not checking")
	}
	for _, a := range assignments {
		if !strings.Contains(a, "MaskPasscode(") {
			t.Errorf("the registration response's passcode is assigned without masking:\n    %s\n"+
				"The stored value is a bcrypt hash. A client that round-trips it stores "+
				"HashPasscode(hash) and destroys the passcode -- #2103, on the portal side.", a)
		}
	}
}
