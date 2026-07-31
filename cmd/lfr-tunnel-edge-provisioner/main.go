// Command lfr-tunnel-edge-provisioner is the optional, AWS-specific sidecar
// described in issue #888: it's the only thing in this deployment that knows
// about AWS. It runs on the central control plane host, binds strictly to a
// loopback address, and exposes the versioned local API contract that
// lfr-tunneld calls to start/stop/restart edge node instances and read/update
// their EventBridge Scheduler stop/start schedules.
//
// Community deployments that don't run on AWS -- or don't want this
// functionality at all -- simply never run this binary and never set
// server-config.yaml's edge_provisioner_url. lfr-tunneld's core code has no
// dependency on this process or on AWS.
package main

import (
	"context"
	"flag"
	"log"
	"log/slog"

	"lfr-tunnel/pkg/provisioner"
)

func main() {
	configPath := flag.String("config", "/etc/lfr-tunneld/edge-provisioner.yaml", "Path to the edge-provisioner sidecar's config file")
	flag.Parse()

	cfg, err := provisioner.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("[edge-provisioner] failed to load config: %v", err)
	}

	token, err := provisioner.GenerateOrLoadToken(cfg.TokenFile)
	if err != nil {
		log.Fatalf("[edge-provisioner] failed to load/generate auth token: %v", err)
	}

	ctx := context.Background()
	backend, err := provisioner.NewAWSBackend(ctx, cfg)
	if err != nil {
		log.Fatalf("[edge-provisioner] failed to initialize AWS backend: %v", err)
	}

	slog.Info("[edge-provisioner] starting", "nodes", len(cfg.Nodes), "schedule_group", cfg.ScheduleGroup)
	srv := provisioner.NewServer(backend, token)
	if err := srv.ListenAndServe(cfg.ListenAddr); err != nil {
		log.Fatalf("[edge-provisioner] server exited: %v", err)
	}
}
