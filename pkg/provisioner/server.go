package provisioner

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// apiVersions lists every API version this build of the sidecar understands,
// returned verbatim by GET /versions. lfr-tunneld caches this and uses the
// highest version it also understands; no overlap means it treats the whole
// feature as absent, never a hard error surfaced to the user (see #888).
var apiVersions = []string{"v1"}

// Server is the edge-provisioner sidecar's HTTP server. It only ever speaks
// the versioned contract from issue #888 -- Backend is where any
// provider-specific behavior actually lives.
type Server struct {
	mux     *http.ServeMux
	backend Backend
	token   string
}

// NewServer wires up the sidecar's routes. token is the shared secret
// lfr-tunneld must present as "Authorization: Bearer <token>" on every
// request except GET /versions (deliberately unauthenticated, so
// lfr-tunneld's version-negotiation probe doesn't need the token just to
// discover whether the sidecar is even reachable).
func NewServer(backend Backend, token string) *Server {
	s := &Server{mux: http.NewServeMux(), backend: backend, token: token}

	s.mux.HandleFunc("GET /versions", s.handleVersions)
	s.mux.HandleFunc("GET /v1/capabilities", s.authenticated(s.handleCapabilities))
	s.mux.HandleFunc("POST /v1/nodes/{id}/start", s.authenticated(s.handleStart))
	s.mux.HandleFunc("POST /v1/nodes/{id}/stop", s.authenticated(s.handleStop))
	s.mux.HandleFunc("POST /v1/nodes/{id}/restart", s.authenticated(s.handleRestart))
	s.mux.HandleFunc("GET /v1/nodes/{id}/schedule", s.authenticated(s.handleGetSchedule))
	s.mux.HandleFunc("PUT /v1/nodes/{id}/schedule", s.authenticated(s.handleSetSchedule))

	return s
}

func (s *Server) ListenAndServe(addr string) error {
	slog.Info("[edge-provisioner] listening", "addr", addr)
	return http.ListenAndServe(addr, s.mux) //nolint:gosec // addr is validated loopback-only by config.LoadConfig's caller
}

func (s *Server) authenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		auth := r.Header.Get("Authorization")
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix || !ValidToken(s.token, auth[len(prefix):]) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
			return
		}
		next(w, r)
	}
}

func (s *Server) handleVersions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"supported": apiVersions})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.backend.Capabilities())
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if err := s.backend.Start(r.Context(), nodeID); err != nil {
		writeBackendError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if err := s.backend.Stop(r.Context(), nodeID); err != nil {
		writeBackendError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleRestart returns 202 as soon as the restart sequence is *launched*,
// not once it completes -- a full stop-wait-start cycle can take 30-90s+,
// and holding the HTTP request open that long serves no purpose the portal's
// existing health-check polling doesn't already cover. Errors from the
// background restart are logged locally; there is deliberately no channel
// to report them back to the original caller (see #888's async design).
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")

	// Validate the node exists before returning 202 -- an unknown node ID
	// should fail fast and visibly, not silently in a background goroutine.
	if _, err := s.backend.GetSchedule(r.Context(), nodeID); err != nil {
		var notFound *ErrNodeNotFound
		if errors.As(err, &notFound) {
			writeBackendError(w, err)
			return
		}
		// Any other error here (e.g. the schedule genuinely doesn't exist,
		// or a transient AWS error) isn't a reason to refuse the restart
		// itself -- only an unknown node ID is.
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := s.backend.Restart(ctx, nodeID); err != nil {
			slog.Error("[edge-provisioner] background restart failed", "node_id", nodeID, "error", err)
		}
	}()

	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	sched, err := s.backend.GetSchedule(r.Context(), nodeID)
	if err != nil {
		writeBackendError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sched)
}

func (s *Server) handleSetSchedule(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")

	var sched Schedule
	if err := json.NewDecoder(r.Body).Decode(&sched); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "could not parse request body as JSON")
		return
	}

	// Validated here rather than in the portal handler so it applies to every caller:
	// the portal proxies through this endpoint, and so does anything driving the sidecar
	// directly. edge-sa was found stopping at 16:00 and starting at 15:45 -- up for fifteen
	// minutes a day -- because nothing on any path rejected it (#1250).
	if err := sched.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_schedule", err.Error())
		return
	}

	if err := s.backend.SetSchedule(r.Context(), nodeID, sched); err != nil {
		writeBackendError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func writeBackendError(w http.ResponseWriter, err error) {
	var notFound *ErrNodeNotFound
	if errors.As(err, &notFound) {
		writeError(w, http.StatusNotFound, "node_not_found", err.Error())
		return
	}
	writeError(w, http.StatusBadGateway, "backend_error", err.Error())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
