package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/provisioner"
)

// edgePowerReason names why edge power actions are off (#1956).
//
// They used to be one state: provisionerClient == nil, reported to the admin as "not
// configured on this server". A deployment that has no sidecar and one whose
// edge_provisioner_token_file is mistyped produced the same nil, the same 501 and the same
// sentence, so an operator error was presented to them as a decision they had made. The
// only discriminator was one INFO line at startup, which scrolls away.
//
// This is the same class as #1938 (a mistyped geolite2_db_path indistinguishable from an
// unset one), and it is fixed the same way: keep the reason beside the nil client, and say
// it on the surface that already reports the feature as absent.
type edgePowerReason string

const (
	// edgePowerNotConfigured is edge_provisioner_url unset. The default, the state every
	// non-AWS deployment is in, and not a fault.
	edgePowerNotConfigured edgePowerReason = "not_configured"
	// edgePowerTokenPathUnset is a URL with no edge_provisioner_token_file beside it.
	edgePowerTokenPathUnset edgePowerReason = "token_path_unset"
	// edgePowerTokenNotFound is a configured token path with no file at it.
	edgePowerTokenNotFound edgePowerReason = "token_not_found"
	// edgePowerTokenUnreadable is a token file that exists and cannot be read.
	edgePowerTokenUnreadable edgePowerReason = "token_unreadable"
	// edgePowerTokenEmpty is a readable token file with nothing in it.
	edgePowerTokenEmpty edgePowerReason = "token_empty"
)

// edgePowerDiagnosis is why the feature is off, kept for the admin surfaces.
//
// TokenFile and Detail are ADMIN-ONLY: they name a filesystem path on the gateway host and
// quote the filesystem's complaint about it. They leave the process through the 501 body on
// /api/admin/edge/* (behind requireAdmin) and through handleEdgeHealth, which emits them
// only for an admin or owner session -- see the role check there.
//
// Neither field ever holds the token, its length or a prefix of it. The whole point of the
// token file is that its contents stay unread by anything but the client that presents
// them, and a diagnosis that narrowed the secret would trade one bug for a worse one. What
// is reported is that the load failed and which way: missing setting, missing file,
// unreadable file, empty file.
type edgePowerDiagnosis struct {
	Reason    edgePowerReason
	TokenFile string
	Detail    string
}

// faulty reports whether this diagnosis is an operator error rather than the default.
//
// The portals render a warning for exactly these: a gateway with no sidecar configured must
// look precisely as it does today, or every non-AWS deployment grows a banner about a
// feature it never asked for.
func (d edgePowerDiagnosis) faulty() bool {
	return d.Reason != "" && d.Reason != edgePowerNotConfigured
}

// newProvisionerClient builds the edge-provisioner client, or returns nil plus the reason
// (#888, #1250, #1956).
//
// nil stays a working no-op exactly as before: the sidecar is optional and AWS-specific,
// and a token that cannot be loaded must disable edge power actions rather than stop the
// gateway starting -- the sidecar owns the token file and may simply not have written it
// yet. What changes is that the reason survives the constructor.
func newProvisionerClient(cfg *config.ServerConfig) (*provisioner.Client, edgePowerDiagnosis) {
	if cfg.EdgeProvisionerURL == "" {
		return nil, edgePowerDiagnosis{Reason: edgePowerNotConfigured}
	}

	token, err := provisioner.LoadToken(cfg.EdgeProvisionerTokenFile)
	if err == nil {
		return provisioner.NewClient(cfg.EdgeProvisionerURL, token), edgePowerDiagnosis{}
	}

	// Warn, not Info (#1956). This line is an operator error every time it is emitted --
	// edge_provisioner_url is set, so this deployment wants the feature -- and it used to
	// sit at INFO among the startup chatter, quieter than the geo case it duplicates.
	slog.Warn("[Server] edge_provisioner_url is set but its token could not be loaded; edge power actions disabled",
		"token_file", cfg.EdgeProvisionerTokenFile, "error", err)

	d := edgePowerDiagnosis{TokenFile: cfg.EdgeProvisionerTokenFile}
	switch {
	case errors.Is(err, provisioner.ErrTokenPathUnset):
		// No path to report: the setting itself is missing, so naming "" would be noise.
		d.Reason, d.TokenFile = edgePowerTokenPathUnset, ""
	case errors.Is(err, provisioner.ErrTokenNotFound):
		d.Reason = edgePowerTokenNotFound
	case errors.Is(err, provisioner.ErrTokenEmpty):
		d.Reason = edgePowerTokenEmpty
	default:
		// Includes ErrTokenUnreadable. Detail carries the filesystem's own words --
		// "permission denied" is the sentence that resolves this state and it exists
		// nowhere else -- and never the file's contents, which were never read.
		d.Reason, d.Detail = edgePowerTokenUnreadable, err.Error()
	}
	return nil, d
}

// edgeProvisionerNodeID extracts the node ID from a path shaped
// "/api/admin/edge/{id}/<suffix>", mirroring the TrimPrefix/TrimSuffix style
// already used for "/api/admin/users/{email}/limit" elsewhere in this file.
func edgeProvisionerNodeID(path, suffix string) string {
	trimmed := strings.TrimPrefix(path, "/api/admin/edge/")
	return strings.TrimSuffix(trimmed, suffix)
}

// requireProvisioner responds with 501 Not Implemented (matching this
// codebase's existing "Database not configured" precedent) when
// edge_provisioner_url isn't set, and returns false so the caller can bail
// out. This is the server-side half of "absent, not erroring" -- the portal
// is expected to hide these actions entirely when unconfigured, but a stray
// call must still fail cleanly rather than panic on a nil client.
//
// The status is unchanged for every state, and so is the sentence for the one state that
// sentence was ever true of (#1956). What is no longer said is that a sidecar this
// deployment HAS configured is not configured: when the URL is set and only the token
// failed to load, the body says so and names the token file, so an admin who reaches this
// through a stale tab or a script sees the same diagnosis the panel shows. Admin-only:
// every route that calls this is dispatched from handleAdminEndpoints, behind requireAdmin.
func (s *Server) requireProvisioner(w http.ResponseWriter) (*provisioner.Client, bool) {
	if s.provisionerClient == nil {
		http.Error(w, `{"error":`+jsonQuoteString(s.edgePowerUnavailableMessage())+`}`, http.StatusNotImplemented)
		return nil, false
	}
	return s.provisionerClient, true
}

// edgePowerUnavailableMessage is the 501 body's sentence for the state this gateway is
// actually in (#1956). English only and deliberately so: it is an API error body, not a
// portal string -- the portals render the translated version from the reason code.
func (s *Server) edgePowerUnavailableMessage() string {
	d := s.edgePowerDiagnosis
	switch d.Reason {
	case edgePowerTokenPathUnset:
		return "Edge power actions are configured (edge_provisioner_url) but edge_provisioner_token_file is not set, so they are disabled"
	case edgePowerTokenNotFound:
		return fmt.Sprintf("Edge power actions are configured (edge_provisioner_url) but no file exists at the edge_provisioner_token_file path %s, so they are disabled", d.TokenFile)
	case edgePowerTokenEmpty:
		return fmt.Sprintf("Edge power actions are configured (edge_provisioner_url) but the token file %s is empty, so they are disabled", d.TokenFile)
	case edgePowerTokenUnreadable:
		return fmt.Sprintf("Edge power actions are configured (edge_provisioner_url) but its token file could not be read, so they are disabled: %s", d.Detail)
	default:
		// Unchanged wording for the unchanged state: no sidecar is configured here.
		return "Edge power actions are not configured on this server"
	}
}

// jsonQuoteString JSON-quotes a message built above. The messages are assembled from the
// gateway's own configuration, which can contain a quote or a backslash in a path, and this
// body is hand-written JSON rather than an encoder -- so escape rather than trusting the
// input to be tame.
func jsonQuoteString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `"Edge power actions are not configured on this server"`
	}
	return string(b)
}

// edgePortalStopWarningSeconds is how long the portal's stop button warns a node's clients
// before the instance is actually stopped (#2167).
//
// Long enough to ride several heartbeats -- they arrive about every five seconds -- so a client
// hears the warning and moves under plannedShutdownCooldown rather than discovering the
// gateway is gone and treating it as a fault. Short enough that an operator who pressed Stop
// does not think it was ignored.
const edgePortalStopWarningSeconds = 15

func (s *Server) handleAdminEdgeStart(w http.ResponseWriter, r *http.Request, actor string) {
	client, ok := s.requireProvisioner(w)
	if !ok {
		return
	}
	nodeID := edgeProvisionerNodeID(r.URL.Path, "/start")

	if err := client.Start(r.Context(), nodeID); err != nil {
		writeProvisionerError(w, err)
		return
	}
	s.setEdgeAdminDisabled(nodeID, false)
	s.writeAudit(actor, "edge.power.start", "node", nodeID, "Edge node start requested via portal", r)
	s.triggerEdgeHealthRecheck(nodeID)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleAdminEdgeStop(w http.ResponseWriter, r *http.Request, actor string) {
	client, ok := s.requireProvisioner(w)
	if !ok {
		return
	}
	nodeID := edgeProvisionerNodeID(r.URL.Path, "/stop")

	// Warn the node's clients BEFORE the instance goes away (#2167).
	//
	// This path used to call client.Stop() first and kick the sessions afterwards, so there was
	// no moment at which a warning could reach anyone. Clients therefore experienced an
	// administrative stop as an ordinary failure and applied regionFailoverCooldown -- 90
	// seconds -- rather than plannedShutdownCooldown, which is an hour and exists precisely
	// because "this gateway is about to be deliberately unreachable".
	//
	// On 2026-09-22 that was harmless: edge-us came back in three minutes and the short
	// cooldown made the failback quick. On the overnight schedule it is not: a client re-elects
	// a powered-off gateway every 90 seconds, fails to register, and churns until morning. The
	// hour-long cooldown was written for exactly that and this path never triggered it.
	//
	// The machinery was already here. BroadcastNodeShutdownWarning has carried this to edges
	// since #1238; the scheduled-stop sweep uses it, the deploy script announces its own drain,
	// and only the portal button skipped it.
	reason := "Administrative stop requested from the portal"
	s.BroadcastNodeShutdownWarning(nodeID, edgePortalStopWarningSeconds, reason)

	// Recorded before the wait, so the audit shows when the operator asked rather than when the
	// instance finally went down.
	s.writeAudit(actor, "edge.power.stop", "node", nodeID, "Edge node stop requested via portal", r)

	s.setEdgeAdminDisabled(nodeID, true)
	s.edgeHealthMu.Lock()
	if h, exists := s.edgeHealth[nodeID]; exists {
		h.Status = "Stopping"
		h.ErrorMessage = fmt.Sprintf("Administrative stop requested; warning clients for %ds", edgePortalStopWarningSeconds)
		s.edgeHealth[nodeID] = h
	}
	s.edgeHealthMu.Unlock()

	// 202 Accepted, and meant literally: the stop happens after the warning window rather than
	// during this request. Blocking an admin HTTP call for the whole window would be the
	// alternative, and a proxy timing out mid-wait would leave the node warned but not stopped.
	s.goTracked(func() {
		time.Sleep(edgePortalStopWarningSeconds * time.Second)

		if err := client.Stop(context.Background(), nodeID); err != nil {
			slog.Error(fmt.Sprintf("[Edge] Stopping %s after its warning window failed: %v", nodeID, err))
			s.edgeHealthMu.Lock()
			if h, exists := s.edgeHealth[nodeID]; exists {
				h.Status = "Online"
				h.ErrorMessage = fmt.Sprintf("Administrative stop failed: %v", err)
				s.edgeHealth[nodeID] = h
			}
			s.edgeHealthMu.Unlock()
			s.setEdgeAdminDisabled(nodeID, false)
			s.triggerEdgeHealthRecheck(nodeID)
			return
		}

		// Kick and close AFTER the instance is going, as before -- but now every client has
		// had the warning window to move of its own accord, so this reaps stragglers rather
		// than being the first anyone hears of it.
		_ = s.SendEdgeKickAll(nodeID) //nolint:errcheck
		s.CloseEdgeControlConn(nodeID)

		s.edgeHealthMu.Lock()
		if h, exists := s.edgeHealth[nodeID]; exists {
			h.Status = "Offline"
			h.ErrorMessage = "Administrative stop requested"
			s.edgeHealth[nodeID] = h
		}
		s.edgeHealthMu.Unlock()
		s.triggerEdgeHealthRecheck(nodeID)
	})

	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleAdminEdgeRestart(w http.ResponseWriter, r *http.Request, actor string) {
	client, ok := s.requireProvisioner(w)
	if !ok {
		return
	}
	nodeID := edgeProvisionerNodeID(r.URL.Path, "/restart")

	if err := client.Restart(r.Context(), nodeID); err != nil {
		writeProvisionerError(w, err)
		return
	}
	s.setEdgeAdminDisabled(nodeID, false)
	s.writeAudit(actor, "edge.power.restart", "node", nodeID, "Edge node restart requested via portal", r)
	s.triggerEdgeHealthRecheck(nodeID)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleAdminEdgeGetSchedule(w http.ResponseWriter, r *http.Request, _ string) {
	client, ok := s.requireProvisioner(w)
	if !ok {
		return
	}
	nodeID := edgeProvisionerNodeID(r.URL.Path, "/schedule")

	sched, err := client.GetSchedule(r.Context(), nodeID)
	if err != nil {
		writeProvisionerError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, sched)
}

func (s *Server) handleAdminEdgeSetSchedule(w http.ResponseWriter, r *http.Request, actor string) {
	client, ok := s.requireProvisioner(w)
	if !ok {
		return
	}
	nodeID := edgeProvisionerNodeID(r.URL.Path, "/schedule")

	var sched provisioner.Schedule
	if err := json.NewDecoder(r.Body).Decode(&sched); err != nil {
		http.Error(w, `{"error":"Invalid request body"}`, http.StatusBadRequest)
		return
	}

	if err := client.SetSchedule(r.Context(), nodeID, sched); err != nil {
		writeProvisionerError(w, err)
		return
	}
	s.invalidateEdgeScheduleCache(nodeID)
	s.writeAudit(actor, "edge.power.schedule_update", "node", nodeID,
		"Edge node schedule updated via portal: stop="+sched.StopTime+" start="+sched.StartTime+" tz="+sched.Timezone, r)
	respondJSON(w, http.StatusOK, sched)
}

// edgeBulkActionRequest is the body for POST /api/admin/edge/bulk (#884):
// apply the same start/stop/restart action to several nodes in one call.
type edgeBulkActionRequest struct {
	NodeIDs []string `json:"node_ids"`
	Action  string   `json:"action"` // "start" | "stop" | "restart"
}

type edgeBulkActionResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (s *Server) handleAdminEdgeBulkAction(w http.ResponseWriter, r *http.Request, actor string) {
	client, ok := s.requireProvisioner(w)
	if !ok {
		return
	}

	var req edgeBulkActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request body"}`, http.StatusBadRequest)
		return
	}

	var action func(nodeID string) error
	switch req.Action {
	case "start":
		action = func(id string) error { return client.Start(r.Context(), id) }
	case "stop":
		action = func(id string) error { return client.Stop(r.Context(), id) }
	case "restart":
		action = func(id string) error { return client.Restart(r.Context(), id) }
	default:
		http.Error(w, `{"error":"action must be one of start, stop, restart"}`, http.StatusBadRequest)
		return
	}

	results := make(map[string]edgeBulkActionResult, len(req.NodeIDs))
	for _, nodeID := range req.NodeIDs {
		if err := action(nodeID); err != nil {
			results[nodeID] = edgeBulkActionResult{OK: false, Error: err.Error()}
			continue
		}
		results[nodeID] = edgeBulkActionResult{OK: true}
		s.setEdgeAdminDisabled(nodeID, req.Action == "stop")
		s.writeAudit(actor, "edge.power."+req.Action, "node", nodeID, "Bulk "+req.Action+" requested via portal", r)
		s.triggerEdgeHealthRecheck(nodeID)
	}

	respondJSON(w, http.StatusOK, map[string]any{"results": results})
}

func writeProvisionerError(w http.ResponseWriter, err error) {
	var notFound *provisioner.ErrRemote
	if errors.As(err, &notFound) && notFound.StatusCode == http.StatusNotFound {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": notFound.Message})
		return
	}
	respondJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to contact edge-provisioner: " + err.Error()})
}
