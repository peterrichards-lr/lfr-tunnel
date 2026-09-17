package main

import (
	"strings"
	"testing"

	"lfr-tunnel/pkg/client"
)

// The pre-flight decision is the difference between a client that can reach its grace window
// and one that kills itself before the gateway gets a say (#1988).
func TestMinVersionPreflight(t *testing.T) {
	cases := []struct {
		name        string
		current     string
		info        *client.ServerVersionInfo
		wantWarning bool
		wantFatal   bool
	}{
		{
			name:    "at the floor",
			current: "v1.45.0",
			info:    &client.ServerVersionInfo{MinVersion: "v1.45.0", MinVersionServerEnforced: true},
		},
		{
			name:    "above the floor",
			current: "v1.48.0",
			info:    &client.ServerVersionInfo{MinVersion: "v1.45.0"},
		},
		{
			name:    "no floor advertised",
			current: "v1.40.0",
			info:    &client.ServerVersionInfo{},
		},
		{
			// The gateway enforces it, so it also runs the grace window. Dying here would
			// make that window unreachable for every client new enough to look.
			name:        "below the floor, gateway enforces",
			current:     "v1.40.0",
			info:        &client.ServerVersionInfo{MinVersion: "v1.45.0", MinVersionServerEnforced: true},
			wantWarning: true,
		},
		{
			// An older gateway relies entirely on this check, so enforcement must not get
			// weaker than it was before the flag existed.
			name:      "below the floor, gateway does not enforce",
			current:   "v1.40.0",
			info:      &client.ServerVersionInfo{MinVersion: "v1.45.0"},
			wantFatal: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warning, fatal := minVersionPreflight(tc.current, tc.info)
			if (warning != "") != tc.wantWarning {
				t.Errorf("warning = %q, wantWarning = %v", warning, tc.wantWarning)
			}
			if (fatal != "") != tc.wantFatal {
				t.Errorf("fatal = %q, wantFatal = %v", fatal, tc.wantFatal)
			}
			// Whichever message is produced has to name all three things the old one named
			// none of. A non-empty check alone is satisfied by the message this issue
			// reports as the defect.
			msg := warning + fatal
			if msg == "" {
				return
			}
			for _, want := range []string{tc.current, tc.info.MinVersion, "lfr-tunnel -upgrade"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the message does not name %q: %s", want, msg)
				}
			}
		})
	}
}
