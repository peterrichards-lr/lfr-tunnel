package provisioner

import (
	"fmt"
	"net"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// NodeTarget maps a central control plane's edge node ID (the same "id" used
// in server-config.yaml's edge_nodes list) to the cloud resource that backs
// it. This mapping lives here, not in server-config.yaml, so the core
// lfr-tunneld config never needs to know about AWS instance IDs or regions.
type NodeTarget struct {
	InstanceID string `yaml:"instance_id"`
	Region     string `yaml:"region"`
}

// Config is the edge-provisioner sidecar's own configuration. It is
// deliberately separate from lfr-tunneld's server-config.yaml -- this
// process is optional, provider-specific, and self-hosters who don't use it
// never need to know its shape.
type Config struct {
	// ListenAddr must be a loopback-only address (127.0.0.1:<port>) -- this
	// sidecar is never meant to be reachable from outside the host.
	ListenAddr string `yaml:"listen_addr"`
	// TokenFile is where the shared auth secret lfr-tunneld presents on every
	// request is stored. Generated on first run if it doesn't already exist.
	TokenFile string `yaml:"token_file"`
	// ScheduleGroup is the EventBridge Scheduler group these nodes' stop/start
	// schedules live in -- must match scripts/common/schedule-edge-node-hours.sh's
	// --schedule-group (default "lfr-tunnel-edge-nodes") for GetSchedule/UpdateSchedule
	// to find the same schedules that script created.
	ScheduleGroup string                `yaml:"schedule_group"`
	Nodes         map[string]NodeTarget `yaml:"nodes"`
}

// LoadConfig reads and validates the sidecar's YAML config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	if cfg.ListenAddr == "" {
		return nil, fmt.Errorf("listen_addr is required")
	}
	if err := requireLoopback(cfg.ListenAddr); err != nil {
		return nil, err
	}
	if cfg.TokenFile == "" {
		return nil, fmt.Errorf("token_file is required")
	}
	if cfg.ScheduleGroup == "" {
		cfg.ScheduleGroup = "lfr-tunnel-edge-nodes"
	}
	for id, target := range cfg.Nodes {
		if target.InstanceID == "" || target.Region == "" {
			return nil, fmt.Errorf("node %q: instance_id and region are both required", id)
		}
	}

	return &cfg, nil
}

// requireLoopback enforces this sidecar's core safety invariant in code,
// not just documentation: it must never be reachable from outside the host.
func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen_addr %q must be host:port: %w", addr, err)
	}
	if host == "" {
		return fmt.Errorf("listen_addr %q must bind to a specific loopback address (127.0.0.1 or ::1), not all interfaces", addr)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen_addr %q is not a loopback address -- this sidecar must never be reachable off-host", addr)
	}
	return nil
}
