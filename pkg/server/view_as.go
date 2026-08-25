package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// View As lets the owner preview the portal as a lower role without keeping a second
// account per role (#1225). It changes the *role* only -- the owner keeps their own
// identity and data, so nobody gains access to another person's records.
//
// Two rules make this safe, and both are enforced here rather than in the UI:
//
//  1. Downgrade only, and only for the owner. The server decides the effective role; the
//     client only asks. There is no input that can raise a privilege.
//  2. A View As session cannot mutate anything. That is refused at the request boundary
//     (see enforceViewAsReadOnly) rather than by disabling controls, because disabling
//     controls means auditing every mutating control in two separate portals and trusting
//     that nobody adds one later without remembering. Greyed-out buttons are courtesy; this
//     is the boundary.
//
// Deliberately not a dry-run mode. That would need every mutating handler to grow a no-op
// path, and the failure mode when one is subtly wrong is performing a destructive action
// the operator believed was simulated. Refusing outright fails the other way.

// viewAsRoles are the roles the owner may preview. Absent from this list, by design:
// "owner", since the override must never raise privilege.
var viewAsRoles = map[string]bool{
	"admin": true,
	"user":  true,
}

// viewAsExemptPaths are the endpoints a View As session may still POST to. Without the
// first, the owner could enter View As and have no way out; without the second, they could
// not log out.
var viewAsExemptPaths = map[string]bool{
	"/api/me/view-as":  true,
	"/api/auth/logout": true,
}

// sessionViewAsRole returns the role this request's session is previewing, or "" when it is
// not in View As. Reads the stored session rather than the resolved user, so it reports
// what was actually set rather than what a handler ended up seeing.
func (s *Server) sessionViewAsRole(r *http.Request) string {
	cookie, err := r.Cookie("lfr_session")
	if err != nil {
		return ""
	}
	sessionData, ok := s.sessionStore().loadPortalSession(cookie.Value)
	if !ok {
		return ""
	}
	return sessionData.ViewAsRole
}

// isMutatingMethod reports whether a method changes state. GET and HEAD are the readable
// ones; OPTIONS is a preflight and changes nothing.
func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// enforceViewAsReadOnly refuses a mutating request from a session that is previewing
// another role, and reports whether it handled the request.
//
// One rule at the boundary, so it cannot be bypassed by a stale page, a hand-written fetch,
// or a control somebody forgets to disable in a year's time.
func (s *Server) enforceViewAsReadOnly(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return false
	}
	if !isMutatingMethod(r.Method) || viewAsExemptPaths[r.URL.Path] {
		return false
	}
	role := s.sessionViewAsRole(r)
	if role == "" {
		return false
	}
	respondJSON(w, http.StatusForbidden, map[string]interface{}{
		"error":     "This session is previewing the portal as '" + role + "', which is read-only. Exit View As to make changes.",
		"view_as":   role,
		"read_only": true,
	})
	return true
}

// handleViewAs sets or clears the previewed role for the current session.
//
// Authorised against the caller's *real* role, deliberately: the resolved user is already
// downgraded while View As is active, so checking that would strand the owner in the
// preview with no way back.
func (s *Server) handleViewAs(w http.ResponseWriter, r *http.Request) {
	realUser, err := s.getCurrentUserRaw(r)
	if err != nil || realUser == nil {
		respondJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "Not authenticated"})
		return
	}
	if !strings.EqualFold(realUser.Role, "owner") {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{
			"error": "Only the owner can preview the portal as another role.",
		})
		return
	}

	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "Invalid request JSON"})
		return
	}

	role := strings.ToLower(strings.TrimSpace(req.Role))
	if role != "" && !viewAsRoles[role] {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "Unsupported role. View As can preview 'admin' or 'user', or send an empty role to exit.",
		})
		return
	}

	cookie, err := r.Cookie("lfr_session")
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "Not authenticated"})
		return
	}
	sessionData, ok := s.sessionStore().loadPortalSession(cookie.Value)
	if !ok {
		respondJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "Session not found"})
		return
	}

	sessionData.ViewAsRole = role
	s.sessionStore().storePortalSession(cookie.Value, sessionData)

	// An owner adopting another role is worth a record even though it grants nothing.
	action, details := "view_as_exit", "Left View As"
	if role != "" {
		action, details = "view_as_enter", "Previewing the portal as '"+role+"' (read-only)"
	}
	s.writeAudit(realUser.Email, action, "session", realUser.Email, details, r)

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"view_as":   role,
		"read_only": role != "",
	})
}
