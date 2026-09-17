package client

import (
	"strings"
	"testing"
	"time"
)

// The property, not the instances (#1948): a field can be added to ClientSettings without any
// bounds only by making this test red. The enumeration comes from the type by reflection, so a
// hand-written list cannot go stale.
//
// This is the durable artefact of the whole PR. Every individual clamp below could be correct
// and the design would still have failed if the NEXT setting could arrive unbounded.
func TestEverySettingIsBounded(t *testing.T) {
	names := settingFieldNames()
	if len(names) == 0 {
		t.Fatal("no settings were enumerated -- the reflection is broken, not the bounds")
	}
	for _, name := range names {
		b, ok := settingBounds[name]
		if !ok {
			t.Errorf("%q is advertised but has no entry in settingBounds -- a gateway could set it to anything", name)
			continue
		}
		if b.min <= 0 || b.max <= 0 || b.def <= 0 {
			t.Errorf("%q has a non-positive bound (%+v) -- a zero is indistinguishable from 'unset' on the wire", name, b)
		}
		if b.min > b.max {
			t.Errorf("%q has min %d above max %d", name, b.min, b.max)
		}
		if b.def < b.min || b.def > b.max {
			t.Errorf("%q has a default (%d) outside its own range [%d,%d]", name, b.def, b.min, b.max)
		}
		if b.why == "" {
			t.Errorf("%q does not record what going outside its range would break", name)
		}
	}

	// And the other direction: a bound with no field is a rename that half happened.
	known := map[string]bool{}
	for _, n := range names {
		known[n] = true
	}
	for name := range settingBounds {
		if !known[name] {
			t.Errorf("settingBounds has %q but ClientSettings has no such field -- a rename left the bound behind", name)
		}
	}
}

// The reason this issue asks for clamping at all: "a typo in server config must not be able to
// set a retry count of zero across the fleet."
func TestAdvertisedValuesAreClamped(t *testing.T) {
	cases := []struct {
		name              string
		settings          *ClientSettings
		legacy            int
		wantReconnect     time.Duration
		wantHeartbeat     time.Duration
		wantAdjustments   int
		adjustmentMention string
	}{
		{
			name:          "nothing advertised uses the client defaults",
			settings:      nil,
			wantReconnect: 60 * time.Second,
			wantHeartbeat: 5 * time.Second,
		},
		{
			name:          "sane values are honoured as sent",
			settings:      &ClientSettings{ReconnectSeconds: 90, HeartbeatSeconds: 10},
			wantReconnect: 90 * time.Second,
			wantHeartbeat: 10 * time.Second,
		},
		{
			// The typo in the issue, spelled out. Zero must not become zero.
			name:              "a zero retry window falls back to the default, not to zero",
			settings:          &ClientSettings{ReconnectSeconds: 0, HeartbeatSeconds: 0},
			wantReconnect:     60 * time.Second,
			wantHeartbeat:     5 * time.Second,
			wantAdjustments:   0,
			adjustmentMention: "",
		},
		{
			name:              "a negative value is clamped, not applied",
			settings:          &ClientSettings{ReconnectSeconds: -30, HeartbeatSeconds: -1},
			wantReconnect:     60 * time.Second,
			wantHeartbeat:     5 * time.Second,
			wantAdjustments:   0,
			adjustmentMention: "",
		},
		{
			name:              "an absurdly large window is capped",
			settings:          &ClientSettings{ReconnectSeconds: 86400},
			wantReconnect:     180 * time.Second,
			wantHeartbeat:     5 * time.Second,
			wantAdjustments:   1,
			adjustmentMention: "reconnect_seconds",
		},
		{
			// One heartbeat per 100ms across a fleet is the load problem the floor exists for.
			name:              "a flooding heartbeat is raised to the floor",
			settings:          &ClientSettings{HeartbeatSeconds: 1},
			wantReconnect:     60 * time.Second,
			wantHeartbeat:     2 * time.Second,
			wantAdjustments:   1,
			adjustmentMention: "heartbeat_seconds",
		},
		{
			name:              "a window below the floor is raised",
			settings:          &ClientSettings{ReconnectSeconds: 1},
			wantReconnect:     20 * time.Second,
			wantHeartbeat:     5 * time.Second,
			wantAdjustments:   1,
			adjustmentMention: "reconnect_seconds",
		},
		{
			// Backwards compatibility with #1946's top-level field.
			name:          "the legacy top-level field still steers a client",
			settings:      nil,
			legacy:        120,
			wantReconnect: 120 * time.Second,
			wantHeartbeat: 5 * time.Second,
		},
		{
			name:          "the block wins over the legacy field",
			settings:      &ClientSettings{ReconnectSeconds: 30},
			legacy:        120,
			wantReconnect: 30 * time.Second,
			wantHeartbeat: 5 * time.Second,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveSettings(tc.settings, tc.legacy)
			if got.ReconnectWindow != tc.wantReconnect {
				t.Errorf("ReconnectWindow = %v, want %v", got.ReconnectWindow, tc.wantReconnect)
			}
			if got.HeartbeatInterval != tc.wantHeartbeat {
				t.Errorf("HeartbeatInterval = %v, want %v", got.HeartbeatInterval, tc.wantHeartbeat)
			}
			if len(got.Adjustments) != tc.wantAdjustments {
				t.Errorf("adjustments = %d (%v), want %d", len(got.Adjustments), got.Adjustments, tc.wantAdjustments)
			}
			if tc.adjustmentMention != "" {
				found := false
				for _, a := range got.Adjustments {
					if strings.Contains(a, tc.adjustmentMention) {
						found = true
					}
				}
				if !found {
					t.Errorf("no adjustment naming %q: %v", tc.adjustmentMention, got.Adjustments)
				}
			}
		})
	}
}

// An out-of-range value is reported, not silently corrected. The gateway config that produced
// it is somewhere nobody is looking, and the client is the only place the effect shows.
func TestOutOfRangeSettingsAreReportedWithAReason(t *testing.T) {
	got := ResolveSettings(&ClientSettings{ReconnectSeconds: 99999, HeartbeatSeconds: 1}, 0)
	if len(got.Adjustments) != 2 {
		t.Fatalf("expected both out-of-range values to be reported, got %v", got.Adjustments)
	}
	for _, a := range got.Adjustments {
		// The number asked for, the number used, and why -- a line saying only "clamped"
		// tells whoever reads it nothing they can act on.
		if !strings.Contains(a, "the gateway advertised") || !strings.Contains(a, "using") {
			t.Errorf("an adjustment does not say what was asked for and what was used: %q", a)
		}
		if !strings.Contains(a, "--") {
			t.Errorf("an adjustment does not say what going outside the range would break: %q", a)
		}
	}
}

// clampReconnectWindow is the pre-existing entry point (#1946) and now shares the one bounds
// table. If the constants and the table ever disagree, the value a real session uses and the
// value the table promises would differ -- two sets of bounds for one number is exactly the
// drift the shared table was introduced to stop.
func TestReconnectBoundsAreTheSame(t *testing.T) {
	b := settingBounds["reconnect_seconds"]
	if time.Duration(b.def)*time.Second != defaultReconnectWindow {
		t.Errorf("table default %ds != defaultReconnectWindow %v", b.def, defaultReconnectWindow)
	}
	if time.Duration(b.min)*time.Second != minReconnectWindow {
		t.Errorf("table min %ds != minReconnectWindow %v", b.min, minReconnectWindow)
	}
	if time.Duration(b.max)*time.Second != maxReconnectWindow {
		t.Errorf("table max %ds != maxReconnectWindow %v", b.max, maxReconnectWindow)
	}

	// And the function still behaves, through the table.
	if got := clampReconnectWindow(0); got != defaultReconnectWindow {
		t.Errorf("clampReconnectWindow(0) = %v, want the default %v", got, defaultReconnectWindow)
	}
	if got := clampReconnectWindow(1 * time.Second); got != minReconnectWindow {
		t.Errorf("clampReconnectWindow(1s) = %v, want the floor %v", got, minReconnectWindow)
	}
	if got := clampReconnectWindow(1 * time.Hour); got != maxReconnectWindow {
		t.Errorf("clampReconnectWindow(1h) = %v, want the cap %v", got, maxReconnectWindow)
	}
}

// The engine must run on the clamped value rather than on whatever was advertised.
func TestEngineUsesTheClampedHeartbeat(t *testing.T) {
	e := &InterceptorEngine{}
	if got := e.HeartbeatInterval(); got != defaultHeartbeatInterval {
		t.Errorf("a fresh engine ticks at %v, want the default %v", got, defaultHeartbeatInterval)
	}

	settings := ResolveSettings(&ClientSettings{HeartbeatSeconds: 1}, 0)
	e.SetHeartbeatInterval(settings.HeartbeatInterval)
	if got := e.HeartbeatInterval(); got != 2*time.Second {
		t.Errorf("engine heartbeat = %v, want the 2s floor rather than the advertised 1s", got)
	}
}
