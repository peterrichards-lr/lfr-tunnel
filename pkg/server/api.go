package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
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
		"id":                  user.ID,
		"email":               user.Email,
		"first_name":          user.FirstName,
		"last_name":           user.LastName,
		"preferred_name":      user.PreferredName,
		"role":                user.Role,
		"status":              user.Status,
		"timezone":            user.Timezone,
		"auth_method":         user.AuthMethod,
		"theme_preference":    user.ThemePreference,
		"language_preference": user.LanguagePreference,
		"notification_prefs":  user.NotificationPrefs,
		"last_login_ip":       user.LastLoginIP,
		"tunnels":             activeLeases,
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

	s.targetedMutex.RLock()
	if tm, ok := s.targetedMessages[user.ID]; ok && tm != "" {
		resp["targeted_message"] = tm
	}
	s.targetedMutex.RUnlock()

	s.maintMutex.RLock()
	isMaint := s.maintenanceMode
	maintScheduled := s.maintScheduledAt
	s.maintMutex.RUnlock()

	maintModeStr := "false"
	var secondsLeft int
	if isMaint {
		maintModeStr = "true"
	} else if !maintScheduled.IsZero() && time.Now().Before(maintScheduled) {
		maintModeStr = "pending"
		secondsLeft = int(time.Until(maintScheduled).Seconds())
	}
	resp["maintenance_mode"] = maintModeStr
	if maintModeStr == "pending" {
		resp["maintenance_seconds_left"] = secondsLeft
	}

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
		FirstName          *string `json:"first_name"`
		LastName           *string `json:"last_name"`
		PreferredName      *string `json:"preferred_name"`
		Timezone           *string `json:"timezone"`
		ThemePreference    *string `json:"theme_preference"`
		NotificationPrefs  *string `json:"notification_prefs"`
		LanguagePreference *string `json:"language_preference"`
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
	if req.LanguagePreference != nil {
		user.LanguagePreference = strings.TrimSpace(*req.LanguagePreference)
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
		log.Printf("handleGetAnalytics: db is nil")
		http.Error(w, `{"error":"Database not enabled"}`, http.StatusNotImplemented)
		return
	}

	user, err := s.getCurrentUser(r)
	if err != nil {
		log.Printf("handleGetAnalytics: getCurrentUser failed: %v", err)
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	days := 30
	isAdmin := user.Role == "admin" || user.Role == "owner"

	log.Printf("handleGetAnalytics: user=%s, role=%s, isAdmin=%v", user.Email, user.Role, isAdmin)

	userStats, err := s.db.GetUserAnalytics(user.ID, days)
	if err != nil {
		log.Printf("handleGetAnalytics: GetUserAnalytics failed: %v", err)
		http.Error(w, `{"error":"Failed to fetch user analytics"}`, http.StatusInternalServerError)
		return
	}

	resp := map[string]interface{}{
		"personal": userStats,
	}

	if isAdmin {
		globalStats, err := s.db.GetGlobalAnalytics(days)
		if err != nil {
			log.Printf("handleGetAnalytics: GetGlobalAnalytics failed: %v", err)
			http.Error(w, `{"error":"Failed to fetch global analytics"}`, http.StatusInternalServerError)
			return
		}
		log.Printf("handleGetAnalytics: globalStats loaded successfully (TopUsers: %d, Daily: %d)", len(globalStats.TopUsers), len(globalStats.Daily))
		resp["global"] = globalStats
	}

	respondJSON(w, http.StatusOK, resp)
}

// handleMFASetup generates a new TOTP secret and returns setup details.
func (s *Server) handleMFASetup(w http.ResponseWriter, r *http.Request) {
	u, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	secret, err := GenerateTOTPSecret()
	if err != nil {
		http.Error(w, `{"error":"Failed to generate secret"}`, http.StatusInternalServerError)
		return
	}

	// URL format compliant with standard authenticator apps (Google/Microsoft Auth, 1Password, etc.)
	otpauthURL := fmt.Sprintf("otpauth://totp/Liferay%%20Tunnel:%s?secret=%s&issuer=Liferay%%20Tunnel", u.Email, secret)

	respondJSON(w, http.StatusOK, map[string]string{
		"secret":      secret,
		"otpauth_url": otpauthURL,
	})
}

// handleMFAEnable validates the provided TOTP code and activates MFA for the user.
func (s *Server) handleMFAEnable(w http.ResponseWriter, r *http.Request) {
	u, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request"}`, http.StatusBadRequest)
		return
	}

	if !ValidateTOTP(req.Secret, req.Code) {
		http.Error(w, `{"error":"Invalid verification code"}`, http.StatusBadRequest)
		return
	}

	if s.db != nil {
		u.TOTPSecret = req.Secret
		u.TOTPEnabled = true
		if err := s.db.UpdateUser(u); err != nil {
			http.Error(w, `{"error":"Failed to enable MFA"}`, http.StatusInternalServerError)
			return
		}
		s.writeAudit(u.Email, "user.mfa_enabled", "user", u.Email, "", r)
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

// handleMFADisable deactivates MFA for the authenticated user after validating their code.
func (s *Server) handleMFADisable(w http.ResponseWriter, r *http.Request) {
	u, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request"}`, http.StatusBadRequest)
		return
	}

	if !ValidateTOTP(u.TOTPSecret, req.Code) {
		http.Error(w, `{"error":"Invalid verification code"}`, http.StatusBadRequest)
		return
	}

	if s.db != nil {
		u.TOTPSecret = ""
		u.TOTPEnabled = false
		if err := s.db.UpdateUser(u); err != nil {
			http.Error(w, `{"error":"Failed to disable MFA"}`, http.StatusInternalServerError)
			return
		}
		s.writeAudit(u.Email, "user.mfa_disabled", "user", u.Email, "", r)
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

// handleMFAVerify completes the 2FA login verification step and issues the final session token.
func (s *Server) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TempToken string `json:"temp_token"`
		Code      string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request"}`, http.StatusBadRequest)
		return
	}

	val, ok := s.portalMap.LoadAndDelete("pre_auth_" + req.TempToken)
	if !ok {
		http.Error(w, `{"error":"Session expired or invalid"}`, http.StatusUnauthorized)
		return
	}

	preAuth := val.(PortalSessionData)
	if time.Now().After(preAuth.ExpiresAt) {
		http.Error(w, `{"error":"Session has expired"}`, http.StatusUnauthorized)
		return
	}

	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusInternalServerError)
		return
	}

	user, err := s.db.GetUserByEmail(preAuth.Email)
	if err != nil {
		http.Error(w, `{"error":"User not found"}`, http.StatusUnauthorized)
		return
	}

	if !ValidateTOTP(user.TOTPSecret, req.Code) {
		// Put the pre-auth session back so they can try again (with a fresh 5 minute lifetime)
		preAuth.ExpiresAt = time.Now().Add(5 * time.Minute)
		s.portalMap.Store("pre_auth_"+req.TempToken, preAuth)
		http.Error(w, `{"error":"Invalid verification code"}`, http.StatusUnauthorized)
		return
	}

	// MFA Validation Success -> Issue Portal Session
	sessionToken, _ := generateSecureToken()
	clientIP := getClientIP(r)

	var previousLoginAt *time.Time
	if user.LastLoginAt != nil {
		prev := *user.LastLoginAt
		previousLoginAt = &prev
	}

	// Update user login audit metrics
	now := time.Now().UTC()
	user.LastLoginAt = &now
	user.LastLoginIP = clientIP
	_ = s.db.UpdateUser(user)

	killedPreviousSession := false
	s.portalMap.Range(func(key, value interface{}) bool {
		k := key.(string)
		if strings.HasPrefix(k, "admin_session_") {
			sessionData := value.(PortalSessionData)
			if sessionData.Email == user.Email {
				s.portalMap.Delete(k)
				killedPreviousSession = true
			}
		}
		return true
	})

	s.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:                 user.Email,
		ExpiresAt:             time.Now().Add(s.cfg.PortalSessionDuration),
		ClientIP:              clientIP,
		PreviousLoginAt:       previousLoginAt,
		KilledPreviousSession: killedPreviousSession,
	})

	s.writeAudit(user.Email, "admin.login", "system", "admin", "Admin logged into dashboard via magic link + MFA", r)

	cookie := &http.Cookie{
		Name:     "lfr_session",
		Value:    sessionToken,
		Path:     "/",
		Expires:  time.Now().Add(s.cfg.PortalSessionDuration),
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, cookie)

	respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

// performGDPRDeletionAndAnonymization executes a complete, compliant account deletion & anonymization.
func (s *Server) performGDPRDeletionAndAnonymization(email string, r *http.Request) error {
	if s.db == nil {
		return errors.New("database not configured")
	}

	user, err := s.db.GetUserByEmail(email)
	if err != nil {
		return err
	}

	// 1. Kick any active tunnels matching this user
	leases := s.registry.ListLeases()
	for _, lease := range leases {
		if lease.UserID == user.ID || strings.EqualFold(lease.UserID, user.Email) {
			s.registry.KickLease(lease.SubdomainPrefix)
		}
	}

	// 2. Revoke and delete all personal access tokens
	pats, err := s.db.ListPATs(user.ID)
	if err == nil {
		for _, pat := range pats {
			_ = s.db.RevokePAT(pat.ID)
		}
	}

	// 3. Generate a secure, unique, and anonymized user ID hash for GDPR compliance
	h := sha256.Sum256([]byte(user.Email))
	anonymizedID := fmt.Sprintf("gdpr-deleted-user-%s", hex.EncodeToString(h[:8]))

	// 4. Anonymize historical audit logs and bandwidth metrics
	_ = s.db.AnonymizeUserData(user.ID, anonymizedID)
	if user.Email != user.ID {
		_ = s.db.AnonymizeUserData(user.Email, anonymizedID)
	}

	// 5. Send a final GDPR-deleted/anonymized confirmation email BEFORE purging profile
	if s.mailSender != nil {
		subject := s.GetTranslation(user.LanguagePreference, "account_deleted_subject")
		greetingName := user.FirstName
		if greetingName == "" {
			greetingName = "there"
		}
		body := fmt.Sprintf(`Hi %s,<br/><br/>
Your Liferay Tunnel account has been successfully deleted and anonymised in accordance with your Right to Be Forgotten (GDPR).<br/><br/>
Your profile details (first name, last name, and preferences) and all active personal access tokens have been completely purged from our servers, and your historical bandwidth metrics and audit trails have been permanently anonymised.<br/><br/>
Best regards,<br/>
Liferay Tunnel Team`, html.EscapeString(greetingName))

		plainBody := fmt.Sprintf("Hi %s,\n\nYour Liferay Tunnel account has been successfully deleted and anonymised under GDPR.\n\nBest regards,\nLiferay Tunnel Team", greetingName)
		_ = s.mailSender.Send(user.Email, subject, body, plainBody)
	}

	// 6. Delete the actual profile record from the users database entirely
	err = s.db.DeleteUser(user.ID)
	if err != nil {
		return err
	}

	// 7. Write an anonymous audit log confirming account deletion
	s.writeAudit("system", "user.gdpr_deleted", "user", anonymizedID, "User account successfully deleted and anonymised", r)

	return nil
}

// handleSelfDeleteAccount handles user self-initiated deletion from their Account Settings.
func (s *Server) handleSelfDeleteAccount(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Prevent Owner from self-deleting
	if s.cfg.Owner.UserID != "" && strings.EqualFold(user.Email, s.cfg.Owner.UserID) {
		http.Error(w, `{"error":"Forbidden: The system Owner account cannot be deleted. Please transfer ownership first."}`, http.StatusForbidden)
		return
	}

	var req struct {
		ConfirmEmail string `json:"confirm_email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request payload"}`, http.StatusBadRequest)
		return
	}

	if !strings.EqualFold(req.ConfirmEmail, user.Email) {
		http.Error(w, `{"error":"Email confirmation does not match your active account email address."}`, http.StatusBadRequest)
		return
	}

	err = s.performGDPRDeletionAndAnonymization(user.Email, r)
	if err != nil {
		http.Error(w, `{"error":"Failed to delete and anonymise account"}`, http.StatusInternalServerError)
		return
	}

	// Clear session cookie
	cookie := &http.Cookie{
		Name:     "lfr_session",
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   r.TLS != nil,
	}
	http.SetCookie(w, cookie)

	respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

// handleAdminDeleteUser processes an admin-initiated user account deletion (GDPR).
func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request, actor string) {
	email, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/admin/users/"))
	if err != nil {
		http.Error(w, `{"error":"Invalid user email"}`, http.StatusBadRequest)
		return
	}

	user, err := s.db.GetUserByEmail(email)
	if err != nil {
		if err == db.ErrNotFound {
			http.Error(w, `{"error":"User not found"}`, http.StatusNotFound)
		} else {
			http.Error(w, `{"error":"Failed to find user"}`, http.StatusInternalServerError)
		}
		return
	}

	// Prevent deleting Owner
	if s.cfg.Owner.UserID != "" && strings.EqualFold(user.Email, s.cfg.Owner.UserID) {
		http.Error(w, `{"error":"Forbidden: Cannot delete the system Owner account."}`, http.StatusForbidden)
		return
	}

	err = s.performGDPRDeletionAndAnonymization(user.Email, r)
	if err != nil {
		http.Error(w, `{"error":"Failed to delete and anonymise user"}`, http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
}
