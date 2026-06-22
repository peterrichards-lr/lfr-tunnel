package client

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"lfr-tunnel/pkg/config"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed inspector.html
var inspectorHTML []byte

// StartInspector starts the local web dashboard for the given engine.
// If the requested port is in use, it will auto-increment up to 10 times to find a free port.
func StartInspector(port int, engine *InterceptorEngine) (int, error) {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(inspectorHTML)
	})

	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		engine.mu.RLock()
		defer engine.mu.RUnlock()

		state := map[string]interface{}{
			"maintenance_mode": engine.MaintenanceMode,
			"added_headers":    engine.AddedHeaders,
			"history":          engine.History,
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

		info := map[string]interface{}{
			"status":  status,
			"version": config.Version,
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
				"requested": engine.SubdomainReq,
				"assigned":  engine.SubdomainAss,
				"leased":    engine.SubdomainLeased,
				"conflict":  engine.SubdomainConflict,
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
		log.Printf("[Inspector] Local Dashboard running at http://%s:%d\n", bindIP, actualPort)
		if err := http.Serve(listener, mux); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			log.Printf("[Inspector] Failed to serve: %v", err)
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
