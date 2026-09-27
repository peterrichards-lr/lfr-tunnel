package server

import (
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// When the gateway tells an operator a tunnel has gone offline (#2270).
//
// Three rules, and the second and third exist because the first one alone was wrong in both
// directions at once:
//
//  1. The TRANSITION is the event. Reporting the steady state meant a mail every heartbeat --
//     twelve a minute, since sendAdminAlert does not throttle -- and an alert that repeats on a
//     timer is one nobody reads.
//  2. A tunnel carrying traffic right now is not one to page anybody about, so the alert is
//     HELD while bytes keep moving.
//  3. Held, not dropped. A status change happens on exactly one heartbeat, so suppressing on
//     that heartbeat and keeping no record means the alert never fires -- and that is the
//     COMMON shape, because a target that dies mid-interval was serving during it. The first
//     implementation of this had exactly that defect and its own comments argued for a
//     safeguard that `changed` made unreachable.
//
// And the hold is bounded, because byte movement cannot tell a healthy tunnel from a dead one
// that visitors are still hitting: request and response header bytes are counted on the way
// through, so a tunnel answering 502 to everyone keeps "serving" forever. maxOfflineAlertHold
// puts a ceiling on how long that can defer the truth.

// maxOfflineAlertHold is the longest the traffic check may defer an offline alert.
//
// One minute: long enough that a tunnel serving normally through a momentary client misreport
// is never reported, short enough that an operator hears about a genuinely dead target promptly
// even while visitors keep hitting it and generating the bytes that would otherwise hold the
// alert indefinitely.
//
// A ceiling rather than a cleverer signal because the accurate one is bigger than this change:
// the gateway knows each upstream response status, so "every response this window was a 502"
// distinguishes carrying traffic from serving it. Worth doing; not worth blocking a fix for a
// live false-alert on.
const maxOfflineAlertHold = time.Minute

// offlineAlertAction is what the heartbeat handler should do about the tunnel-offline alert.
type offlineAlertAction int

const (
	// offlineAlertNone: nothing to report. The tunnel is up, or its outage has already been
	// reported, or this gateway holds no lease for the session.
	offlineAlertNone offlineAlertAction = iota
	// offlineAlertSend: an unreported outage, with no traffic to contradict it.
	offlineAlertSend
	// offlineAlertHeld: an unreported outage on a tunnel that is still carrying bytes. It
	// stays pending and will be sent on the first quiet heartbeat, or when the hold expires.
	offlineAlertHeld
)

// NoteHeartbeat records a heartbeat's status against every lease of a session and decides what
// the offline alert should do.
//
// One method under one lock rather than a handful of accessors, because every part of this
// decision reads or writes lease state and a caller assembling it from pieces is a caller that
// can assemble it wrongly -- which is how the first version came to discard the event it was
// meant to defer.
func (r *Registry) NoteHeartbeat(sessionToken, status string, now time.Time) (known bool, action offlineAlertAction) {
	r.Lock()
	defer r.Unlock()

	leases, exists := r.sessionLeases[sessionToken]
	if !exists || len(leases) == 0 {
		return false, offlineAlertNone
	}

	// Traffic on ANY lease counts: a session is one tunnel to the person being paged, and
	// waking them because one of three ports was idle is the same false alert by another
	// route. The watermark advances on every lease every heartbeat, held or not, so the
	// window stays one interval rather than growing to "since we last looked".
	served := false
	for _, lease := range leases {
		total := atomic.LoadUint64(&lease.BytesIn) + atomic.LoadUint64(&lease.BytesOut)
		if total > lease.LastHeartbeatBytes {
			served = true
		}
		lease.LastHeartbeatBytes = total
	}

	changed := false
	for _, lease := range leases {
		if lease.Status != status {
			changed = true
		}
		lease.Status = status
	}

	if status != leaseStatusDown {
		// Recovered, or never down. Any pending outage is moot: the thing it would have
		// reported is over, and reporting it now would page somebody about the past.
		for _, lease := range leases {
			lease.offlineAlertPending = false
		}
		return true, offlineAlertNone
	}

	// Latch the transition. Only on the change, so an outage is one pending alert however many
	// heartbeats it spans.
	if changed {
		for _, lease := range leases {
			lease.offlineAlertPending = true
			lease.offlineAlertSince = now
		}
	}

	if !leases[0].offlineAlertPending {
		return true, offlineAlertNone
	}

	if served && now.Sub(leases[0].offlineAlertSince) < maxOfflineAlertHold {
		return true, offlineAlertHeld
	}

	for _, lease := range leases {
		lease.offlineAlertPending = false
	}
	return true, offlineAlertSend
}

// leaseStatusDown is the status a client reports when its local target does not answer. Named
// because the heartbeat handler and this decision both test for it, and a typo in one of two
// string literals is a tunnel that silently never alerts.
const leaseStatusDown = "down"

// sendTunnelOfflineAlert and logTunnelOfflineAlertHeld are the two outcomes of NoteHeartbeat
// that do anything, named so the wiring between the decision and the action can be asserted.
//
// The decision is unit-tested exhaustively above, and the previous version of this change still
// shipped a defect -- because the bug was in how the handler COMBINED the results, not in the
// results. Two named methods make that mapping something a test can hold; see
// tunnel_offline_wiring_test.go.
func (s *Server) sendTunnelOfflineAlert() {
	body, err := s.renderNotificationTemplate("en", "admin_tunnel_offline.txt", nil)
	if err != nil {
		slog.Warn(fmt.Sprintf("[Alert] Could not render the tunnel-offline template; sending the alert anyway: %v", err))
	}
	s.sendAdminAlert("alert_notify_tunnel_offline", "LFR Tunnel Alert: Tunnel Offline", body)
}

// logTunnelOfflineAlertHeld records an alert the traffic check is deferring.
//
// Logged rather than silent: a held alert is the one case where the gateway knowingly does not
// tell anybody something, so it has to leave a trace that says so.
func (s *Server) logTunnelOfflineAlertHeld(sessionToken string) {
	slog.Info(fmt.Sprintf("[Alert] Tunnel session reported down but is still carrying traffic; "+
		"holding the offline alert for up to %s. Session token suffix: %s",
		maxOfflineAlertHold, sessionTokenSuffix(sessionToken)))
}
