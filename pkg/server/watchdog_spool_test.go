package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the watchdog spool reader (#1875).
//
// The failure this guards is not "the parser is wrong" -- it is a restart that happens and never
// reaches anyone, which is indistinguishable from a gateway that never restarted. So the cases
// below are mostly about what must NOT be silently dropped: a torn final line, an unknown field,
// a spool that is mid-write.

func writeSpool(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "watchdog-events.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing spool: %v", err)
	}
	return path
}

func ev(ts, service, outcome string, count int) string {
	return fmt.Sprintf(`{"ts":%q,"event":"restart","service":%q,"outcome":%q,"restarts_last_hour":%d}`,
		ts, service, outcome, count)
}

func TestReadWatchdogEventsSince(t *testing.T) {
	t.Run("a missing spool is not an error", func(t *testing.T) {
		// The overwhelmingly common case: a gateway whose watchdog has never had to do
		// anything. It must not log or error as though something were wrong.
		events, skipped, err := readWatchdogEventsSince(filepath.Join(t.TempDir(), "absent"), "")
		if err != nil {
			t.Fatalf("expected no error for a missing spool, got %v", err)
		}
		if len(events) != 0 || skipped != 0 {
			t.Fatalf("expected nothing from a missing spool, got %d events / %d skipped", len(events), skipped)
		}
	})

	t.Run("an empty spool yields nothing", func(t *testing.T) {
		events, _, err := readWatchdogEventsSince(writeSpool(t), "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 0 {
			t.Fatalf("expected 0 events, got %d", len(events))
		}
	})

	t.Run("every event is returned when there is no high-water mark", func(t *testing.T) {
		path := writeSpool(t,
			ev("2026-09-11T01:00:00Z", "lfr-tunneld", "recovered", 1),
			ev("2026-09-11T02:00:00Z", "nginx", "recovered", 1),
		)
		events, _, err := readWatchdogEventsSince(path, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 2 {
			t.Fatalf("expected 2 events, got %d", len(events))
		}
	})

	t.Run("the high-water mark excludes what was already forwarded", func(t *testing.T) {
		// Without this the owner is mailed the same restart once an hour forever, which is
		// how an alert channel gets muted and then stops working for everything.
		path := writeSpool(t,
			ev("2026-09-11T01:00:00Z", "lfr-tunneld", "recovered", 1),
			ev("2026-09-11T02:00:00Z", "nginx", "recovered", 1),
			ev("2026-09-11T03:00:00Z", "nginx", "failed", 2),
		)
		events, _, err := readWatchdogEventsSince(path, "2026-09-11T02:00:00Z")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("expected only the newest event, got %d", len(events))
		}
		if events[0].TS != "2026-09-11T03:00:00Z" || events[0].Outcome != "failed" {
			t.Fatalf("wrong event survived the mark: %+v", events[0])
		}
	})

	t.Run("the mark is exclusive, so the boundary event is not re-sent", func(t *testing.T) {
		path := writeSpool(t, ev("2026-09-11T02:00:00Z", "nginx", "recovered", 1))
		events, _, err := readWatchdogEventsSince(path, "2026-09-11T02:00:00Z")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 0 {
			t.Fatalf("the event equal to the mark was forwarded again: %+v", events)
		}
	})

	t.Run("a torn final line does not suppress the events above it", func(t *testing.T) {
		// The watchdog is a shell script appending to this file and can be killed mid-write,
		// so a half-written last line is expected rather than exceptional. Dropping the whole
		// read on it would lose the restart recorded on the line before.
		path := writeSpool(t,
			ev("2026-09-11T01:00:00Z", "lfr-tunneld", "recovered", 1),
			`{"ts":"2026-09-11T02:00:00Z","event":"rest`,
		)
		events, skipped, err := readWatchdogEventsSince(path, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("expected the intact event to survive, got %d", len(events))
		}
		if skipped != 1 {
			t.Fatalf("expected the torn line to be counted as skipped, got %d", skipped)
		}
	})

	t.Run("lines that are not restarts are ignored", func(t *testing.T) {
		path := writeSpool(t,
			`{"ts":"2026-09-11T01:00:00Z","event":"something_else","service":"x"}`,
			ev("2026-09-11T02:00:00Z", "nginx", "recovered", 1),
		)
		events, _, err := readWatchdogEventsSince(path, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 1 || events[0].Service != "nginx" {
			t.Fatalf("expected only the restart event, got %+v", events)
		}
	})

	t.Run("events come back oldest first regardless of file order", func(t *testing.T) {
		path := writeSpool(t,
			ev("2026-09-11T03:00:00Z", "nginx", "failed", 2),
			ev("2026-09-11T01:00:00Z", "lfr-tunneld", "recovered", 1),
		)
		events, _, err := readWatchdogEventsSince(path, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// forwardWatchdogEvents takes the LAST element as the new high-water mark, so an
		// unsorted return would set the mark to an older timestamp and re-send the newer
		// event on every tick.
		if len(events) != 2 || events[0].TS > events[1].TS {
			t.Fatalf("expected oldest-first ordering, got %+v", events)
		}
	})
}

func TestWatchdogAlertSubjectDistinguishesRecovery(t *testing.T) {
	// The subject is what decides whether the owner opens it now or after coffee, so "it fixed
	// itself" and "it is still broken" must not read the same.
	recovered := []watchdogEvent{{TS: "t1", Service: "nginx", Outcome: "recovered"}}
	if got := watchdogAlertSubject(recovered); strings.Contains(strings.ToLower(got), "failed") {
		t.Fatalf("a recovered restart must not read as a failure: %q", got)
	}

	failed := []watchdogEvent{{TS: "t1", Service: "nginx", Outcome: "failed"}}
	if got := watchdogAlertSubject(failed); !strings.Contains(strings.ToLower(got), "failed to recover") {
		t.Fatalf("a failed restart must say so in the subject: %q", got)
	}
}

func TestWatchdogAlertBodyCallsOutARestartLoop(t *testing.T) {
	// The issue's second ask: three restarts in an hour is a different event from one, and
	// nothing counted them. A body that reports the restarts but not the rate loses that.
	single := watchdogAlertBody([]watchdogEvent{
		{TS: "2026-09-11T01:00:00Z", Service: "nginx", Outcome: "recovered", RestartsLastHour: 1},
	}, "/spool")
	if strings.Contains(single, "within an hour") {
		t.Fatalf("a single restart must not be described as a loop:\n%s", single)
	}

	looping := watchdogAlertBody([]watchdogEvent{
		{TS: "2026-09-11T01:00:00Z", Service: "nginx", Outcome: "recovered", RestartsLastHour: 1},
		{TS: "2026-09-11T01:20:00Z", Service: "nginx", Outcome: "recovered", RestartsLastHour: 2},
		{TS: "2026-09-11T01:40:00Z", Service: "nginx", Outcome: "recovered", RestartsLastHour: 3},
	}, "/spool")
	if !strings.Contains(looping, "within an hour") {
		t.Fatalf("three restarts in an hour must be called out:\n%s", looping)
	}
}

func TestWatchdogAlertBodyTruncatesButKeepsTheCount(t *testing.T) {
	// A box that restart-loops all night produces hundreds of events. The owner needs to know
	// that it did; they do not need to read each one. Truncating without saying so would
	// understate the outage, which is the one thing this must not do.
	var events []watchdogEvent
	for i := 0; i < watchdogMaxEventsPerAlert+5; i++ {
		events = append(events, watchdogEvent{
			TS:               fmt.Sprintf("2026-09-11T%02d:00:00Z", i),
			Service:          "nginx",
			Outcome:          "recovered",
			RestartsLastHour: 1,
		})
	}
	body := watchdogAlertBody(events, "/spool")
	if !strings.Contains(body, "5 earlier event(s), omitted") {
		t.Fatalf("truncation must say how much it dropped:\n%s", body)
	}
	if !strings.Contains(body, fmt.Sprintf("%d times", len(events))) &&
		!strings.Contains(watchdogAlertSubject(events), fmt.Sprintf("%d times", len(events))) {
		t.Fatalf("the full count must survive truncation somewhere the reader sees it")
	}
}

func TestWatchdogSpoolDefaultMatchesTheWatchdogScript(t *testing.T) {
	// Two defaults in two languages, one in Go and one in shell. Asserted rather than trusted:
	// if they drift, the daemon reads a file nothing writes and reports no restarts forever,
	// which looks exactly like a healthy gateway.
	data, err := os.ReadFile("../../scripts/common/gateway-watchdog.sh")
	if err != nil {
		t.Fatalf("reading the watchdog script: %v", err)
	}
	want := fmt.Sprintf("${LFT_WATCHDOG_SPOOL:-%s}", watchdogSpoolDefaultPath)
	if !strings.Contains(string(data), want) {
		t.Fatalf("gateway-watchdog.sh does not default to %q -- the daemon would read a file nothing writes", watchdogSpoolDefaultPath)
	}
}
