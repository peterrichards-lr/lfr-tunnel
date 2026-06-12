package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log"
	"net/http"
	"strings"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// generateRandomState generates a secure random state for OAuth2 CSRF protection
func generateRandomState() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func getOIDCConfig(cfg *config.ServerConfig, providerID string, r *http.Request) (*config.SSOProviderConfig, *oauth2.Config, *oidc.Provider, error) {
	var p *config.SSOProviderConfig
	for _, prov := range cfg.SSOProviders {
		if prov.ID == providerID {
			p = &prov
			break
		}
	}
	if p == nil {
		return nil, nil, nil, http.ErrNotSupported
	}

	ctx := context.Background()
	provider, err := oidc.NewProvider(ctx, p.IssuerURL)
	if err != nil {
		return nil, nil, nil, err
	}

	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	redirectURL := scheme + "://" + r.Host + "/api/auth/callback?provider=" + p.ID

	oauth2Config := &oauth2.Config{
		ClientID:     p.ClientID,
		ClientSecret: p.ClientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
	return p, oauth2Config, provider, nil
}

func (s *Server) handleSSOLogin(w http.ResponseWriter, r *http.Request) {
	providerID := r.URL.Query().Get("provider")
	if providerID == "" {
		http.Error(w, `{"error":"provider required"}`, http.StatusBadRequest)
		return
	}

	_, oauth2Config, _, err := getOIDCConfig(s.cfg, providerID, r)
	if err != nil {
		log.Printf("[SSO] Config error: %v", err)
		http.Error(w, `{"error":"Invalid provider configuration"}`, http.StatusInternalServerError)
		return
	}

	state := generateRandomState()
	// In production, you should set this as a secure HttpOnly cookie to verify in callback.
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    state,
		Path:     "/",
		Expires:  time.Now().Add(10 * time.Minute),
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
	})

	url := oauth2Config.AuthCodeURL(state)
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) handleSSOCallback(w http.ResponseWriter, r *http.Request) {
	providerID := r.URL.Query().Get("provider")
	if providerID == "" {
		http.Error(w, "Provider required", http.StatusBadRequest)
		return
	}

	state, err := r.Cookie("oidc_state")
	if err != nil || r.URL.Query().Get("state") != state.Value {
		http.Error(w, "State invalid", http.StatusBadRequest)
		return
	}

	p, oauth2Config, provider, err := getOIDCConfig(s.cfg, providerID, r)
	if err != nil {
		http.Error(w, "Invalid provider configuration", http.StatusInternalServerError)
		return
	}

	ctx := context.Background()
	oauth2Token, err := oauth2Config.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "Failed to exchange token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "No id_token field in oauth2 token", http.StatusInternalServerError)
		return
	}

	oidcConfig := &oidc.Config{
		ClientID:        oauth2Config.ClientID,
		SkipIssuerCheck: p.SkipIssuerCheck,
	}
	verifier := provider.Verifier(oidcConfig)
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		http.Error(w, "Failed to verify ID token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		GivenName     string `json:"given_name"`
		FamilyName    string `json:"family_name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "Failed to parse claims: "+err.Error(), http.StatusInternalServerError)
		return
	}

	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if email == "" {
		http.Error(w, "Email required from provider", http.StatusBadRequest)
		return
	}

	// ENFORCE EMAIL DOMAIN WHITELIST
	allowed := false
	for _, d := range s.cfg.AllowedEmailDomains {
		if strings.HasSuffix(email, "@"+d) {
			allowed = true
			break
		}
	}
	if !allowed && len(s.cfg.AllowedEmailDomains) > 0 {
		http.Error(w, "Your email domain is not authorized to access this gateway.", http.StatusForbidden)
		return
	}

	// Create or load the user
	user, err := s.db.GetUserByEmail(email)
	if err != nil && err != db.ErrNotFound {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if err == db.ErrNotFound {
		user = &db.User{
			ID:         email,
			Email:      email,
			FirstName:  claims.GivenName,
			LastName:   claims.FamilyName,
			Role:       "user",
			Status:     "approved",
			AuthMethod: "sso - " + providerID,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		// If they match the owner config, grant admin
		if user.Email == s.cfg.Owner.UserID {
			user.Role = "admin"
		}
		if err := s.db.CreateUser(user); err != nil {
			log.Printf("[SSO] Failed to create user %s: %v", email, err)
			http.Error(w, "Failed to create user", http.StatusInternalServerError)
			return
		}
	} else if user.Status != "approved" {
		user.Status = "approved"
		if err := s.db.UpdateUser(user); err != nil {
			log.Printf("Failed to update user tokens after SSO login: %v", err)
		}
	}

	// Issue the admin session cookie
	sessionID, _ := generateSecureToken()
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

	s.portalMap.Store("admin_session_"+sessionID, PortalSessionData{
		Email:                 user.Email,
		ExpiresAt:             time.Now().Add(s.cfg.PortalSessionDuration),
		ClientIP:              r.Header.Get("X-Real-IP"),
		KilledPreviousSession: killedPreviousSession,
	})

	http.SetCookie(w, &http.Cookie{
		Name:     "lfr_session",
		Value:    sessionID,
		Path:     "/",
		Expires:  time.Now().Add(s.cfg.PortalSessionDuration),
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
	})

	// Inject an audit log
	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "user.login.sso",
		TargetType: "user",
		TargetID:   user.Email,
		IPAddress:  r.Header.Get("X-Real-IP"),
	})

	// Redirect to Dashboard (Root)
	http.Redirect(w, r, "/", http.StatusFound)
}
