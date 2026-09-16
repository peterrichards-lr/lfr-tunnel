package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
)

// Acting on "the node set changed" (#1937).
//
// pkg/client/node_set.go recognises the signal on the heartbeat and records it. Everything here
// is the decision, and it is deliberately all on this side: the gateway says only that its roster
// hashes to a different value, and this client then measures for itself, applies its own
// thresholds, and may well decide to stay. Nothing the gateway sends names a destination.
//
// The case this exists for, from #1937: edges run 08:00-00:00 in their own timezone, so a
// developer who starts before their nearest edge wakes elects the best of what answered and stays
// there all day. Nothing tells a running client that the edge came up at 08:00.
//
// NOT the explanation for the US user in #1937's comment thread, which is worth writing down
// because the issue was filed believing it was. That client probed with every region available
// and still elected Ireland: #1947, where the probe reused a warm pooled connection to the
// incumbent and measured it at one round trip while its rivals paid three. That is fixed, and
// this change is about a different gap.

// reelectionMinGain and reelectionMinGainFraction are how much closer another gateway has to be
// before a running, working session is torn down to move to it.
//
// A move is an INTERRUPTION -- a re-registration plus a chisel reconnect, seconds of downtime for
// every request in flight -- so "any improvement" is the wrong bar. Both thresholds must be met:
//
//   - The absolute floor rejects two gateways that are effectively the same distance away. The
//     probe times DNS + TCP + TLS + a full HTTPS GET, measured in this repo at 2.7-3.0x one round
//     trip (#1947), so 40ms of measured difference is roughly 13ms of real path difference. Below
//     that we are reading jitter, and #1947 is the standing proof that this measurement can be
//     wrong by more than that.
//   - The fractional floor stops a long-haul pair qualifying on the absolute rule alone: 400ms
//     against 350ms clears 40ms comfortably and means almost nothing, whereas 200ms against 47ms
//     is a different continent.
//
// Sized against the real numbers this exists for. #1947 measured the Orlando user's paths by
// traceroute at ~200ms to Ireland and ~47ms to Ohio: a 153ms, 76% gain, clearing both floors by a
// wide margin. Something that only just clears them is not worth an interruption, and staying put
// is always a safe answer here -- the next restart re-elects from scratch anyway.
const (
	reelectionMinGain         = 40 * time.Millisecond
	reelectionMinGainFraction = 0.30
)

// reelectionMinInterval bounds how often a client will move for this reason.
//
// A flapping node changes the fingerprint every time it flaps, and without this each flap would
// be an interruption. One move per ten minutes means the worst a flapping edge can do is one
// re-registration per ten minutes, while the case this is for -- an edge waking once a morning --
// is unaffected. A var, and env-overridable, for the same reason plannedShutdownCooldown is
// (#1374): an end-to-end test must not have to sit out a real ten minutes. Nothing documents it
// as user configuration.
var reelectionMinInterval = cooldownFromEnv("LFT_REELECTION_MIN_INTERVAL", 10*time.Minute)

// nodeSetPollInterval is how often the watcher checks for the signal. The signal is set from a
// heartbeat response rather than pushed, so there is nothing to select on -- the same shape, and
// the same interval, as shutdownMigrationPollInterval.
var nodeSetPollInterval = time.Second

// reelectionTarget is where a topology-driven move is going, decided before the session is torn
// down so that a session is only ever ended for a move that is already known to be worth making.
type reelectionTarget struct {
	Region string
	URL    string
	// Regions is the roster the decision was made against, handed to the session loop so it
	// registers with the same list the probe ranked rather than the one fetched at startup --
	// which, in the case this exists for, is precisely the list missing the new node.
	Regions map[string]string
	Reason  string
}

// reelection holds the pending move and the throttle. A package-level singleton for the same
// reason cooldowns is one: the watcher is started fresh for every session and the throttle has to
// outlive any one of them.
var reelection = &reelectionState{}

type reelectionState struct {
	mu      sync.Mutex
	pending *reelectionTarget
	lastAt  time.Time
}

// throttled reports whether another topology-driven move is too soon after the last one.
func (r *reelectionState) throttled() (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastAt.IsZero() {
		return 0, false
	}
	remaining := reelectionMinInterval - time.Since(r.lastAt)
	if remaining <= 0 {
		return 0, false
	}
	return remaining, true
}

// propose records the target and starts the throttle.
//
// The throttle starts when the move is decided, not when it succeeds. A target that turns out to
// refuse the registration would otherwise be retried on every subsequent roster change, which is
// the thrash this is here to prevent.
func (r *reelectionState) propose(t *reelectionTarget) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = t
	r.lastAt = time.Now()
}

// consume takes the pending move, leaving none.
func (r *reelectionState) consume() *reelectionTarget {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.pending
	r.pending = nil
	return t
}

// startNodeSetWatcher ends the current session when a materially closer gateway has appeared,
// so the session loop re-establishes on it.
//
// It does not perform the move itself, exactly as StartShutdownMigrator does not: cancelling the
// session hands control back to the loop, which already knows how to register somewhere else.
// Reusing that path is what keeps a planned move and an unplanned one from drifting apart.
//
// Deliberately not started for a client pinned with -server. regionvocab.IsPinned draws that line
// and draws it at -server alone: that user named a gateway, and moving them off it would break
// the promise PinnedRoutingNotice makes. -region does not pin, and is handled below by moving the
// client to the region it actually asked for once that region exists again.
func startNodeSetWatcher(ctx context.Context, cancel context.CancelFunc, engine *client.InterceptorEngine, serverURL, requestedRegion string) {
	// Read the tunable on the caller's goroutine, not inside the one being started (#1328).
	pollInterval := nodeSetPollInterval
	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !engine.ConsumeNodeSetChange() {
					continue
				}
				target := chooseReelectionTarget(serverURL, requestedRegion, engine)
				if target == nil {
					continue
				}
				reelection.propose(target)
				slog.Info(fmt.Sprintf("[Client] %s Moving to '%s' (%s).", target.Reason, target.Region, target.URL))
				engine.LogEvent("info", "node_set_move_started", map[string]any{
					"to":           target.Region,
					logFieldURL:    target.URL,
					"from_url":     serverURL,
					fieldMoveCause: target.Reason,
				})
				cancel()
				return
			}
		}
	}()
}

// fieldMoveCause is the diagnostic key explaining why a topology-driven move happened. Named
// because a second spelling of it would write events nothing queries.
const fieldMoveCause = "cause"

// chooseReelectionTarget decides where, if anywhere, this client should move now that the roster
// has changed. Returns nil for "stay put", which is the answer in every doubtful case.
func chooseReelectionTarget(currentURL, requestedRegion string, engine *client.InterceptorEngine) *reelectionTarget {
	if remaining, throttled := reelection.throttled(); throttled {
		engine.LogEvent("info", "node_set_move_declined", map[string]any{
			fieldMoveCause: "throttled",
			"retry_in":     remaining.Round(time.Second).String(),
		})
		return nil
	}

	// Its own config, never the live one: this runs on a goroutine beside a session loop that
	// reads cfg, and fetchRemoteRegions replaces cfg.Regions wholesale. Sharing it would be a
	// data race on the map the session is using to decide where it can fail over to.
	roster := &config.ClientConfig{ServerURL: currentURL}
	fetchRemoteRegionsFn(roster)
	if len(roster.Regions) == 0 {
		return nil
	}
	// A gateway we deliberately left is not one to move to just because it is advertised
	// again (#1310), so the same cooldowns failover honours apply here.
	candidates := cooldowns.filter(roster.Regions)
	currentHost := gatewayHostKey(currentURL)

	// A client that named a region is not asking to be put on the fastest gateway; it is asking
	// for that region. When the region it asked for is missing at startup the client falls back
	// to a probe (#1690), and this is the point where the request can finally be honoured.
	// No threshold: the user's stated choice is not a latency question.
	if requestedRegion != "" {
		url, ok := candidates[requestedRegion]
		if !ok || url == "" || gatewayHostKey(url) == currentHost {
			return nil
		}
		return &reelectionTarget{
			Region:  requestedRegion,
			URL:     url,
			Regions: roster.Regions,
			Reason:  fmt.Sprintf("The region you asked for, '%s', is available again.", requestedRegion),
		}
	}

	rtts, unreachable := probeRegionLatencies(candidates)
	reportProbeResults(rtts, unreachable)

	best := fastestRegion(rtts)
	if best == "" {
		return nil
	}
	bestURL := candidates[best]
	if gatewayHostKey(bestURL) == currentHost {
		return nil
	}

	// Compared by host, not by name: the same gateway is advertised under several names, so a
	// name comparison finds no measurement for the gateway we are actually on (issue #1166).
	currentRTT, measured := rttForHost(rtts, candidates, currentHost)
	if !measured {
		// Our own gateway did not answer its probe. That is failover's business, not this
		// one's -- and an unanswered probe is not evidence that somewhere else is better.
		engine.LogEvent("info", "node_set_move_declined", map[string]any{
			fieldMoveCause: "current gateway did not answer the probe",
		})
		return nil
	}

	gain := currentRTT - rtts[best]
	if !materiallyCloser(currentRTT, rtts[best]) {
		engine.LogEvent("info", "node_set_move_declined", map[string]any{
			fieldMoveCause: "not materially closer",
			"candidate":    best,
			"current_ms":   currentRTT.Milliseconds(),
			"candidate_ms": rtts[best].Milliseconds(),
		})
		return nil
	}

	return &reelectionTarget{
		Region:  best,
		URL:     bestURL,
		Regions: roster.Regions,
		Reason: fmt.Sprintf("A closer gateway is now available: '%s' answered %v faster than the one in use (%v against %v).",
			best, gain.Round(time.Millisecond), rtts[best].Round(time.Millisecond), currentRTT.Round(time.Millisecond)),
	}
}

// rttForHost finds the measurement for the gateway at a given host, whatever name it was probed
// under.
func rttForHost(rtts map[string]time.Duration, regions map[string]string, host string) (time.Duration, bool) {
	if host == "" {
		return 0, false
	}
	for region, rtt := range rtts {
		if gatewayHostKey(regions[region]) == host {
			return rtt, true
		}
	}
	return 0, false
}

// materiallyCloser reports whether a candidate is enough closer to justify tearing down a working
// session. See reelectionMinGain for why both floors exist.
func materiallyCloser(current, candidate time.Duration) bool {
	gain := current - candidate
	if gain < reelectionMinGain {
		return false
	}
	return float64(gain) >= reelectionMinGainFraction*float64(current)
}
