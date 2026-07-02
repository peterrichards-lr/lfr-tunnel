package server

import (
	"context"

	"testing"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

func TestMetricsCollector_Start(t *testing.T) {
	database, _ := db.Open(":memory:")
	cfg := &config.ServerConfig{}
	registry := NewRegistry(nil)
	collector := NewMetricsCollector(database, cfg, registry)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // instantly stop

	collector.Start(ctx)
}
