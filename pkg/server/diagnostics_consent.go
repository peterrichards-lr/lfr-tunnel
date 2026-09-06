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
// The transport itself is NOT built yet -- see the issue filed alongside #1696. A
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

	respondJSON(w, http.StatusNotImplemented, diagnosticsCollectResponse{
		Status:    "consent_granted",
		Consented: true,
		Error:     "Consent is in place, but log collection is not available on this gateway yet.",
	})
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
