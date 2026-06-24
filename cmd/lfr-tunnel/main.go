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
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"
	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/mcp"
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
	targetHost := flag.String("target-host", "", "Target hostname or IP to route traffic to (e.g. my-project.local)")
	preserveHost := flag.Bool("preserve-host", false, "Preserve incoming Host header instead of rewriting to target host")
	background := flag.Bool("background", false, "Run client in background")
	status := flag.Bool("status", false, "Check status of the background tunnel")
	statusJSON := flag.Bool("status-json", false, "Print JSON status of the background tunnel")
	stop := flag.Bool("stop", false, "Stop the background tunnel")
	versionFlag := flag.Bool("version", false, "Print client version")
	checkVersionFlag := flag.Bool("check-version", false, "Check server API for version requirements and print as JSON")
	upgradeFlag := flag.Bool("upgrade", false, "Self-upgrade client to the latest release")
	noTUI := flag.Bool("no-tui", false, "Disable interactive terminal dashboard UI")

	flag.Parse()

	if len(os.Args) > 1 && os.Args[1] == "install-service" {
		if err := client.InstallService(); err != nil {
			log.Fatalf("[Error] Failed to install service: %v", err)
		}
		return
	}

	if *versionFlag {
		fmt.Printf("lfr-tunnel version %s\n", config.Version)
		return
	}

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
	if *targetHost != "" {
		cfg.TargetHost = *targetHost
	}
	if *preserveHost {
		_ = os.Setenv("LFT_PRESERVE_HOST", "true")
	}

	if *upgradeFlag {
		if err := client.SelfUpgrade(config.Version, cfg.ServerURL); err != nil {
			log.Fatalf("[Error] Upgrade failed: %v", err)
		}
		return
	}

	// Determine if subdomain flag was explicitly passed
	subdomainFlagPassed := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "subdomain" {
			subdomainFlagPassed = true
		}
	})

	// Resolve subdomain prefix early (so background, stop, status checks know the subdomain name)
	sub := cfg.Subdomain
	if sub == "" {
		hostname, err := os.Hostname()
		if err == nil && hostname != "" {
			sub = strings.ToLower(hostname)
			if idx := strings.Index(sub, "."); idx != -1 {
				sub = sub[:idx]
			}
			sub = strings.ReplaceAll(sub, " ", "-")
			sub = strings.ReplaceAll(sub, "_", "-")
		} else {
			sub = "se-dev"
		}
	}
	sub = strings.ToLower(strings.TrimSpace(sub))

	if len(os.Args) > 1 && os.Args[1] == "login" {
		if err := client.RunLogin(cfg.ServerURL); err != nil {
			log.Fatalf("[Error] Login failed: %v", err)
		}
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		mcp.StartMCPServer()
		return
	}

	if *checkVersionFlag {
		info, err := client.CheckServerCompatibility(cfg.ServerURL)
		if err != nil {
			log.Fatalf("[Error] Failed to check server compatibility: %v", err)
		}
		b, _ := json.Marshal(info)
		fmt.Println(string(b))
		return
	}

	if *stop {
		handleStop(sub, subdomainFlagPassed)
		return
	}

	if *status {
		handleStatus(sub, subdomainFlagPassed)
		return
	}

	if *statusJSON {
		handleStatusJSON(sub, subdomainFlagPassed)
		return
	}

	if *background {
		handleBackground(sub)
		return
	}

	// Start compatibility check asynchronously
	compatChan := make(chan *client.ServerVersionInfo, 1)
	go func() {
		info, _ := client.CheckServerCompatibility(cfg.ServerURL)
		compatChan <- info
	}()

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
		if client.IsLiferayWorkspace(".") {
			log.Println("[Client] Liferay workspace detected. Scanning for Client Extensions...")
			portMappings, err = client.DetectWorkspacePorts(".")
			if err != nil {
				log.Printf("[Warning] Failed to scan workspace: %v. Using defaults.", err)
				portMappings = []client.PortMapping{{LocalPort: 8080}}
			}
		} else {
			log.Println("[Client] No Liferay workspace detected in current directory. Probing typical ports (8080, 13000, 3000)...")
			activePorts := client.ProbeLocalPorts([]int{8080, 13000, 3000})
			if len(activePorts) > 0 {
				log.Printf("[Client] Detected active local service ports: %v", activePorts)
				for idx, port := range activePorts {
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
				log.Println("[Client] No active local services found on typical ports. Defaulting to port 8080.")
				portMappings = []client.PortMapping{{LocalPort: 8080}}
			}
		}
	}

	// Copy original ports for status monitoring before we modify portMappings to point to Interceptor ports
	var originalPorts []int
	for _, pm := range portMappings {
		originalPorts = append(originalPorts, pm.LocalPort)
	}

	// Start Interceptor Engine
	engine := client.NewInterceptorEngine(cfg.TargetHost, addHeaders)
	actualInspectorPort, err := client.StartInspector(*inspectorPort, engine)
	if err != nil {
		log.Fatalf("[Error] Failed to start Inspector dashboard: %v", err)
	}
	*inspectorPort = actualInspectorPort

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
		if info != nil && config.Version != "dev" {
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
	fmt.Printf("[Client] Registering tunnel (%s) at %s...\n", sub, cfg.ServerURL)
	if cfg.RateLimit > 0 {
		fmt.Printf("[Client] Requested Subdomain Rate Limit: %d req/s\n", cfg.RateLimit)
	}
	if cfg.BasicAuth != "" {
		fmt.Printf("[Client] Data Plane HTTP Basic Auth is ENABLED\n")
	}
	clientOS := runtime.GOOS
	if client.IsDocker() {
		clientOS += " (Docker)"
	}
	regResp, err := client.RegisterTunnel(cfg.ServerURL, cfg.AuthToken, sub, portMappings, cfg.RateLimit, cfg.BasicAuth, engine.AddedHeaders, clientOS)
	if err != nil {
		if regErr, ok := err.(*client.RegistrationError); ok && regErr.StatusCode == 403 {
			log.Printf("[Error] Failed to register: %s\n", regErr.Message)
			portalURL := regErr.PortalURL
			if portalURL == "" {
				portalURL = strings.Replace(cfg.ServerURL, "tunnel.", "portal.", 1)
				if !strings.Contains(portalURL, "portal.") {
					portalURL = cfg.ServerURL + "/portal"
				}
			}
			log.Println("[Client] Subdomain reservation or limit issue detected.")
			log.Println("[Client] Please visit the User Portal to resolve it:")
			log.Printf("         👉 %s (Cmd/Ctrl+Click to open)\n", portalURL)
			os.Exit(1)
		}

		errStr := err.Error()
		isGatewayIssue := false
		if strings.Contains(errStr, "registration request failed") ||
			strings.Contains(errStr, "gateway error (5") ||
			strings.Contains(errStr, "gateway returned status 5") {
			isGatewayIssue = true
		}

		if isGatewayIssue {
			log.Printf("[Error] Failed to register: %v\n", err)
			log.Println("[Client] Gateway appears to be offline or undergoing maintenance.")
			log.Println("[Client] Check the service status page for active outages:")
			log.Println("         👉 https://status.lfr-demo.se (Cmd/Ctrl+Click to open)")
			os.Exit(1)
		} else {
			log.Fatalf("[Error] Failed to register: %v\n", err)
		}
	}

	if regResp.Warning != "" {
		log.Printf("\n[WARNING] %s\n\n", regResp.Warning)
	}

	// Modify portMappings to point to dynamic Interceptor ports
	portMap := make(map[int]int)
	for i, pm := range portMappings {
		targetPort := pm.LocalPort
		interceptPort, err := engine.InterceptPort(targetPort)
		if err != nil {
			log.Fatalf("[Error] Failed to start interceptor for port %d: %v", targetPort, err)
		}
		engine.StartHealthChecks(cfg.ServerURL, regResp.SessionToken, targetPort)
		portMappings[i].LocalPort = interceptPort
		portMap[targetPort] = interceptPort
	}

	// Rewrite remotes to route through interceptor ports
	for idx, remote := range regResp.Remotes {
		parts := strings.Split(remote, ":")
		if len(parts) >= 4 {
			lastPart := parts[len(parts)-1]
			if targetP, err := strconv.Atoi(lastPart); err == nil {
				if intP, exists := portMap[targetP]; exists {
					parts[len(parts)-1] = strconv.Itoa(intP)
					regResp.Remotes[idx] = strings.Join(parts, ":")
				}
			}
		}
	}

	var publicURLs []string
	log.Println("[Client] Registration successful! Your public tunnel URLs are:")
	subHost := sub
	if regResp.SubdomainPrefix != "" {
		subHost = regResp.SubdomainPrefix
	}
	for _, domain := range regResp.Domains {
		for _, pm := range portMappings {
			var fullSubdomain string
			if pm.NameSuffix == "" {
				fullSubdomain = subHost
			} else {
				fullSubdomain = fmt.Sprintf("%s-%s", subHost, pm.NameSuffix)
			}
			urlStr := fmt.Sprintf("https://%s.%s", fullSubdomain, domain)
			publicURLs = append(publicURLs, urlStr)
			log.Printf("  %s -> local port %d", urlStr, pm.LocalPort)
		}
	}

	// Write dynamic client state to file
	state := &client.ClientState{
		PID:           os.Getpid(),
		InspectorPort: *inspectorPort,
		InspectorURL:  fmt.Sprintf("http://127.0.0.1:%d", *inspectorPort),
		Subdomain:     subHost,
		PublicURLs:    publicURLs,
		Ports:         originalPorts,
		StartTime:     time.Now().Format(time.RFC3339),
	}
	if err := client.WriteState(subHost, state); err != nil {
		log.Printf("[Warning] Failed to write state file: %v\n", err)
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

	// Set lease status and subdomains info on engine
	engine.SetSubdomainDetails(sub, regResp.SubdomainPrefix, true, false)

	// Check if TUI is enabled and if stdout/stderr are terminals (not redirected, not backgrounded)
	tuiEnabled := !*noTUI && !*background && isatty.IsTerminal(os.Stdout.Fd()) && isatty.IsTerminal(os.Stderr.Fd())
	var cleanupTUI func()
	if tuiEnabled {
		cleanupTUI = client.StartTUIDashboard(ctx, engine, publicURLs)
	}

	err = client.RunClient(ctx, cfg.ServerURL, regResp.SessionToken, regResp.Remotes, publicURLs, engine)
	if cleanupTUI != nil {
		cleanupTUI()
	}
	client.DeleteState(subHost)
	if err != nil && ctx.Err() == nil {
		log.Fatalf("[Client] Tunnel disconnected with error: %v", err)
	}
	log.Println("[Client] Tunnel shutdown completed.")
}

func getPIDFilePath(subdomain string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".lfr-tunnel")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	safeSub := strings.ReplaceAll(subdomain, "/", "-")
	safeSub = strings.ReplaceAll(safeSub, "\\", "-")
	return filepath.Join(dir, fmt.Sprintf("lfr-tunnel-%s.pid", safeSub)), nil
}

func writePID(subdomain string, pid int) error {
	path, err := getPIDFilePath(subdomain)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0600)
}

func readPID(subdomain string) (int, error) {
	path, err := getPIDFilePath(subdomain)
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

func getActiveSubdomains() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".lfr-tunnel")
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var subs []string
	for _, f := range files {
		if !f.IsDir() && strings.HasPrefix(f.Name(), "lfr-tunnel-") && strings.HasSuffix(f.Name(), ".pid") {
			sub := strings.TrimPrefix(f.Name(), "lfr-tunnel-")
			sub = strings.TrimSuffix(sub, ".pid")
			subs = append(subs, sub)
		}
	}
	return subs, nil
}

func handleBackground(sub string) {
	pid, err := readPID(sub)
	if err == nil && pid > 0 && isPIDRunning(pid) {
		log.Fatalf("[Client] A background tunnel for subdomain '%s' is already running (PID: %d). Stop it first using: lfr-tunnel -stop -subdomain %s\n", sub, pid, sub)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("[Client] Failed to resolve home directory: %v\n", err)
	}
	logDir := filepath.Join(home, ".lfr-tunnel")
	_ = os.MkdirAll(logDir, 0700)
	logPath := filepath.Join(logDir, fmt.Sprintf("client-%s.log", sub))

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

	if err := writePID(sub, cmd.Process.Pid); err != nil {
		log.Printf("[Warning] Failed to write PID file: %v\n", err)
	}

	log.Printf("[Client] Tunnel started in background for subdomain '%s' (PID: %d).\n", sub, cmd.Process.Pid)
	log.Printf("[Client] Logs: %s\n", logPath)
	log.Printf("[Client] To stop this tunnel, run: lfr-tunnel -stop -subdomain %s\n", sub)
}

func handleStop(sub string, targetSpecific bool) {
	var subsToStop []string
	if targetSpecific {
		subsToStop = []string{sub}
	} else {
		var err error
		subsToStop, err = getActiveSubdomains()
		if err != nil {
			log.Fatalf("[Error] Failed to read active subdomains: %v\n", err)
		}
		if len(subsToStop) == 0 {
			log.Println("[Client] No active background tunnels found.")
			return
		}
	}

	for _, s := range subsToStop {
		pid, err := readPID(s)
		if err != nil || pid <= 0 {
			if targetSpecific {
				log.Printf("[Client] No background tunnel is active for subdomain '%s'.\n", s)
			}
			continue
		}
		if !isPIDRunning(pid) {
			log.Printf("[Client] Stale PID file found for subdomain '%s'. Process %d is not running. Cleaning up...\n", s, pid)
			pidPath, _ := getPIDFilePath(s)
			_ = os.Remove(pidPath)
			client.DeleteState(s)
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			log.Printf("[Warning] Failed to find process for subdomain '%s': %v\n", s, err)
			continue
		}

		log.Printf("[Client] Stopping background tunnel for subdomain '%s' (PID: %d)...\n", s, pid)
		_ = proc.Signal(syscall.SIGINT)

		for i := 0; i < 10; i++ {
			time.Sleep(200 * time.Millisecond)
			if !isPIDRunning(pid) {
				break
			}
		}

		if isPIDRunning(pid) {
			log.Printf("[Client] Process %d did not respond to SIGINT. Force terminating...\n", pid)
			_ = proc.Kill()
		}
		pidPath, _ := getPIDFilePath(s)
		_ = os.Remove(pidPath)
		client.DeleteState(s)
		log.Printf("[Client] Tunnel for subdomain '%s' stopped.\n", s)
	}
}

func handleStatus(sub string, targetSpecific bool) {
	var subsToCheck []string
	if targetSpecific {
		subsToCheck = []string{sub}
	} else {
		var err error
		subsToCheck, err = getActiveSubdomains()
		if err != nil {
			log.Fatalf("[Error] Failed to read active subdomains: %v\n", err)
		}
		if len(subsToCheck) == 0 {
			log.Println("[Client] No active background tunnels found.")
			return
		}
	}

	for _, s := range subsToCheck {
		pid, err := readPID(s)
		if err != nil || pid <= 0 {
			if targetSpecific {
				log.Printf("[Client] No background tunnel is active for subdomain '%s'.\n", s)
			}
			continue
		}
		if isPIDRunning(pid) {
			log.Printf("[Client] Background tunnel for subdomain '%s' is active (PID: %d).\n", s, pid)
			home, _ := os.UserHomeDir()
			log.Printf("[Client] Logs: %s\n", filepath.Join(home, ".lfr-tunnel", fmt.Sprintf("client-%s.log", s)))
		} else {
			log.Printf("[Client] No background tunnel is active for subdomain '%s' (found stale PID file). Cleaning up...\n", s)
			pidPath, _ := getPIDFilePath(s)
			_ = os.Remove(pidPath)
			client.DeleteState(s)
		}
	}
}

func handleStatusJSON(sub string, targetSpecific bool) {
	if targetSpecific {
		statePath, err := client.GetStateFilePath(sub)
		if err != nil {
			fmt.Println(`{"running":false}`)
			return
		}
		bytes, err := client.QueryStatusJSON(statePath, isPIDRunning)
		if err != nil {
			fmt.Println(`{"running":false}`)
			return
		}
		fmt.Println(string(bytes))
		return
	}

	// Global query: print aggregated status list
	subs, err := getActiveSubdomains()
	if err != nil || len(subs) == 0 {
		fmt.Println(`{"tunnels":[]}`)
		return
	}

	type Response struct {
		Tunnels []json.RawMessage `json:"tunnels"`
	}
	resp := Response{Tunnels: []json.RawMessage{}}

	for _, s := range subs {
		statePath, err := client.GetStateFilePath(s)
		if err != nil {
			continue
		}
		bytes, err := client.QueryStatusJSON(statePath, isPIDRunning)
		if err == nil {
			resp.Tunnels = append(resp.Tunnels, json.RawMessage(bytes))
		}
	}

	outputBytes, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		fmt.Println(`{"tunnels":[]}`)
		return
	}
	fmt.Println(string(outputBytes))
}
