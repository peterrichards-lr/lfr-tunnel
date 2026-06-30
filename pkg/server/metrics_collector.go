package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

// MetricsCollector handles background metric collection and forwarding.
type MetricsCollector struct {
	queue    chan *db.TunnelMetric
	db       *db.DB
	cfg      *config.ServerConfig
	registry *Registry
}

// NewMetricsCollector initializes a new metrics collector.
func NewMetricsCollector(database *db.DB, cfg *config.ServerConfig, registry *Registry) *MetricsCollector {
	return &MetricsCollector{
		queue:    make(chan *db.TunnelMetric, 1000),
		db:       database,
		cfg:      cfg,
		registry: registry,
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
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	var localBuffer []*db.TunnelMetric

	for {
		select {
		case <-ctx.Done():
			return
		case m := <-c.queue:
			if c.db != nil {
				if err := c.db.RecordTunnelMetric(m); err != nil {
					slog.Info(fmt.Sprintf("[MetricsCollector] Failed to record tunnel metrics for %s: %v", m.FullHost, err))
				}
			} else if c.cfg.ControlPlaneURL != "" && c.cfg.EdgeToken != "" {
				localBuffer = append(localBuffer, m)
			}
		case <-ticker.C:
			leases := c.registry.ListLeases()
			for _, lease := range leases {
				bytesIn := atomic.LoadUint64(&lease.BytesIn)
				bytesOut := atomic.LoadUint64(&lease.BytesOut)
				diffIn := int64(bytesIn - lease.LastBytesIn)
				diffOut := int64(bytesOut - lease.LastBytesOut)

				if diffIn > 0 || diffOut > 0 {
					m := &db.TunnelMetric{
						UserID:          lease.UserID,
						SubdomainPrefix: lease.SubdomainPrefix,
						FullHost:        lease.FullHost,
						BytesIn:         diffIn,
						BytesOut:        diffOut,
						ConnectedAt:     lease.CreatedAt,
						RecordedAt:      time.Now().UTC(),
					}
					if c.db != nil {
						if err := c.db.RecordTunnelMetric(m); err != nil {
							slog.Info(fmt.Sprintf("[MetricsCollector] Failed to periodically record tunnel metrics for %s: %v", m.FullHost, err))
						} else {
							lease.LastBytesIn = bytesIn
							lease.LastBytesOut = bytesOut
						}
					} else if c.cfg.ControlPlaneURL != "" && c.cfg.EdgeToken != "" {
						localBuffer = append(localBuffer, m)
						lease.LastBytesIn = bytesIn
						lease.LastBytesOut = bytesOut
					}
				}
			}

			if len(localBuffer) > 0 && c.db == nil && c.cfg.ControlPlaneURL != "" && c.cfg.EdgeToken != "" {
				c.forwardToControlPlane(localBuffer)
				localBuffer = nil
			}
		}
	}
}

func (c *MetricsCollector) forwardToControlPlane(metrics []*db.TunnelMetric) {
	client := &http.Client{Timeout: 5 * time.Second}
	payloadBytes, err := json.Marshal(metrics)
	if err != nil {
		slog.Info(fmt.Sprintf("[MetricsCollector] Failed to marshal metrics: %v", err))
		return
	}

	req, err := http.NewRequest("POST", c.cfg.ControlPlaneURL+"/api/internal/edge-metrics", bytes.NewReader(payloadBytes))
	if err != nil {
		slog.Info(fmt.Sprintf("[MetricsCollector] Failed to create metrics request: %v", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Edge-Token", c.cfg.EdgeToken)

	resp, err := client.Do(req)
	if err != nil {
		slog.Info(fmt.Sprintf("[MetricsCollector] Failed to forward metrics to control plane: %v", err))
		return
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		slog.Info(fmt.Sprintf("[MetricsCollector] Control plane metrics returned status: %d", resp.StatusCode))
	}
}
