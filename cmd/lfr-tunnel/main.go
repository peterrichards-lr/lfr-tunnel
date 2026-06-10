package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
)

func main() {
	configPath := flag.String("config", "", "Path to client-config.yaml")
	serverURL := flag.String("server", "", "Gateway server URL (e.g. https://tunnel.liferay.com)")
	token := flag.String("token", "", "Gateway auth token")
	subdomain := flag.String("subdomain", "", "Requested subdomain prefix (e.g. alpha-se)")
	portsStr := flag.String("ports", "", "Comma-separated ports to expose (e.g. 8080,3000)")
	background := flag.Bool("background", false, "Run client in background")
	status := flag.Bool("status", false, "Check status of the background tunnel")
	stop := flag.Bool("stop", false, "Stop the background tunnel")

	flag.Parse()

	if *stop {
		handleStop()
		return
	}

	if *status {
		handleStatus()
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

	// 5. Registration Handshake
	log.Println("[Client] Registering tunnel lease with gateway...")
	regResp, err := client.RegisterTunnel(cfg.ServerURL, cfg.AuthToken, sub, portMappings)
	if err != nil {
		log.Fatalf("[Client] Registration failed: %v", err)
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
	defer logFile.Close()

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
