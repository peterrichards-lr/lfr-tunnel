package server

import (
	"net/http"
	"sort"
	"time"
)

// The admin API half of session-key rotation (#2195): a manual trigger, and the read-back a
// portal needs to render what the rotation engine is doing.
//
// THE PORTAL UI IS THE SIBLING ISSUE (#2196) and is deliberately not here. What is here is
// shaped so that issue is a view rather than a redesign: the GET answers "which generation is
// current, when does the next rotation run, and what did the last one do", because without that
// a silently-aborting rotation is invisible -- the pattern this repo keeps finding.
//
// NOTHING ON EITHER PATH CARRIES KEY MATERIAL. Generation ids are labels (see
// VisitorSessionSecret.String, and #2135/#2137 for why that type refuses to render itself), and
// these responses are built from ids, timestamps and node names only.

// visitorSessionRotationStatus is the body of GET /api/admin/session-secrets.
type visitorSessionRotationStatus struct {
	// CurrentGeneration is the generation every node should be minting with.
	CurrentGeneration string `json:"current_generation"`
	// AcceptedGenerations is every generation this control plane still verifies against. It is
	// longer than one during the window between a commit and a retirement, which is the window
	// that keeps a visitor signed in across a rotation.
	AcceptedGenerations []string `json:"accepted_generations"`
	// NextRotationAt is the persisted due instant, so the portal shows the same answer before
	// and after a control-plane restart.
	NextRotationAt time.Time `json:"next_rotation_at"`
	// RetiringAt maps a generation to when it stops verifying. This is phase three made
	// visible: "old becomes invalid" is a scheduled event, not something that happened at the
	// commit.
	RetiringAt map[string]time.Time `json:"retiring_at,omitempty"`
	// LastRotation is the last attempt, committed or aborted, with the reason on an abort.
	LastRotation *visitorSessionRotationOutcome `json:"last_rotation,omitempty"`
	// ConnectedNodes is the roster a rotation is actually gated on -- currently connected, never
	// configured. Surfaced because an admin about to press the button should be able to see
	// that edge-us and edge-sa are asleep and that this is not a reason to wait.
	ConnectedNodes []string `json:"connected_nodes"`
	// RotationInterval and RetirementLag are stated rather than left for a UI to hard-code a
	// second copy of.
	RotationInterval string `json:"rotation_interval"`
	RetirementLag    string `json:"retirement_lag"`
	// AcceptedLimit is how many generations a node will hold at once, for the same reason the
	// two above are stated: a UI that wants to compare AcceptedGenerations against the bound --
	// to explain why a rotation just refused, or that the next one will -- should read the
	// server's number rather than keep a second copy of it that can drift (#2198).
	AcceptedLimit int `json:"accepted_limit"`
}

// handleAdminVisitorSessionRotationStatus serves the read-back.
//
// Dispatched from handleAdminEndpoints, which has already run requireAdmin.
func (s *Server) handleAdminVisitorSessionRotationStatus(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		respondJSON(w, http.StatusNotImplemented, apiError{Error: "Database storage not enabled"})
		return
	}
	stored := s.visitorSessionSecrets.get()
	state := loadVisitorSessionRotationState(s.db)

	accepted := generationIDs(stored.Secrets)
	sort.Strings(accepted)

	respondJSON(w, http.StatusOK, visitorSessionRotationStatus{
		CurrentGeneration:   stored.CurrentID,
		AcceptedGenerations: accepted,
		NextRotationAt:      state.NextRotationAt,
		RetiringAt:          state.Retirements,
		LastRotation:        state.Last,
		ConnectedNodes:      s.connectedEdgeNodeIDs(),
		RotationInterval:    visitorSessionRotationInterval.String(),
		RetirementLag:       visitorSessionRetirementLag.String(),
		AcceptedLimit:       maxAcceptedVisitorSessionSecrets,
	})
}

// handleAdminVisitorSessionRotate is the MANUAL trigger.
//
// The role is re-checked here against the database rather than trusted from requireAdmin's
// return, for the reason handleAdminDiagnosticsCollect states: requireAdmin's cookie path
// currently rewrites a stored role of "user" to "admin" (#1760), so inheriting that check would
// make this endpoint reachable by any logged-in portal user. Rotating the fleet's session-signing
// key is worth one extra read to be certain.
//
// Synchronous, and bounded by visitorSessionAckTimeout: an admin pressing the button wants the
// outcome, and "queued" would be indistinguishable from an abort nobody saw.
func (s *Server) handleAdminVisitorSessionRotate(w http.ResponseWriter, r *http.Request) {
	actor, err := s.getCurrentUserRaw(r)
	if err != nil || actor == nil {
		respondJSON(w, http.StatusUnauthorized, apiError{Error: "Unauthorized"})
		return
	}
	// s.isOwner as well as the stored role, because the owner named in configuration need not
	// carry the role in the database.
	if actor.Role != roleAdmin && actor.Role != roleOwner && !s.isOwner(actor.Email) {
		respondJSON(w, http.StatusForbidden, apiError{Error: "Forbidden: administrator access required"})
		return
	}
	if s.db == nil {
		respondJSON(w, http.StatusNotImplemented, apiError{Error: "Database storage not enabled"})
		return
	}

	// s.db read here, on the request goroutine, exactly as every other handler in this
	// package reads it -- and handed to the engine, which reads no Server field of its own.
	outcome := s.RotateVisitorSessionSecret(r.Context(), s.db, visitorSessionTriggerManual, actor.Email, r)
	if !outcome.committed() {
		// 409 rather than 500: nothing is broken here, the fleet was not in a state to switch.
		// The whole outcome is still the body, reason included, because a UI that can only say
		// "it failed" is the invisible-abort problem wearing a status code.
		respondJSON(w, http.StatusConflict, outcome)
		return
	}
	respondJSON(w, http.StatusOK, outcome)
}
