package client

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
)

//go:embed inspector.html
var inspectorHTML []byte

// StartInspector starts the local web dashboard for the given engine.
func StartInspector(port int, engine *InterceptorEngine) {
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
	} else if isDocker() {
		bindIP = "0.0.0.0"
	}

	addr := fmt.Sprintf("%s:%d", bindIP, port)
	go func() {
		log.Printf("[Inspector] Local Dashboard running at http://%s\n", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("[Inspector] Failed to start on %s: %v", addr, err)
		}
	}()
}

// isDocker checks if the application is running inside a Docker container.
func isDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}
