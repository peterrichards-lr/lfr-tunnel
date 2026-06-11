package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
)

type arrayFlags []string

func (i *arrayFlags) String() string {
	return strings.Join(*i, ", ")
}

func (i *arrayFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}

func main() {
	configPath := flag.String("config", "", "Path to client-config.yaml")
	serverURL := flag.String("server", "", "Gateway server URL (e.g. https://tunnel.liferay.com)")
	token := flag.String("token", "", "Gateway auth token")
	subdomain := flag.String("subdomain", "", "Requested subdomain prefix (e.g. alpha-se)")
	portsStr := flag.String("ports", "", "Comma-separated ports to expose (e.g. 8080,3000)")
	basicAuth := flag.String("basic-auth", "", "Require HTTP Basic Auth (format: 'username:password')")
	inspectorPort := flag.Int("inspector-port", 4040, "Local port for the Inspector Web UI")
	var addHeaders arrayFlags
	flag.Var(&addHeaders, "add-header", "Inject HTTP header (e.g. 'X-Bypass-CORS: true')")
	rateLimit := flag.Int("rate-limit", 0, "Max requests per second for your subdomains (0 = unlimited)")
	background := flag.Bool("background", false, "Run client in background")
	status := flag.Bool("status", false, "Check status of the background tunnel")
	stop := flag.Bool("stop", false, "Stop the background tunnel")
	versionFlag := flag.Bool("version", false, "Print client version")
	checkVersionFlag := flag.Bool("check-version", false, "Check server API for version requirements and print as JSON")
	upgradeFlag := flag.Bool("upgrade", false, "Self-upgrade client to the latest release")

	flag.Parse()

	if len(os.Args) > 1 && os.Args[1] == "install-service" {
		if err := client.InstallService(); err != nil {
			log.Fatalf("[Error] Failed to install service: %v", err)
		}
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "login" {
		cfg, err := config.LoadClientConfig(*configPath)
		if err != nil {
			log.Fatalf("[Client] Failed to load configuration: %v", err)
		}
		if *serverURL != "" {
			cfg.ServerURL = *serverURL
		}
		if err := client.RunLogin(cfg.ServerURL); err != nil {
			log.Fatalf("[Error] Login failed: %v", err)
		}
		return
	}

	if *versionFlag {
		fmt.Printf("lfr-tunnel version %s\n", config.Version)
		return
	}

	if *checkVersionFlag {
		cfg, err := config.LoadClientConfig(*configPath)
		if err != nil {
			log.Fatalf("[Client] Failed to load configuration: %v", err)
		}
		if *serverURL != "" {
			cfg.ServerURL = *serverURL
		}
		info, err := client.CheckServerCompatibility(cfg.ServerURL)
		if err != nil {
			log.Fatalf("[Error] Failed to check server compatibility: %v", err)
		}
		b, _ := json.Marshal(info)
		fmt.Println(string(b))
		return
	}

	if *upgradeFlag {
		if err := client.SelfUpgrade(config.Version); err != nil {
			log.Fatalf("[Error] Upgrade failed: %v", err)
		}
		return
	}

	if *stop {
		handleStop()
		return
	}

	if *status {
		handleStatus()
		return
	}

	// Start compatibility check asynchronously
	compatChan := make(chan *client.ServerVersionInfo, 1)
	go func() {
		// Temporarily infer server URL before full load to start early
		sURL := *serverURL
		if sURL == "" {
			tmpCfg, _ := config.LoadClientConfig(*configPath)
			if tmpCfg != nil {
				sURL = tmpCfg.ServerURL
			}
		}
		info, _ := client.CheckServerCompatibility(sURL)
		compatChan <- info
	}()

	// 1. Load config from file and environment variables
	cfg, err := config.LoadClientConfig(*configPath)
	if err != nil {
		log.Fatalf("[Client] Failed to load configuration: %v", err)
	}

	// 2. Override with CLI flags
	if *serverURL != "" {
		cfg.ServerURL = *serverURL
	}
	if *token != "" {
		cfg.AuthToken = *token
	}
	if *subdomain != "" {
		cfg.Subdomain = *subdomain
	}
	if *rateLimit > 0 {
		cfg.RateLimit = *rateLimit
	}
	if *basicAuth != "" {
		cfg.BasicAuth = *basicAuth
	}

	if *background {
		handleBackground()
		return
	}

	var ports []int
	if *portsStr != "" {
		parts := strings.Split(*portsStr, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if p, err := strconv.Atoi(part); err == nil {
				ports = append(ports, p)
			}
		}
	} else {
		ports = cfg.Ports
	}

	// 3. Resolve port mappings
	var portMappings []client.PortMapping
	if len(ports) > 0 && *portsStr != "" {
		// Ports were explicitly requested via flag
		for idx, port := range ports {
			suffix := ""
			if idx > 0 {
				suffix = fmt.Sprintf("-%d", port)
			}
			portMappings = append(portMappings, client.PortMapping{
				LocalPort:  port,
				NameSuffix: suffix,
			})
		}
	} else {
		// Auto-detect workspace ports
		log.Println("[Client] Scanning workspace for Liferay Client Extensions...")
		portMappings, err = client.DetectWorkspacePorts(".")
		if err != nil {
			log.Printf("[Warning] Failed to scan workspace: %v. Using defaults.", err)
			portMappings = []client.PortMapping{{LocalPort: 8080}}
		}
	}

	// Start Interceptor Engine
	engine := client.NewInterceptorEngine(addHeaders)
	client.StartInspector(*inspectorPort, engine)

	// 4. Resolve subdomain prefix
	sub := cfg.Subdomain
	if sub == "" {
		// Use hostname or environment username as fallback
		hostname, err := os.Hostname()
		if err == nil && hostname != "" {
			sub = strings.ToLower(hostname)
			// Remove domain parts and replace unsafe characters
			if idx := strings.Index(sub, "."); idx != -1 {
				sub = sub[:idx]
			}
			sub = strings.ReplaceAll(sub, " ", "-")
			sub = strings.ReplaceAll(sub, "_", "-")
		} else {
			sub = "se-dev"
		}
	}

	// Sanity clean subdomain prefix
	sub = strings.ToLower(strings.TrimSpace(sub))

	log.Printf("[Client] Subdomain prefix: %s", sub)
	log.Printf("[Client] Exposing ports:")
	for _, pm := range portMappings {
		suffixStr := " (Primary)"
		if pm.NameSuffix != "" {
			suffixStr = fmt.Sprintf(" (Suffix: -%s)", pm.NameSuffix)
		}
		log.Printf("  - Local port %d%s", pm.LocalPort, suffixStr)
	}

	// Check compatibility result with 500ms timeout
	select {
	case info := <-compatChan:
		if info != nil {
			if client.CompareVersions(config.Version, info.MinVersion) < 0 {
				log.Fatalf("[Error] Your Liferay Tunnel client is too old to connect to the server. Minimum required version is %s.", info.MinVersion)
			}
			if client.CompareVersions(config.Version, info.LatestVersion) < 0 {
				log.Printf("[Warning] A new version of Liferay Tunnel (%s) is available. You are running %s.", info.LatestVersion, config.Version)
			}
		}
	case <-time.After(500 * time.Millisecond):
		// Silent timeout if server is slow or offline
	}

	// 5. Registration Handshake
	fmt.Printf("[Client] Registering tunnel (%s) at %s...\n", cfg.Subdomain, cfg.ServerURL)
	if cfg.RateLimit > 0 {
		fmt.Printf("[Client] Requested Subdomain Rate Limit: %d req/s\n", cfg.RateLimit)
	}
	if cfg.BasicAuth != "" {
		fmt.Printf("[Client] Data Plane HTTP Basic Auth is ENABLED\n")
	}
	regResp, err := client.RegisterTunnel(cfg.ServerURL, cfg.AuthToken, sub, portMappings, cfg.RateLimit, cfg.BasicAuth, engine.AddedHeaders)
	if err != nil {
		log.Fatalf("[Error] Failed to register: %v\n", err)
	}

	if regResp.Warning != "" {
		log.Printf("\n[WARNING] %s\n\n", regResp.Warning)
	}

	// Modify portMappings to point to dynamic Interceptor ports
	for i, pm := range portMappings {
		targetPort := pm.LocalPort
		interceptPort, err := engine.InterceptPort(targetPort)
		if err != nil {
			log.Fatalf("[Error] Failed to start interceptor for port %d: %v", targetPort, err)
		}
		engine.StartHealthChecks(cfg.ServerURL, regResp.SessionToken, targetPort)
		portMappings[i].LocalPort = interceptPort
	}

	var publicURLs []string
	log.Println("[Client] Registration successful! Your public tunnel URLs are:")
	for _, domain := range regResp.Domains {
		for _, pm := range portMappings {
			var fullSubdomain string
			if pm.NameSuffix == "" {
				fullSubdomain = sub
			} else {
				fullSubdomain = fmt.Sprintf("%s-%s", sub, pm.NameSuffix)
			}
			urlStr := fmt.Sprintf("https://%s.%s", fullSubdomain, domain)
			publicURLs = append(publicURLs, urlStr)
			log.Printf("  %s -> local port %d", urlStr, pm.LocalPort)
		}
	}

	// 6. Run Client and wait for signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Println("[Client] Shutdown signal received, closing tunnel...")
		cancel()
	}()

	if err := client.RunClient(ctx, cfg.ServerURL, regResp.SessionToken, regResp.Remotes, publicURLs); err != nil && ctx.Err() == nil {
		log.Fatalf("[Client] Tunnel disconnected with error: %v", err)
	}
	log.Println("[Client] Tunnel shutdown completed.")
}

func getPIDFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".lfr-tunnel")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "lfr-tunnel.pid"), nil
}

func writePID(pid int) error {
	path, err := getPIDFilePath()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0600)
}

func readPID() (int, error) {
	path, err := getPIDFilePath()
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pidStr := strings.TrimSpace(string(data))
	return strconv.Atoi(pidStr)
}

func isPIDRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

func handleBackground() {
	pid, err := readPID()
	if err == nil && pid > 0 && isPIDRunning(pid) {
		log.Fatalf("[Client] A background tunnel is already running (PID: %d). Stop it first using: lfr-tunnel -stop\n", pid)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("[Client] Failed to resolve home directory: %v\n", err)
	}
	logDir := filepath.Join(home, ".lfr-tunnel")
	_ = os.MkdirAll(logDir, 0700)
	logPath := filepath.Join(logDir, "client.log")

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatalf("[Client] Failed to create log file: %v\n", err)
	}
	defer logFile.Close() //nolint:errcheck

	execPath, err := os.Executable()
	if err != nil {
		log.Fatalf("[Client] Failed to get executable path: %v\n", err)
	}

	var childArgs []string
	for _, arg := range os.Args[1:] {
		if arg == "-background" || arg == "--background" {
			continue
		}
		childArgs = append(childArgs, arg)
	}

	cmd := exec.Command(execPath, childArgs...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Dir = "."

	if err := cmd.Start(); err != nil {
		log.Fatalf("[Client] Failed to start background process: %v\n", err)
	}

	if err := writePID(cmd.Process.Pid); err != nil {
		log.Printf("[Warning] Failed to write PID file: %v\n", err)
	}

	log.Printf("[Client] Tunnel started in background (PID: %d).\n", cmd.Process.Pid)
	log.Printf("[Client] Logs: %s\n", logPath)
	log.Println("[Client] To stop the tunnel, run: lfr-tunnel -stop")
}

func handleStop() {
	pid, err := readPID()
	if err != nil || pid <= 0 {
		log.Println("[Client] No background tunnel is active.")
		return
	}
	if !isPIDRunning(pid) {
		log.Printf("[Client] Stale PID file found. Process %d is not running.\n", pid)
		pidPath, _ := getPIDFilePath()
		_ = os.Remove(pidPath)
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		log.Fatalf("[Client] Failed to find process: %v\n", err)
	}

	log.Printf("[Client] Stopping background tunnel (PID: %d)...\n", pid)
	_ = proc.Signal(syscall.SIGINT)

	for i := 0; i < 10; i++ {
		time.Sleep(200 * time.Millisecond)
		if !isPIDRunning(pid) {
			break
		}
	}

	if isPIDRunning(pid) {
		log.Println("[Client] Process did not respond to SIGINT. Force terminating...")
		_ = proc.Kill()
	}
	pidPath, _ := getPIDFilePath()
	_ = os.Remove(pidPath)
	log.Println("[Client] Tunnel stopped.")
}

func handleStatus() {
	pid, err := readPID()
	if err != nil || pid <= 0 {
		log.Println("[Client] No background tunnel is active.")
		return
	}
	if isPIDRunning(pid) {
		log.Printf("[Client] Background tunnel is active (PID: %d).\n", pid)
		home, _ := os.UserHomeDir()
		log.Printf("[Client] Logs: %s\n", filepath.Join(home, ".lfr-tunnel", "client.log"))
	} else {
		log.Println("[Client] No background tunnel is active (found stale PID file).")
		pidPath, _ := getPIDFilePath()
		_ = os.Remove(pidPath)
	}
}
