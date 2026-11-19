package client

import (
	"strings"
	"testing"
)

// The warning has to appear in the CLIENT'S OWN OUTPUT, because the people running old
// clients are the least likely to open the portal (#1988). It speaks from the first day of the
// window with the date (#2523), and names the remedy in every phase.
func TestMinVersionNotice(t *testing.T) {
	cases := []struct {
		name   string
		state  *MinVersionState
		silent bool
	}{
		{name: "nil state", state: nil, silent: true},
		{name: "nothing outstanding", state: &MinVersionState{}, silent: true},
		{
			// Without a deadline there is no date to give, and an undated line is what the
			// preflight already says.
			name:   "grace without a deadline stays quiet",
			state:  &MinVersionState{Required: true, Phase: ConsentPhaseGrace, MinVersion: "v1.45.0", ClientVersion: "v1.40.0", SecondsRemaining: 10 * 24 * 3600},
			silent: true,
		},
		{
			name:  "grace with a deadline speaks",
			state: &MinVersionState{Required: true, Phase: ConsentPhaseGrace, MinVersion: "v1.45.0", ClientVersion: "v1.40.0", SecondsRemaining: 10 * 24 * 3600, Deadline: "2026-12-03T10:00:00Z", UpgradeCommand: "lfr-tunnel -upgrade"},
		},
		{
			name:  "warning speaks",
			state: &MinVersionState{Required: true, Phase: ConsentPhaseWarning, MinVersion: "v1.45.0", ClientVersion: "v1.40.0", SecondsRemaining: 3 * 24 * 3600, UpgradeCommand: "lfr-tunnel -upgrade"},
		},
		{
			name:  "expired speaks",
			state: &MinVersionState{Required: true, Phase: ConsentPhaseExpired, MinVersion: "v1.45.0", ClientVersion: "v1.40.0", UpgradeCommand: "lfr-tunnel -upgrade"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			notice := MinVersionNotice(&RegisterResponse{MinVersion: tc.state})
			if tc.silent {
				if notice != "" {
					t.Errorf("expected silence, got %q", notice)
				}
				return
			}
			if notice == "" {
				t.Fatal("expected a notice -- a deadline nobody is told about is not a warning")
			}
			for _, want := range []string{"v1.40.0", "v1.45.0", "lfr-tunnel -upgrade"} {
				if !strings.Contains(notice, want) {
					t.Errorf("the notice does not name %q: %s", want, notice)
				}
			}
		})
	}
}

// A gateway that sends no command still has to produce a message that names one. The whole
// complaint in #1948 is a refusal with no remedy in it.
func TestMinVersionNoticeFallsBackToTheUpgradeCommand(t *testing.T) {
	notice := MinVersionNotice(&RegisterResponse{MinVersion: &MinVersionState{
		Required: true, Phase: ConsentPhaseExpired, MinVersion: "v1.45.0", ClientVersion: "v1.40.0",
	}})
	if !strings.Contains(notice, "lfr-tunnel -upgrade") {
		t.Errorf("no remedy named when the gateway sent none: %s", notice)
	}
}

// The two deadlines travel together and must stay separable: a version notice must never be
// worded as a consent one, or the user is sent to the portal to fix something the portal
// cannot fix.
func TestVersionNoticeIsNotAConsentNotice(t *testing.T) {
	resp := &RegisterResponse{
		MinVersion:    &MinVersionState{Required: true, Phase: ConsentPhaseWarning, MinVersion: "v1.45.0", ClientVersion: "v1.40.0", SecondsRemaining: 3 * 24 * 3600},
		PolicyConsent: &PolicyConsentState{Required: true, Phase: ConsentPhaseWarning, SecondsRemaining: 3 * 24 * 3600, PortalURL: "https://portal.example.com"},
	}
	version := MinVersionNotice(resp)
	consent := PolicyConsentNotice(resp)

	if strings.Contains(version, "Privacy Policy") {
		t.Errorf("the version notice is worded as a consent notice: %s", version)
	}
	if strings.Contains(consent, "lfr-tunnel -upgrade") {
		t.Errorf("the consent notice sends the user to the upgrade command: %s", consent)
	}
	if version == "" || consent == "" {
		t.Fatalf("both deadlines are outstanding and both must speak: version=%q consent=%q", version, consent)
	}
}

// The deadline is given as a date, in every phase before it, so the user can plan rather than
// count (#2523). Rendered in local time; asserted on the year and month, which no time zone moves.
func TestMinVersionNoticeGivesTheDate(t *testing.T) {
	for _, phase := range []string{ConsentPhaseGrace, ConsentPhaseWarning} {
		notice := MinVersionNotice(&RegisterResponse{MinVersion: &MinVersionState{
			Required: true, Phase: phase, MinVersion: "v1.45.0", ClientVersion: "v1.40.0",
			SecondsRemaining: 3 * 24 * 3600, Deadline: "2026-12-15T10:00:00Z",
		}})
		if !strings.Contains(notice, "2026-12-") {
			t.Errorf("%s: the notice does not give the deadline's date: %s", phase, notice)
		}
	}
	expired := MinVersionNotice(&RegisterResponse{MinVersion: &MinVersionState{
		Required: true, Phase: ConsentPhaseExpired, MinVersion: "v1.45.0", ClientVersion: "v1.40.0",
	}})
	if !strings.Contains(expired, "already running are not affected") {
		t.Errorf("after the deadline a running tunnel's user must be told it keeps running: %s", expired)
	}
}
