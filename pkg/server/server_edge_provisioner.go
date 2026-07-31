package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"lfr-tunnel/pkg/provisioner"
)

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
func (s *Server) requireProvisioner(w http.ResponseWriter) (*provisioner.Client, bool) {
	if s.provisionerClient == nil {
		http.Error(w, `{"error":"Edge power actions are not configured on this server"}`, http.StatusNotImplemented)
		return nil, false
	}
	return s.provisionerClient, true
}

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

	if err := client.Stop(r.Context(), nodeID); err != nil {
		writeProvisionerError(w, err)
		return
	}
	s.writeAudit(actor, "edge.power.stop", "node", nodeID, "Edge node stop requested via portal", r)
	s.triggerEdgeHealthRecheck(nodeID)
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
	s.invalidateEdgeTimezoneCache(nodeID)
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
