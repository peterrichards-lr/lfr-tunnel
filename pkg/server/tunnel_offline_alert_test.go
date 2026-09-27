package server

import (
	"testing"
	"time"
)

// When the gateway tells an operator a tunnel is offline (#2270).
//
// These are sequence tests, not predicate tests, because the defect the first implementation
// shipped was invisible to a predicate: `changed` was correct, `served` was correct, and
// combining them at the call site meant a real outage was never reported at all. Each test below
// drives a run of heartbeats and counts the alerts, which is the only shape that could have
// caught it.

func aLeasedSession(t *testing.T, token string, leases ...*TunnelLease) *Registry {
	t.Helper()
	r := newTestRegistry(t)
	r.sessionLeases[token] = leases
	return r
}

func aLease(host, token string) *TunnelLease {
	return &TunnelLease{FullHost: host, SessionToken: token, Status: "up", CreatedAt: time.Now()}
}

// heartbeats drives n ticks of the given status, interval apart, and returns how many alerts
// were sent and how many were held. traffic is called before each tick so a test can say what
// moved during that interval.
func heartbeats(r *Registry, token, status string, n int, start time.Time, interval time.Duration,
	traffic func(tick int)) (sent, held int, end time.Time) {
	now := start
	for i := 0; i < n; i++ {
		if traffic != nil {
			traffic(i)
		}
		_, action := r.NoteHeartbeat(token, status, now)
		switch action {
		case offlineAlertSend:
			sent++
		case offlineAlertHeld:
			held++
		case offlineAlertNone:
		}
		now = now.Add(interval)
	}
	return sent, held, now
}

// FIRING, and the defect the first implementation had. A target that dies mid-interval was
// serving during that interval, so the bytes that arrive with the down-transition are the bytes
// from before it died. Suppressing on that tick and keeping no record meant the outage was never
// reported -- not delayed, never -- because the status never changes again.
func TestAnOutageThatBeganWhileServingIsStillReported(t *testing.T) {
	lease := aLease("a.example.com", "s")
	r := aLeasedSession(t, "s", lease)
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	// t=0: healthy, and the watermark is established.
	if _, action := r.NoteHeartbeat("s", "up", start); action != offlineAlertNone {
		t.Fatalf("a healthy heartbeat produced %v", action)
	}

	// The interval in which the target died, having served normally for most of it.
	lease.BytesIn += 4096
	lease.BytesOut += 65536

	// The transition, plus ten minutes of silence afterwards.
	sent, held, _ := heartbeats(r, "s", "down", 120, start.Add(5*time.Second), 5*time.Second, nil)

	if sent != 1 {
		t.Errorf("ten minutes of a genuinely dead tunnel produced %d alerts, want exactly 1 "+
			"(the traffic in the interval it died must DEFER the alert, not discard it)", sent)
	}
	if held == 0 {
		t.Error("the first heartbeat carried traffic and should have been held")
	}
}

// The other half of that defect: a dead target that visitors keep hitting. The gateway counts
// request and response header bytes on the way through, so a tunnel answering 502 to everyone
// keeps looking like it is serving -- forever. The hold has to be bounded or it never reports.
func TestADeadTargetUnderContinuousTrafficIsReportedWithinTheHold(t *testing.T) {
	lease := aLease("a.example.com", "s")
	r := aLeasedSession(t, "s", lease)
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	r.NoteHeartbeat("s", "up", start)

	// Every interval carries a 502'd request's headers.
	sent, held, _ := heartbeats(r, "s", "down", 120, start.Add(5*time.Second), 5*time.Second,
		func(int) {
			lease.BytesIn += 300
			lease.BytesOut += 200
		})

	if sent != 1 {
		t.Errorf("a dead target under continuous polling produced %d alerts in ten minutes, want 1", sent)
	}
	if held == 0 {
		t.Error("nothing was held, so the traffic check did nothing")
	}
	// And the wait was bounded by the hold, not by the traffic stopping -- which it never does.
	if held > int(maxOfflineAlertHold/(5*time.Second))+1 {
		t.Errorf("the alert was held for %d heartbeats, which is longer than maxOfflineAlertHold (%s)",
			held, maxOfflineAlertHold)
	}
}

// The alert is sent on the first QUIET heartbeat, not only when the hold expires -- an operator
// should not wait a minute for news that is already certain.
func TestAHeldAlertIsSentAsSoonAsTheTrafficStops(t *testing.T) {
	lease := aLease("a.example.com", "s")
	r := aLeasedSession(t, "s", lease)
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	r.NoteHeartbeat("s", "up", start)

	lease.BytesIn += 1000
	now := start.Add(5 * time.Second)
	if _, action := r.NoteHeartbeat("s", "down", now); action != offlineAlertHeld {
		t.Fatalf("the transition carried traffic and produced %v, want held", action)
	}

	// Next interval: nothing moved.
	now = now.Add(5 * time.Second)
	if _, action := r.NoteHeartbeat("s", "down", now); action != offlineAlertSend {
		t.Errorf("the first quiet heartbeat produced %v, want the held alert to be sent", action)
	}
	// And it is sent once.
	now = now.Add(5 * time.Second)
	if _, action := r.NoteHeartbeat("s", "down", now); action != offlineAlertNone {
		t.Errorf("the alert was sent again on the next heartbeat (%v); this is the "+
			"twelve-mails-a-minute defect", action)
	}
}

// FIRING. The steady state is not an event.
func TestTheSteadyStateIsNotReported(t *testing.T) {
	r := aLeasedSession(t, "s", aLease("a.example.com", "s"))
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	r.NoteHeartbeat("s", "up", start)

	sent, held, _ := heartbeats(r, "s", "down", 24, start.Add(5*time.Second), 5*time.Second, nil)
	if sent != 1 {
		t.Errorf("two minutes of a down tunnel produced %d alerts, want 1", sent)
	}
	if held != 0 {
		t.Errorf("nothing was carrying traffic and %d heartbeats were held", held)
	}
}

// A tunnel that flaps is reported each time it goes down, not once and then never.
func TestEachOutageIsReportedOnce(t *testing.T) {
	r := aLeasedSession(t, "s", aLease("a.example.com", "s"))
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	tick := func(status string) offlineAlertAction {
		now = now.Add(5 * time.Second)
		_, action := r.NoteHeartbeat("s", status, now)
		return action
	}

	tick("up")
	for outage := 1; outage <= 3; outage++ {
		if action := tick("down"); action != offlineAlertSend {
			t.Errorf("outage %d: first down heartbeat produced %v, want send", outage, action)
		}
		if action := tick("down"); action != offlineAlertNone {
			t.Errorf("outage %d: second down heartbeat produced %v, want nothing", outage, action)
		}
		if action := tick("up"); action != offlineAlertNone {
			t.Errorf("outage %d: recovery produced %v", outage, action)
		}
	}
}

// Recovery clears a HELD alert. The thing it would have reported is over, and sending it then
// pages somebody about the past.
func TestRecoveryClearsAHeldAlert(t *testing.T) {
	lease := aLease("a.example.com", "s")
	r := aLeasedSession(t, "s", lease)
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	r.NoteHeartbeat("s", "up", now)

	lease.BytesOut += 4096
	now = now.Add(5 * time.Second)
	if _, action := r.NoteHeartbeat("s", "down", now); action != offlineAlertHeld {
		t.Fatalf("want held, got %v", action)
	}

	now = now.Add(5 * time.Second)
	if _, action := r.NoteHeartbeat("s", "up", now); action != offlineAlertNone {
		t.Fatalf("recovery produced %v", action)
	}

	// The held alert is gone, not merely deferred past the recovery.
	now = now.Add(5 * time.Second)
	if _, action := r.NoteHeartbeat("s", "down", now); action != offlineAlertSend {
		t.Errorf("the NEXT outage produced %v, want send", action)
	}
	now = now.Add(5 * time.Second)
	if _, action := r.NoteHeartbeat("s", "down", now); action != offlineAlertNone {
		t.Errorf("a stale held alert fired after recovery: %v", action)
	}
}

// Traffic on ANY lease of a multi-port session counts. Both orderings, because with the busy
// lease last a `served = <expr>` that only kept the final lease's answer still passed.
func TestTrafficOnAnyLeaseOfASessionCounts(t *testing.T) {
	for _, order := range []string{"busy first", "busy last"} {
		t.Run(order, func(t *testing.T) {
			quiet := aLease("a.example.com", "s")
			busy := aLease("b.example.com", "s")
			leases := []*TunnelLease{quiet, busy}
			if order == "busy first" {
				leases = []*TunnelLease{busy, quiet}
			}
			r := aLeasedSession(t, "s", leases...)
			now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
			r.NoteHeartbeat("s", "up", now)

			busy.BytesOut += 2048
			now = now.Add(5 * time.Second)
			if _, action := r.NoteHeartbeat("s", "down", now); action != offlineAlertHeld {
				t.Errorf("one busy lease out of two produced %v, want held", action)
			}
		})
	}
}

// CONTROL. The watermark advances on HELD heartbeats too, so the window stays one interval. If
// it only advanced on quiet ones, bytes from an old interval would keep holding the alert.
func TestTheWatermarkAdvancesOnHeldHeartbeats(t *testing.T) {
	lease := aLease("a.example.com", "s")
	r := aLeasedSession(t, "s", lease)
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	r.NoteHeartbeat("s", "up", now)

	lease.BytesIn += 5000
	now = now.Add(5 * time.Second)
	if _, action := r.NoteHeartbeat("s", "down", now); action != offlineAlertHeld {
		t.Fatalf("want held, got %v", action)
	}
	// Nothing new moved. The same 5000 bytes must not hold it a second time.
	now = now.Add(5 * time.Second)
	if _, action := r.NoteHeartbeat("s", "down", now); action != offlineAlertSend {
		t.Errorf("the same bytes held the alert twice; the watermark did not advance on a held heartbeat")
	}
}

// An unknown session is not an event of any kind.
func TestAnUnknownSessionReportsNothing(t *testing.T) {
	r := newTestRegistry(t)
	if known, action := r.NoteHeartbeat("nobody", "down", time.Now()); known || action != offlineAlertNone {
		t.Errorf("an unknown session reported known=%v action=%v", known, action)
	}
	// And a session whose lease slice is empty, which CleanLease can leave behind.
	r.sessionLeases["empty"] = nil
	if known, action := r.NoteHeartbeat("empty", "down", time.Now()); known || action != offlineAlertNone {
		t.Errorf("a session with no leases reported known=%v action=%v", known, action)
	}
}

// The status reaches every lease, which is what the portal and failover read.
func TestTheStatusIsWrittenToEveryLease(t *testing.T) {
	a, b := aLease("a.example.com", "s"), aLease("b.example.com", "s")
	r := aLeasedSession(t, "s", a, b)

	r.NoteHeartbeat("s", "down", time.Now())
	if a.Status != "down" || b.Status != "down" {
		t.Errorf("statuses are %q and %q, want both down", a.Status, b.Status)
	}
	r.NoteHeartbeat("s", "up", time.Now())
	if a.Status != "up" || b.Status != "up" {
		t.Errorf("statuses are %q and %q, want both up", a.Status, b.Status)
	}
}

// The suffix that goes in the held-alert log is enough to correlate two lines and not enough to
// be a credential.
func TestTheLoggedSessionSuffixIsNotTheToken(t *testing.T) {
	token := "abcdef0123456789abcdef0123456789"

	got := sessionTokenSuffix(token)
	if got == token {
		t.Fatal("the whole session token would be written to the log")
	}
	if got[len(got)-6:] != token[len(token)-6:] {
		t.Errorf("the suffix %q does not end with the token's last six characters", got)
	}
	// A token short enough that six characters would be most of it is withheld entirely.
	// "abcdefg" used to render as "…bcdefg" -- six of its seven characters, the token with a
	// hat on.
	for _, short := range []string{"", "a", "abcdef", "abcdefg", "abcdefghijkl"} {
		if s := sessionTokenSuffix(short); s != "…" {
			t.Errorf("a %d-character token rendered as %q; that is most of the token", len(short), s)
		}
	}
	// And a real one still renders usefully, or the log line names nothing.
	if s := sessionTokenSuffix("abcdefghijklm"); s == "…" {
		t.Error("a 13-character token was withheld entirely; the log line would name nothing")
	}
}
