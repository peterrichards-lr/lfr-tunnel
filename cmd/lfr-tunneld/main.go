package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

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
	go func() {
		<-sigs
		log.Println("[Server] Shutdown signal received, stopping...")
		srv.Stop()
		os.Exit(0)
	}()

	// 6. Start server
	log.Println("[Server] Initializing Liferay Tunnel Gateway daemon...")
	if err := srv.Start(); err != nil {
		log.Fatalf("[Server] Server stopped with error: %v", err)
	}
}
