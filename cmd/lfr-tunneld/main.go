package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/server"
)

func main() {
	configPath := flag.String("config", "", "Path to server-config.yaml")
	domain1 := flag.String("domain1", "", "Primary wildcard domain (e.g. liferay.com)")
	domain2 := flag.String("domain2", "", "Secondary wildcard domain (e.g. tunnel.com)")
	token := flag.String("token", "", "Shared client auth token")
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
	if *domain1 != "" {
		cfg.Domain1 = *domain1
	}
	if *domain2 != "" {
		cfg.Domain2 = *domain2
	}
	if *token != "" {
		cfg.AuthToken = *token
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
	if cfg.Domain1 == "" && cfg.Domain2 == "" {
		log.Println("[Warning] No public domains configured. Running in localhost-only fallback mode.")
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
