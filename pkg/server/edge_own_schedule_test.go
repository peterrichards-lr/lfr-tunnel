package server

import (
	"encoding/json"
	"testing"
)

// TestOwnScheduleRoundTrip checks the wire format an edge is told its schedule with (#1276).
//
// ScheduleEnabled deliberately has no omitempty. False is meaningful: it is how central tells
// a node it has come off the rota, and dropping it from the payload would leave the edge
// believing whatever it was last told -- a node that thinks it still sleeps at midnight when
// it no longer does.
func TestOwnScheduleRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		sched nodeSchedule
	}{
		{
			name:  "a scheduled node",
			sched: nodeSchedule{Enabled: true, StopTime: "00:00", StartTime: "08:00", Timezone: "Asia/Kolkata"},
		},
		{
			// The case omitempty would have silently dropped.
			name:  "a node taken off the rota",
			sched: nodeSchedule{Enabled: false},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(ControlMessage{
				Type:              "node_schedule",
				NodeID:            "edge-in",
				ScheduleEnabled:   tc.sched.Enabled,
				ScheduleStopTime:  tc.sched.StopTime,
				ScheduleStartTime: tc.sched.StartTime,
				Timezone:          tc.sched.Timezone,
			})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var got ControlMessage
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Type != "node_schedule" {
				t.Errorf("type = %q", got.Type)
			}
			if got.ScheduleEnabled != tc.sched.Enabled {
				t.Errorf("enabled = %v, want %v -- an absent flag would leave the edge on its previous schedule", got.ScheduleEnabled, tc.sched.Enabled)
			}
			if got.ScheduleStopTime != tc.sched.StopTime || got.ScheduleStartTime != tc.sched.StartTime {
				t.Errorf("window = %s/%s, want %s/%s", got.ScheduleStopTime, got.ScheduleStartTime, tc.sched.StopTime, tc.sched.StartTime)
			}
			if got.Timezone != tc.sched.Timezone {
				t.Errorf("timezone = %q, want %q", got.Timezone, tc.sched.Timezone)
			}
		})
	}
}

// TestSetAndReadOwnSchedule covers the edge-side store. The zero value must read as "no known
// downtime", since an edge that has not yet been told anything and one that is genuinely
// unscheduled should behave identically -- both mean a caller must not assume a stop.
func TestSetAndReadOwnSchedule(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	if got := srv.OwnSchedule(); got.Enabled {
		t.Errorf("a node told nothing yet must report no schedule, got %+v", got)
	}

	want := nodeSchedule{Enabled: true, StopTime: "00:00", StartTime: "08:00", Timezone: "Asia/Kolkata"}
	srv.setOwnSchedule(want)
	if got := srv.OwnSchedule(); got != want {
		t.Errorf("OwnSchedule() = %+v, want %+v", got, want)
	}

	// Coming off the rota has to actually clear it, not merely stop refreshing it.
	srv.setOwnSchedule(nodeSchedule{})
	if got := srv.OwnSchedule(); got.Enabled {
		t.Errorf("expected the schedule to be cleared, got %+v", got)
	}
}

// TestScheduleChangeDetection covers what decides whether central re-tells a node. Pushing on
// every health cycle would be noise; pushing on nothing would leave a node stale after an
// out-of-band edit, which is the failure #1245 documented.
func TestScheduleChangeDetection(t *testing.T) {
	current := nodeSchedule{Enabled: true, StopTime: "00:00", StartTime: "08:00", Timezone: "Asia/Kolkata"}

	cases := []struct {
		name    string
		fetched nodeSchedule
		want    bool
	}{
		{"identical", current, false},
		{"stop time moved", nodeSchedule{Enabled: true, StopTime: "01:00", StartTime: "08:00", Timezone: "Asia/Kolkata"}, true},
		{"start time moved", nodeSchedule{Enabled: true, StopTime: "00:00", StartTime: "09:00", Timezone: "Asia/Kolkata"}, true},
		{"timezone corrected", nodeSchedule{Enabled: true, StopTime: "00:00", StartTime: "08:00", Timezone: "UTC"}, true},
		{"taken off the rota", nodeSchedule{Enabled: false, StopTime: "00:00", StartTime: "08:00", Timezone: "Asia/Kolkata"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := current.Timezone != tc.fetched.Timezone ||
				current.StopTime != tc.fetched.StopTime ||
				current.StartTime != tc.fetched.StartTime ||
				current.Enabled != tc.fetched.Enabled
			if changed != tc.want {
				t.Errorf("changed = %v, want %v", changed, tc.want)
			}
		})
	}
}
