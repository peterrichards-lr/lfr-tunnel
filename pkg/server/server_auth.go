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

// isValidToken checks if a token is valid, checking personal access tokens (PATs)
// in the database.
func (s *Server) isValidToken(token string) (*db.User, bool) {
	if token == "" {
		return nil, false
	}
	if s.db != nil {
		hashBytes := sha256.Sum256([]byte(token))
		tokenHash := hex.EncodeToString(hashBytes[:])

		pat, err := s.db.GetPATByHash(tokenHash)
		if err == nil {
			now := time.Now().UTC()
			if pat.RevokedAt == nil && (pat.ExpiresAt == nil || pat.ExpiresAt.After(now)) {
				user, err := s.db.GetUser(pat.UserID)
				if err == nil && user.Status == "approved" {
					// Update last used asynchronously
					dbConn := s.db
					go func(patID int64) {
						if err := dbConn.UpdatePATUsed(patID); err != nil {
							slog.Info(fmt.Sprintf("[Server] Failed to update PAT last used time: %v", err))
						}
					}(pat.ID)
					return user, true
				}
			}
		}
	}

	return nil, false
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	var actorEmail string
	var actorRole string
	var authenticated bool

	// 1. Check HTTP-only cookie
	cookie, err := r.Cookie("lfr_session")
	if err == nil && cookie.Value != "" {
		if val, ok := s.portalMap.Load("admin_session_" + cookie.Value); ok {
			sessionData := val.(PortalSessionData)
			if time.Now().Before(sessionData.ExpiresAt) {
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
				s.portalMap.Store("admin_session_"+cookie.Value, sessionData)
			} else {
				s.portalMap.Delete("admin_session_" + cookie.Value)
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

		if token != "" && strings.HasPrefix(token, "lfr_pat_") && s.db != nil {
			hashBytes := sha256.Sum256([]byte(token))
			tokenHash := hex.EncodeToString(hashBytes[:])

			pat, err := s.db.GetPATByHash(tokenHash)
			if err == nil {
				now := time.Now().UTC()
				if pat.RevokedAt == nil && (pat.ExpiresAt == nil || pat.ExpiresAt.After(now)) {
					user, err := s.db.GetUser(pat.UserID)
					if err == nil && user.Status == "approved" && (user.Role == "admin" || user.Role == "owner") {
						go func(patID int64) { _ = s.db.UpdatePATUsed(patID) }(pat.ID) //nolint:errcheck
						actorEmail = user.Email
						actorRole = user.Role
						authenticated = true
					}
				}
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
