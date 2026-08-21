package main

import (
	"errors"
	"flag"
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

	flag.Parse()

	// 1. Load config from file and environment variables
	cfg, err := config.LoadServerConfig(*configPath)
	if err != nil {
		log.Fatalf("[Server] Failed to load configuration: %v", err)
	}

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
