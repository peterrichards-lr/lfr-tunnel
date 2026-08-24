package provisioner

import (
	"context"
	"fmt"
	"time"
)

// Capabilities describes what a Backend implementation can actually do, so
// the portal can hide UI affordances a given backend doesn't support --
// independent of which API version it speaks. See GET /v1/capabilities.
type Capabilities struct {
	StartStop  bool `json:"start_stop"`
	Restart    bool `json:"restart"`
	Scheduling bool `json:"scheduling"`
}

// Schedule is the wire shape for GET/PUT /v1/nodes/{id}/schedule.
type Schedule struct {
	Enabled   bool   `json:"enabled"`
	StopTime  string `json:"stop_time,omitempty"`
	StartTime string `json:"start_time,omitempty"`
	Timezone  string `json:"timezone,omitempty"`
}

// minDailyUptime is the least a node may be up per day before a schedule is treated as a
// mistake rather than an intention. It exists because edge-sa was found configured to stop
// at 16:00 and start at 15:45, leaving it up for fifteen minutes a day -- the times had been
// swapped, and nothing rejected it. Only `enabled: false` stood between that and a nightly
// outage (#1250).
//
// A tunnel gateway that is genuinely wanted for under an hour a day is not a case worth
// supporting at the cost of catching swapped times.
const minDailyUptime = time.Hour

// Validate reports whether a schedule is coherent enough to apply.
//
// It cannot catch every bad schedule -- edge-in was set to stop at 15:45 Asia/Kolkata, in
// the middle of the working day, which is structurally fine and semantically wrong. What it
// does catch is the structural error: times that leave a node down almost permanently, which
// is what swapping stop and start produces.
func (s Schedule) Validate() error {
	// An unset schedule is allowed: disabling one is how a node is taken off the rota, and
	// the times are then irrelevant.
	if !s.Enabled {
		return nil
	}

	if s.Timezone == "" {
		return fmt.Errorf("timezone is required when a schedule is enabled")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return fmt.Errorf("unknown timezone %q", s.Timezone)
	}

	stopHour, stopMin, err := parseHHMM(s.StopTime)
	if err != nil {
		return fmt.Errorf("stop_time: %w", err)
	}
	startHour, startMin, err := parseHHMM(s.StartTime)
	if err != nil {
		return fmt.Errorf("start_time: %w", err)
	}

	stop := time.Duration(stopHour)*time.Hour + time.Duration(stopMin)*time.Minute
	start := time.Duration(startHour)*time.Hour + time.Duration(startMin)*time.Minute
	if stop == start {
		return fmt.Errorf("stop_time and start_time are both %s, which would never start or stop the node", s.StopTime)
	}

	// Downtime measured forwards from stop to start, wrapping midnight -- the same
	// arithmetic the gateway uses to decide whether a node is inside its window.
	const day = 24 * time.Hour
	downtime := (start - stop + day) % day
	if uptime := day - downtime; uptime < minDailyUptime {
		return fmt.Errorf("stopping at %s and starting at %s leaves the node up for only %s a day -- are stop_time and start_time the wrong way round?",
			s.StopTime, s.StartTime, uptime.Round(time.Minute))
	}

	return nil
}

// Backend is the provider-specific implementation behind the sidecar's HTTP
// contract (see issue #888). AWSBackend is the only implementation today,
// but nothing in the HTTP layer (server.go) depends on AWS directly -- a
// GCP, Azure, bare-metal, or no-op Backend could be substituted without
// touching the API surface.
//
// Start/Stop/Restart are expected to block until the underlying action has
// *completed*, not just submitted -- the HTTP layer (server.go) is what
// turns this into the async 202-Accepted contract, by running these calls
// in a background goroutine rather than waiting on them inline. Restart in
// particular may legitimately take a while (stop, wait for stopped, start)
// since whatever calls it already isn't blocking on it.
type Backend interface {
	Capabilities() Capabilities
	Start(ctx context.Context, nodeID string) error
	Stop(ctx context.Context, nodeID string) error
	Restart(ctx context.Context, nodeID string) error
	GetSchedule(ctx context.Context, nodeID string) (Schedule, error)
	SetSchedule(ctx context.Context, nodeID string, s Schedule) error
}

// ErrNodeNotFound is returned by Backend methods when the given node ID
// isn't in this sidecar's configured node list.
type ErrNodeNotFound struct {
	NodeID string
}

func (e *ErrNodeNotFound) Error() string {
	return "unknown node: " + e.NodeID
}
