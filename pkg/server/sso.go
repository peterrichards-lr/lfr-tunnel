package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// ActionSSODeniedRejected is written when SSO sign-in is refused because an administrator had
// already rejected this registration.
//
// Its own action rather than a variant of the rejection row: this is somebody attempting to use
// the account after the decision, which is the row an owner wants to see if a rejection turns out
// to have been contentious.
const ActionSSODeniedRejected = "user.sso_denied.rejected"

// errSSORegistrationRejected distinguishes "this gateway has decided against this person" from
// "the approval write failed", which the callback answers with 403 and 500 respectively. A bare
// bool made both of them read as a refusal to the person signing in.
var errSSORegistrationRejected = errors.New("registration was rejected by an administrator")

// ssoPortalCookie remembers which portal started an SSO sign-in, so the callback can return the
// person to it (#2447). The callback used to redirect to "/" unconditionally, so a sign-in started
// from Portal V2 landed in V1 -- one arm of a live A/B test handing its users to the other.
//
// It holds a fixed value, never a URL: a return URL carried through the identity provider is an
// open redirect waiting for someone to put an address in it. ssoReturnPath maps the value to a
// path, and anything it does not recognise returns to V1, the old behaviour.
const ssoPortalCookie = "oidc_portal"

// ssoPortalV2 is the only value ssoPortalCookie can usefully hold.
const ssoPortalV2 = "v2"

// ssoReturnPath is where the callback sends someone who has just signed in.
func ssoReturnPath(r *http.Request) string {
	if c, err := r.Cookie(ssoPortalCookie); err == nil && c.Value == ssoPortalV2 {
		return "/portalv2/dashboard"
	}
	return "/"
}

// generateRandomState generates a secure random state for OAuth2 CSRF protection
func generateRandomState() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) //nolint:errcheck
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
		slog.Info(fmt.Sprintf("[SSO] Config error: %v", err))
		http.Error(w, `{"error":"Invalid provider configuration"}`, http.StatusInternalServerError)
		return
	}

	state := generateRandomState()
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	// In production, you should set this as a secure HttpOnly cookie to verify in callback.
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    state,
		Path:     "/",
		Expires:  time.Now().Add(10 * time.Minute),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	// Always written, so a V1 sign-in clears a V2 one abandoned in the same browser rather than
	// inheriting its destination.
	portal := ""
	if r.URL.Query().Get("portal") == ssoPortalV2 {
		portal = ssoPortalV2
	}
	http.SetCookie(w, &http.Cookie{
		Name:     ssoPortalCookie,
		Value:    portal,
		Path:     "/",
		Expires:  time.Now().Add(10 * time.Minute),
		HttpOnly: true,
		Secure:   secure,
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
		// #910: umbrella flag gating NEW account creation regardless of method. Without
		// this, SSO first-login always auto-provisioned a new account with only the
		// AllowedEmailDomains whitelist as a gate -- no way to close off new signups
		// independent of disabling SSO/email login entirely. The owner is exempt: they're
		// a pre-configured account, not a new registration, and locking them out would
		// leave nobody able to administer the server.
		if s.cfg.DisableNewRegistrations && !strings.EqualFold(email, s.cfg.Owner.UserID) {
			http.Error(w, "New registrations are currently closed.", http.StatusForbidden)
			return
		}
		user = &db.User{
			ID:         email,
			Email:      email,
			FirstName:  claims.GivenName,
			LastName:   claims.FamilyName,
			Role:       "user",
			Status:     db.UserStatusApproved,
			AuthMethod: "sso - " + providerID,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		// If they match the owner config, grant owner
		if strings.EqualFold(user.Email, s.cfg.Owner.UserID) {
			user.Role = "owner"
		}
		if err := s.db.CreateUser(user); err != nil {
			slog.Info(fmt.Sprintf("[SSO] Failed to create user %s: %v", email, err))
			http.Error(w, "Failed to create user", http.StatusInternalServerError)
			return
		}
	} else if user.Status != db.UserStatusApproved {
		// A rejected registration is the one status SSO must not promote, so the outcome is
		// checked rather than assumed: approveOnSSOSignIn refuses it, and refusing to approve
		// while still issuing a session would hand a rejected person a portal session anyway
		// (#1830).
		if err := s.approveOnSSOSignIn(user, providerID, r); err != nil {
			if errors.Is(err, errSSORegistrationRejected) {
				http.Error(w, "Your access request has been declined. Please contact the gateway "+
					"administrator if you think this is a mistake.", http.StatusForbidden)
				return
			}
			// The approval did not persist, so there is no approved account to issue a session
			// for. Saying so beats signing them in against a status the database does not hold.
			http.Error(w, "Failed to complete sign-in", http.StatusInternalServerError)
			return
		}
	}

	// Issue the admin session cookie
	sessionID, err := generateSecureToken()
	if err != nil {
		http.Error(w, "Failed to generate session ID", http.StatusInternalServerError)
		return
	}
	killedPreviousSession := s.sessionStore().killPortalSessionsFor(user.Email)

	s.sessionStore().storePortalSession(sessionID, PortalSessionData{
		Email:                 user.Email,
		ExpiresAt:             time.Now().Add(s.cfg.PortalSessionDuration),
		ClientIP:              s.clientIP(r),
		KilledPreviousSession: killedPreviousSession,
		// Recorded so a slide can re-issue the cookie with the mode it was created with
		// rather than a default (#1655).
		SameSite:  sameSiteToStored(http.SameSiteLaxMode),
		CreatedAt: time.Now().UTC(),
	})

	http.SetCookie(w, s.newSessionCookie(r, sessionID, http.SameSiteLaxMode,
		s.sessionDeadline(PortalSessionData{
			ExpiresAt: time.Now().Add(s.cfg.PortalSessionDuration),
			CreatedAt: time.Now().UTC(),
		})))

	// Inject an audit log
	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    user.Email,
		Action:     "user.login.sso",
		TargetType: "user",
		TargetID:   user.Email,
		IPAddress:  s.clientIP(r),
	})

	// Back to the portal the sign-in started from (#2447).
	http.Redirect(w, r, ssoReturnPath(r), http.StatusFound)
}

// approveOnSSOSignIn completes the approval of an existing user who has just authenticated
// through SSO.
//
// Auto-approving here is deliberate: once Liferay SSO is configured, the Liferay server is what
// controls who may authenticate, so reaching this point IS the authorisation. A second, manual
// approval would only duplicate a decision already made upstream.
//
// What was wrong (#1824) is that this approved PARTIALLY -- it set the status and stopped,
// leaving three loose ends:
//
//   - approval_token stayed live, so the admin's emailed approve link still worked on an
//     already-approved user. Following it later would mint a claim token and send a
//     "Registration Approved!" mail for something that happened days earlier.
//   - no audit row, so the single most security-relevant transition a user can undergo --
//     pending to approved -- left no trace naming what did it.
//   - no last_login_at, so these users read as "never logged in" (only server.go's magic-link
//     path and api_service_mfa.go set it, and neither is on this route). That is what made the
//     production data look as though nobody had ever signed in, when they had.
//
// Deliberately does NOT send a "you are approved" email: the user is completing a sign-in as this
// runs, so they are about to be looking at the dashboard. The email in the admin-approval path
// exists to reach somebody who is NOT present. Here, they are.
//
// It refuses exactly one status, and returning false is how it says so: a registration an admin
// explicitly REJECTED (#1830). Everything above is about a decision the identity provider has
// already made and this gateway has not; a rejection is a decision this gateway made about a
// person who can authenticate perfectly well upstream. Without this, rejection lasted until the
// rejected person clicked "Sign in with Liferay" -- status was not "approved", so this function
// made it "approved", and they had approved themselves. An admin who wants to reverse a rejection
// does it deliberately, from the admin portal.
//
// Extracted from handleSSOCallback so the behaviour can be asserted directly: reaching the
// callback requires a full OIDC token exchange, and a test that faked one would be testing the
// fake. This is the real function the callback calls.
func (s *Server) approveOnSSOSignIn(user *db.User, providerID string, r *http.Request) error {
	if user.Status == statusRejected {
		slog.Warn(fmt.Sprintf("[SSO] Refused to auto-approve %s on SSO sign-in: the registration "+
			"was rejected by an administrator", user.Email))
		s.writeAudit(user.Email, ActionSSODeniedRejected, "user", user.Email,
			fmt.Sprintf("Sign-in through %s refused: this registration was rejected by an "+
				"administrator, and SSO auto-approval does not override that", providerID), r)
		return errSSORegistrationRejected
	}

	previousStatus := user.Status
	user.Status = db.UserStatusApproved
	user.ApprovalToken = "" // consumed by this approval; a stale link must not re-approve
	now := time.Now().UTC()
	user.LastLoginAt = &now

	if err := s.db.UpdateUser(user); err != nil {
		slog.Error(fmt.Sprintf("[SSO] Failed to approve %s on SSO sign-in: %v", user.Email, err))
		// The write failed, so this user is not approved. Reporting success would issue a portal
		// session on the strength of an approval that is not in the database.
		return fmt.Errorf("persisting the SSO approval of %s: %w", user.Email, err)
	}
	s.invalidateUserCache(user.Email)
	s.writeAudit(user.Email, "user.approved.sso", "user", user.Email,
		fmt.Sprintf("Auto-approved on SSO sign-in via %s (was %q); the identity provider is the access decision",
			providerID, previousStatus), r)
	return nil
}
