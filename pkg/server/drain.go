package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Draining a gateway before restarting it (#1303).
//
// A deploy used to enable nginx maintenance mode, restart lfr-tunneld, and disable it again.
// That stops new connections arriving; it does nothing about the ones already attached, which
// the restart kills outright. Measured against real infrastructure, a client dropped that way
// was down 24m36s, while one that moved on a warning was not down at all (#1246).
//
// Everything needed to do better already existed and was only reachable from central: a
// gateway can carry a pending shutdown, and its clients pick it up on the tunnel-status
// heartbeat they already send (#1238). This exposes the same state locally, so whoever is
// performing the deploy can announce the restart, watch the node empty, and only then take it
// down.
//
// Deliberately localhost-only rather than authenticated: it is invoked over SSH on the box
// being deployed to, in the same breath as `systemctl restart`. Anyone who can reach it can
// already restart the service outright, so a token would guard nothing while adding a secret
// to distribute.

// drainRetryAfterSeconds is what a refused registration is told to wait. Sized for a restart:
// long enough that a client is not hammering a gateway mid-restart, short enough that a client
// with nowhere else to go is not parked for minutes after it comes back (#1238).
const drainRetryAfterSeconds = 15

// drainStatus is what a caller polls: whether a shutdown is announced, and how much is still
// attached. Clients are counted by lease rather than by connection, because a lease is what a
// visitor's URL resolves to and therefore what actually breaks when the process stops.
type drainStatus struct {
	Draining         bool   `json:"draining"`
	SecondsRemaining int64  `json:"seconds_remaining"`
	Reason           string `json:"reason,omitempty"`
	// LocalLeases counts tunnels this gateway is serving itself. Tunnels held by edge nodes
	// are not included: an edge proxies independently, so restarting the control plane does
	// not interrupt them, and waiting for them to drain would mean waiting forever.
	LocalLeases int `json:"local_leases"`
	// PortalSessions counts people currently logged into the portal, which local_leases says
	// nothing about -- a drain announcement travels on the tunnel-status heartbeat, which a
	// browser never sends, so a restart decision made on local_leases alone is blind to them
	// (#1455). The two genuinely diverge: on 2026-08-27 central had zero tunnels attached and
	// one active portal session at the moment of a restart.
	//
	// Not a reason to refuse a restart. Portal sessions are persisted, so nobody is logged out
	// -- the cost is failed in-flight requests and the maintenance page for its duration. It is
	// here so that cost is a decision rather than a surprise.
	PortalSessions int `json:"portal_sessions"`
}

// isLocalRequest reports whether a request arrived directly over loopback. The proxy-header
// check matters as much as the address one: nginx forwards to 127.0.0.1, so a request that
// came in from the internet would otherwise look local by the time it arrived here.
func isLocalRequest(r *http.Request) bool {
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteHost = r.RemoteAddr
	}
	if remoteHost != "127.0.0.1" && remoteHost != "::1" && remoteHost != "localhost" {
		return false
	}
	return r.Header.Get("X-Forwarded-For") == "" &&
		r.Header.Get("X-Forwarded-Host") == "" &&
		r.Header.Get("X-Real-IP") == ""
}

// handleLocalDrain announces, clears or reports a pending shutdown for this gateway.
//
//	POST /api/local/drain {"seconds": 60, "reason": "..."}  announce
//	POST /api/local/drain {"seconds": 0}                     cancel
//	GET  /api/local/drain                                    report
//
// Both verbs answer with the current drainStatus, so a caller can announce and then poll the
// same endpoint until LocalLeases reaches zero.
func (s *Server) handleLocalDrain(w http.ResponseWriter, r *http.Request) {
	if !isLocalRequest(r) {
		http.Error(w, `{"error":"Forbidden: direct localhost connection required"}`, http.StatusForbidden)
		return
	}

	if r.Method == http.MethodPost {
		var req struct {
			Seconds int64  `json:"seconds"`
			Reason  string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"Invalid payload"}`, http.StatusBadRequest)
			return
		}

		s.maintMutex.Lock()
		if req.Seconds <= 0 {
			// Cancelling matters as much as announcing: a deploy that fails partway through
			// must be able to put the gateway back, or clients keep migrating away from a
			// node that is not going anywhere.
			s.pendingShutdownAt = 0
			s.pendingShutdownReason = ""
			slog.Info("[Drain] Pending shutdown cleared; this gateway is staying up")
		} else {
			s.pendingShutdownAt = time.Now().Add(time.Duration(req.Seconds) * time.Second).Unix()
			s.pendingShutdownReason = req.Reason
			if s.pendingShutdownReason == "" {
				s.pendingShutdownReason = "Gateway is restarting for a deployment"
			}
			slog.Info(fmt.Sprintf("[Drain] Announced shutdown in %ds (%s); connected clients will move to another gateway",
				req.Seconds, s.pendingShutdownReason))
		}
		s.maintMutex.Unlock()
	}

	respondJSON(w, http.StatusOK, s.currentDrainStatus())
}

// isDraining reports whether this gateway has announced a pending shutdown. Used to turn new
// work away while the node empties -- see healthzPayload and the registration paths (#1238).
func (s *Server) isDraining() bool {
	s.maintMutex.RLock()
	defer s.maintMutex.RUnlock()
	return s.pendingShutdownAt > 0 && s.pendingShutdownAt > time.Now().Unix()
}

// currentDrainStatus reads the announcement and counts what is still attached.
func (s *Server) currentDrainStatus() drainStatus {
	s.maintMutex.RLock()
	at, reason := s.pendingShutdownAt, s.pendingShutdownReason
	s.maintMutex.RUnlock()

	status := drainStatus{Reason: reason}
	if at > 0 {
		status.Draining = true
		status.SecondsRemaining = at - time.Now().Unix()
		if status.SecondsRemaining < 0 {
			status.SecondsRemaining = 0
		}
	}
	if s.registry != nil {
		status.LocalLeases = len(s.registry.ListLeases())
	}
	// Best-effort: an edge runs with db_path empty and has no portal at all, and a counting
	// error must not stop an operator finding out how many tunnels are attached.
	if s.db != nil {
		if n, err := s.db.CountActivePortalSessions(); err == nil {
			status.PortalSessions = n
		}
	}
	return status
}
