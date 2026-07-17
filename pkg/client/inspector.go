package client

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"lfr-tunnel/pkg/config"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed dashboard.html
var DashboardHTML []byte

//go:embed favicon-light.svg
var FaviconSVG []byte

func GetEmbeddedFaviconSVG() []byte {
	return FaviconSVG
}

// StartInspector starts the local web dashboard for the given engine.
// If the requested port is in use, it will auto-increment up to 10 times to find a free port.
func StartInspector(port int, engine *InterceptorEngine) (int, error) {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/settings" && r.URL.Path != "/logs" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(DashboardHTML)
	})

	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(FaviconSVG)
	})

	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			engine.mu.Lock()
			engine.History = make([]*RequestRecord, 0)
			engine.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}

		engine.mu.RLock()
		defer engine.mu.RUnlock()

		state := map[string]interface{}{
			"maintenance_mode":    engine.MaintenanceMode,
			"added_headers":       engine.AddedHeaders,
			"history":             engine.History,
			"passcode":            engine.Passcode,
			"whitelist_ips":       engine.WhitelistIPs,
			"access_mode":         engine.AccessMode,
			"assigned":            engine.SubdomainAss,
			"public_urls":         engine.PublicURLs,
			"language_preference": engine.LanguagePreference,
			"theme_preference":    engine.ThemePreference,
			"preserve_host":       engine.PreserveHost,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state)
	})

	mux.HandleFunc("/api/healthz", func(w http.ResponseWriter, r *http.Request) {
		engine.mu.RLock()
		isWSConnected := engine.ConnState == "connected"
		isAuthValid := engine.AuthValid
		isLeased := engine.SubdomainLeased
		targetHost := engine.TargetHost
		targetPort := engine.DestPort
		engine.mu.RUnlock()

		// Perform real-time TCP dial check to local downstream target
		var destResponsive bool
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", targetHost, targetPort), 500*time.Millisecond)
		if err == nil {
			destResponsive = true
			_ = conn.Close()
		}

		w.Header().Set("Content-Type", "application/json")
		if isWSConnected && isAuthValid && isLeased && destResponsive {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"healthy"}`))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"unhealthy"}`))
		}
	})

	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		engine.mu.RLock()
		defer engine.mu.RUnlock()

		// Calculate uptime seconds
		var uptimeSeconds int64
		if !engine.UptimeStart.IsZero() && engine.ConnState == "connected" {
			uptimeSeconds = int64(time.Since(engine.UptimeStart).Seconds())
		}

		// Calculate average latency
		var avgLatency int64
		if len(engine.LatencyHistory) > 0 {
			var sum int64
			for _, lat := range engine.LatencyHistory {
				sum += lat
			}
			avgLatency = sum / int64(len(engine.LatencyHistory))
		} else {
			avgLatency = engine.LatencyLast
		}

		// Dial test target responsiveness
		var destResponsive bool
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", engine.TargetHost, engine.DestPort), 500*time.Millisecond)
		if err == nil {
			destResponsive = true
			_ = conn.Close()
		}

		status := "healthy"
		if engine.ConnState != "connected" || !engine.AuthValid || !engine.SubdomainLeased || !destResponsive {
			status = "unhealthy"
		}

		var authErrMsg interface{}
		if engine.AuthErrorMessage != "" {
			authErrMsg = engine.AuthErrorMessage
		}

		home, _ := os.UserHomeDir()
		logFile := ""
		if engine.ClientSubdomain != "" {
			logFile = fmt.Sprintf("%s/.lfr-tunnel/client-%s.log", home, engine.ClientSubdomain)
		}

		info := map[string]interface{}{
			"status":           status,
			"version":          config.Version,
			"client_version":   config.Version,
			"server_version":   engine.ServerVersion,
			"server_url":       engine.ServerURL,
			"client_subdomain": engine.ClientSubdomain,
			"log_file":         logFile,
			"connection": map[string]interface{}{
				"state":          engine.ConnState,
				"uptime_seconds": uptimeSeconds,
				"latency_ms": map[string]interface{}{
					"last":   engine.LatencyLast,
					"avg_5m": avgLatency,
				},
				"reconnect_count": engine.ReconnectCount,
			},
			"auth": map[string]interface{}{
				"valid":         engine.AuthValid,
				"error_message": authErrMsg,
			},
			"subdomain": map[string]interface{}{
				"requested":   engine.SubdomainReq,
				"assigned":    engine.SubdomainAss,
				"leased":      engine.SubdomainLeased,
				"conflict":    engine.SubdomainConflict,
				"public_urls": engine.PublicURLs,
			},
			"destination": map[string]interface{}{
				"host":       engine.TargetHost,
				"port":       engine.DestPort,
				"responsive": destResponsive,
			},
			"traffic": map[string]interface{}{
				"requests_total": engine.RequestsTotal,
				"bytes_in":       engine.BytesIn,
				"bytes_out":      engine.BytesOut,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	})

	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		home, err := os.UserHomeDir()
		if err != nil || engine.ClientSubdomain == "" {
			http.Error(w, "Log file not found", http.StatusNotFound)
			return
		}
		logFile := filepath.Join(home, ".lfr-tunnel", fmt.Sprintf("client-%s.log", engine.ClientSubdomain))

		data, err := os.ReadFile(logFile)
		if err != nil {
			http.Error(w, "Failed to read log file", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(data)
	})

	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodGet {
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
				"server_url":           cfg.ServerURL,
				"auth_token":           maskToken(cfg.AuthToken),
				"target_host":          cfg.TargetHost,
				"dest_port":            destPort,
				"subdomain":            cfg.Subdomain,
				"preserve_host":        cfg.PreserveHost,
				"insecure_skip_verify": cfg.InsecureSkipVerify,
				"passcode":             cfg.Passcode,
				"rate_limit":           cfg.RateLimit,
				"maintenance_path":     cfg.MaintenancePath,
				"nav_placement":        cfg.NavPlacement,
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if r.Method == http.MethodPost {
			var req struct {
				ServerURL          string `json:"server_url"`
				AuthToken          string `json:"auth_token"`
				TargetHost         string `json:"target_host"`
				DestPort           int    `json:"dest_port"`
				Subdomain          string `json:"subdomain"`
				PreserveHost       bool   `json:"preserve_host"`
				InsecureSkipVerify bool   `json:"insecure_skip_verify"`
				Passcode           string `json:"passcode"`
				RateLimit          int    `json:"rate_limit"`
				MaintenancePath    string `json:"maintenance_path"`
				NavPlacement       string `json:"nav_placement"`
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
			cfg.InsecureSkipVerify = req.InsecureSkipVerify
			cfg.Passcode = req.Passcode
			cfg.RateLimit = req.RateLimit
			cfg.MaintenancePath = req.MaintenancePath
			cfg.NavPlacement = req.NavPlacement
			
			engine.MaintenancePath = req.MaintenancePath
			engine.NavPlacement = req.NavPlacement

			err = config.SaveClientConfig("", cfg)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save configuration: " + err.Error()})
				return
			}

			_, _ = w.Write([]byte(`{"status":"saved"}`))
			return
		}

		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	})

	mux.HandleFunc("/api/access-control", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Passcode     string `json:"passcode"`
			WhitelistIPs string `json:"whitelist_ips"`
			AccessMode   string `json:"access_mode"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
			return
		}

		engine.mu.Lock()
		engine.Passcode = req.Passcode
		engine.WhitelistIPs = req.WhitelistIPs
		engine.AccessMode = req.AccessMode
		token := engine.Token
		serverURL := engine.ServerURL
		subdomainAss := engine.SubdomainAss
		engine.mu.Unlock()

		if token == "" || serverURL == "" || subdomainAss == "" {
			http.Error(w, "Client connection state is not fully initialized", http.StatusBadRequest)
			return
		}

		parts := strings.SplitN(subdomainAss, ".", 2)
		if len(parts) != 2 {
			http.Error(w, "Invalid assigned subdomain format", http.StatusBadRequest)
			return
		}
		prefix := parts[0]
		domain := parts[1]

		updatePayload := map[string]string{
			"subdomain":     prefix,
			"domain":        domain,
			"passcode":      req.Passcode,
			"whitelist_ips": req.WhitelistIPs,
			"access_mode":   req.AccessMode,
		}

		bodyBytes, _ := json.Marshal(updatePayload)
		gatewayURL := fmt.Sprintf("%s/api/portal/reservations/access-control", serverURL)

		reqHTTP, err := http.NewRequest(http.MethodPost, gatewayURL, bytes.NewReader(bodyBytes))
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to construct gateway request: %v", err), http.StatusInternalServerError)
			return
		}

		reqHTTP.Header.Set("Content-Type", "application/json")
		reqHTTP.Header.Set("X-Auth-Token", token)

		clientHTTP := &http.Client{Timeout: 5 * time.Second}
		resp, err := clientHTTP.Do(reqHTTP)
		if err != nil {
			http.Error(w, fmt.Sprintf("Gateway communication error: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close() //nolint:errcheck

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			http.Error(w, fmt.Sprintf("Gateway rejected update (HTTP %d): %s", resp.StatusCode, string(respBody)), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	mux.HandleFunc("/api/maintenance", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		engine.mu.Lock()
		engine.MaintenanceMode = req.Enabled
		engine.mu.Unlock()

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok"}`) //nolint:errcheck
	})

	mux.HandleFunc("/api/replay", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		engine.mu.RLock()
		var record *RequestRecord
		for _, rec := range engine.History {
			if rec.ID == req.ID {
				record = rec
				break
			}
		}
		engine.mu.RUnlock()

		if record == nil {
			http.Error(w, "Request not found", http.StatusNotFound)
			return
		}

		// Replay the request to local service
		newRec, err := ReplayRequest(engine.TargetHost, record)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		engine.AddRecord(newRec)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"new_id": newRec.ID,
		})
	})

	bindIP := "127.0.0.1"
	if envBind := os.Getenv("LFT_INSPECTOR_BIND"); envBind != "" {
		bindIP = envBind
	} else if IsDocker() {
		bindIP = "0.0.0.0"
	}

	var listener net.Listener
	var err error
	actualPort := port

	for i := 0; i < 10; i++ {
		addr := fmt.Sprintf("%s:%d", bindIP, actualPort)
		listener, err = net.Listen("tcp", addr)
		if err == nil {
			break
		}
		if strings.Contains(err.Error(), "address already in use") {
			actualPort++
			continue
		}
		return 0, fmt.Errorf("failed to bind inspector on %s: %w", addr, err)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to find free inspector port starting from %d: %w", port, err)
	}

	go func() {
		slog.Info(fmt.Sprintf("[Inspector] Local Dashboard running at http://%s:%d\n", bindIP, actualPort))
		if err := http.Serve(listener, mux); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			slog.Info(fmt.Sprintf("[Inspector] Failed to serve: %v", err))
		}
	}()

	return actualPort, nil
}

// IsDocker checks if the application is running inside a Docker container.
func IsDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	// Check cgroups for container signatures
	for _, path := range []string{"/proc/1/cgroup", "/proc/self/cgroup"} {
		data, err := os.ReadFile(path)
		if err == nil {
			content := string(data)
			if strings.Contains(content, "docker") ||
				strings.Contains(content, "containerd") ||
				strings.Contains(content, "kubepods") {
				return true
			}
		}
	}
	return false
}

// ReplayRequest handles copying and re-sending a recorded request to the target local host/port.
func ReplayRequest(targetHost string, record *RequestRecord) (*RequestRecord, error) {
	if targetHost == "" {
		targetHost = "127.0.0.1"
	}
	targetURL := fmt.Sprintf("http://%s:%d%s", targetHost, record.TargetPort, record.Path)

	startTime := time.Now()

	var reqBodyReader io.Reader
	if record.ReqBody != "" {
		reqBodyReader = strings.NewReader(record.ReqBody)
	}

	req, err := http.NewRequest(record.Method, targetURL, reqBodyReader)
	if err != nil {
		return nil, err
	}

	// Copy headers
	for k, v := range record.ReqHeaders {
		req.Header.Set(k, v)
	}

	// Execute the request
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	res, err := client.Do(req)

	rec := &RequestRecord{
		ID:         fmt.Sprintf("%d", time.Now().UnixNano()),
		Time:       startTime,
		Method:     record.Method + " (Replay)",
		Path:       record.Path,
		ReqHeaders: record.ReqHeaders,
		ReqBody:    record.ReqBody,
		TargetPort: record.TargetPort,
	}

	if err != nil {
		rec.Status = 502
		rec.RespBody = fmt.Sprintf("Replay connection error: %v", err)
		rec.DurationMs = time.Since(startTime).Milliseconds()
		return rec, nil
	}
	defer res.Body.Close() //nolint:errcheck

	rec.Status = res.StatusCode
	rec.DurationMs = time.Since(startTime).Milliseconds()

	// Capture response headers
	respHeaders := make(map[string]string)
	for k, v := range res.Header {
		respHeaders[k] = strings.Join(v, ", ")
	}
	rec.RespHeaders = respHeaders

	// Capture response body (up to 10KB)
	var bodyBuf bytes.Buffer
	limitReader := io.LimitReader(res.Body, 10240)
	_, _ = io.Copy(&bodyBuf, limitReader)
	rec.RespBody = bodyBuf.String()

	return rec, nil
}
