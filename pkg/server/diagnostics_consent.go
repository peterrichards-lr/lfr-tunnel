package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"lfr-tunnel/pkg/db"
)

// Diagnostic log collection, and the consent that gates it (#1696).
//
// An administrator can ask a client to hand over its diagnostic logs. Those logs are a
// record of what the developer was working on -- see diagnosticsLogContents below -- so
// collection is gated on that user having explicitly turned it on, and the gateway is
// where that is enforced. The client does not decide; it reads its state from an
// authenticated exchange, and the request simply never leaves here for somebody who has
// not consented. A withdrawal made mid-session is therefore honoured immediately,
// without any message having to reach a running client.
//
// Consent is binary and defaults to off. A tri-state (granted / denied / ask me) with an
// in-client prompt was designed and then dropped: every hard problem in it came from the
// prompt rather than from the consent -- no TTY under Docker, `ldm`, CI or a service, a
// timeout with an admin on a spinner, a modal appearing mid-session on somebody's
// terminal -- and the only thing it bought was resolving users who had never been asked.
// See the issue thread; do not reintroduce it without reading why it went.

const (
	// diagnosticsAuditGranted and friends are the audit actions this feature writes.
	// Named rather than inlined because the refused case is the one that matters most
	// and is the easiest to typo into never being searchable.
	diagnosticsAuditGranted   = "diagnostics.consent_granted"
	diagnosticsAuditWithdrawn = "diagnostics.consent_withdrawn"
	diagnosticsAuditRequested = "diagnostics.collect_requested"
	diagnosticsAuditRefused   = "diagnostics.collect_refused"
)

// The two roles that may collect somebody else's logs. pkg/server spells these as bare
// literals in ~37 places; naming them is not a refactor of those, it is this file
// declining to add the 38th.
const (
	roleAdmin = "admin"
	roleOwner = "owner"
)

// DiagnosticsConsentState is one user's diagnostics standing, as serialised onto
// /api/me for both portals.
//
// Enabled is the whole decision. ConsentedAt is carried so the portal can say "on since
// D" rather than only ever showing a switch, and because an auditable consent record has
// to be able to say when it was given.
type DiagnosticsConsentState struct {
	Enabled     bool   `json:"enabled"`
	ConsentedAt string `json:"consented_at,omitempty"`
}

// diagnosticsConsentState reads a user's standing. A nil user is not consenting, which
// is the same answer as an unknown one -- there is no state in which absence means yes.
func diagnosticsConsentState(user *db.User) DiagnosticsConsentState {
	if user == nil || user.DiagnosticsConsentAt == nil {
		return DiagnosticsConsentState{}
	}
	return DiagnosticsConsentState{
		Enabled:     true,
		ConsentedAt: user.DiagnosticsConsentAt.UTC().Format(time.RFC3339),
	}
}

// diagnosticsCollectionAllowed is THE enforcement predicate. Everything that could cause
// a client's logs to be collected must pass through it.
//
// It reads the stored column rather than anything the caller supplied, and it is called
// at the moment a request would be issued -- not at login, not at client startup -- which
// is what makes a withdrawal take effect immediately.
func diagnosticsCollectionAllowed(user *db.User) bool {
	return user != nil && user.DiagnosticsConsentAt != nil
}

// diagnosticsConsentResponse is what POST /api/me/diagnostics-consent answers with: the
// resulting state, so a caller need not re-fetch /api/me to find out what it now is.
type diagnosticsConsentResponse struct {
	Status  string                  `json:"status"`
	Consent DiagnosticsConsentState `json:"diagnostics_consent"`
}

// diagnosticsCollectResponse is the answer to an administrator's collection request,
// permitted or refused. One type for both so the two cannot drift into reporting the
// consent decision under different keys -- which is the field a caller acts on.
type diagnosticsCollectResponse struct {
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
	// Consented is always present, including in the refusal, so an admin tool can tell a
	// consent refusal from any other 403 without parsing prose.
	Consented bool `json:"diagnostics_consent"`
	// RequestID identifies the queued command, so the audit trail and any later upload can be
	// tied back to the request that caused them (#1763). Empty when nothing was queued.
	RequestID string `json:"request_id,omitempty"`
	// Delivery is the transport outcome, and is deliberately NOT folded into Status.
	//
	// Status answers "was this allowed" and Delivery answers "did it go anywhere"; they are
	// independent, and a consenting user whose client is simply offline is a permitted request
	// that could not be delivered, not a refused one. #1763 predicted that Status would change
	// meaning here -- it should not, because an admin tool that keyed on "consent_granted"
	// would start reading a permitted request as a failed one.
	//
	// One of: queued, already_requested, not_reachable.
	Delivery string `json:"delivery,omitempty"`
	// DeliveryDetail explains a Delivery that is not "queued", in words an admin can act on.
	DeliveryDetail string `json:"delivery_detail,omitempty"`
}

// diagnosticsConsentRequest is the body of POST /api/me/diagnostics-consent.
//
// A plain bool, not a pointer: this endpoint exists only to set the value, so an absent
// field is a malformed request rather than "leave it alone". Withdrawal is spelled
// {"enabled": false} and is a first-class operation, not a delete.
type diagnosticsConsentRequest struct {
	Enabled bool `json:"enabled"`
}

// handleDiagnosticsConsent grants or withdraws this user's diagnostics consent.
//
// A POST of its own rather than a field on PUT /api/me, for the reason
// handlePolicyConsentAccept gives: consent is an event, not a profile attribute, and what
// is being recorded is that it changed at a particular moment from a particular address.
// It also means a withdrawal applies the instant the switch is flipped, instead of
// waiting for somebody to press Save on a form.
func (s *Server) handleDiagnosticsConsent(w http.ResponseWriter, r *http.Request) {
	// getCurrentUserRaw, not getCurrentUser: an owner previewing another role must not be
	// able to grant or withdraw consent as somebody else. Same reasoning as
	// handlePolicyConsentAccept.
	user, err := s.getCurrentUserRaw(r)
	if err != nil || user == nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if s.db == nil {
		http.Error(w, `{"error":"Database storage not enabled"}`, http.StatusNotImplemented)
		return
	}

	var req diagnosticsConsentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid payload"}`, http.StatusBadRequest)
		return
	}

	if req.Enabled {
		now := time.Now().UTC()
		user.DiagnosticsConsentAt = &now
	} else {
		user.DiagnosticsConsentAt = nil
	}

	if err := s.db.UpdateUser(user); err != nil {
		slog.Error("[Diagnostics] Failed to store diagnostics consent", "user", user.ID, "error", err)
		http.Error(w, `{"error":"Failed to save your preference"}`, http.StatusInternalServerError)
		return
	}
	s.invalidateUserCache(user.Email)

	action := diagnosticsAuditWithdrawn
	details := "Withdrew consent for administrators to collect this client's diagnostic logs"
	if req.Enabled {
		action = diagnosticsAuditGranted
		details = "Granted consent for administrators to collect this client's diagnostic logs"
	}
	s.auditDiagnostics(user.Email, action, "user", user.ID, details, r)

	respondJSON(w, http.StatusOK, diagnosticsConsentResponse{
		Status:  "ok",
		Consent: diagnosticsConsentState(user),
	})
}

// The values Status and Delivery take on the wire (#1763).
//
// Named because they are a contract, not prose: the portals and any admin tooling read them to
// tell a consent refusal from a permitted request, and a queued command from one that had
// nowhere to go. The two are separate axes on purpose -- a consenting user whose client is
// offline is a PERMITTED request that could not be DELIVERED, and folding that into one field
// would make it read as a refusal.
const (
	// diagnosticsStatusConsentGranted is the authorisation verdict. It does not depend on
	// whether the client happened to be connected.
	diagnosticsStatusConsentGranted = "consent_granted"

	// diagnosticsDeliveryQueued means the command is waiting for the client's next heartbeat.
	diagnosticsDeliveryQueued = "queued"
	// diagnosticsDeliveryAlreadyRequested means one is already waiting; an admin clicking
	// twice gets this rather than a second command.
	diagnosticsDeliveryAlreadyRequested = "already_requested"
	// diagnosticsDeliveryNotReachable means nothing was queued because nothing could collect
	// it -- see diagnosticsReachability for which case and why.
	diagnosticsDeliveryNotReachable = "not_reachable"
)

// diagnosticsCollectRequest is the body of POST /api/admin/diagnostics/collect.
type diagnosticsCollectRequest struct {
	Email string `json:"email"`
}

// handleAdminDiagnosticsCollect is where an administrator asks for a user's client logs,
// and therefore where consent is enforced.
//
// Dispatched from handleAdminEndpoints, which has already run requireAdmin, but the role
// is re-checked here against the database rather than trusted from that return value.
// This endpoint reaches into another person's machine for a record of what they were
// working on, and is worth one extra read to be certain the caller is genuinely an
// administrator.
//
// That is not belt and braces: requireAdmin's cookie path currently rewrites a stored
// role of "user" to "admin" (#1760), so trusting it would have made this endpoint
// reachable by any logged-in portal user. The re-check stays regardless of when #1760
// lands -- an endpoint that hands over somebody else's data should state its own
// requirement rather than inherit one.
//
// The transport itself is NOT built yet -- that is #1763. A
// consenting user's request is audited and then answered with 501, which is the honest
// answer: permission was granted, the mechanism to act on it does not exist. The refusal
// path below is complete and live today, and it is the half that protects anybody.
func (s *Server) handleAdminDiagnosticsCollect(w http.ResponseWriter, r *http.Request) {
	actor, err := s.getCurrentUserRaw(r)
	if err != nil || actor == nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	// s.isOwner as well as the stored role, because the owner named in configuration
	// need not carry the role in the database.
	if actor.Role != roleAdmin && actor.Role != roleOwner && !s.isOwner(actor.Email) {
		http.Error(w, `{"error":"Forbidden: administrator access required"}`, http.StatusForbidden)
		return
	}
	if s.db == nil {
		http.Error(w, `{"error":"Database storage not enabled"}`, http.StatusNotImplemented)
		return
	}

	var req diagnosticsCollectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid payload"}`, http.StatusBadRequest)
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" {
		http.Error(w, `{"error":"An email address is required"}`, http.StatusBadRequest)
		return
	}

	target, err := s.db.GetUserByEmail(req.Email)
	if err != nil || target == nil {
		http.Error(w, `{"error":"No such user"}`, http.StatusNotFound)
		return
	}

	if !diagnosticsCollectionAllowed(target) {
		// Audited before the response is written, and audited at all, because a refused
		// attempt to collect somebody's data is exactly as worth recording as a
		// successful one -- more so, arguably, since a run of them is a pattern.
		s.auditDiagnostics(actor.Email, diagnosticsAuditRefused, "user", target.ID,
			fmt.Sprintf("Refused: %s has not enabled diagnostic log sharing", target.Email), r)
		respondJSON(w, http.StatusForbidden, diagnosticsCollectResponse{
			Error: fmt.Sprintf("%s has not enabled diagnostic log sharing, so their logs cannot be collected. Ask them to turn it on in Account Settings.", target.Email),
		})
		return
	}

	// The consent state is read back through the same helper the portals are given
	// rather than dereferencing the column here: the audit entry then records exactly
	// what the user was shown, and this line does not have to be correct about a pointer
	// that the guard above is the only thing keeping non-nil.
	s.auditDiagnostics(actor.Email, diagnosticsAuditRequested, "user", target.ID,
		fmt.Sprintf("Requested diagnostic logs from %s (consent given %s)",
			target.Email, diagnosticsConsentState(target).ConsentedAt), r)

	// The transport (#1763). Consent is established above and is not re-established here --
	// this is the one authorisation point, and a second would be a second thing to get wrong.
	//
	// Whether the command can actually be handed over is a separate question from whether it is
	// permitted, and the response says which. A permitted request to a client that is not
	// connected is not an error: the admin asked a reasonable thing at an unreasonable moment,
	// and telling them that is more useful than queueing into a void.
	if s.hasPendingDiagnosticsCommand(target.ID) {
		respondJSON(w, http.StatusAccepted, diagnosticsCollectResponse{
			Status:         diagnosticsStatusConsentGranted,
			Consented:      true,
			Delivery:       diagnosticsDeliveryAlreadyRequested,
			DeliveryDetail: fmt.Sprintf("A collection from %s is already waiting to be delivered. It expires after %s if the client does not pick it up.", target.Email, diagnosticsCommandTTL),
		})
		return
	}

	reach := s.diagnosticsReachability(target.ID)
	if !reach.served {
		respondJSON(w, http.StatusAccepted, diagnosticsCollectResponse{
			Status:         diagnosticsStatusConsentGranted,
			Consented:      true,
			Delivery:       diagnosticsDeliveryNotReachable,
			DeliveryDetail: reach.reason,
		})
		return
	}

	cmd := s.queueDiagnosticsCollect(target.ID, actor.Email)
	respondJSON(w, http.StatusAccepted, diagnosticsCollectResponse{
		Status:    diagnosticsStatusConsentGranted,
		Consented: true,
		Delivery:  diagnosticsDeliveryQueued,
		RequestID: cmd.ID,
		// Honest about where this stops today, and in DeliveryDetail rather than Error,
		// because nothing has gone wrong. The command reaches the client and the client
		// acknowledges it; reading, redacting and uploading the files is the next part of
		// #1763, and an admin told "collected" when nothing was collected would be worse
		// than an admin told exactly this.
		DeliveryDetail: "Requested. The client will acknowledge it within a few seconds; log upload itself is not implemented yet.",
	})
}

// diagnosticsReachability answers whether this gateway can hand a command to a user's client,
// and if not, why not in words an admin can act on.
//
// The distinction matters because only the SERVING gateway's heartbeat response is parsed by the
// client (interceptor.go checks `pingURL == serverURL`). Central receives the heartbeat of an
// edge-hosted session too, but the client ignores central's body, so central cannot deliver to
// one. Forwarding via the edge control channel is the next step; until it exists this says so
// rather than queueing a command that would expire undelivered.
type diagnosticsReach struct {
	served bool
	reason string
}

func (s *Server) diagnosticsReachability(userID string) diagnosticsReach {
	for _, l := range s.registry.ListLeases() {
		if l != nil && l.UserID == userID && l.NodeID == "" {
			return diagnosticsReach{served: true}
		}
	}

	s.edgeLeasesMu.RLock()
	edgeNode := ""
	for _, el := range s.edgeLeases[userID] {
		edgeNode = el.NodeID
		break
	}
	s.edgeLeasesMu.RUnlock()

	if edgeNode != "" {
		return diagnosticsReach{reason: fmt.Sprintf(
			"This user's tunnel is served by edge node %s. Only the gateway serving a session can deliver to it, and forwarding a collection request to an edge is not implemented yet.", edgeNode)}
	}
	return diagnosticsReach{reason: "This user has no connected tunnel right now, so there is nothing to collect from. Ask them to start their client and try again."}
}

// auditDiagnostics writes one diagnostics audit entry, synchronously.
//
// Deliberately not s.writeAudit, which fires a goroutine and drops the error: the entry
// IS the record that somebody's logs were asked for, and a trail with silent gaps is
// worse than one known to be incomplete. Modelled on auditAnalyticsView, which made the
// same call for the same reason. A failure is logged loudly and does not fail the
// request -- refusing to record consent because the audit write failed would be worse
// than recording it and saying so.
func (s *Server) auditDiagnostics(actorID, action, targetType, targetID, details string, r *http.Request) {
	if s.db == nil {
		return
	}
	ip := ""
	if r != nil {
		ip = s.clientIP(r)
	}
	if err := s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    actorID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Details:    details,
		IPAddress:  ip,
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		slog.Error("[Audit] Could not record a diagnostics event",
			"action", action, "actor", actorID, "target", targetID, "error", err)
	}
}
