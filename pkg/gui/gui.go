package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net"
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

const (
	osDarwin  = "darwin"
	osWindows = "windows"
	osLinux   = "linux"
)

// TempSettingsServer handles serving /settings when the tunnel is offline.
//
// It records the port it ACTUALLY bound rather than assuming the one it was asked for, the same
// way the Inspector does (client.InterceptorEngine.SetInspectorPort / InspectorPort). A fixed
// port was the root of #2055: a second tray instance made the bind fail, the failure vanished
// into a goroutine log, the object still looked started, and the menu went on linking to the
// port nothing was serving.
type TempSettingsServer struct {
	server   *http.Server
	listener net.Listener
	port     int // preferred; 0 means "let the OS choose"
	bound    int // what we actually got, 0 when not running
	mu       sync.Mutex
}

func NewTempSettingsServer(port int) *TempSettingsServer {
	return &TempSettingsServer{port: port}
}

func (s *TempSettingsServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server != nil {
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/favicon.ico", s.handleFavicon)

	mux.HandleFunc("/logs", s.handleLogs)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/info", s.handleInfo)
	mux.HandleFunc("/api/logs", s.handleApiLogs)

	// Bind BEFORE starting the goroutine, so a failure is returned to the caller instead of
	// being logged from a goroutine that nobody is watching (#2055).
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		// The preferred port is taken -- in practice a second tray instance. Take whatever the
		// OS will give us rather than going without a settings UI: the page is served from
		// whichever port we end up on, and nothing hardcodes it. dashboard.html fetches only
		// relative URLs, and the menu asks us for the URL rather than constructing one.
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("settings server could not bind any loopback port: %w", err)
		}
	}

	s.listener = ln
	if addr, ok := ln.Addr().(*net.TCPAddr); ok {
		s.bound = addr.Port
	}
	s.server = &http.Server{
		Handler: mux,
		// Header reading only (#1372). This is a loopback settings UI, so nothing it serves
		// needs an unbounded header read, and the other three timeouts are left unset for the
		// same reason as elsewhere: they would bound whole requests, not just the exposure.
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Captured locally: Stop() sets s.server to nil, and a goroutine still reading the field
	// then calls Serve on a nil *http.Server. The old code had the same shape and never showed
	// it, because nothing stopped the server quickly enough to lose the race.
	srv := s.server
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("TempSettingsServer stopped serving", "error", err)
		}
	}()
	return nil
}

// Port reports the port the settings server actually bound, or 0 when it is not running.
// Mirrors client.InterceptorEngine.InspectorPort, and exists for the same reason: a literal
// port is wrong the moment the requested one is unavailable.
func (s *TempSettingsServer) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server == nil {
		return 0
	}
	return s.bound
}

// IsRunning reports whether the settings UI is actually reachable. The menu gates on this, so
// "Settings..." can be disabled rather than opening a browser at a dead port.
func (s *TempSettingsServer) IsRunning() bool {
	return s.Port() != 0
}

func (s *TempSettingsServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/settings" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(client.DashboardHTML); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
}

func (s *TempSettingsServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(client.DashboardHTML); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
}

// loadUIConfig loads the client config for the settings server's handlers. It never
// returns a nil config, and it never discards the error.
//
// LoadClientConfig is nil-on-error on every path -- an unopenable file, unparseable YAML,
// and an unreadable token_file alike (#1777). So the fallback below is the only thing
// standing between a config file that will not load and four handlers that dereference the
// result; it is load-bearing, not belt-and-braces.
//
// It used to return the defaults alongside the error on the token_file branch alone (#1758),
// under a comment saying that stopped this package panicking on a nil. It did not: the two
// commoner failures still returned nil, and unparseable YAML is the one that was actually
// reported. Normalising here rather than in pkg/config is what let that accommodation be
// removed, instead of pkg/config having to remember which of its callers cannot cope with a
// nil on every future error it learns to return (#1771).
//
// Falling back to the defaults rather than failing is deliberate. This server only runs
// while the tunnel is offline, and its /settings page is the only in-app way to repair a
// broken config; refusing to serve it would take the repair tool away at exactly the
// moment it is needed. A config that is already broken at launch never reaches here at
// all -- cmd/lfr-tunnel exits on it before StartGUI -- so the case this handles is a file
// edited while the tray is running, which is the user mid-repair. The error is logged and
// returned so each handler can say why the values on screen are not the ones in the file.
func loadUIConfig() (*config.ClientConfig, error) {
	cfg, err := config.LoadClientConfig("")
	if err != nil {
		slog.Error("Failed to load client config; falling back to defaults",
			"path", config.ResolveDefaultConfigPath(), "error", err)
	}
	if cfg == nil {
		cfg = config.DefaultClientConfig()
	}
	return cfg, err
}

func (s *TempSettingsServer) handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	cfg, cfgErr := loadUIConfig()
	const unknownStr = "unknown"
	sub := unknownStr
	url := unknownStr
	if cfgErr == nil {
		sub = cfg.Subdomain
		url = cfg.ServerURL
	}
	home, _ := os.UserHomeDir()
	logFile := filepath.Join(home, ".lfr-tunnel", fmt.Sprintf("client-%s.log", sub))

	serverVer := "n/a"
	if url != unknownStr {
		client := &http.Client{Timeout: 2 * time.Second}
		if res, err := client.Get(fmt.Sprintf("%s/api/version", url)); err == nil {
			defer func() {
				_ = res.Body.Close() //nolint:errcheck
			}()
			var vResp struct {
				Version string `json:"server_version"`
			}
			if json.NewDecoder(res.Body).Decode(&vResp) == nil && vResp.Version != "" {
				serverVer = vResp.Version
			}
		}
	}

	info := map[string]interface{}{
		"status":           "unhealthy",
		"version":          config.Version,
		"client_version":   config.Version,
		"server_version":   serverVer,
		"server_url":       url,
		"client_subdomain": sub,
		"log_file":         logFile,
	}
	if cfgErr != nil {
		// Reported rather than swallowed (#1771). Without it, a config file that does not
		// parse is indistinguishable from having no config file at all: every field just
		// reads "unknown", and nothing tells the user their typo is the reason.
		info["config_error"] = cfgErr.Error()
	}
	_ = json.NewEncoder(w).Encode(info) //nolint:errcheck
}

func (s *TempSettingsServer) handleApiLogs(w http.ResponseWriter, r *http.Request) {
	cfg, cfgErr := loadUIConfig()
	sub := cfg.Subdomain
	if sub == "" {
		// "You have not set a subdomain yet" and "your config file could not be read" are
		// different problems, and this used to report both as the first (#1771). The
		// dashboard renders a 404 here as "No log file yet -- the client has not
		// connected" (pkg/client/dashboard.html), which is an actively wrong explanation
		// for a YAML syntax error.
		if cfgErr != nil {
			http.Error(w, fmt.Sprintf("Failed to load client config: %v", cfgErr), http.StatusInternalServerError)
			return
		}
		http.Error(w, "Subdomain not configured", http.StatusNotFound)
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, "Home dir not found", http.StatusInternalServerError)
		return
	}
	logFile := filepath.Join(home, ".lfr-tunnel", fmt.Sprintf("client-%s.log", sub))
	data, err := os.ReadFile(logFile)
	if err != nil {
		http.Error(w, "Failed to read log file", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if _, err := w.Write(data); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
}

func (s *TempSettingsServer) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte(`{"maintenance_mode":false,"added_headers":{},"history":[],"public_urls":[]}`)); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
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
	cfg, cfgErr := loadUIConfig()
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
		"server_url":           cfg.ServerURL,
		"auth_token":           maskToken(cfg.AuthToken),
		"target_host":          cfg.TargetHost,
		"dest_port":            destPort,
		"subdomain":            cfg.Subdomain,
		"preserve_host":        cfg.PreserveHost,
		"insecure_skip_verify": cfg.InsecureSkipVerify,
		"passcode":             cfg.Passcode,
		"rate_limit":           cfg.RateLimit,
	}
	if cfgErr != nil {
		resp["config_error"] = cfgErr.Error()
	}
	_ = json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// settingsFormRequest is the Settings form's payload.
//
// EVERY field is a pointer, so an ABSENT one is distinguishable from one deliberately set empty
// (#1793, #1762, #2056). Omitting a field means "leave it alone", never "clear it".
//
// This handler and pkg/client/inspector.go's serve the SAME client.DashboardHTML page and must
// agree about what a partial save means. They have disagreed before: #1762 fixed the inspector
// and missed this one. Both are pointered in full now.
//
// A named type with methods rather than an anonymous struct inside the handler: the nil checks
// are the bulk of the decoding, and inlining them put handleConfigPost over the gocyclo ceiling
// at 27. They are behaviour of the payload, not of the HTTP handler.
type settingsFormRequest struct {
	ServerURL          *string `json:"server_url"`
	AuthToken          *string `json:"auth_token"`
	TargetHost         *string `json:"target_host"`
	DestPort           *int    `json:"dest_port"`
	Subdomain          *string `json:"subdomain"`
	PreserveHost       *bool   `json:"preserve_host"`
	InsecureSkipVerify *bool   `json:"insecure_skip_verify"`
	// Owned by the Access Control tab, which posts to /api/access-control; the Settings
	// tab has no control for them and stopped sending them in #1762.
	Passcode  *string `json:"passcode"`
	RateLimit *int    `json:"rate_limit"`
}

// anyFieldSet reports whether the caller named anything at all. A body naming no field is a
// malformed or truncated request, not a save (#2056) -- answering 200 to it is how the original
// defect stayed invisible.
func (q *settingsFormRequest) anyFieldSet() bool {
	return q.ServerURL != nil || q.AuthToken != nil || q.TargetHost != nil ||
		q.DestPort != nil || q.Subdomain != nil || q.PreserveHost != nil ||
		q.InsecureSkipVerify != nil || q.Passcode != nil || q.RateLimit != nil
}

// applyTo copies only the fields the caller actually sent onto cfg.
func (q *settingsFormRequest) applyTo(cfg *config.ClientConfig) {
	if q.ServerURL != nil {
		cfg.ServerURL = *q.ServerURL
	}
	// SetInlineAuthToken, not a bare assignment: a token whose provenance is not declared is
	// not written to the config file at all (#1772). The mask is what GET returns, so a form
	// round-tripping GET->POST sends it back; writing it would replace the real token with
	// eight asterisks.
	if q.AuthToken != nil && *q.AuthToken != "********" && *q.AuthToken != "" {
		cfg.SetInlineAuthToken(*q.AuthToken)
	}
	if q.TargetHost != nil {
		cfg.TargetHost = *q.TargetHost
	}
	if q.DestPort != nil {
		cfg.Ports = []int{*q.DestPort}
	}
	if q.Subdomain != nil {
		cfg.Subdomain = *q.Subdomain
	}
	if q.PreserveHost != nil {
		cfg.PreserveHost = *q.PreserveHost
	}
	if q.InsecureSkipVerify != nil {
		cfg.InsecureSkipVerify = *q.InsecureSkipVerify
	}
	if q.Passcode != nil {
		cfg.Passcode = *q.Passcode
	}
	if q.RateLimit != nil {
		cfg.RateLimit = *q.RateLimit
	}
}

func (s *TempSettingsServer) handleConfigPost(w http.ResponseWriter, r *http.Request) {
	var req settingsFormRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		if _, err := w.Write([]byte(`{"error":"Invalid request JSON"}`)); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
		return
	}

	if !req.anyFieldSet() {
		w.WriteHeader(http.StatusBadRequest)
		if _, err := w.Write([]byte(`{"error":"No settings were supplied"}`)); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
		return
	}

	cfg, cfgErr := loadUIConfig()
	if cfgErr != nil {
		// Only the fields the form actually sent are applied now (#2056), but a config that
		// would not parse starts from defaults, so everything it held is still lost. Still
		// better than refusing to save -- this form is the user's way out of a config that
		// will not parse -- but it should not happen silently.
		slog.Warn("Saving settings over a client config that could not be read",
			"error", cfgErr)
	}

	req.applyTo(cfg)

	if err := config.SaveClientConfig("", cfg); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}) //nolint:errcheck
		return
	}
	if _, err := w.Write([]byte(`{"status":"saved"}`)); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
}

func (s *TempSettingsServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = s.server.Shutdown(ctx) //nolint:errcheck
	s.server = nil
	s.listener = nil
	s.bound = 0
}

var tempServer = NewTempSettingsServer(55556)
var lockPath string

func acquireGUILock() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	lockPath = filepath.Join(home, ".lfr-tunnel", "gui.pid")

	// Read existing PID
	if data, err := os.ReadFile(lockPath); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			if client.IsPIDRunning(pid) {
				return false
			}
		}
	}

	// Write our PID
	// 0600: only this process and its successor read the lock (#1408).
	_ = os.WriteFile(lockPath, []byte(strconv.Itoa(os.Getpid())), 0o600) //nolint:errcheck
	return true
}

// StartGUI initializes and runs the system tray UI.
func StartGUI(cfg *config.ClientConfig) {
	if !acquireGUILock() {
		slog.Warn("Another instance of Liferay Tunnel GUI is already running. Exiting.")
		return
	}

	tray := systray.New()
	tray.SetIcon(IconInactive)
	tray.SetTooltip("Liferay Tunnel")

	_, _, isRunning := getRunningState(cfg.Subdomain)
	if !isRunning {
		if err := tempServer.Start(); err != nil {
			slog.Error("settings UI unavailable", "error", err)
		}
	}

	// Built HERE, on the main goroutine, before Run() takes the thread (#2062). The watcher
	// below only ever updates items in place, which systray dispatches to the main thread.
	state, _, _ := getRunningState(cfg.Subdomain)
	var initialURL string
	if isRunning && state != nil && len(state.PublicURLs) > 0 {
		initialURL = state.PublicURLs[0]
	}
	menu := buildMenu(tray, cfg, isRunning, initialURL)

	go func() {
		lastRunning := isRunning
		activeURL := initialURL

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
					if err := tempServer.Start(); err != nil {
						slog.Error("settings UI unavailable", "error", err)
					}
				}
				lastRunning = isRunningNow
				activeURL = urlStr
				menu.refresh(isRunningNow, urlStr)
			}
		}
	}()

	tray.Show()

	if err := tray.Run(); err != nil {
		slog.Error("systray runner failed", "error", err)
	}
}

// settingsURL returns where the settings UI is actually being served, or "" when it is not
// served anywhere.
//
// Two servers can host it and only one runs at a time: the Inspector while a tunnel is up, and
// TempSettingsServer while it is not. Both serve the same page, and the page fetches only
// relative URLs, so it works unchanged on either -- which is why this returns a URL rather than
// the caller hardcoding a port.
func settingsURL(cfg *config.ClientConfig) string {
	if state, _, running := getRunningState(cfg.Subdomain); running && state != nil && state.InspectorPort != 0 {
		return fmt.Sprintf("http://127.0.0.1:%d/settings", state.InspectorPort)
	}
	if port := tempServer.Port(); port != 0 {
		return fmt.Sprintf("http://127.0.0.1:%d/settings", port)
	}
	return ""
}

// trayMenu holds the MenuItem handles created when the menu is built.
//
// THE MENU IS BUILT ONCE, ON THE MAIN GOROUTINE, BEFORE tray.Run() (#2062). It used to be
// rebuilt and re-attached with tray.SetMenu from the watcher goroutine, which crashed the app:
//
//	SIGTRAP: trace trap / signal arrived during cgo execution
//	systray/internal.(*darwinTray).SetMenu -> msgSend    [goroutine 9]
//	systray/internal.(*darwinTray).Run                   [goroutine 1, locked to thread]
//
// AppKit requires UI mutation on the main thread. systray dispatches *item* updates there for
// you -- SetLabel/SetDisabled/SetChecked go through performSelectorOnMainThread -- but SetMenu
// and SetIcon call msgSend directly, so calling either off the main thread is a crash waiting
// for the race to go the wrong way.
//
// The rebuild pattern was correct when written (#481): systray v0.1.2's Menu.Add returned *Menu,
// so there were no item handles and rebuilding was the only way to change anything. v0.3.0
// (#1911, first shipped in v1.48.32) introduced *MenuItem and in-place updates and made the old
// pattern unsafe. This moves to the supported one.
type trayMenu struct {
	cfg *config.ClientConfig

	status      *systray.MenuItem
	toggle      *systray.MenuItem
	copyURL     *systray.MenuItem
	settings    *systray.MenuItem
	launchLogin *systray.MenuItem
}

// buildMenu constructs the menu and attaches it.
//
// MUST run on the main goroutine, before tray.Run() -- it calls SetMenu, which systray does not
// dispatch for you.
func buildMenu(tray *systray.SystemTray, cfg *config.ClientConfig, isRunning bool, activeURL string) *trayMenu {
	m := &trayMenu{cfg: cfg}
	menu := systray.NewMenu()

	m.status = menu.Add(statusLabel(isRunning, activeURL), func() {})
	menu.AddSeparator()

	m.toggle = menu.Add(toggleLabel(isRunning), func() {
		handleToggle(cfg)
	})

	// Always present, disabled when there is nothing to copy. It used to be added only while a
	// tunnel was up -- but that is a change to the menu's SHAPE, and changing the shape is what
	// needs the rebuild this type exists to avoid.
	m.copyURL = menu.Add("Copy Public URL", func() {
		// Read at click time rather than closing over the value: the item outlives any
		// particular URL now that it is not rebuilt per state change.
		if state, _, running := getRunningState(cfg.Subdomain); running && state != nil && len(state.PublicURLs) > 0 {
			handleCopyURLString(state.PublicURLs[0])
		}
	})

	menu.Add("Open Request Inspector", func() {
		handleOpenInspector(cfg)
	})

	menu.Add("Open Live Logs", func() {
		handleOpenLogs(cfg)
	})

	menu.Add("Copy Logs to Clipboard", func() {
		handleCopyLogsToClipboard(cfg)
	})

	// Whichever server is actually up serves the same page, so ask rather than assume (#2055).
	m.settings = menu.Add("Settings...", func() {
		if url := settingsURL(cfg); url != "" {
			openBrowser(url)
		}
	})

	m.launchLogin = menu.Add(launchOnLoginLabel(), func() {
		handleToggleLaunchOnLogin(m)
	})

	menu.AddSeparator()

	menu.Add("Quit", func() {
		go func() {
			tempServer.Stop()
			if lockPath != "" {
				_ = os.Remove(lockPath) //nolint:errcheck
			}
			if runtime.GOOS != "darwin" {
				tray.Remove()
			}
			os.Exit(0)
		}()
	})

	tray.SetMenu(menu)
	m.refresh(isRunning, activeURL)
	return m
}

// refresh updates the items in place. Safe from any goroutine: every call below is one of the
// item setters systray dispatches to the main thread for you.
//
// It deliberately does NOT touch the tray icon. tray.SetIcon is not dispatched either, so calling
// it from the watcher is the same crash this type removes, and systray v0.3.0 offers no queued
// alternative -- pendingUpdates is a chan menuItemSnapshot, menu items only. The connection state
// is carried by the status line and the Connect/Disconnect label instead.
func (m *trayMenu) refresh(isRunning bool, activeURL string) {
	m.status.SetLabel(statusLabel(isRunning, activeURL))
	m.toggle.SetLabel(toggleLabel(isRunning))
	m.copyURL.SetDisabled(!isRunning || activeURL == "")
	m.settings.SetDisabled(settingsURL(m.cfg) == "")
	m.launchLogin.SetLabel(launchOnLoginLabel())
}

func statusLabel(isRunning bool, activeURL string) string {
	if !isRunning {
		return "Liferay Tunnel: Disconnected"
	}
	if activeURL != "" {
		return fmt.Sprintf("Connected: %s", activeURL)
	}
	return "Liferay Tunnel: Connected"
}

func toggleLabel(isRunning bool) string {
	if isRunning {
		return "Disconnect"
	}
	return "Connect"
}

func launchOnLoginLabel() string {
	if client.IsGUIServiceInstalled() {
		return "✓ Launch on Login"
	}
	return "Launch on Login"
}

func handleToggleLaunchOnLogin(m *trayMenu) {
	if client.IsGUIServiceInstalled() {
		if err := client.UninstallGUIService(); err != nil {
			slog.Error("Failed to uninstall GUI service", "error", err)
		}
	} else {
		if err := client.InstallGUIService(); err != nil {
			slog.Error("Failed to install GUI service", "error", err)
		}
	}
	// One label, in place. This used to call updateMenu -- rebuilding and re-attaching the whole
	// menu from a click handler. That runs on the main thread so it did not crash, but it is the
	// same unsafe pattern and there is no reason to keep it.
	m.launchLogin.SetLabel(launchOnLoginLabel())
}

// clientArgsForConnect is what the tray's Connect spawns the client with.
//
// It used to be exactly []string{"-background"}, discarding every flag the GUI itself was
// started with (#2074). So `lfr-tunnel -gui -prefer-region apac` launched a tray that knew the
// user wanted apac, and then connected a client that did not -- and an empty cfg.Region is the
// exact condition that hands the choice to the region cache (main.go:2320), so a cached election
// silently won. The user asked for apac by name and got eu, with nothing saying so.
//
// Everything the GUI was given is forwarded except the flags that describe how to RUN, which
// would be wrong or duplicated in the child: -gui (the child is not a tray) and -background
// (added once, below).
//
// Values are separate argv elements for every flag this binary defines, so filtering whole
// tokens cannot orphan one. -gui and -background are booleans, which Go's flag package never
// spells as "-flag value", so there is no paired value to lose.
func clientArgsForConnect(guiArgs []string) []string {
	out := make([]string, 0, len(guiArgs)+1)
	for _, a := range guiArgs {
		switch {
		case a == "-gui" || a == "--gui" ||
			strings.HasPrefix(a, "-gui=") || strings.HasPrefix(a, "--gui="):
			continue
		case a == "-background" || a == "--background" ||
			strings.HasPrefix(a, "-background=") || strings.HasPrefix(a, "--background="):
			continue // re-added below, so passing it twice cannot happen
		}
		out = append(out, a)
	}
	return append(out, "-background")
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
		_ = cmd.Run() //nolint:errcheck
	} else {
		// Everything the GUI was started with, so the tray honours what the user asked for
		// on the command line (#2074).
		cmd := exec.Command(execPath, clientArgsForConnect(os.Args[1:])...) //nolint:gosec
		_ = cmd.Start()                                                     //nolint:errcheck
	}
}

func handleCopyURLString(urlStr string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case osDarwin:
		cmd = exec.Command("pbcopy")
	case osWindows:
		cmd = exec.Command("clip")
	case osLinux:
		cmd = exec.Command("xclip", "-selection", "clipboard")
	default:
		return
	}
	cmd.Stdin = strings.NewReader(urlStr)
	_ = cmd.Run() //nolint:errcheck
}

// These two already asked the running client where it was listening. Only their fallback
// assumed the settings server had got the port it wanted, which stopped being true once that
// port became negotiable (#2055) -- and was never true when something else held it.
func handleOpenInspector(cfg *config.ClientConfig) {
	state, _, isRunning := getRunningState(cfg.Subdomain)
	if isRunning && state != nil && state.InspectorURL != "" {
		openBrowser(state.InspectorURL)
		return
	}
	if port := tempServer.Port(); port != 0 {
		openBrowser(fmt.Sprintf("http://127.0.0.1:%d", port))
	}
}

func handleOpenLogs(cfg *config.ClientConfig) {
	state, _, isRunning := getRunningState(cfg.Subdomain)
	if isRunning && state != nil && state.InspectorURL != "" {
		openBrowser(state.InspectorURL + "/logs")
		return
	}
	if port := tempServer.Port(); port != 0 {
		openBrowser(fmt.Sprintf("http://127.0.0.1:%d/logs", port))
	}
}

func handleCopyLogsToClipboard(cfg *config.ClientConfig) {
	_, sub, _ := getRunningState(cfg.Subdomain)
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Error("Failed to get home dir", "error", err)
		return
	}
	logFile := filepath.Join(home, ".lfr-tunnel", fmt.Sprintf("client-%s.log", sub))
	data, err := os.ReadFile(logFile)
	if err != nil {
		slog.Error("Failed to read log file", "error", err)
		return
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case osDarwin:
		cmd = exec.Command("pbcopy")
	case osWindows:
		cmd = exec.Command("clip")
	case osLinux:
		cmd = exec.Command("xclip", "-selection", "clipboard")
	default:
		slog.Error("Unsupported OS for clipboard copy")
		return
	}
	cmd.Stdin = strings.NewReader(string(data))
	if err := cmd.Run(); err != nil {
		slog.Error("Failed to copy to clipboard", "error", err)
	}
}

func openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case osLinux:
		err = exec.Command("xdg-open", url).Start()
	case osWindows:
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case osDarwin:
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

func (s *TempSettingsServer) handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if _, err := w.Write(client.GetEmbeddedFaviconSVG()); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
}
