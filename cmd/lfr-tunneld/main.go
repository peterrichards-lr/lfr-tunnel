package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/server"
)

func main() {
	configPath := flag.String("config", "", "Path to server-config.yaml")
	domainsFlag := flag.String("domains", "", "Comma-separated list of wildcard domains (e.g. liferay.com,tunnel.com)")
	certFile := flag.String("cert", "", "Wildcard SSL certificate path")
	keyFile := flag.String("key", "", "Wildcard SSL private key path")
	bindAddr := flag.String("bind", "", "HTTPS gateway bind address (e.g. :443)")
	httpBindAddr := flag.String("http-bind", "", "HTTP gateway bind address (e.g. :80)")
	checkConfig := flag.Bool("check-config", false,
		"validate the configuration and exit, without starting the gateway or touching the database")

	flag.Parse()

	// 1. Load config from file and environment variables
	cfg, err := config.LoadServerConfig(*configPath)
	if err != nil {
		// -check-config exists so this failure can be discovered BEFORE a restart rather than
		// during one. Editing server-config.yaml and restarting used to be a bet that the file
		// parsed: if it did not, LoadServerConfig failed here and the control plane did not come
		// back (#1455). Same message either way, so the check and the real start agree.
		if *checkConfig {
			fmt.Fprintf(os.Stderr, "INVALID: %v\n", err)
			os.Exit(1)
		}
		log.Fatalf("[Server] Failed to load configuration: %v", err)
	}

	exitIfConfigCheck(*checkConfig, cfg, *configPath)

	// 2. Override with command line flags if provided
	if *domainsFlag != "" {
		domains := strings.Split(*domainsFlag, ",")
		for i, d := range domains {
			domains[i] = strings.ToLower(strings.TrimSpace(d))
		}
		cfg.Domains = domains
	}
	if *certFile != "" {
		cfg.SSLCertFile = *certFile
	}
	if *keyFile != "" {
		cfg.SSLKeyFile = *keyFile
	}
	if *bindAddr != "" {
		cfg.BindAddr = *bindAddr
	}
	if *httpBindAddr != "" {
		cfg.HTTPBindAddr = *httpBindAddr
	}

	// 3. Validation
	if len(cfg.Domains) == 0 {
		log.Fatalf("Fatal: No domains specified. You must provide at least one domain via configuration or LFT_DOMAINS environment variable.")
	}

	// 4. Initialize server
	srv, err := server.NewServer(cfg)
	if err != nil {
		log.Fatalf("[Server] Failed to initialize server: %v", err)
	}

	// 5. Setup graceful shutdown handler
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	stopped := make(chan struct{})
	go func() {
		<-sigs
		slog.Info("[Server] Shutdown signal received, stopping...")
		srv.Stop()
		close(stopped)
	}()

	// 6. Start server
	slog.Info("[Server] Initializing Liferay Tunnel Gateway daemon...")
	err = srv.Start()

	// http.ErrServerClosed is the documented, expected return once Stop has closed the
	// listener: it means the shutdown worked, not that anything went wrong. Treating it as
	// fatal raced the signal handler's os.Exit(0) and usually won, so every clean stop
	// exited 1 -- systemd recorded FAILURE on each ordinary restart, and a real crash
	// looked identical to a normal one (issue #1169).
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("[Server] Server stopped with error: %v", err)
	}

	// Start returns the moment the listener closes, which is well before Stop has
	// recorded the clean shutdown and closed the database. Returning straight away would
	// race the signal handler and could cut that short.
	if errors.Is(err, http.ErrServerClosed) {
		select {
		case <-stopped:
		case <-time.After(15 * time.Second):
			slog.Warn("[Server] Shutdown did not complete within 15s; exiting anyway.")
		}
	}
	slog.Info("[Server] Shutdown complete.")
}

// exitIfConfigCheck prints the summary and exits when -check-config was passed. Lifted out of
// main only to keep it within funlen; it is one branch and belongs conceptually where it is used.
func exitIfConfigCheck(enabled bool, cfg *config.ServerConfig, path string) {
	if !enabled {
		return
	}
	fmt.Print(configSummary(cfg, path))
	os.Exit(0)
}

// reportConfigSummary prints what the gateway WOULD run with, so -check-config answers "is this
// the config I meant" as well as "does it parse".
//
// Reports shape and counts only. This file holds edge token hashes, SMTP credentials and webhook
// URLs, and an operator runs this in a terminal and pastes the output into tickets -- so no value
// that could be a credential is printed, only whether one is set. Same rule the drift check in
// pkg/ops follows (#1465).
func configSummary(cfg *config.ServerConfig, path string) string {
	var w strings.Builder
	if path == "" {
		path = config.ResolveDefaultConfigPath()
	}
	fmt.Fprintf(&w, "OK: %s parses and loads.\n\n", path)

	fmt.Fprintf(&w, "  domains:            %s\n", strings.Join(cfg.Domains, ", "))
	fmt.Fprintf(&w, "  http_bind_addr:     %s\n", cfg.HTTPBindAddr)
	fmt.Fprintf(&w, "  db_path:            %s\n", orNone(cfg.DBPath))
	fmt.Fprintf(&w, "  edge_nodes:         %d\n", len(cfg.EdgeNodes))
	for _, n := range cfg.EdgeNodes {
		fmt.Fprintf(&w, "    - %-12s %s  token_hash:%s\n", n.ID, orNone(n.URL), setOrUnset(n.TokenHash))
	}
	fmt.Fprintf(&w, "  control_plane_url:  %s\n", orNone(cfg.ControlPlaneURL))
	fmt.Fprintf(&w, "  edge_token:         %s\n", setOrUnset(cfg.EdgeToken))
	fmt.Fprintf(&w, "  dns_hook:           %s\n", orNone(cfg.DNSHook))

	// The gateway would come up, but not in the shape the operator probably intended.
	var warnings []string
	if len(cfg.Domains) == 0 {
		warnings = append(warnings, "no domains configured, so no hostname will be served")
	}
	if cfg.DBPath == "" && len(cfg.EdgeNodes) > 0 {
		warnings = append(warnings, "edge_nodes are configured but db_path is empty (stateless edge mode) -- "+
			"an edge does not route to other edges, so this is probably a control-plane config on an edge")
	}
	for _, n := range cfg.EdgeNodes {
		if n.URL == "" {
			warnings = append(warnings, fmt.Sprintf("edge %q has no url, so nothing can be routed to it", n.ID))
		}
		if n.TokenHash == "" {
			warnings = append(warnings, fmt.Sprintf("edge %q has no token_hash, so it can never authenticate", n.ID))
		}
	}
	if len(warnings) > 0 {
		fmt.Fprintln(&w)
		for _, warning := range warnings {
			fmt.Fprintf(&w, "  WARNING: %s\n", warning)
		}
	}

	fmt.Fprintln(&w, "\nThis checks the file only. Run `lfr-tunnel-ops check-config` against a live node to")
	fmt.Fprintln(&w, "also compare edge_nodes urls with the committed DNS spec, and check file ownership.")

	return w.String()
}

// orNone keeps an empty value legible rather than printing a blank.
func orNone(v string) string {
	if v == "" {
		return "(not set)"
	}
	return v
}

// setOrUnset reports presence without ever revealing the value.
func setOrUnset(v string) string {
	if v == "" {
		return "(not set)"
	}
	return "set"
}
