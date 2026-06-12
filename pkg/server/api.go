package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"lfr-tunnel/pkg/db"
)

// getCurrentUser is a helper to extract the authenticated user from the session cookie
func (s *Server) getCurrentUser(r *http.Request) (*db.User, error) {
	cookie, err := r.Cookie("lfr_session")
	if err != nil {
		return nil, err
	}
	sessionRaw, ok := s.portalMap.Load("admin_session_" + cookie.Value)
	if !ok {
		return nil, http.ErrNoCookie
	}
	sessionData, ok := sessionRaw.(PortalSessionData)
	if !ok {
		return nil, http.ErrNoCookie
	}

	// Handle Owner
	if s.cfg.Owner.UserID != "" && strings.EqualFold(sessionData.Email, s.cfg.Owner.UserID) {
		// Try to get from DB first to get the correct DB ID if it exists
		if s.db != nil {
			if u, err := s.db.GetUserByEmail(sessionData.Email); err == nil {
				return u, nil
			}
		}
		// Fallback to artificial user
		return &db.User{
			ID:        s.cfg.Owner.UserID,
			Email:     sessionData.Email,
			FirstName: s.cfg.Owner.Name,
			Role:      "owner",
		}, nil
	}

	if s.db == nil {
		return nil, http.ErrNoCookie
	}
	return s.db.GetUserByEmail(sessionData.Email)
}

// handleGetMe returns the currently authenticated user's profile and role.
func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var activeLeases []map[string]interface{}
	if s.registry != nil {
		leases := s.registry.ListLeases()
		for _, l := range leases {
			if l.UserID == user.ID {
				activeLeases = append(activeLeases, map[string]interface{}{
					"subdomain_prefix": l.SubdomainPrefix,
					"full_host":        l.FullHost,
					"status":           l.Status,
					"bytes_in":         l.BytesIn,
					"bytes_out":        l.BytesOut,
					"created_at":       l.CreatedAt,
				})
			}
		}
	}

	resp := map[string]interface{}{
		"id":                 user.ID,
		"email":              user.Email,
		"first_name":         user.FirstName,
		"last_name":          user.LastName,
		"preferred_name":     user.PreferredName,
		"role":               user.Role,
		"timezone":           user.Timezone,
		"auth_method":        user.AuthMethod,
		"theme_preference":   user.ThemePreference,
		"notification_prefs": user.NotificationPrefs,
		"last_login_ip":      user.LastLoginIP,
		"tunnels":            activeLeases,
	}

	cookie, err := r.Cookie("lfr_session")
	if err == nil {
		if sessionRaw, ok := s.portalMap.Load("admin_session_" + cookie.Value); ok {
			if sessionData, ok := sessionRaw.(PortalSessionData); ok {
				if sessionData.PreviousLoginAt != nil {
					resp["last_login_at"] = *sessionData.PreviousLoginAt
				}
				if sessionData.KilledPreviousSession {
					resp["killed_previous_session"] = true
					// Unset it so the UI only alerts once
					sessionData.KilledPreviousSession = false
					s.portalMap.Store("admin_session_"+cookie.Value, sessionData)
				}
			}
		}
	}

	s.broadcastMutex.RLock()
	resp["broadcast_message"] = s.broadcastMessage
	s.broadcastMutex.RUnlock()

	respondJSON(w, http.StatusOK, resp)
}

// handleUpdateMe updates the currently authenticated user's profile.
func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req struct {
		FirstName         *string `json:"first_name"`
		LastName          *string `json:"last_name"`
		PreferredName     *string `json:"preferred_name"`
		Timezone          *string `json:"timezone"`
		ThemePreference   *string `json:"theme_preference"`
		NotificationPrefs *string `json:"notification_prefs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request payload"}`, http.StatusBadRequest)
		return
	}

	if req.FirstName != nil {
		user.FirstName = strings.TrimSpace(*req.FirstName)
	}
	if req.LastName != nil {
		user.LastName = strings.TrimSpace(*req.LastName)
	}
	if req.PreferredName != nil {
		user.PreferredName = strings.TrimSpace(*req.PreferredName)
	}
	if req.Timezone != nil {
		user.Timezone = strings.TrimSpace(*req.Timezone)
	}
	if req.ThemePreference != nil {
		user.ThemePreference = strings.TrimSpace(*req.ThemePreference)
	}
	if req.NotificationPrefs != nil {
		user.NotificationPrefs = strings.TrimSpace(*req.NotificationPrefs)
	}

	if err := s.db.UpdateUser(user); err != nil {
		http.Error(w, `{"error":"Failed to update profile"}`, http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleListTokens returns the current user's PATs.
func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	pats, err := s.db.ListPATs(user.ID)
	if err != nil {
		http.Error(w, `{"error":"Failed to list tokens"}`, http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusOK, pats)
}

// handleCreateToken creates a new PAT and returns the raw token exactly once.
func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req struct {
		Name      string `json:"name"`
		ExpiresIn int    `json:"expires_in_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request"}`, http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, `{"error":"Token name is required"}`, http.StatusBadRequest)
		return
	}

	rawToken, err := generateSecureToken()
	if err != nil {
		http.Error(w, `{"error":"Failed to generate token"}`, http.StatusInternalServerError)
		return
	}
	hash := sha256.Sum256([]byte(rawToken))
	hashStr := hex.EncodeToString(hash[:])

	prefix := rawToken[:12]

	var expiresAt *time.Time
	if req.ExpiresIn > 0 {
		t := time.Now().AddDate(0, 0, req.ExpiresIn)
		expiresAt = &t
	}

	pat := &db.PersonalAccessToken{
		UserID:      user.ID,
		TokenHash:   hashStr,
		TokenPrefix: prefix,
		Name:        req.Name,
		ExpiresAt:   expiresAt,
	}

	if err := s.db.CreatePAT(pat); err != nil {
		log.Printf("[API] Failed to save PAT: %v", err)
		http.Error(w, `{"error":"Failed to create token"}`, http.StatusInternalServerError)
		return
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "token.created",
		TargetType: "token",
		TargetID:   prefix,
		IPAddress:  r.Header.Get("X-Real-IP"),
	})

	// Return the raw token EXACTLY ONCE
	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"id":           pat.ID,
		"name":         pat.Name,
		"token_prefix": pat.TokenPrefix,
		"raw_token":    rawToken, // The user MUST copy this now
		"expires_at":   pat.ExpiresAt,
	})
}

// handleDeleteToken revokes a PAT
func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	tokenIDStr := path.Base(r.URL.Path)
	tokenID, err := strconv.ParseInt(tokenIDStr, 10, 64)
	if err != nil {
		http.Error(w, `{"error":"Invalid token ID"}`, http.StatusBadRequest)
		return
	}

	// Make sure the token belongs to this user!
	pats, err := s.db.ListPATs(user.ID)
	if err != nil {
		http.Error(w, `{"error":"Server error"}`, http.StatusInternalServerError)
		return
	}

	ownsToken := false
	for _, p := range pats {
		if p.ID == tokenID {
			ownsToken = true
			break
		}
	}

	if !ownsToken {
		http.Error(w, `{"error":"Not found"}`, http.StatusNotFound)
		return
	}

	if err := s.db.RevokePAT(tokenID); err != nil {
		http.Error(w, `{"error":"Failed to revoke token"}`, http.StatusInternalServerError)
		return
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "token.revoked",
		TargetType: "token",
		TargetID:   strconv.FormatInt(tokenID, 10),
		IPAddress:  r.Header.Get("X-Real-IP"),
	})

	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleGetAnalytics returns analytics data for the authenticated user and globally if admin.
func (s *Server) handleGetAnalytics(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not enabled"}`, http.StatusNotImplemented)
		return
	}

	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	days := 30
	isAdmin := user.Role == "admin" || user.Role == "owner"

	userStats, err := s.db.GetUserAnalytics(user.ID, days)
	if err != nil {
		http.Error(w, `{"error":"Failed to fetch user analytics"}`, http.StatusInternalServerError)
		return
	}

	resp := map[string]interface{}{
		"personal": userStats,
	}

	if isAdmin {
		globalStats, err := s.db.GetGlobalAnalytics(days)
		if err != nil {
			http.Error(w, `{"error":"Failed to fetch global analytics"}`, http.StatusInternalServerError)
			return
		}
		resp["global"] = globalStats
	}

	respondJSON(w, http.StatusOK, resp)
}
