package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"lfr-tunnel/pkg/db"
)

// HTTP for the `approval` state of never_expires.tokens (#2267).

// handleRequestTokenPermanence records that the caller wants one of their own tokens never to
// expire. POST /api/tokens/{id}/request-permanence.
func (s *Server) handleRequestTokenPermanence(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/tokens/"), "/request-permanence")
	pat, err := s.portalService.RequestTokenPermanence(user, id, s.clientIP(r))
	if err != nil {
		respondWithError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, pat)
}

// handleAdminListTokenPermanenceRequests answers the admin queue.
// GET /api/admin/tokens/permanence-requests.
func (s *Server) handleAdminListTokenPermanenceRequests(w http.ResponseWriter, r *http.Request, actor string) {
	list, err := s.portalService.AdminListTokenPermanenceRequests()
	if err != nil {
		respondWithError(w, err)
		return
	}
	// An empty queue answers [] and not null: a portal that has to distinguish "no requests"
	// from "the call failed" should not have to do it by reading a JSON literal.
	if list == nil {
		list = []*db.PersonalAccessToken{}
	}
	respondJSON(w, http.StatusOK, list)
}

// handleAdminDecideTokenPermanence grants or denies one request.
// POST /api/admin/tokens/{id}/permanence with {"grant": true|false}.
func (s *Server) handleAdminDecideTokenPermanence(w http.ResponseWriter, r *http.Request, actor string) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/admin/tokens/"), "/permanence")

	// A POINTER, so "the body did not say" is distinguishable from "the body said deny".
	// A plain bool would make a malformed or truncated body read as a denial, which is a
	// decision recorded against an admin who never made it.
	var req struct {
		Grant *bool `json:"grant"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Grant == nil {
		http.Error(w, `{"error":"Request must say grant: true or grant: false"}`, http.StatusBadRequest)
		return
	}

	pat, err := s.portalService.AdminDecideTokenPermanence(actor, id, *req.Grant, s.clientIP(r))
	if err != nil {
		respondWithError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, pat)
}
