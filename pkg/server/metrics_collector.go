package server

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// metricsCollectorInterval is how often the control plane sweeps its live leases for
// bytes to record.
const metricsCollectorInterval = 5 * time.Minute

// MetricsCollector handles background metric collection and forwarding.
//
// Control plane only, as of #1958. An edge has no database and reports its byte deltas over
// the edge control channel instead (edge_metrics.go); this used to buffer them here and POST
// them to /api/internal/edge-metrics, and keeping both would double-count every byte.
type MetricsCollector struct {
	queue    chan *db.TunnelMetric
	db       *db.DB
	cfg      *config.ServerConfig
	registry *Registry
	// interval is how often Start sweeps, read from the field rather than the constant so a
	// test can shorten it. At five minutes no test could observe a sweep at all, and
	// TestMetricsCollectorLeavesEdgeDeltasForTheReporter asserted that an edge's deltas had
	// survived a sweep that had never once run (#2038).
	interval time.Duration
	// sweeps counts ticker passes that have started, so a test can wait for a real sweep
	// instead of sleeping a guess at one. Incremented before the edge/control branch below:
	// it counts the sweep happening, not what the sweep decided to do.
	sweeps atomic.Uint64
}

// NewMetricsCollector initializes a new metrics collector.
func NewMetricsCollector(database *db.DB, cfg *config.ServerConfig, registry *Registry) *MetricsCollector {
	return &MetricsCollector{
		queue:    make(chan *db.TunnelMetric, 1000),
		db:       database,
		cfg:      cfg,
		registry: registry,
		interval: metricsCollectorInterval,
	}
}

// Queue adds a new tunnel metric to the processing queue.
func (c *MetricsCollector) Queue(m *db.TunnelMetric) {
	select {
	case c.queue <- m:
	default:
		slog.Info(fmt.Sprintf("[MetricsCollector] Queue full; dropping metric for %s", m.FullHost))
	}
}

// Start begins the background processing loop for metrics.
func (c *MetricsCollector) Start(ctx context.Context) {
	interval := c.interval
	if interval <= 0 {
		interval = metricsCollectorInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case m := <-c.queue:
			if c.db == nil {
				// Said out loud rather than dropped in silence. A node with no database
				// should not be queueing here at all -- an edge's deltas go to
				// edgeMetricsReporter (#1958) -- so reaching this is a wiring mistake, and
				// the symptom it produces is measurement that reads as no traffic.
				slog.Warn(fmt.Sprintf("[MetricsCollector] No database on this node; discarding a metric for %s", m.FullHost))
				continue
			}
			if err := c.db.RecordTunnelMetric(m); err != nil {
				slog.Info(fmt.Sprintf("[MetricsCollector] Failed to record tunnel metrics for %s: %v", m.FullHost, err))
			}
		case <-ticker.C:
			c.sweeps.Add(1)
			if c.db == nil {
				// Deliberately does NOT call TakeByteDeltas: taking a delta consumes it, and
				// on an edge the reporter that can actually deliver it is the one entitled
				// to take it.
				continue
			}
			// TakeByteDeltas advances each lease's watermark as it reads it, so a tick
			// records the interval rather than the running total. The previous loop wrote
			// the watermark back onto ListLeases' copy, which is not the lease, so every
			// tick re-recorded the whole session from the beginning (#1958).
			for _, d := range c.registry.TakeByteDeltas() {
				m := &db.TunnelMetric{
					UserID:          d.UserID,
					SubdomainPrefix: d.SubdomainPrefix,
					FullHost:        d.FullHost,
					BytesIn:         d.BytesIn,
					BytesOut:        d.BytesOut,
					ConnectedAt:     d.ConnectedAt,
					RecordedAt:      time.Now().UTC(),
				}
				if err := c.db.RecordTunnelMetric(m); err != nil {
					slog.Info(fmt.Sprintf("[MetricsCollector] Failed to periodically record tunnel metrics for %s: %v", m.FullHost, err))
				}
			}
		}
	}
}
