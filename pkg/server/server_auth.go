package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"lfr-tunnel/pkg/db"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Credential validation lives in one place per credential type (#1308).
//
// It used to be copied: isValidToken and requireAdmin's PAT branch each hashed the token, looked
// it up, checked revocation, checked expiry, checked the user was approved and fired the async
// last-used update. Both were correct, which is exactly why this is worth doing now -- #1304 is
// the evidence of what happens next. There, two paths resolved a portal session and only one
// checked expiry, so an expired session still resolved a user on every /api path routed through
// the other. Nobody wrote that deliberately; the two simply drifted, because there were two.
//
// The role requirement deliberately stays with requireAdmin. "Is this credential currently good?"
// and "may this user do admin things?" are different questions, and folding the second in here
// would mean every caller had to opt out of it.

// validatePAT resolves a personal access token to its user, or reports that it is not currently
// usable. A nil error from the lookup is not enough: the token must also be unrevoked, unexpired,
// and belong to an approved user.
func (s *Server) validatePAT(token string) (*db.User, *db.PersonalAccessToken, bool) {
	if token == "" || s.db == nil {
		return nil, nil, false
	}

	hashBytes := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(hashBytes[:])

	pat, err := s.db.GetPATByHash(tokenHash)
	if err != nil {
		return nil, nil, false
	}
	if pat.RevokedAt != nil {
		return nil, nil, false
	}
	if pat.ExpiresAt != nil && !pat.ExpiresAt.After(time.Now().UTC()) {
		return nil, nil, false
	}

	user, err := s.db.GetUser(pat.UserID)
	if err != nil || user == nil || user.Status != "approved" {
		return nil, nil, false
	}
	return user, pat, true
}

// touchPAT records the token as used, without making the caller wait for a write it does not
// depend on. Failure is logged rather than surfaced: a last-used timestamp that did not update is
// not a reason to reject a credential that is otherwise good.
func (s *Server) touchPAT(patID int64) {
	dbConn := s.db
	go func() {
		if err := dbConn.UpdatePATUsed(patID); err != nil {
			slog.Info(fmt.Sprintf("[Server] Failed to update PAT last used time: %v", err))
		}
	}()
}

// isValidToken checks if a token is valid, checking personal access tokens (PATs)
// in the database.
func (s *Server) isValidToken(token string) (*db.User, bool) {
	user, pat, ok := s.validatePAT(token)
	if !ok {
		return nil, false
	}
	s.touchPAT(pat.ID)
	return user, true
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	var actorEmail string
	var actorRole string
	var authenticated bool

	// 1. Check HTTP-only cookie
	cookie, err := r.Cookie("lfr_session")
	if err == nil && cookie.Value != "" {
		if sessionData, ok := s.sessionStore().loadPortalSession(cookie.Value); ok {
			{
				actorEmail = sessionData.Email
				actorRole = "admin"
				if s.db != nil {
					if u, err := s.db.GetUserByEmail(actorEmail); err == nil && u != nil {
						actorRole = u.Role
					}
				}
				if actorRole == "" || actorRole == "user" {
					if s.cfg.Owner.UserID != "" && strings.EqualFold(actorEmail, s.cfg.Owner.UserID) {
						actorRole = "owner"
					} else {
						actorRole = "admin"
					}
				}
				authenticated = true

				// Optional: sliding expiration
				sessionData.ExpiresAt = time.Now().Add(s.cfg.PortalSessionDuration)
				s.sessionStore().storePortalSession(cookie.Value, sessionData)
			}
		}
	}

	// 2. Fallback to API Token (PAT)
	if !authenticated {
		token := r.Header.Get("Authorization")
		if strings.HasPrefix(strings.ToLower(token), "bearer ") {
			token = token[7:]
		} else {
			token = r.URL.Query().Get("token")
		}

		if token != "" && strings.HasPrefix(token, "lfr_pat_") {
			// Same validation as every other PAT caller, with the ROLE requirement kept here --
			// that is the one legitimate difference between this path and isValidToken (#1308).
			if user, pat, ok := s.validatePAT(token); ok && (user.Role == "admin" || user.Role == "owner") {
				s.touchPAT(pat.ID)
				actorEmail = user.Email
				actorRole = user.Role
				authenticated = true
			}
		}
	}

	if !authenticated {
		http.Error(w, `{"error":"Unauthorized: admin access required"}`, http.StatusUnauthorized)
		return "", "", false
	}
	return actorEmail, actorRole, true
}

func (s *Server) isOwner(actor string) bool {
	if s.cfg.Owner.UserID != "" && strings.EqualFold(actor, s.cfg.Owner.UserID) {
		return true
	}
	if s.db != nil {
		if u, err := s.db.GetUserByEmail(actor); err == nil && u != nil {
			return u.Role == "owner"
		}
	}
	return false
}
