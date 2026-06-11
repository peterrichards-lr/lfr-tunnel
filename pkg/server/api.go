package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"path"
	"strconv"
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
	return s.db.GetUserByEmail(sessionData.Email)
}

// handleGetMe returns the currently authenticated user's profile and role.
func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"id":         user.ID,
		"email":      user.Email,
		"first_name": user.FirstName,
		"last_name":  user.LastName,
		"role":       user.Role,
	})
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
