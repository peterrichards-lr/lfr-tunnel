package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
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

// getCurrentUserOrToken extracts the authenticated user from session cookie or X-Auth-Token/Bearer headers.
func (s *Server) getCurrentUserOrToken(r *http.Request) (*db.User, error) {
	if user, err := s.getCurrentUser(r); err == nil {
		return user, nil
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.URL.Query().Get("auth_token")
	}
	if token == "" {
		token = r.Header.Get("X-Auth-Token")
	}
	if token == "" {
		if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			token = authHeader[7:]
		}
	}

	if token != "" {
		if user, ok := s.isValidToken(token); ok && user != nil {
			return user, nil
		}
	}

	return nil, errors.New("unauthorized")
}

// handleGetMe returns the currently authenticated user's profile and role.
func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	s.portalActivityMu.Lock()
	s.lastPortalActivity[user.ID] = time.Now()
	s.portalActivityMu.Unlock()

	var sessionToken string
	cookie, err := r.Cookie("lfr_session")
	if err == nil {
		sessionToken = cookie.Value
	}

	resp := s.getUserTelemetryData(user, sessionToken)
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

func (s *Server) handleUpdateOnboarding(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req struct {
		Status   string `json:"status"`
		LastStep string `json:"last_step"`
		IsRerun  bool   `json:"is_rerun"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request payload"}`, http.StatusBadRequest)
		return
	}

	if req.Status == "" {
		http.Error(w, `{"error":"Missing status"}`, http.StatusBadRequest)
		return
	}

	if err := s.db.UpdateUserOnboarding(user.ID, req.Status, req.LastStep, req.IsRerun); err != nil {
		http.Error(w, `{"error":"Failed to update onboarding progress"}`, http.StatusInternalServerError)
		return
	}

	s.writeAudit(user.Email, "user.onboarding_updated", "user", user.ID, fmt.Sprintf("Onboarding status updated to %s (last step: %s, rerun: %t)", req.Status, req.LastStep, req.IsRerun), r)

	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleListTokens returns the current user's PATs (or all PATs for admin/owner).
func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var pats []*db.PersonalAccessToken
	if user.Role == "admin" || user.Role == "owner" {
		pats, err = s.db.ListAllPATs()
	} else {
		pats, err = s.db.ListPATs(user.ID)
	}
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

	if req.ExpiresIn <= 0 && user.Role != "admin" && user.Role != "owner" {
		http.Error(w, `{"error":"Only admins and owners can create non-expiring tokens"}`, http.StatusForbidden)
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

	// Make sure the token belongs to this user (or user is admin/owner)
	ownsToken := false
	if user.Role == "admin" || user.Role == "owner" {
		ownsToken = true
	} else {
		pats, err := s.db.ListPATs(user.ID)
		if err != nil {
			http.Error(w, `{"error":"Server error"}`, http.StatusInternalServerError)
			return
		}
		for _, p := range pats {
			if p.ID == tokenID {
				ownsToken = true
				break
			}
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
	if s.notifications != nil && s.notifications.Sender() != nil {
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
		_ = s.notifications.Sender().Send(user.Email, subject, body, plainBody)
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

// getPortalBaseURL constructs the portal's base URL from the incoming request.
func (s *Server) getPortalBaseURL(r *http.Request) string {
	if r == nil {
		if len(s.cfg.Domains) > 0 {
			return "https://tunnel." + s.cfg.Domains[0]
		}
		return "https://localhost"
	}
	host := r.Host
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "http"
	}

	// If the request Host is a subdomain (other than tunnel. or the raw apex domain),
	// rewrite it to tunnel.<apex_domain> so that links point to the portal.
	for _, domain := range s.cfg.Domains {
		if host == domain {
			return fmt.Sprintf("%s://tunnel.%s", scheme, domain)
		}
		if strings.HasSuffix(host, "."+domain) {
			prefix := strings.TrimSuffix(host, "."+domain)
			if prefix != "tunnel" {
				return fmt.Sprintf("%s://tunnel.%s", scheme, domain)
			}
		}
	}

	return fmt.Sprintf("%s://%s", scheme, host)
}

func (s *Server) sendSubdomainReservedEmail(user *db.User, subdomain, domain string, expiresAt *time.Time, r *http.Request) {
	if s.notifications == nil || s.notifications.Sender() == nil {
		return
	}
	lang := user.LanguagePreference
	baseURL := s.getPortalBaseURL(r)
	portalLink := baseURL + "/portal"

	formattedExpiry := "Never"
	if expiresAt != nil {
		formattedExpiry = expiresAt.Format("2006-01-02 15:04:05 MST")
	}

	body, err := s.renderEmailTemplate(lang, "subdomain_reserved.html", map[string]interface{}{
		"Name":       user.FirstName,
		"Subdomain":  subdomain,
		"Domain":     domain,
		"ExpiresAt":  formattedExpiry,
		"PortalLink": portalLink,
	})
	if err != nil {
		log.Printf("[Server] Failed to render subdomain_reserved email: %v", err)
		return
	}
	subject := fmt.Sprintf("Subdomain Reserved: %s.%s", subdomain, domain)
	plain := fmt.Sprintf("Hi %s,\n\nYou have reserved the subdomain %s.%s.\nExpires on: %s\nPortal: %s", user.FirstName, subdomain, domain, formattedExpiry, portalLink)

	go func() { _ = s.notifications.Sender().Send(user.Email, subject, body, plain) }()
}

func (s *Server) sendExtensionApprovedEmail(user *db.User, subdomain, domain string, expiresAt *time.Time, r *http.Request) {
	if s.notifications == nil || s.notifications.Sender() == nil {
		return
	}
	lang := user.LanguagePreference
	baseURL := s.getPortalBaseURL(r)
	portalLink := baseURL + "/portal"

	formattedExpiry := "Never"
	isPermanent := true
	if expiresAt != nil {
		formattedExpiry = expiresAt.Format("2006-01-02 15:04:05 MST")
		isPermanent = false
	}

	body, err := s.renderEmailTemplate(lang, "extension_approved.html", map[string]interface{}{
		"Name":        user.FirstName,
		"Subdomain":   subdomain,
		"Domain":      domain,
		"ExpiresAt":   formattedExpiry,
		"IsPermanent": isPermanent,
		"PortalLink":  portalLink,
	})
	if err != nil {
		log.Printf("[Server] Failed to render extension_approved email: %v", err)
		return
	}
	subject := fmt.Sprintf("Extension Approved: %s.%s", subdomain, domain)
	plain := fmt.Sprintf("Hi %s,\n\nYour extension request for %s.%s has been approved.\nNew Expiration: %s\nPortal: %s", user.FirstName, subdomain, domain, formattedExpiry, portalLink)

	go func() { _ = s.notifications.Sender().Send(user.Email, subject, body, plain) }()
}

func (s *Server) sendSubdomainDemotedEmail(user *db.User, subdomain, domain string, expiresAt *time.Time, r *http.Request) {
	if s.notifications == nil || s.notifications.Sender() == nil {
		return
	}
	lang := user.LanguagePreference
	baseURL := s.getPortalBaseURL(r)
	portalLink := baseURL + "/portal"

	formattedExpiry := "Never"
	if expiresAt != nil {
		formattedExpiry = expiresAt.Format("2006-01-02 15:04:05 MST")
	}

	body, err := s.renderEmailTemplate(lang, "subdomain_demoted.html", map[string]interface{}{
		"Name":       user.FirstName,
		"Subdomain":  subdomain,
		"Domain":     domain,
		"ExpiresAt":  formattedExpiry,
		"PortalLink": portalLink,
	})
	if err != nil {
		log.Printf("[Server] Failed to render subdomain_demoted email: %v", err)
		return
	}
	subject := fmt.Sprintf("Subdomain Demoted: %s.%s", subdomain, domain)
	plain := fmt.Sprintf("Hi %s,\n\nYour permanent subdomain reservation %s.%s has been demoted back to a standard reservation.\nNew Expiration: %s\nPortal: %s", user.FirstName, subdomain, domain, formattedExpiry, portalLink)

	go func() { _ = s.notifications.Sender().Send(user.Email, subject, body, plain) }()
}

/*
func (s *Server) sendSubdomainExpiredEmail(user *db.User, subdomain, domain string, releasedAt time.Time, r *http.Request) {
	if s.notifications == nil || s.notifications.Sender() == nil {
		return
	}
	lang := user.LanguagePreference
	baseURL := s.getPortalBaseURL(r)
	portalLink := baseURL + "/portal"

	formattedRelease := releasedAt.Format("2006-01-02 15:04:05 MST")

	body, err := s.renderEmailTemplate(lang, "subdomain_expired.html", map[string]interface{}{
		"Name":       user.FirstName,
		"Subdomain":  subdomain,
		"Domain":     domain,
		"ReleasedAt": formattedRelease,
		"PortalLink": portalLink,
	})
	if err != nil {
		log.Printf("[Server] Failed to render subdomain_expired email: %v", err)
		return
	}
	subject := fmt.Sprintf("Subdomain Expired: %s.%s", subdomain, domain)
	plain := fmt.Sprintf("Hi %s,\n\nYour subdomain reservation %s.%s has expired.\nIt will be released to the public pool on: %s\nPortal: %s", user.FirstName, subdomain, domain, formattedRelease, portalLink)

	go func() { _ = s.notifications.Sender().Send(user.Email, subject, body, plain) }()
}
*/

// getUserMaxReservations resolves the maximum reservations limit for a given user,
// taking into account explicit user overrides, role-specific defaults, and server settings.
func (s *Server) getUserMaxReservations(user *db.User) int {
	if user.MaxReservations != nil {
		return *user.MaxReservations
	}

	if s.cfg.RoleSettings != nil {
		if setting, ok := s.cfg.RoleSettings[user.Role]; ok {
			if setting.MaxReservations != nil {
				return *setting.MaxReservations
			}
		}
	}

	if user.Role == "admin" {
		if s.cfg.AdminMaxReservations != nil {
			return *s.cfg.AdminMaxReservations
		}
		return 3
	}
	if user.Role == "owner" {
		if s.cfg.OwnerMaxReservations != nil {
			return *s.cfg.OwnerMaxReservations
		}
		return -1 // Default infinite for owner!
	}
	return s.cfg.DefaultMaxReservations
}

// getUserSubdomainExpiry computes the default expiry date for a subdomain reservation.
// Returns nil if the reservation should be permanent (no expiration).
func (s *Server) getUserSubdomainExpiry(user *db.User) *time.Time {
	days := 7 // default fallback

	if s.cfg.RoleSettings != nil {
		if setting, ok := s.cfg.RoleSettings[user.Role]; ok {
			if setting.SubdomainExpiryDays != nil {
				if *setting.SubdomainExpiryDays <= 0 {
					return nil // Permanent
				}
				days = *setting.SubdomainExpiryDays
			}
		}
	} else {
		// Default fallback for owner if RoleSettings not defined
		if user.Role == "owner" {
			return nil // Owner subdomains do not expire by default
		}
	}

	expiry := time.Now().AddDate(0, 0, days)
	return &expiry
}

// handleListReservations returns a list of reservations held by the current user.
func (s *Server) handleListReservations(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var list []*db.SubdomainReservation
	if user.Role == "admin" || user.Role == "owner" {
		list, err = s.db.ListAllSubdomainReservations()
	} else {
		list, err = s.db.ListSubdomainReservationsByUserID(user.ID)
	}
	if err != nil {
		log.Printf("[API] Failed to list reservations: %v", err)
		http.Error(w, `{"error":"Failed to retrieve reservations"}`, http.StatusInternalServerError)
		return
	}

	var usedCount int
	if user.Role == "admin" || user.Role == "owner" {
		ownList, err := s.db.ListSubdomainReservationsByUserID(user.ID)
		if err == nil {
			usedCount = len(ownList)
		} else {
			usedCount = 0
		}
	} else {
		usedCount = len(list)
	}

	limit := s.getUserMaxReservations(user)

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"reservations": list,
		"limit":        limit,
		"used":         usedCount,
	})
}

// handleCreateReservation reserves a subdomain for 7 days.
func (s *Server) handleCreateReservation(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req struct {
		Subdomain string `json:"subdomain"`
		Domain    string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request"}`, http.StatusBadRequest)
		return
	}

	req.Subdomain = strings.ToLower(strings.TrimSpace(req.Subdomain))
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))

	if req.Subdomain == "" || req.Domain == "" {
		http.Error(w, `{"error":"Subdomain and domain are required"}`, http.StatusBadRequest)
		return
	}

	// Validate subdomain format
	if !isValidSubdomain(req.Subdomain) {
		http.Error(w, `{"error":"Invalid or reserved subdomain format"}`, http.StatusBadRequest)
		return
	}

	// Verify requested domain is supported
	domainSupported := false
	for _, d := range s.cfg.Domains {
		if strings.EqualFold(d, req.Domain) {
			domainSupported = true
			break
		}
	}
	if !domainSupported {
		http.Error(w, `{"error":"Domain is not supported by this gateway"}`, http.StatusBadRequest)
		return
	}

	// Enforce quota limits
	limit := s.getUserMaxReservations(user)

	list, err := s.db.ListSubdomainReservationsByUserID(user.ID)
	if err != nil {
		log.Printf("[API] Failed to check reservations: %v", err)
		http.Error(w, `{"error":"Server error"}`, http.StatusInternalServerError)
		return
	}

	// Filter out expired reservations that are not in quarantine
	activeCount := 0
	for _, res := range list {
		if res.ExpiresAt == nil || res.ExpiresAt.After(time.Now()) {
			activeCount++
		}
	}

	if limit >= 0 && activeCount >= limit {
		http.Error(w, `{"error":"Subdomain reservation quota limit reached"}`, http.StatusBadRequest)
		return
	}

	// Check if already reserved
	existing, err := s.db.GetSubdomainReservationByName(req.Subdomain, req.Domain)
	if err == nil && existing != nil {
		// Check if expired
		if existing.ExpiresAt != nil && existing.ExpiresAt.Before(time.Now()) {
			// Check quarantine
			quarantineCutoff := existing.ExpiresAt.AddDate(0, 0, s.cfg.SubdomainQuarantineDays)
			if time.Now().Before(quarantineCutoff) {
				// Quarantined! Can only reclaim if previous owner is the same user
				if existing.UserID != user.ID {
					http.Error(w, `{"error":"Subdomain is currently quarantined by another user"}`, http.StatusConflict)
					return
				}
				// Reclaimable! Delete the expired one first
				_ = s.db.DeleteSubdomainReservation(existing.ID)
			} else {
				// Past quarantine, delete expired reservation
				_ = s.db.DeleteSubdomainReservation(existing.ID)
			}
		} else {
			// Active reservation by someone else
			http.Error(w, `{"error":"Subdomain is already reserved"}`, http.StatusConflict)
			return
		}
	}

	// Create reservation
	expiry := s.getUserSubdomainExpiry(user)
	res := &db.SubdomainReservation{
		UserID:    user.ID,
		Subdomain: req.Subdomain,
		Domain:    req.Domain,
		ExpiresAt: expiry,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.db.CreateSubdomainReservation(res); err != nil {
		log.Printf("[API] Failed to save reservation: %v", err)
		http.Error(w, `{"error":"Failed to create reservation"}`, http.StatusInternalServerError)
		return
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "subdomain.reserved",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", req.Subdomain, req.Domain),
		Details:    fmt.Sprintf("Subdomain reserved. ExpiresAt: %v", expiry),
		IPAddress:  getClientIP(r),
		CreatedAt:  time.Now(),
	})

	s.sendSubdomainReservedEmail(user, req.Subdomain, req.Domain, expiry, r)

	respondJSON(w, http.StatusOK, res)
}

// handleDeleteReservation deletes a reservation.
func (s *Server) handleDeleteReservation(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/api/portal/reservations/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, `{"error":"Invalid reservation ID"}`, http.StatusBadRequest)
		return
	}

	res, err := s.db.GetSubdomainReservation(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, `{"error":"Reservation not found"}`, http.StatusNotFound)
		} else {
			http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		}
		return
	}

	if res.UserID != user.ID && user.Role != "admin" && user.Role != "owner" {
		http.Error(w, `{"error":"Forbidden: cannot delete other user's reservation"}`, http.StatusForbidden)
		return
	}

	if err := s.db.DeleteSubdomainReservation(id); err != nil {
		log.Printf("[API] Failed to delete reservation: %v", err)
		http.Error(w, `{"error":"Failed to delete reservation"}`, http.StatusInternalServerError)
		return
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "subdomain.released",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", res.Subdomain, res.Domain),
		Details:    "Subdomain reservation deleted / released by owner",
		IPAddress:  getClientIP(r),
		CreatedAt:  time.Now(),
	})

	respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

// handleRequestExtension requests an extension for a reservation.
func (s *Server) handleRequestExtension(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	suffix := strings.TrimPrefix(r.URL.Path, "/api/portal/reservations/")
	parts := strings.Split(suffix, "/")
	if len(parts) == 0 {
		http.Error(w, `{"error":"Invalid reservation ID"}`, http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, `{"error":"Invalid reservation ID"}`, http.StatusBadRequest)
		return
	}

	res, err := s.db.GetSubdomainReservation(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, `{"error":"Reservation not found"}`, http.StatusNotFound)
		} else {
			http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		}
		return
	}

	if res.UserID != user.ID && user.Role != "admin" && user.Role != "owner" {
		http.Error(w, `{"error":"Forbidden: cannot extend other user's reservation"}`, http.StatusForbidden)
		return
	}

	if res.ExpiresAt == nil {
		http.Error(w, `{"error":"Permanent reservations cannot be extended"}`, http.StatusBadRequest)
		return
	}

	res.ExtensionRequested = true
	if err := s.db.UpdateSubdomainReservation(res); err != nil {
		log.Printf("[API] Failed to update reservation: %v", err)
		http.Error(w, `{"error":"Failed to request extension"}`, http.StatusInternalServerError)
		return
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "subdomain.extension_requested",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", res.Subdomain, res.Domain),
		Details:    "Extension requested for subdomain reservation",
		IPAddress:  getClientIP(r),
		CreatedAt:  time.Now(),
	})

	s.notifications.SendAdminAlert("alert_notify_extension_requested", "LFR Tunnel Alert: Subdomain Extension Requested",
		fmt.Sprintf("User %s has requested an extension for subdomain %s.%s.", user.Email, res.Subdomain, res.Domain))

	respondJSON(w, http.StatusOK, res)
}

// handlePromoteReservation promotes an active random tunnel lease to a reservation.
func (s *Server) handlePromoteReservation(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req struct {
		Subdomain string `json:"subdomain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request"}`, http.StatusBadRequest)
		return
	}

	req.Subdomain = strings.ToLower(strings.TrimSpace(req.Subdomain))
	if req.Subdomain == "" {
		http.Error(w, `{"error":"Subdomain is required"}`, http.StatusBadRequest)
		return
	}

	var activeLease *TunnelLease
	if s.registry != nil {
		leases := s.registry.ListLeases()
		for _, l := range leases {
			if l.UserID == user.ID && strings.EqualFold(l.SubdomainPrefix, req.Subdomain) {
				activeLease = l
				break
			}
		}
	}

	if activeLease == nil {
		http.Error(w, `{"error":"No active tunnel session found for this subdomain prefix"}`, http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(activeLease.FullHost, ".", 2)
	domain := "lfr-demo.se"
	if len(parts) == 2 {
		domain = parts[1]
	}

	limit := s.getUserMaxReservations(user)

	list, err := s.db.ListSubdomainReservationsByUserID(user.ID)
	if err != nil {
		log.Printf("[API] Failed to check reservations: %v", err)
		http.Error(w, `{"error":"Server error"}`, http.StatusInternalServerError)
		return
	}

	// Filter out expired reservations that are not in quarantine
	activeCount := 0
	for _, res := range list {
		if res.ExpiresAt == nil || res.ExpiresAt.After(time.Now()) {
			activeCount++
		}
	}

	if limit >= 0 && activeCount >= limit {
		http.Error(w, `{"error":"Quota limit reached: cannot promote to reservation"}`, http.StatusBadRequest)
		return
	}

	existing, err := s.db.GetSubdomainReservationByName(req.Subdomain, domain)
	if err == nil && existing != nil {
		if existing.ExpiresAt == nil || existing.ExpiresAt.After(time.Now()) {
			if existing.UserID != user.ID {
				http.Error(w, `{"error":"Subdomain is already reserved by another user"}`, http.StatusConflict)
				return
			}
			respondJSON(w, http.StatusOK, existing)
			return
		}
		_ = s.db.DeleteSubdomainReservation(existing.ID)
	}

	expiry := s.getUserSubdomainExpiry(user)
	res := &db.SubdomainReservation{
		UserID:    user.ID,
		Subdomain: req.Subdomain,
		Domain:    domain,
		ExpiresAt: expiry,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.db.CreateSubdomainReservation(res); err != nil {
		log.Printf("[API] Failed to promote subdomain: %v", err)
		http.Error(w, `{"error":"Failed to save reservation"}`, http.StatusInternalServerError)
		return
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "subdomain.promoted",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", req.Subdomain, domain),
		Details:    "Subdomain promoted from active random lease to standard reservation",
		IPAddress:  getClientIP(r),
		CreatedAt:  time.Now(),
	})

	s.sendSubdomainReservedEmail(user, req.Subdomain, domain, expiry, r)

	respondJSON(w, http.StatusOK, res)
}

// handleAdminListExtensions lists reservations requesting extension.
func (s *Server) handleAdminListExtensions(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}

	all, err := s.db.ListAllSubdomainReservations()
	if err != nil {
		log.Printf("[API] Failed to list reservations for admin: %v", err)
		http.Error(w, `{"error":"Failed to retrieve reservations"}`, http.StatusInternalServerError)
		return
	}

	var list []*db.SubdomainReservation
	for _, res := range all {
		if res.ExtensionRequested {
			list = append(list, res)
		}
	}

	respondJSON(w, http.StatusOK, list)
}

// handleAdminApproveExtension approves an extension request.
func (s *Server) handleAdminApproveExtension(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}

	suffix := strings.TrimPrefix(r.URL.Path, "/api/admin/reservations/")
	parts := strings.Split(suffix, "/")
	if len(parts) == 0 {
		http.Error(w, `{"error":"Invalid reservation ID"}`, http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, `{"error":"Invalid reservation ID"}`, http.StatusBadRequest)
		return
	}

	var req struct {
		Days      int  `json:"days"`
		Permanent bool `json:"permanent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request"}`, http.StatusBadRequest)
		return
	}

	res, err := s.db.GetSubdomainReservation(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, `{"error":"Reservation not found"}`, http.StatusNotFound)
		} else {
			http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		}
		return
	}

	res.ExtensionRequested = false
	if req.Permanent {
		res.ExpiresAt = nil
	} else {
		baseTime := time.Now()
		if res.ExpiresAt != nil && res.ExpiresAt.After(time.Now()) {
			baseTime = *res.ExpiresAt
		}
		extended := baseTime.AddDate(0, 0, req.Days)
		res.ExpiresAt = &extended
	}

	res.ExpiryWarningSent = 0
	if err := s.db.UpdateSubdomainReservation(res); err != nil {
		log.Printf("[API] Failed to update reservation: %v", err)
		http.Error(w, `{"error":"Failed to approve extension"}`, http.StatusInternalServerError)
		return
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    actor,
		Action:     "subdomain.extension_approved",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", res.Subdomain, res.Domain),
		Details:    fmt.Sprintf("Extension approved. Permanent: %t, Days: %d", req.Permanent, req.Days),
		IPAddress:  getClientIP(r),
		CreatedAt:  time.Now(),
	})

	user, err := s.db.GetUser(res.UserID)
	if err == nil && user != nil {
		s.sendExtensionApprovedEmail(user, res.Subdomain, res.Domain, res.ExpiresAt, r)
	}

	respondJSON(w, http.StatusOK, res)
}

// handleAdminDemoteReservation demotes a permanent reservation.
func (s *Server) handleAdminDemoteReservation(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}

	suffix := strings.TrimPrefix(r.URL.Path, "/api/admin/reservations/")
	parts := strings.Split(suffix, "/")
	if len(parts) == 0 {
		http.Error(w, `{"error":"Invalid reservation ID"}`, http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, `{"error":"Invalid reservation ID"}`, http.StatusBadRequest)
		return
	}

	res, err := s.db.GetSubdomainReservation(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, `{"error":"Reservation not found"}`, http.StatusNotFound)
		} else {
			http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		}
		return
	}

	resOwner, err := s.db.GetUser(res.UserID)
	if err != nil {
		http.Error(w, `{"error":"Failed to retrieve reservation owner"}`, http.StatusInternalServerError)
		return
	}
	res.ExpiresAt = s.getUserSubdomainExpiry(resOwner)
	res.ExtensionRequested = false
	res.ExpiryWarningSent = 0

	if err := s.db.UpdateSubdomainReservation(res); err != nil {
		log.Printf("[API] Failed to update reservation: %v", err)
		http.Error(w, `{"error":"Failed to demote reservation"}`, http.StatusInternalServerError)
		return
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    actor,
		Action:     "subdomain.demoted",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", res.Subdomain, res.Domain),
		Details:    fmt.Sprintf("Permanent reservation demoted. ExpiresAt: %v", res.ExpiresAt),
		IPAddress:  getClientIP(r),
		CreatedAt:  time.Now(),
	})

	if resOwner != nil {
		s.sendSubdomainDemotedEmail(resOwner, res.Subdomain, res.Domain, res.ExpiresAt, r)
	}

	respondJSON(w, http.StatusOK, res)
}

// handleAdminOverrideLimit overrides a user's maximum reservation limit.
func (s *Server) handleAdminOverrideLimit(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}

	suffix := strings.TrimPrefix(r.URL.Path, "/api/admin/users/")
	parts := strings.Split(suffix, "/")
	if len(parts) < 2 || parts[1] != "limit" {
		http.Error(w, `{"error":"Invalid URL path"}`, http.StatusBadRequest)
		return
	}
	email, err := url.PathUnescape(parts[0])
	if err != nil {
		http.Error(w, `{"error":"Invalid user email"}`, http.StatusBadRequest)
		return
	}

	var req struct {
		MaxReservations *int `json:"max_reservations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request"}`, http.StatusBadRequest)
		return
	}

	user, err := s.db.GetUserByEmail(email)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, `{"error":"User not found"}`, http.StatusNotFound)
		} else {
			http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		}
		return
	}

	user.MaxReservations = req.MaxReservations
	if err := s.db.UpdateUser(user); err != nil {
		log.Printf("[API] Failed to update user reservations limit: %v", err)
		http.Error(w, `{"error":"Failed to update quota limit"}`, http.StatusInternalServerError)
		return
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    actor,
		Action:     "user.limit_changed",
		TargetType: "user",
		TargetID:   user.Email,
		Details:    fmt.Sprintf("Max reservations limit overridden. Value: %v", req.MaxReservations),
		IPAddress:  getClientIP(r),
		CreatedAt:  time.Now(),
	})

	respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

// handleAdminOverrideTunnelsLimit overrides a user's maximum active tunnels limit.
func (s *Server) handleAdminOverrideTunnelsLimit(w http.ResponseWriter, r *http.Request, actor string) {
	if s.db == nil {
		http.Error(w, `{"error":"Database not configured"}`, http.StatusNotImplemented)
		return
	}

	suffix := strings.TrimPrefix(r.URL.Path, "/api/admin/users/")
	parts := strings.Split(suffix, "/")
	if len(parts) < 2 || parts[1] != "tunnels-limit" {
		http.Error(w, `{"error":"Invalid URL path"}`, http.StatusBadRequest)
		return
	}
	email, err := url.PathUnescape(parts[0])
	if err != nil {
		http.Error(w, `{"error":"Invalid user email"}`, http.StatusBadRequest)
		return
	}

	var req struct {
		MaxTunnels *int `json:"max_tunnels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request"}`, http.StatusBadRequest)
		return
	}

	user, err := s.db.GetUserByEmail(email)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, `{"error":"User not found"}`, http.StatusNotFound)
		} else {
			http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		}
		return
	}

	user.MaxTunnels = req.MaxTunnels
	if err := s.db.UpdateUser(user); err != nil {
		log.Printf("[API] Failed to update user active tunnels limit: %v", err)
		http.Error(w, `{"error":"Failed to update quota limit"}`, http.StatusInternalServerError)
		return
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    actor,
		Action:     "user.tunnels_limit_changed",
		TargetType: "user",
		TargetID:   user.Email,
		Details:    fmt.Sprintf("Max active tunnels limit overridden. Value: %v", req.MaxTunnels),
		IPAddress:  getClientIP(r),
		CreatedAt:  time.Now(),
	})

	respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

// handleGenerateSubdomain generates a random subdomain prefix.
func (s *Server) handleGenerateSubdomain(w http.ResponseWriter, r *http.Request) {
	_, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	style := r.URL.Query().Get("style")
	sub := s.generateRandomSubdomainPrefix(style)
	respondJSON(w, http.StatusOK, map[string]string{"subdomain": sub})
}

type createInvitationRequest struct {
	Subdomain    string `json:"subdomain"`
	Domain       string `json:"domain"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	ValidityDays int    `json:"validity_days"`
}

// handleListInvitations lists the current user's invitations, or all if admin.
func (s *Server) handleListInvitations(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var list []*db.GuestInvitation
	if user.Role == "admin" || user.Role == "owner" {
		list, err = s.db.ListAllGuestInvitations()
	} else {
		list, err = s.db.ListGuestInvitationsByCreator(user.Email)
	}

	if err != nil {
		log.Printf("[API] Failed to list invitations: %v", err)
		http.Error(w, `{"error":"Failed to retrieve invitations"}`, http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusOK, list)
}

// handleCreateInvitation creates a new guest invitation.
func (s *Server) handleCreateInvitation(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req createInvitationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request JSON"}`, http.StatusBadRequest)
		return
	}

	req.Subdomain = strings.TrimSpace(strings.ToLower(req.Subdomain))
	req.Domain = strings.TrimSpace(strings.ToLower(req.Domain))
	req.Email = strings.TrimSpace(req.Email)
	req.Name = strings.TrimSpace(req.Name)

	if req.Subdomain == "" || req.Domain == "" || req.Email == "" || req.Name == "" {
		http.Error(w, `{"error":"Missing required fields (subdomain, domain, email, name)"}`, http.StatusBadRequest)
		return
	}

	if req.ValidityDays <= 0 {
		req.ValidityDays = 7
	}
	if req.ValidityDays > 365 {
		req.ValidityDays = 365
	}

	// Verify subdomain ownership
	res, err := s.db.GetSubdomainReservationByName(req.Subdomain, req.Domain)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, `{"error":"Subdomain reservation not found"}`, http.StatusNotFound)
		} else {
			http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		}
		return
	}

	if res.UserID != user.ID && user.Role != "admin" && user.Role != "owner" {
		http.Error(w, `{"error":"Forbidden: you do not own this subdomain"}`, http.StatusForbidden)
		return
	}

	token := generateToken(16)
	expiresAt := time.Now().AddDate(0, 0, req.ValidityDays)

	invite := &db.GuestInvitation{
		Token:     token,
		Subdomain: req.Subdomain,
		Domain:    req.Domain,
		Name:      req.Name,
		Email:     req.Email,
		ExpiresAt: expiresAt,
		CreatedBy: user.Email,
	}

	if err := s.db.CreateGuestInvitation(invite); err != nil {
		log.Printf("[API] Failed to create guest invitation: %v", err)
		http.Error(w, `{"error":"Failed to create invitation"}`, http.StatusInternalServerError)
		return
	}

	// Build the claim URL
	baseURL := s.cfg.PortalURL
	if baseURL == "" {
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		baseURL = fmt.Sprintf("%s://%s", scheme, r.Host)
	}
	claimURL := fmt.Sprintf("%s/api/portal/invitations/claim?token=%s", baseURL, token)

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "invitation.created",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", req.Subdomain, req.Domain),
		Details:    fmt.Sprintf("Guest invitation created for %s (%s), claim URL: %s", req.Name, req.Email, claimURL),
		IPAddress:  getClientIP(r),
		CreatedAt:  time.Now(),
	})

	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"invitation": invite,
		"claim_url":  claimURL,
	})
}

// handleDeleteInvitation revokes/deletes an invitation.
func (s *Server) handleDeleteInvitation(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/api/portal/invitations/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, `{"error":"Invalid invitation ID"}`, http.StatusBadRequest)
		return
	}

	invite, err := s.db.GetGuestInvitation(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, `{"error":"Invitation not found"}`, http.StatusNotFound)
		} else {
			http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		}
		return
	}

	if invite.CreatedBy != user.Email && user.Role != "admin" && user.Role != "owner" {
		http.Error(w, `{"error":"Forbidden: you do not own this invitation"}`, http.StatusForbidden)
		return
	}

	if err := s.db.DeleteGuestInvitation(id); err != nil {
		log.Printf("[API] Failed to delete invitation: %v", err)
		http.Error(w, `{"error":"Failed to delete invitation"}`, http.StatusInternalServerError)
		return
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "invitation.deleted",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", invite.Subdomain, invite.Domain),
		Details:    fmt.Sprintf("Guest invitation revoked for %s (%s)", invite.Name, invite.Email),
		IPAddress:  getClientIP(r),
		CreatedAt:  time.Now(),
	})

	respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

// handleClaimInvitation processes the invitation claim and downloads the PKCS#12 client cert bundle.
func (s *Server) handleClaimInvitation(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "Missing claim token", http.StatusBadRequest)
		return
	}

	invite, err := s.db.GetGuestInvitationByToken(token)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, "Invalid or expired claim link", http.StatusNotFound)
		} else {
			http.Error(w, "Database error", http.StatusInternalServerError)
		}
		return
	}

	if invite.ExpiresAt.Before(time.Now()) {
		http.Error(w, "This invitation link has expired", http.StatusGone)
		return
	}

	if invite.ClaimedAt != nil {
		http.Error(w, "This invitation link has already been claimed", http.StatusConflict)
		return
	}

	if s.caCert == nil || s.caKey == nil {
		http.Error(w, "Server Root CA is not initialized", http.StatusInternalServerError)
		return
	}

	pfxPassword := r.URL.Query().Get("password")
	if pfxPassword == "" {
		pfxPassword = "tunnel"
	}

	validityDays := int(time.Until(invite.ExpiresAt).Hours() / 24)
	if validityDays <= 0 {
		validityDays = 1
	}

	identity := "guest:" + token
	pfxBytes, err := GenerateClientP12(s.caCert, s.caKey, identity, invite.Email, invite.Name, validityDays, pfxPassword)
	if err != nil {
		log.Printf("[API] Failed to generate client PKCS#12 bundle: %v", err)
		http.Error(w, "Failed to sign client certificate", http.StatusInternalServerError)
		return
	}

	acl := &db.SubdomainACL{
		Subdomain: invite.Subdomain,
		Domain:    invite.Domain,
		Identity:  identity,
		Name:      invite.Name,
		Email:     invite.Email,
		ExpiresAt: &invite.ExpiresAt,
	}
	if err := s.db.CreateSubdomainACL(acl); err != nil {
		log.Printf("[API] Failed to create subdomain ACL: %v", err)
		http.Error(w, "Database error mapping access permission", http.StatusInternalServerError)
		return
	}

	if err := s.db.MarkGuestInvitationClaimed(token); err != nil {
		log.Printf("[API] Failed to mark invitation claimed: %v", err)
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    invite.Email,
		Action:     "invitation.claimed",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", invite.Subdomain, invite.Domain),
		Details:    fmt.Sprintf("Guest invitation claimed by %s (%s) using identity CN %s", invite.Name, invite.Email, identity),
		IPAddress:  getClientIP(r),
		CreatedAt:  time.Now(),
	})

	w.Header().Set("Content-Type", "application/x-pkcs12")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s-guest.p12", invite.Subdomain))
	w.Header().Set("Content-Length", strconv.Itoa(len(pfxBytes)))
	_, _ = w.Write(pfxBytes)
}

// handleCSRSignInvitation handles a guest-generated CSR and returns the signed certificate PEM.
func (s *Server) handleCSRSignInvitation(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "Missing invitation token", http.StatusBadRequest)
		return
	}

	invite, err := s.db.GetGuestInvitationByToken(token)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, "Invalid or expired invitation token", http.StatusNotFound)
		} else {
			http.Error(w, "Database error", http.StatusInternalServerError)
		}
		return
	}

	if invite.ExpiresAt.Before(time.Now()) {
		http.Error(w, "This invitation link has expired", http.StatusGone)
		return
	}

	if invite.ClaimedAt != nil {
		http.Error(w, "This invitation has already been claimed", http.StatusConflict)
		return
	}

	if s.caCert == nil || s.caKey == nil {
		http.Error(w, "Server Root CA is not initialized", http.StatusInternalServerError)
		return
	}

	var bodyBytes []byte
	if r.Body != nil {
		defer r.Body.Close() //nolint:errcheck
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
	}
	if len(bodyBytes) == 0 {
		http.Error(w, "Empty CSR payload", http.StatusBadRequest)
		return
	}

	validityDays := int(time.Until(invite.ExpiresAt).Hours() / 24)
	if validityDays <= 0 {
		validityDays = 1
	}

	identity := "guest:" + token
	certBytes, err := SignClientCSR(s.caCert, s.caKey, bodyBytes, identity, validityDays)
	if err != nil {
		log.Printf("[API] CSR sign failure: %v", err)
		http.Error(w, fmt.Sprintf("CSR signature/signing failure: %v", err), http.StatusBadRequest)
		return
	}

	acl := &db.SubdomainACL{
		Subdomain: invite.Subdomain,
		Domain:    invite.Domain,
		Identity:  identity,
		Name:      invite.Name,
		Email:     invite.Email,
		ExpiresAt: &invite.ExpiresAt,
	}
	if err := s.db.CreateSubdomainACL(acl); err != nil {
		log.Printf("[API] Failed to create subdomain ACL for signed CSR: %v", err)
		http.Error(w, "Database error mapping access permission", http.StatusInternalServerError)
		return
	}

	if err := s.db.MarkGuestInvitationClaimed(token); err != nil {
		log.Printf("[API] Failed to mark invitation claimed: %v", err)
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    invite.Email,
		Action:     "invitation.csr_claimed",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", invite.Subdomain, invite.Domain),
		Details:    fmt.Sprintf("Guest CSR signed for %s (%s) using identity CN %s", invite.Name, invite.Email, identity),
		IPAddress:  getClientIP(r),
		CreatedAt:  time.Now(),
	})

	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s-guest.crt", invite.Subdomain))
	w.Header().Set("Content-Length", strconv.Itoa(len(certBytes)))
	_, _ = w.Write(certBytes)
}

func generateToken(length int) string {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

type updateAccessControlRequest struct {
	Subdomain    string `json:"subdomain"`
	Domain       string `json:"domain"`
	Passcode     string `json:"passcode"`
	WhitelistIPs string `json:"whitelist_ips"`
	AccessMode   string `json:"access_mode"`
}

// handleUpdateReservationAccessControl dynamically updates passcode and whitelist settings on the gateway.
func (s *Server) handleUpdateReservationAccessControl(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUserOrToken(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req updateAccessControlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request JSON"}`, http.StatusBadRequest)
		return
	}

	req.Subdomain = strings.TrimSpace(strings.ToLower(req.Subdomain))
	req.Domain = strings.TrimSpace(strings.ToLower(req.Domain))
	req.AccessMode = strings.TrimSpace(strings.ToLower(req.AccessMode))

	if req.Subdomain == "" || req.Domain == "" {
		http.Error(w, `{"error":"Missing subdomain or domain"}`, http.StatusBadRequest)
		return
	}

	if req.AccessMode != "and" && req.AccessMode != "or" && req.AccessMode != "" {
		http.Error(w, `{"error":"Access mode must be 'and' or 'or'"}`, http.StatusBadRequest)
		return
	}

	res, err := s.db.GetSubdomainReservationByName(req.Subdomain, req.Domain)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, `{"error":"Reservation not found"}`, http.StatusNotFound)
		} else {
			http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		}
		return
	}

	if res.UserID != user.ID && user.Role != "admin" && user.Role != "owner" {
		http.Error(w, `{"error":"Forbidden: you do not own this reservation"}`, http.StatusForbidden)
		return
	}

	res.Passcode = req.Passcode
	res.WhitelistIPs = req.WhitelistIPs
	if req.AccessMode != "" {
		res.AccessMode = req.AccessMode
	} else {
		res.AccessMode = "or"
	}

	if err := s.db.UpdateSubdomainReservation(res); err != nil {
		log.Printf("[API] Failed to update reservation access controls: %v", err)
		http.Error(w, `{"error":"Failed to update access control configuration"}`, http.StatusInternalServerError)
		return
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "subdomain.access_control_updated",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", req.Subdomain, req.Domain),
		Details:    fmt.Sprintf("Access controls updated: Mode=%s, Passcode=%s, IPs=%s", res.AccessMode, res.Passcode, res.WhitelistIPs),
		IPAddress:  getClientIP(r),
		CreatedAt:  time.Now(),
	})

	respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

type EdgeActionRequest struct {
	NodeID   string `json:"node_id"`
	Action   string `json:"action"` // "restart", "maintenance_enable", "maintenance_disable", "kick_tunnels"
	Reason   string `json:"reason,omitempty"`
	Duration int    `json:"duration,omitempty"`
}

func (s *Server) handleEdgeAction(w http.ResponseWriter, r *http.Request) {
	user, err := s.getCurrentUser(r)
	if err != nil {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	if user.Role != "admin" && user.Role != "owner" {
		http.Error(w, `{"error":"Forbidden"}`, http.StatusForbidden)
		return
	}

	var req EdgeActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request JSON"}`, http.StatusBadRequest)
		return
	}

	if req.NodeID == "" {
		http.Error(w, `{"error":"Missing node_id"}`, http.StatusBadRequest)
		return
	}

	actor := user.Email
	if actor == "" {
		actor = user.ID
	}

	switch req.Action {
	case "restart":
		err = s.SendEdgeRestart(req.NodeID)
		s.writeAudit(actor, "edge.restart", "node", req.NodeID, "Triggered regional daemon restart", r)
	case "maintenance_enable":
		reason := req.Reason
		if reason == "" {
			reason = "Administrative Maintenance"
		}
		duration := req.Duration
		if duration <= 0 {
			duration = 30
		}
		err = s.SendEdgeMaintenance(req.NodeID, "enable", duration, reason)
		s.writeAudit(actor, "edge.maintenance", "node", req.NodeID, fmt.Sprintf("Enabled soft maintenance: %s (%d mins)", reason, duration), r)
	case "maintenance_disable":
		err = s.SendEdgeMaintenance(req.NodeID, "disable", 0, "")
		s.writeAudit(actor, "edge.maintenance", "node", req.NodeID, "Disabled soft maintenance", r)
	case "kick_tunnels":
		err = s.SendEdgeKickAll(req.NodeID)
		s.writeAudit(actor, "edge.kick_tunnels", "node", req.NodeID, "Kicked all active tunnels", r)
	default:
		http.Error(w, `{"error":"Unknown action"}`, http.StatusBadRequest)
		return
	}

	if err != nil {
		log.Printf("[API] Failed to perform edge action %s on %s: %v", req.Action, req.NodeID, err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
}
