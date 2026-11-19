package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/client"
)

// Below the floor, a client at a terminal offers the upgrade; anywhere else it only says so;
// with auto_upgrade on it restarts after upgrading (#2523).
func TestFloorUpgradeDecision(t *testing.T) {
	below := &client.ServerVersionInfo{MinVersion: "v1.53.0", MinVersionServerEnforced: true}
	cases := []struct {
		name                  string
		current               string
		info                  *client.ServerVersionInfo
		autoUpgrade, terminal bool
		want                  floorUpgradeAction
	}{
		{"below the floor at a terminal", "v1.52.1", below, false, true, floorAsk},
		// Scripts, LDM, services: a prompt nobody can answer is a hang.
		{"below the floor, not a terminal", "v1.52.1", below, false, false, floorNothing},
		{"below the floor, auto_upgrade on", "v1.52.1", below, true, true, floorRestartAfterAuto},
		{"below the floor, auto_upgrade on, no terminal", "v1.52.1", below, true, false, floorRestartAfterAuto},
		{"at the floor", "v1.53.0", below, false, true, floorNothing},
		{"above the floor", "v1.54.0", below, false, true, floorNothing},
		{"no floor advertised", "v1.40.0", &client.ServerVersionInfo{}, false, true, floorNothing},
		{"gateway unreachable", "v1.40.0", nil, false, true, floorNothing},
		{"development build", "dev", below, false, true, floorNothing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := floorUpgradeDecision(tc.current, tc.info, tc.autoUpgrade, tc.terminal); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAskToUpgrade(t *testing.T) {
	for answer, want := range map[string]bool{
		"\n": true, "y\n": true, "Yes\n": true, " YES \n": true,
		"n\n": false, "no\n": false, "later\n": false,
	} {
		var out bytes.Buffer
		if got := askToUpgrade(&out, strings.NewReader(answer), "v1.52.1", "v1.53.0", time.Second); got != want {
			t.Errorf("answer %q: got %v, want %v", answer, got, want)
		}
		for _, named := range []string{"v1.52.1", "v1.53.0", "[Y/n]"} {
			if !strings.Contains(out.String(), named) {
				t.Errorf("the question does not name %q: %s", named, out.String())
			}
		}
	}
}

// stdin closed before anything was typed is not an Enter, so not a yes.
func TestAskToUpgradeClosedInputIsNo(t *testing.T) {
	var out bytes.Buffer
	if askToUpgrade(&out, strings.NewReader(""), "v1.52.1", "v1.53.0", time.Second) {
		t.Fatal("an empty, closed stdin was taken as yes")
	}
}

// Nobody at the terminal: the client must carry on, and must not upgrade.
func TestAskToUpgradeUnansweredIsNo(t *testing.T) {
	r, w := io.Pipe()
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Logf("closing the pipe: %v", err)
		}
	})
	var out bytes.Buffer
	start := time.Now()
	if askToUpgrade(&out, r, "v1.52.1", "v1.53.0", 50*time.Millisecond) {
		t.Fatal("an unanswered question was taken as yes")
	}
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("waited %v for an answer; the timeout was not honoured", waited)
	}
	if !strings.Contains(out.String(), "continuing without upgrading") {
		t.Errorf("the timeout is not explained: %s", out.String())
	}
}
