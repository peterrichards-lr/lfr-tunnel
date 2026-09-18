package client

import "time"

// waitForDeadline is generous on purpose. The heartbeat ticks every defaultHeartbeatInterval
// (5s), so this covers several ticks rather than one plus a margin -- a loaded runner waits
// longer instead of reporting a defect that is not there (#2026, following #1390).
const waitForDeadline = 20 * time.Second

// waitFor polls cond until it holds or waitForDeadline passes, and reports whether it held.
//
// It returns rather than failing, because the useful message at a timeout usually depends on
// what the value turned out to BE, not merely on its never arriving -- "the client recorded the
// control plane's fingerprint" and "the client recorded nothing" are different findings and the
// caller is the only place that can tell them apart (§5c).
//
// Use it instead of sleeping a fixed margin. A fixed sleep here is wall-clock dependent in the
// unsafe direction: more delay makes it fail. Polling to a deadline fails safe.
func waitFor(cond func() bool) bool {
	deadline := time.Now().Add(waitForDeadline)
	for {
		if cond() {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}
