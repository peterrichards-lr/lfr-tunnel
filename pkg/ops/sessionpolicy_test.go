package ops

import (
	"strings"
	"testing"
)

const liveConfigFixture = `# gateway config
enable_user_portal: true
portal_session_duration: "24h"
# a credential, which must survive untouched and unread
smtp_password: "hunter2"
owner:
  user_id: "someone@example.com"
`

func TestReadSessionPolicy(t *testing.T) {
	got, err := ReadSessionPolicy([]byte(liveConfigFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Duration != "24h" {
		t.Errorf("Duration = %q, want 24h", got.Duration)
	}
	// Absent must read as "" and not as a zero value, so "unset" stays distinguishable from
	// "set to nothing".
	if got.MaxLifetime != "" {
		t.Errorf("MaxLifetime = %q, want empty for an absent key", got.MaxLifetime)
	}
}

func TestDiffSessionPolicy(t *testing.T) {
	live := SessionPolicy{Duration: "24h"}

	drift := DiffSessionPolicy(SessionPolicy{Duration: "8h", MaxLifetime: "24h"}, live)
	if len(drift) != 2 {
		t.Fatalf("expected both keys to drift, got %d: %v", len(drift), drift)
	}

	// A declaration that matches is not drift.
	if d := DiffSessionPolicy(SessionPolicy{Duration: "24h"}, live); len(d) != 0 {
		t.Errorf("a matching declaration reported drift: %v", d)
	}

	// An undeclared setting is not drift. A deployment managing only the idle timeout must not
	// be told the cap has drifted -- it never expressed an opinion about it.
	if d := DiffSessionPolicy(SessionPolicy{}, SessionPolicy{Duration: "24h", MaxLifetime: "48h"}); len(d) != 0 {
		t.Errorf("an empty declaration reported drift: %v", d)
	}
}

func TestValidateSessionPolicyRejectsNonsense(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy SessionPolicy
		want   string
	}{
		{"unparseable", SessionPolicy{Duration: "8 hours"}, "not a duration"},
		{"negative", SessionPolicy{Duration: "-1h"}, "must be positive"},
		{"zero", SessionPolicy{Duration: "0s"}, "must be positive"},
		// The cap below the idle timeout makes the idle setting unreachable. Not invalid to a
		// parser, but certainly a mistake, and applying it silently would hide it.
		{"cap under idle", SessionPolicy{Duration: "8h", MaxLifetime: "1h"}, "shorter than"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSessionPolicy(tc.policy)
			if err == nil {
				t.Fatalf("%+v was accepted", tc.policy)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not explain the problem (want %q)", err, tc.want)
			}
		})
	}

	// Valid, and the empty declaration, must both pass.
	if err := ValidateSessionPolicy(SessionPolicy{Duration: "8h", MaxLifetime: "24h"}); err != nil {
		t.Errorf("a valid policy was rejected: %v", err)
	}
	if err := ValidateSessionPolicy(SessionPolicy{}); err != nil {
		t.Errorf("an empty policy was rejected: %v", err)
	}
}

// The file belongs to the operator. Rewriting it into an unrecognisable shape -- however
// semantically identical -- is not something a tool should do to a file it does not own.
func TestApplySessionPolicyPreservesCommentsAndOtherKeys(t *testing.T) {
	out, err := ApplySessionPolicy([]byte(liveConfigFixture), SessionPolicy{
		Duration:    "8h",
		MaxLifetime: "24h",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := string(out)

	if !strings.Contains(got, `portal_session_duration: "8h"`) {
		t.Errorf("the existing key was not updated in place:\n%s", got)
	}
	if !strings.Contains(got, `portal_session_max_lifetime: "24h"`) {
		t.Errorf("the absent key was not appended:\n%s", got)
	}
	if !strings.Contains(got, "# gateway config") {
		t.Errorf("comments were lost -- this rewrites a file the operator maintains:\n%s", got)
	}
	// Every other key must survive byte-for-byte in meaning. The credential is checked
	// explicitly: this function must never drop or alter one.
	if !strings.Contains(got, "hunter2") {
		t.Errorf("an unrelated key was lost:\n%s", got)
	}
	if !strings.Contains(got, "someone@example.com") {
		t.Errorf("a nested key was lost:\n%s", got)
	}

	// And it must still parse back to the same policy.
	round, err := ReadSessionPolicy(out)
	if err != nil {
		t.Fatalf("the rewritten config does not parse: %v", err)
	}
	if round.Duration != "8h" || round.MaxLifetime != "24h" {
		t.Errorf("round trip lost the policy: %+v", round)
	}
}

// An empty declaration must return the input untouched rather than reformatting it for nothing.
func TestApplySessionPolicyIsANoOpWhenNothingIsDeclared(t *testing.T) {
	out, err := ApplySessionPolicy([]byte(liveConfigFixture), SessionPolicy{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != liveConfigFixture {
		t.Errorf("an empty declaration rewrote the file:\n%s", out)
	}
}

// Only one of the two declared must leave the other alone, not blank it.
func TestApplySessionPolicyLeavesUndeclaredKeysAlone(t *testing.T) {
	out, err := ApplySessionPolicy([]byte(liveConfigFixture), SessionPolicy{MaxLifetime: "24h"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := ReadSessionPolicy(out)
	if err != nil {
		t.Fatalf("re-reading: %v", err)
	}
	if got.Duration != "24h" {
		t.Errorf("an undeclared key was changed to %q; it must be left as it was", got.Duration)
	}
	if got.MaxLifetime != "24h" {
		t.Errorf("the declared key was not applied: %q", got.MaxLifetime)
	}
}
