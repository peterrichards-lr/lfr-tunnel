package client

import (
	"os"
	"strings"
	"testing"
)

// What the client reports about how it was started (#2148).

// The constraint that is not optional: the value of a flag must never leave the machine.
// -passcode, -basic-auth and -token are all flags.
func TestNothingReportsAFlagValue(t *testing.T) {
	src, err := os.ReadFile("../../cmd/lfr-tunnel/launch_overrides.go")
	if err != nil {
		t.Fatalf("reading launch_overrides.go: %v", err)
	}
	body := string(src)

	start := strings.Index(body, "func launchFlagNames()")
	if start < 0 {
		t.Fatal("launchFlagNames not found -- this check guards code that no longer exists, " +
			"which is worse than not checking")
	}
	fn := body[start:]
	if end := strings.Index(fn[10:], "\nfunc "); end >= 0 {
		fn = fn[:end+10]
	}

	if !strings.Contains(fn, "f.Name") {
		t.Error("launchFlagNames does not take the flag's NAME")
	}
	if strings.Contains(fn, "f.Value") || strings.Contains(fn, ".Value.String()") {
		t.Error("launchFlagNames reads a flag's VALUE. -passcode and -token are flags: their " +
			"values must never leave the machine (#2135, #2137)")
	}
}

// Copied on the way in and on the way out, so a caller mutating its slice afterwards cannot
// change what a later registration reports.
func TestTheLaunchRecordIsCopiedNotAliased(t *testing.T) {
	flags := []string{"gui", "prefer-region"}
	overrides := map[string]string{"auth_token": "LFT_CLIENT_TOKEN"}

	RecordLaunchContext(flags, overrides)

	flags[0] = "mutated"
	overrides["auth_token"] = "mutated"

	gotFlags, gotOverrides := reportableLaunchContext()
	if gotFlags[0] != "gui" {
		t.Errorf("a caller's later mutation changed the reported flags: %v", gotFlags)
	}
	if gotOverrides["auth_token"] != "LFT_CLIENT_TOKEN" {
		t.Errorf("a caller's later mutation changed the reported overrides: %v", gotOverrides)
	}

	gotFlags[0] = "mutated-again"
	again, _ := reportableLaunchContext()
	if again[0] != "gui" {
		t.Errorf("the reader handed out its own slice: %v", again)
	}
}

// Nothing recorded means nothing reported, so a client that never calls this adds nothing to a
// registration rather than an empty map the server has to special-case.
func TestNothingRecordedReportsNothing(t *testing.T) {
	RecordLaunchContext(nil, nil)
	flags, overrides := reportableLaunchContext()
	if flags != nil || overrides != nil {
		t.Errorf("an unrecorded launch reported %v / %v", flags, overrides)
	}
}
