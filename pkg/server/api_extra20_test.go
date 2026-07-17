package server

import (
	"context"

	"testing"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

func TestMetricsCollector_Start(t *testing.T) {
	database, _err := db.Open(":memory:")
	_ = _err //nolint:errcheck
	cfg := &config.ServerConfig{}
	registry := NewRegistry(nil)
	collector := NewMetricsCollector(database, cfg, registry)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // instantly stop

	collector.Start(ctx)
}
