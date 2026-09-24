package client

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"lfr-tunnel/pkg/config"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
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

// dashboardPortToken marks every place in dashboard.html that has to name the port the Inspector
// actually bound. It cannot be baked into the page or its translation bundles: StartInspector
// falls forward to the next port when the requested one is taken, so the page shipped inside the
// binary does not know the answer (#2190). Before this, the header read "Listening on
// localhost:4040" on every Inspector, including the one on 4041.
const dashboardPortToken = "__LFT_INSPECTOR_PORT__"

// RenderDashboardHTML returns the Inspector page with the port it really bound stamped in, which
// is the same number StartInspector returns and logs. Substituted server-side rather than read
// from location.port in the browser so that the page is correct as served -- the served bytes are
// what a test, a saved copy or a `curl` shows.
func RenderDashboardHTML(port int) []byte {
	return bytes.ReplaceAll(DashboardHTML, []byte(dashboardPortToken), []byte(strconv.Itoa(port)))
}

// StartInspector starts the local web dashboard for the given engine.
// If the requested port is in use, it will auto-increment up to 10 times to find a free port.
func StartInspector(port int, engine *InterceptorEngine) (int, error) {
	mux := http.NewServeMux()

	// The page with the real port stamped in. Assigned below, once the bind loop has settled on a
	// port -- which is why this is a closure variable rather than a value computed here: the routes
	// are registered before the listener exists. The assignment happens-before the `go srv.Serve`
	// that first admits a request, so no handler can observe it unset.
	var dashboardPage []byte

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/settings" && r.URL.Path != "/logs" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write(dashboardPage); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
	})

	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		if _, err := w.Write(FaviconSVG); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
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
		_ = json.NewEncoder(w).Encode(state) //nolint:errcheck
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
			_ = conn.Close() //nolint:errcheck
		}

		w.Header().Set("Content-Type", "application/json")
		if isWSConnected && isAuthValid && isLeased && destResponsive {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`{"status":"healthy"}`)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := w.Write([]byte(`{"status":"unhealthy"}`)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
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
			_ = conn.Close() //nolint:errcheck
		}

		status := "healthy"
		if engine.ConnState != "connected" || !engine.AuthValid || !engine.SubdomainLeased || !destResponsive {
			status = "unhealthy"
		}

		var authErrMsg interface{}
		if engine.AuthErrorMessage != "" {
			authErrMsg = engine.AuthErrorMessage
		}

		logFile := ""
		if engine.ClientSubdomain != "" {
			logFile, _ = ResolveClientLogPath(engine.ClientSubdomain) //nolint:errcheck
		}

		acEditable, acReason := accessControlEditability(
			engine.SubdomainAss, engine.PublicURLs,
			engine.controlPlaneReachable, engine.controlPlaneKnown,
		)

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
			"simulation": map[string]interface{}{
				"latency_ms":      engine.Latency.Milliseconds(),
				"rate_limit_kbps": engine.RateLimitKBPS,
			},
			"access_control": map[string]interface{}{
				"mode":             engine.AccessMode,
				"passcode":         engine.Passcode,
				"whitelist_ips":    engine.WhitelistIPs,
				"is_custom_domain": engine.IsCustomDomain,
				// Whether a save could succeed at all, decided by the SAME function the save
				// itself uses, so the form and the endpoint cannot disagree about it. A field
				// that can never be applied is disabled rather than left to fail on submit
				// (#2116) -- the rule the Settings tab already follows.
				"editable":        acEditable,
				"editable_reason": acReason,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info) //nolint:errcheck
	})

	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		if engine.ClientSubdomain == "" {
			http.Error(w, "Log file not found", http.StatusNotFound)
			return
		}
		logFile, err := ResolveClientLogPath(engine.ClientSubdomain)
		if err != nil {
			http.Error(w, "Log file not found", http.StatusNotFound)
			return
		}

		data, err := os.ReadFile(logFile)
		if err != nil {
			http.Error(w, "Failed to read log file", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if _, err := w.Write(data); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
	})

	// The other two logs #1129 added (#1423). Neither had a reader for as long as they
	// have existed, while /api/logs served the console log only.
	//
	// Serving them is consistent with what this listener already does: /api/state returns
	// the last 100 RequestRecords with ReqBody, RespBody and ReqHeaders -- Authorization
	// included -- plus the passcode, and /api/replay re-sends them. Everything here is
	// behind the same guardLocalOnly origin check. The traffic log is that same data over
	// a longer window, so withholding it protected nothing while costing the operator the
	// history they came for.
	//
	// Returned as a tail rather than whole files: these rotate at 8 MiB and the Logs tab
	// polls, so a full read would ship megabytes per poll for a view whose useful part is
	// the recent end.
	for _, kind := range []string{LogKindTraffic, LogKindError} {
		logKind := kind
		mux.HandleFunc("/api/logs/"+logKind, func(w http.ResponseWriter, r *http.Request) {
			engine.mu.RLock()
			subdomain := engine.ClientSubdomain
			engine.mu.RUnlock()
			if subdomain == "" {
				http.Error(w, "Log file not found", http.StatusNotFound)
				return
			}

			data, truncated, err := ReadSessionLog(logKind, subdomain, DefaultLogTailBytes)
			if err != nil {
				if os.IsNotExist(err) {
					// Distinct from a read failure: the file not existing yet is the
					// normal state of the traffic log when body logging has never run,
					// and the panel says so rather than reporting an error.
					http.Error(w, "Log file not found", http.StatusNotFound)
					return
				}
				http.Error(w, "Failed to read log file", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
			// Says the view is partial rather than letting a tail look like a whole file.
			if truncated {
				w.Header().Set("X-Log-Truncated", "true")
			}
			if _, err := w.Write(data); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
		})
	}

	// Restarting the client so saved settings take effect (#2088).
	//
	// Eight of the nine settings in the panel are read only at startup, so saving them changes
	// nothing about the session in front of the user. Telling them to do it by hand is a poor
	// answer when the process knows how.
	//
	// POST only: a restart is not something a link preview, a prefetch or a refresh should be
	// able to cause.
	mux.HandleFunc("/api/restart", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"status":"restarting"}`)); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
			return
		}
		// Flushed before the process goes away, or the browser sees a dropped connection and
		// reports a failure for a restart that is working exactly as asked.
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		// On its own goroutine, after a moment: RestartSelf replaces this process image, and
		// doing that inside the handler kills the connection mid-response.
		go func() {
			time.Sleep(250 * time.Millisecond)
			if err := RestartSelf(); err != nil {
				log.Printf("[Error] Restart failed, the client is still running with the old "+
					"settings: %v", err)
			}
		}()
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
				"log_dir":              cfg.LogDir,
			}
			// Also report where logs are being written right now. The saved value can be
			// blank (meaning "the default") or changed since this session started, and the
			// question the Inspector is actually asked is "where are my logs?" (#1223).
			if effective, derr := LogDir(); derr == nil {
				resp["log_dir_effective"] = effective
			}

			// Same reasoning, applied to the rest of the panel (#1211). Everything above is
			// the *saved* config, but a client started with flags or environment variables
			// is not using it -- run the client with -server and -subdomain and no config
			// file, and this panel showed two empty boxes while the process was happily
			// connected to a gateway. The Inspector is the diagnostic tool; someone asking
			// "am I on the right gateway?" is exactly who reads this, and it was exactly
			// what it would not tell them.
			//
			// Reported alongside the saved values rather than replacing them, because the
			// difference is the useful part: editing a field here writes the saved config,
			// which does not change what the running process is using.
			engine.mu.RLock()
			effSubdomain := engine.ClientSubdomain
			if effSubdomain == "" {
				effSubdomain = engine.SubdomainAss
			}
			if effSubdomain == "" {
				effSubdomain = engine.SubdomainReq
			}
			resp["effective"] = map[string]interface{}{
				"server_url":    engine.ServerURL,
				"auth_token":    maskToken(engine.Token),
				"target_host":   engine.TargetHost,
				"dest_port":     engine.DestPort,
				"subdomain":     effSubdomain,
				"preserve_host": engine.PreserveHost,
			}
			// Which settings were claimed at launch, and by what (#2088). The panel renders
			// these read-only: a restart reuses the same argv, so a flag or exported variable
			// takes the field again every time and an editable box would be a lie.
			overrides := make(map[string]string, len(engine.LaunchOverrides))
			for k, v := range engine.LaunchOverrides {
				overrides[k] = v
			}
			resp["launch_overrides"] = overrides
			// A running client can be restarted from here; the settings server the GUI runs
			// when nothing is connected has no process to restart, and says so.
			resp["can_restart"] = true
			engine.mu.RUnlock()

			// All three logs the client writes, with their resolved paths (#1423).
			// #1129 added the traffic and error logs and nothing ever surfaced them;
			// #1223 then added a Settings field naming the directory, so the operator
			// was told where to look and found two files the tool never mentioned.
			//
			// Paths as well as the viewers at /api/logs/traffic and /api/logs/error.
			// An earlier revision withheld the contents, arguing the traffic log's
			// bodies were too sensitive for this listener. That was wrong: /api/state
			// above already returns ReqBody, RespBody, ReqHeaders and the passcode for
			// the last 100 requests, and /api/replay re-sends them. The paths are still
			// worth reporting for someone who wants to grep or ship the file rather
			// than read it here.
			//
			// Resolved outside the lock: SessionLogPaths stats the filesystem, and
			// engine.mu guards in-memory state that request handling also reads.
			if logs := SessionLogPaths(effSubdomain); len(logs) > 0 {
				resp["logs"] = logs
			}
			_ = json.NewEncoder(w).Encode(resp) //nolint:errcheck
			return
		}

		if r.Method == http.MethodPost {
			// EVERY field is a pointer, so an ABSENT one is distinguishable from one
			// deliberately set empty (#1762, #2056). Omitting a field means "leave it alone",
			// never "clear it".
			//
			// This has now caught three sets of fields in this one struct, each found the same
			// way -- by something being silently wiped. AuthToken was guarded first;
			// Passcode/RateLimit were, in the words of the comment this replaces, "simply
			// missed" and fixed in #1762; the plain-value remainder was missed again and wiped
			// a live client's server_url, subdomain, target_host, ports and preserve_host in
			// #2056. Pointering the whole struct ends the class rather than waiting for the
			// next field to burn someone.
			var req struct {
				ServerURL          *string `json:"server_url"`
				AuthToken          *string `json:"auth_token"`
				TargetHost         *string `json:"target_host"`
				DestPort           *int    `json:"dest_port"`
				Subdomain          *string `json:"subdomain"`
				PreserveHost       *bool   `json:"preserve_host"`
				InsecureSkipVerify *bool   `json:"insecure_skip_verify"`
				Passcode           *string `json:"passcode"`
				RateLimit          *int    `json:"rate_limit"`
				MaintenancePath    *string `json:"maintenance_path"`
				LogDir             *string `json:"log_dir"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				if _, err := w.Write([]byte(`{"error":"Invalid request JSON"}`)); err != nil {
					log.Printf("[Warning] Failed to write response: %v", err)
				}
				return
			}

			// A body that names no field at all is not a save -- it is a malformed or
			// truncated request, and treating it as "write the config unchanged" would be
			// generous about something the caller plainly did not mean. Rejecting it also
			// makes the `{}` case that found #2056 loud instead of a 200.
			if req.ServerURL == nil && req.AuthToken == nil && req.TargetHost == nil &&
				req.DestPort == nil && req.Subdomain == nil && req.PreserveHost == nil &&
				req.InsecureSkipVerify == nil && req.Passcode == nil && req.RateLimit == nil &&
				req.MaintenancePath == nil && req.LogDir == nil {
				w.WriteHeader(http.StatusBadRequest)
				if _, err := w.Write([]byte(`{"error":"No settings were supplied"}`)); err != nil {
					log.Printf("[Warning] Failed to write response: %v", err)
				}
				return
			}

			cfg, err := config.LoadClientConfig("")
			if err != nil {
				cfg = config.DefaultClientConfig()
			}

			if req.ServerURL != nil {
				cfg.ServerURL = *req.ServerURL
			}
			// SetInlineAuthToken, not a bare assignment: a token whose provenance is
			// not declared is not written to the config file at all (#1772). The mask is
			// what GET returns, so a form that round-trips GET->POST sends it back; writing
			// it would replace the real token with eight asterisks.
			if req.AuthToken != nil && *req.AuthToken != "********" && *req.AuthToken != "" {
				cfg.SetInlineAuthToken(*req.AuthToken)
			}
			if req.TargetHost != nil {
				cfg.TargetHost = *req.TargetHost
			}
			if req.DestPort != nil {
				cfg.Ports = []int{*req.DestPort}
			}
			if req.Subdomain != nil {
				cfg.Subdomain = *req.Subdomain
			}
			if req.PreserveHost != nil {
				cfg.PreserveHost = *req.PreserveHost
			}
			if req.InsecureSkipVerify != nil {
				cfg.InsecureSkipVerify = *req.InsecureSkipVerify
			}
			if req.Passcode != nil {
				cfg.Passcode = *req.Passcode
			}
			if req.RateLimit != nil {
				cfg.RateLimit = *req.RateLimit
			}
			if req.MaintenancePath != nil {
				cfg.MaintenancePath = *req.MaintenancePath
				engine.MaintenancePath = *req.MaintenancePath
			}
			// Saved for the next run only. The traffic and error logs are opened once at
			// startup, so a directory change cannot move an open file handle -- the
			// Settings tab says as much rather than implying it takes effect now (#1223).
			if req.LogDir != nil {
				cfg.LogDir = strings.TrimSpace(*req.LogDir)
			}

			err = config.SaveClientConfig("", cfg)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save configuration: " + err.Error()}) //nolint:errcheck
				return
			}

			if _, err := w.Write([]byte(`{"status":"saved"}`)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
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

		// Read only. The engine is updated after the gateway has accepted the change, not
		// before: writing first left it holding values the gateway had refused, and
		// registration sends them, so a rejected save applied itself at the next
		// re-registration while the dialog reported failure (#2116).
		engine.mu.RLock()
		token := engine.Token
		serverURL := engine.ServerURL
		centralURL := engine.centralURL
		subdomainAss := engine.SubdomainAss
		publicURLs := append([]string(nil), engine.PublicURLs...)
		engine.mu.RUnlock()

		if token == "" || serverURL == "" || subdomainAss == "" {
			http.Error(w, "Client connection state is not fully initialized", http.StatusBadRequest)
			return
		}

		prefix, domain, err := splitAssignedHost(subdomainAss, publicURLs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Sent as chosen. The gateway decides which factors the mode applies, so the values
		// are stored whether or not they are currently enforced -- which is what makes Public
		// mean "not applied" rather than "discarded", in the portal as well as here (#2098).
		updatePayload := map[string]string{
			"subdomain":     prefix,
			"domain":        domain,
			"passcode":      req.Passcode,
			"whitelist_ips": req.WhitelistIPs,
			"access_mode":   req.AccessMode,
		}

		bodyBytes, _ := json.Marshal(updatePayload)

		// Addressed to the CONTROL PLANE, never to the gateway serving this tunnel. Reservations
		// live in central's database and an edge has none, so validatePAT returns false on
		// `s.db == nil` before it even looks at the token -- every save from an edge-served
		// session was refused 401, whatever the mode (#2116). The client is told central's
		// address by the gateway and already pairs the two this way for status reports.
		controlPlaneURL := centralURL
		if controlPlaneURL == "" {
			controlPlaneURL = serverURL
		}
		gatewayURL := fmt.Sprintf("%s/api/portal/reservations/access-control", controlPlaneURL)

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

		// Accepted, so the engine may now hold it. Registration sends these, which is how the
		// change survives a reconnect.
		engine.mu.Lock()
		engine.Passcode = req.Passcode
		engine.WhitelistIPs = req.WhitelistIPs
		engine.AccessMode = req.AccessMode
		engine.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"status":"ok"}`)); err != nil {
			log.Printf("[Warning] Failed to write response: %v", err)
		}
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
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}) //nolint:errcheck
			return
		}

		engine.AddRecord(newRec)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
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

	// actualPort is final from here, so the page can be rendered once and served to everyone.
	dashboardPage = RenderDashboardHTML(actualPort)

	// The Host check only makes sense on a loopback bind; beyond that the legitimate
	// Host is whatever the container or operator mapped.
	loopbackOnly := isLoopbackHostname(bindIP)
	if !loopbackOnly {
		slog.Info(fmt.Sprintf("[Inspector] Warning: bound to %s, which is reachable beyond this machine. Cross-origin requests are still rejected, but anything that can reach this port can read captured traffic.", bindIP))
	}
	handler := noStoreByDefault(guardLocalOnly(mux, actualPort, loopbackOnly))

	// A configured server rather than http.Serve, which cannot set timeouts (#1372). Only
	// ReadHeaderTimeout is set: ReadTimeout and WriteTimeout would apply to whole requests, and
	// the interceptor below proxies tunnel traffic and WebSocket upgrades that legitimately
	// outlive any fixed bound. Header reading is not one of those.
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: readHeaderTimeout}

	go func() {
		slog.Info(fmt.Sprintf("[Inspector] Local Dashboard running at http://%s:%d\n", bindIP, actualPort))
		if err := srv.Serve(listener); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			slog.Info(fmt.Sprintf("[Inspector] Failed to serve: %v", err))
		}
	}()

	return actualPort, nil
}

// readHeaderTimeout bounds how long a caller may take to send request headers, on every
// listener this package opens (#1372). Loopback-bound, so the Slowloris exposure is smaller
// than the gateway's -- but http.Serve cannot set it at all, which is what gosec's G114 is
// about, and a local process holding goroutines open is still worth bounding.
const readHeaderTimeout = 10 * time.Second

// isLoopbackHostname reports whether a hostname refers to this machine.
func isLoopbackHostname(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// isOwnOrigin reports whether an Origin header names this Inspector.
func isOwnOrigin(origin string, port int) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	hostname := u.Hostname()
	if !isLoopbackHostname(hostname) {
		return false
	}
	return u.Port() == strconv.Itoa(port)
}

// noStoreByDefault gives every Inspector response freshness information, so that caching is
// something a route opts into rather than something it forgets to prevent (#2182).
//
// A response with no Cache-Control, no ETag and no Last-Modified is not "uncached" -- RFC 9111
// §4.2.2 lets a browser invent its own expiry for it. Every route here served exactly that, and
// the one it hurt was the dashboard HTML: it is embedded in the binary, so it changes on upgrade
// and never otherwise. After `lfr-tunnel -upgrade` a browser could keep showing the previous
// version's UI, which made a successful upgrade look like a failed one -- and the natural way to
// re-check is to reload the same cached page. A user on v1.48.50 reported #2155's Inspector
// change as missing on exactly this; the binary was right and the screen was wrong.
//
// Applied in front of the whole chain rather than per handler, because the defect is the class:
// the API routes return live state and the mux's own 404/405 responses are heuristically
// cacheable too, and a route added next year would have inherited the same silence. Set before
// the handler runs, so a handler that genuinely wants caching just says so -- /favicon.ico does,
// and its `public, max-age=86400` still wins. That is the one deliberate exception; adding
// another should be a decision, which is what TestInspectorCacheControlByRoute pins.
//
// no-store rather than no-cache: the bytes are small and served over loopback, so there is
// nothing to gain from revalidation, and "never reuse" needs no reasoning about validators.
func noStoreByDefault(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// guardLocalOnly rejects requests a browser on another site could have driven.
//
// The Inspector binds loopback, which protects it from the network but not from the
// browser: localhost is not a trust boundary a browser enforces, so any page a developer
// visits can issue cross-origin requests at a known fixed port. Five of these endpoints
// mutate live tunnel state, and CORS blocks reading the response but not the side effect.
// Reading the disclosing ones needs DNS rebinding on top, which the Host check defeats
// (issue #1138).
//
// Requests with no Origin at all -- curl, scripts, the client's own tooling -- are
// untouched, because they are not browser-driven.
//
// hostCheck is disabled when the Inspector is bound beyond loopback, since the legitimate
// Host is then whatever the container or operator mapped and cannot be predicted.
func guardLocalOnly(next http.Handler, port int, hostCheck bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && !isOwnOrigin(origin, port) {
			http.Error(w, "cross-origin requests are not accepted", http.StatusForbidden)
			return
		}
		if hostCheck {
			hostname := r.Host
			if h, _, err := net.SplitHostPort(r.Host); err == nil {
				hostname = h
			}
			if !isLoopbackHostname(hostname) {
				http.Error(w, "unexpected Host header", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
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

	cleanMethod := record.Method
	if idx := strings.Index(cleanMethod, " "); idx != -1 {
		cleanMethod = cleanMethod[:idx]
	}
	cleanMethod = strings.ToUpper(strings.TrimSpace(cleanMethod))

	req, err := http.NewRequest(cleanMethod, targetURL, reqBodyReader)
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
		Method:     cleanMethod + " (Replay)",
		Path:       record.Path,
		ReqHeaders: record.ReqHeaders,
		ReqBody:    record.ReqBody,
		TargetPort: record.TargetPort,
		// The host this replay was actually sent to -- the argument AFTER the empty-value
		// default above, not the caller's, so a record cannot claim a host that was never
		// dialled (#2191). Set here rather than left to AddRecord's stamp because this is the
		// one path whose target can differ from engine.TargetHost.
		TargetHost: targetHost,
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
	_, _ = io.Copy(&bodyBuf, limitReader) //nolint:errcheck
	rec.RespBody = bodyBuf.String()

	return rec, nil
}

// splitAssignedHost recovers the (prefix, domain) pair the gateway keys a reservation on.
//
// It used to be `strings.SplitN(subdomainAss, ".", 2)` with a hard failure when that produced
// fewer than two parts -- and engine.SubdomainAss is set from regResp.SubdomainPrefix, the
// PREFIX ALONE. So it was always one part, and every access-control save returned 400 "Invalid
// assigned subdomain format" before the gateway was ever contacted (#2098).
//
// The domain was never missing, only looked for in the wrong place: the engine already holds the
// public URLs, and the host of one of them is prefix + "." + domain.
// accessControlEditability reports whether the Inspector can save access control for this
// session, and if not, why -- in words meant for the person reading the panel.
//
// It asks splitAssignedHost, which is what the save path calls, so the form and the endpoint
// cannot drift into disagreeing. Two states can never be saved from here however long you wait:
// a session with no assignment yet, and a custom domain, which splitAssignedHost refuses on
// purpose because guessing a (prefix, domain) pair for one would address somebody else's
// reservation. Letting either be typed into and fail on submit is the behaviour this replaces.
//
// The third is temporary and follows the owner's rule: configuration central stores must be
// changed at central, so when central cannot be reached it cannot be changed -- and the edge
// carries on enforcing what it was last told. Ordered after the permanent two deliberately: a
// custom domain is not reported as a passing outage that will clear on its own.
func accessControlEditability(assigned string, publicURLs []string, controlPlaneUp, controlPlaneKnown bool) (bool, string) {
	if strings.TrimSpace(assigned) == "" {
		return false, "Access control can be set once the tunnel is connected and has a public URL."
	}
	if _, _, err := splitAssignedHost(assigned, publicURLs); err != nil {
		return false, "This tunnel is served on a custom domain, whose access control is managed " +
			"from the portal rather than here."
	}
	// The only TEMPORARY refusal of the three, and the reason this returns a sentence rather
	// than a flag: a disabled access-control panel must not read as "your tunnel is now open".
	// It is still enforcing exactly what it was last told, on the lease the edge holds.
	if controlPlaneKnown && !controlPlaneUp {
		return false, "The control plane is unreachable, so access control cannot be changed " +
			"right now. Your tunnel is still enforcing the settings it already has, and these " +
			"fields return by themselves once the control plane is back."
	}
	return true, ""
}

func splitAssignedHost(assigned string, publicURLs []string) (string, string, error) {
	assigned = strings.TrimSpace(assigned)
	if assigned == "" {
		return "", "", fmt.Errorf("the client has no assigned subdomain yet")
	}

	// Derived from the public URLs, never from the shape of the string.
	//
	// The first version of this trusted an assignment that already contained a dot, on the
	// theory that it was prefix.domain. A test caught what that does to a custom domain:
	// "vanity.example.com" is dotted, so it split into prefix "vanity" and domain
	// "example.com" -- a plausible-looking pair addressing a reservation that is not the
	// user's. Guessing wrong here edits somebody else's access control.
	for _, raw := range publicURLs {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Hostname() == "" {
			continue
		}
		host := parsed.Hostname()
		if rest, ok := strings.CutPrefix(host, assigned+"."); ok && rest != "" {
			return assigned, rest, nil
		}
	}

	return "", "", fmt.Errorf(
		"cannot determine the domain for subdomain %q from the public URLs %v -- a custom "+
			"domain has no prefix to strip, and that case is not handled here", assigned, publicURLs)
}
