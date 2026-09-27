package server

import (
	"testing"
	"time"
)

// When the gateway should tell an operator a tunnel is offline (#2270).
//
// Two independent defects met here. The client reported `down` for any tunnel with -target-host
// set, because the health check dialled the target host on the interceptor's port. And the
// gateway re-sent the alert on EVERY heartbeat, because UpdateLeaseStatus returned "the session
// exists" rather than "the status changed" and sendAdminAlert does not throttle -- so one stuck
// tunnel mailed the operator twelve times a minute.
//
// The client fix is the real one. These are the two properties the gateway has to hold anyway,
// because an alert that repeats on a timer is one nobody reads, and an alert contradicted by
// live traffic is wrong whatever caused it.

func aLeasedSession(t *testing.T, token string, leases ...*TunnelLease) *Registry {
	t.Helper()
	r := newTestRegistry(t)
	r.sessionLeases[token] = leases
	return r
}

// FIRING. The steady state is not an event.
func TestOnlyTheTransitionToDownIsReported(t *testing.T) {
	r := aLeasedSession(t, "s", &TunnelLease{FullHost: "a.example.com", SessionToken: "s", Status: "up", CreatedAt: time.Now()})

	known, changed, _ := r.UpdateLeaseStatusReportingTraffic("s", "down")
	if !known {
		t.Fatal("the session is not known")
	}
	if !changed {
		t.Error("up -> down was not reported as a change; the alert would never fire")
	}

	// Every subsequent heartbeat says the same thing, and must not be an event.
	for i := 0; i < 3; i++ {
		_, changed, _ := r.UpdateLeaseStatusReportingTraffic("s", "down")
		if changed {
			t.Fatalf("heartbeat %d reported a change while the status stayed down; "+
				"this is the twelve-mails-a-minute defect", i+2)
		}
	}

	// And recovery is an event again, or a tunnel that flaps is reported once and never more.
	if _, changed, _ := r.UpdateLeaseStatusReportingTraffic("s", "up"); !changed {
		t.Error("down -> up was not reported as a change")
	}
	if _, changed, _ := r.UpdateLeaseStatusReportingTraffic("s", "down"); !changed {
		t.Error("the second up -> down was not reported; a flapping tunnel would be silent after the first")
	}
}

// A lease carrying bytes since the previous heartbeat is not one to page anybody about.
func TestALeaseThatCarriedTrafficReportsItHasServed(t *testing.T) {
	lease := &TunnelLease{FullHost: "a.example.com", SessionToken: "s", Status: "up", CreatedAt: time.Now()}
	r := aLeasedSession(t, "s", lease)

	// The first heartbeat establishes the watermark. A lease that has served nothing since
	// registration must not read as having served.
	if _, _, served := r.UpdateLeaseStatusReportingTraffic("s", "up"); served {
		t.Error("a lease with no traffic reported that it had served")
	}

	lease.BytesIn += 512
	lease.BytesOut += 1024
	if _, _, served := r.UpdateLeaseStatusReportingTraffic("s", "down"); !served {
		t.Error("a lease that moved 1536 bytes since the last heartbeat did not report it")
	}

	// The watermark advances on EVERY heartbeat, including this one, so the window stays
	// "since the last heartbeat" rather than growing to "since we last alerted" -- which would
	// suppress the alert for a genuinely dead tunnel that served one request a while ago.
	if _, _, served := r.UpdateLeaseStatusReportingTraffic("s", "down"); served {
		t.Error("the same bytes counted twice; the watermark did not advance")
	}
}

// Traffic on ANY lease of a multi-port session counts. A session is one tunnel to its operator,
// and paging them because one of three ports was idle is the same false alert by another route.
func TestTrafficOnAnyLeaseOfASessionCounts(t *testing.T) {
	quiet := &TunnelLease{FullHost: "a.example.com", SessionToken: "s", Status: "up", CreatedAt: time.Now()}
	busy := &TunnelLease{FullHost: "b.example.com", SessionToken: "s", Status: "up", CreatedAt: time.Now()}
	r := aLeasedSession(t, "s", quiet, busy)

	// Establish the byte watermark; the return values are not the subject here.
	r.UpdateLeaseStatusReportingTraffic("s", "up")

	busy.BytesOut += 2048
	if _, _, served := r.UpdateLeaseStatusReportingTraffic("s", "down"); !served {
		t.Error("one busy lease out of two did not count as the session having served")
	}
}

// CONTROL / anti-vacuity. A genuinely dead tunnel -- status changed, nothing served -- is still
// reported, or the suppression has turned the alert off altogether.
func TestADeadTunnelIsStillReported(t *testing.T) {
	r := aLeasedSession(t, "s", &TunnelLease{FullHost: "a.example.com", SessionToken: "s", Status: "up", CreatedAt: time.Now()})

	// Establish the byte watermark; the return values are not the subject here.
	r.UpdateLeaseStatusReportingTraffic("s", "up")

	known, changed, served := r.UpdateLeaseStatusReportingTraffic("s", "down")
	if !known || !changed {
		t.Fatalf("known=%v changed=%v; the alert would not fire for a tunnel that really went down", known, changed)
	}
	if served {
		t.Error("a lease that moved no bytes reported that it had served")
	}
}

// An unknown session is not an event of any kind, and must not look like a recovery.
func TestAnUnknownSessionReportsNothing(t *testing.T) {
	r := newTestRegistry(t)

	known, changed, served := r.UpdateLeaseStatusReportingTraffic("nobody", "down")
	if known || changed || served {
		t.Errorf("an unknown session reported known=%v changed=%v served=%v", known, changed, served)
	}
}

// The old boolean keeps its old meaning. Its one caller was replaced, but redefining a public
// method's return value in place is how a caller starts answering a different question without
// anyone noticing -- which is the defect being fixed here.
func TestUpdateLeaseStatusStillReportsWhetherTheSessionIsKnown(t *testing.T) {
	r := aLeasedSession(t, "s", &TunnelLease{FullHost: "a.example.com", SessionToken: "s", Status: "down", CreatedAt: time.Now()})

	// Same status as it already holds: "known" is true, even though nothing changed.
	if !r.UpdateLeaseStatus("s", "down") {
		t.Error("UpdateLeaseStatus returned false for a known session whose status did not change")
	}
	if r.UpdateLeaseStatus("nobody", "down") {
		t.Error("UpdateLeaseStatus returned true for an unknown session")
	}
}

// The suffix that goes in the suppression log is enough to correlate and not enough to reuse.
func TestTheLoggedSessionSuffixIsNotTheToken(t *testing.T) {
	token := "abcdef0123456789abcdef0123456789"

	got := sessionTokenSuffix(token)
	if got == token {
		t.Fatal("the whole session token would be written to the log")
	}
	if len(got) >= len(token) {
		t.Errorf("the suffix %q is not shorter than the token", got)
	}
	if got[len(got)-6:] != token[len(token)-6:] {
		t.Errorf("the suffix %q does not end with the token's last six characters", got)
	}
	// A short or empty token must not index out of range, and must not be echoed whole.
	for _, short := range []string{"", "a", "abcdef"} {
		if s := sessionTokenSuffix(short); s == short && short != "" {
			t.Errorf("a short token %q was echoed verbatim", short)
		}
	}
}
