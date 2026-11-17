package server

import (
	"net/http"
)

// handleClientTokenCheck answers whether the access token in X-Auth-Token is usable here (#2458).
//
// `lfr-tunnel login` asks this before it reports success. It used to save whatever it was handed
// and say "Successfully authenticated", so a revoked, expired or wrong-gateway token surfaced
// only later, as a 401 from the tunnel itself (#2457).
//
// It answers with the token's verdict and nothing else, and changes nothing: no last-used update
// (a login check is not a use), no audit row, no lease. Every outcome is a distinct status so the
// client never has to guess:
//
//	204  the token is valid
//	401  it is not -- missing, unknown, revoked, expired, or its user is not approved; one answer
//	     for all of them, as registration gives (#2342)
//	503  the token store could not be read, so nothing is known about the token (#2347)
//	421  this is an edge, which holds no tokens to check against (#2123)
//
// The token is read from the header only, never the query string, so it cannot land in an access
// log. The path sits under /api/, so the per-IP rate limiter applies as it does to registration.
func (s *Server) handleClientTokenCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// An edge would refuse every token unread -- checkPAT returns tokenRefused on a nil store --
	// and a good token reported as bad is the exact failure this endpoint exists to end.
	if s.holdsNoControlPlaneData() {
		s.respondMisdirected(w, r)
		return
	}
	switch _, _, verdict := s.checkPAT(r.Header.Get("X-Auth-Token")); verdict {
	case tokenValid:
		w.WriteHeader(http.StatusNoContent)
	case tokenStoreUnavailable:
		respondJSON(w, http.StatusServiceUnavailable, apiError{Error: tokenStoreUnavailableMessage})
	default:
		respondJSON(w, http.StatusUnauthorized, apiError{Error: tokenCheckRefused})
	}
}

// tokenCheckRefused is the token check's one answer for every refusal, as registration's is.
const tokenCheckRefused = "unauthorized"
