package provisioner

import (
	"strings"
	"testing"
)

// TestScheduleValidate is the regression test for #1250. edge-sa was found configured to
// stop at 16:00 and start at 15:45 -- up for fifteen minutes a day -- because the times had
// been swapped and nothing on any path rejected them. Only `enabled: false` prevented a
// nightly outage.
func TestScheduleValidate(t *testing.T) {
	cases := []struct {
		name    string
		sched   Schedule
		wantErr string // substring; empty means the schedule must be accepted
	}{
		{
			name:  "a normal overnight window",
			sched: Schedule{Enabled: true, StopTime: "00:00", StartTime: "08:00", Timezone: "America/Sao_Paulo"},
		},
		{
			name:  "a window that does not cross midnight",
			sched: Schedule{Enabled: true, StopTime: "22:00", StartTime: "06:00", Timezone: "Asia/Kolkata"},
		},
		{
			// The exact configuration found on edge-sa.
			name:    "swapped times leaving minutes of uptime",
			sched:   Schedule{Enabled: true, StopTime: "16:00", StartTime: "15:45", Timezone: "America/Sao_Paulo"},
			wantErr: "the wrong way round",
		},
		{
			name:    "identical times would never fire meaningfully",
			sched:   Schedule{Enabled: true, StopTime: "03:00", StartTime: "03:00", Timezone: "UTC"},
			wantErr: "never start or stop",
		},
		{
			name:    "a timezone that does not exist",
			sched:   Schedule{Enabled: true, StopTime: "00:00", StartTime: "08:00", Timezone: "Mars/Olympus_Mons"},
			wantErr: "unknown timezone",
		},
		{
			name:    "no timezone at all",
			sched:   Schedule{Enabled: true, StopTime: "00:00", StartTime: "08:00"},
			wantErr: "timezone is required",
		},
		{
			name:    "malformed stop time",
			sched:   Schedule{Enabled: true, StopTime: "midnight", StartTime: "08:00", Timezone: "UTC"},
			wantErr: "stop_time",
		},
		{
			// Disabling is how a node is taken off the rota, and the times stop mattering.
			// Rejecting this would make a bad schedule impossible to turn off.
			name:  "a disabled schedule is accepted whatever the times",
			sched: Schedule{Enabled: false, StopTime: "16:00", StartTime: "15:45", Timezone: "America/Sao_Paulo"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.sched.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected the schedule to be accepted, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected rejection containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestScheduleValidateDoesNotCatchSemanticMistakes records a deliberate limit. edge-in was
// set to stop at 15:45 Asia/Kolkata -- the middle of the working day -- which is
// structurally sound and semantically wrong. Validation catches swapped times, not badly
// chosen ones, and pretending otherwise would mean rejecting legitimate windows.
func TestScheduleValidateDoesNotCatchSemanticMistakes(t *testing.T) {
	sched := Schedule{Enabled: true, StopTime: "15:45", StartTime: "07:00", Timezone: "Asia/Kolkata"}
	if err := sched.Validate(); err != nil {
		t.Errorf("a mid-afternoon stop is structurally valid and must be accepted, got: %v", err)
	}
}
