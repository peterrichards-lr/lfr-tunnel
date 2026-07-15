package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gogpu/systray"
	"lfr-tunnel/pkg/client"
	"lfr-tunnel/pkg/config"
)

// TempSettingsServer handles serving /settings when the tunnel is offline.
type TempSettingsServer struct {
	server *http.Server
	port   int
	mu     sync.Mutex
}

func NewTempSettingsServer(port int) *TempSettingsServer {
	return &TempSettingsServer{port: port}
}

func (s *TempSettingsServer) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server != nil {
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/config", s.handleConfig)

	s.server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", s.port),
		Handler: mux,
	}

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("TempSettingsServer failed to listen", "error", err)
		}
	}()
}

func (s *TempSettingsServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/settings" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(client.InspectorHTML)
}

func (s *TempSettingsServer) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"maintenance_mode":false,"added_headers":{},"history":[],"public_urls":[]}`))
}

func (s *TempSettingsServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		s.handleConfigGet(w)
		return
	}
	if r.Method == http.MethodPost {
		s.handleConfigPost(w, r)
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *TempSettingsServer) handleConfigGet(w http.ResponseWriter) {
	cfg, err := config.LoadClientConfig("")
	if err != nil {
		cfg = config.DefaultClientConfig()
	}
	maskToken := func(t string) string {
		if t == "" {
			return ""
		}
		return "********"
	}
	destPort := 8080
	if len(cfg.Ports) > 0 {
		destPort = cfg.Ports[0]
	}
	resp := map[string]interface{}{
		"server_url":    cfg.ServerURL,
		"auth_token":    maskToken(cfg.AuthToken),
		"target_host":   cfg.TargetHost,
		"dest_port":     destPort,
		"subdomain":     cfg.Subdomain,
		"preserve_host": cfg.PreserveHost,
		"passcode":      cfg.Passcode,
		"rate_limit":    cfg.RateLimit,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *TempSettingsServer) handleConfigPost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerURL    string `json:"server_url"`
		AuthToken    string `json:"auth_token"`
		TargetHost   string `json:"target_host"`
		DestPort     int    `json:"dest_port"`
		Subdomain    string `json:"subdomain"`
		PreserveHost bool   `json:"preserve_host"`
		Passcode     string `json:"passcode"`
		RateLimit    int    `json:"rate_limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"Invalid request JSON"}`))
		return
	}

	cfg, err := config.LoadClientConfig("")
	if err != nil {
		cfg = config.DefaultClientConfig()
	}

	cfg.ServerURL = req.ServerURL
	if req.AuthToken != "********" && req.AuthToken != "" {
		cfg.AuthToken = req.AuthToken
	}
	cfg.TargetHost = req.TargetHost
	cfg.Ports = []int{req.DestPort}
	cfg.Subdomain = req.Subdomain
	cfg.PreserveHost = req.PreserveHost
	cfg.Passcode = req.Passcode
	cfg.RateLimit = req.RateLimit

	err = config.SaveClientConfig("", cfg)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_, _ = w.Write([]byte(`{"status":"saved"}`))
}

func (s *TempSettingsServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = s.server.Shutdown(ctx)
	s.server = nil
}

var tempServer = NewTempSettingsServer(55556)

// StartGUI initializes and runs the system tray UI.
func StartGUI(cfg *config.ClientConfig) {
	tray := systray.New()
	tray.SetIcon(IconInactive)
	tray.SetTooltip("Liferay Tunnel")

	_, _, isRunning := getRunningState(cfg.Subdomain)
	if !isRunning {
		tempServer.Start()
	}

	go func() {
		var lastRunning bool
		var activeURL string

		updateMenu(tray, cfg, isRunning, activeURL)

		for {
			time.Sleep(1 * time.Second)
			state, _, isRunningNow := getRunningState(cfg.Subdomain)

			var urlStr string
			if isRunningNow && state != nil && len(state.PublicURLs) > 0 {
				urlStr = state.PublicURLs[0]
			}

			if isRunningNow != lastRunning || urlStr != activeURL {
				if isRunningNow && !lastRunning {
					tempServer.Stop()
				} else if !isRunningNow && lastRunning {
					tempServer.Start()
				}
				lastRunning = isRunningNow
				activeURL = urlStr
				updateMenu(tray, cfg, isRunningNow, urlStr)
			}
		}
	}()

	tray.Show()

	if err := tray.Run(); err != nil {
		slog.Error("systray runner failed", "error", err)
	}
}

func updateMenu(tray *systray.SystemTray, cfg *config.ClientConfig, isRunning bool, activeURL string) {
	menu := systray.NewMenu()

	var statusText string
	if isRunning {
		if activeURL != "" {
			statusText = fmt.Sprintf("Connected: %s", activeURL)
		} else {
			statusText = "Liferay Tunnel: Connected"
		}
		tray.SetIcon(IconActive)
	} else {
		statusText = "Liferay Tunnel: Disconnected"
		tray.SetIcon(IconInactive)
	}

	menu.Add(statusText, func() {})
	menu.AddSeparator()

	var toggleText string
	if isRunning {
		toggleText = "Disconnect"
	} else {
		toggleText = "Connect"
	}
	menu.Add(toggleText, func() {
		handleToggle(cfg)
	})

	if isRunning && activeURL != "" {
		menu.Add("Copy Public URL", func() {
			handleCopyURLString(activeURL)
		})
	}

	menu.Add("Open Request Inspector", func() {
		handleOpenInspector(cfg)
	})

	menu.Add("Settings...", func() {
		openBrowser("http://127.0.0.1:55556/settings")
	})

	var startOnLoginText string
	if client.IsGUIServiceInstalled() {
		startOnLoginText = "✓ Launch on Login"
	} else {
		startOnLoginText = "Launch on Login"
	}
	menu.Add(startOnLoginText, func() {
		handleToggleLaunchOnLogin(tray, cfg, isRunning, activeURL)
	})

	menu.AddSeparator()

	menu.Add("Quit", func() {
		tray.Remove()
		tempServer.Stop()
		os.Exit(0)
	})

	tray.SetMenu(menu)
}

func handleToggleLaunchOnLogin(tray *systray.SystemTray, cfg *config.ClientConfig, isRunning bool, activeURL string) {
	if client.IsGUIServiceInstalled() {
		if err := client.UninstallGUIService(); err != nil {
			slog.Error("Failed to uninstall GUI service", "error", err)
		}
	} else {
		if err := client.InstallGUIService(); err != nil {
			slog.Error("Failed to install GUI service", "error", err)
		}
	}
	updateMenu(tray, cfg, isRunning, activeURL)
}

func handleToggle(cfg *config.ClientConfig) {
	_, sub, isRunning := getRunningState(cfg.Subdomain)

	execPath, err := os.Executable()
	if err != nil {
		slog.Error("Failed to resolve executable path", "error", err)
		return
	}

	if isRunning {
		cmd := exec.Command(execPath, "-stop", "-subdomain", sub)
		_ = cmd.Run()
	} else {
		cmd := exec.Command(execPath, "-background")
		_ = cmd.Start()
	}
}

func handleCopyURLString(urlStr string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "windows":
		cmd = exec.Command("clip")
	case "linux":
		cmd = exec.Command("xclip", "-selection", "clipboard")
	default:
		return
	}
	cmd.Stdin = strings.NewReader(urlStr)
	_ = cmd.Run()
}

func handleOpenInspector(cfg *config.ClientConfig) {
	state, _, isRunning := getRunningState(cfg.Subdomain)
	if isRunning && state != nil && state.InspectorURL != "" {
		openBrowser(state.InspectorURL)
	} else {
		openBrowser("http://127.0.0.1:55556")
	}
}

func openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}
	if err != nil {
		slog.Error("Failed to open browser", "error", err)
	}
}

func getFallbackSubdomain() string {
	hostname, err := os.Hostname()
	if err == nil && hostname != "" {
		sub := strings.ToLower(hostname)
		if idx := strings.Index(sub, "."); idx != -1 {
			sub = sub[:idx]
		}
		sub = strings.ReplaceAll(sub, " ", "-")
		sub = strings.ReplaceAll(sub, "_", "-")
		return sub
	}
	return "se-dev"
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

func checkSubdomainRunning(sub string) (*client.ClientState, bool) {
	pid, err := readPID(sub)
	if err != nil || pid <= 0 || !client.IsPIDRunning(pid) {
		return nil, false
	}
	statePath, err := client.GetStateFilePath(sub)
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, false
	}
	var state client.ClientState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, false
	}
	return &state, true
}

func getRunningState(configuredSub string) (*client.ClientState, string, bool) {
	sub := configuredSub
	if sub == "" {
		sub = getFallbackSubdomain()
	}

	if state, ok := checkSubdomainRunning(sub); ok {
		return state, sub, true
	}

	subs, err := getActiveSubdomains()
	if err == nil {
		for _, s := range subs {
			if state, ok := checkSubdomainRunning(s); ok {
				return state, s, true
			}
		}
	}

	return nil, sub, false
}
