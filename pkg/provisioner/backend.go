package provisioner

import "context"

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
