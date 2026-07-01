package server

import (
	"fmt"
	"strings"
	"time"

	"lfr-tunnel/pkg/db"
)

// MFASetup initiates MFA setup by generating a secret and provisioning URI.
func (s *portalService) MFASetup(user *db.User) (string, string, error) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		return "", "", ErrInternalError
	}

	// URL format compliant with standard authenticator apps
	otpauthURL := fmt.Sprintf("otpauth://totp/Liferay%%20Tunnel:%s?secret=%s&issuer=Liferay%%20Tunnel", user.Email, secret)

	return secret, otpauthURL, nil
}

// MFAEnable validates the provided TOTP code and activates MFA for the user.
func (s *portalService) MFAEnable(user *db.User, secret, code, ip string) error {
	if !ValidateTOTP(secret, code) {
		return ErrInvalidRequest
	}

	user.TOTPSecret = secret
	user.TOTPEnabled = true

	if err := s.db.UpdateUser(user); err != nil {
		return ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "user.mfa_enabled",
		TargetType: "user",
		TargetID:   user.Email,
		Details:    "",
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return nil
}

// MFADisable deactivates MFA for the authenticated user after validating their code.
func (s *portalService) MFADisable(user *db.User, code, ip string) error {
	if !ValidateTOTP(user.TOTPSecret, code) {
		return ErrInvalidRequest
	}

	user.TOTPSecret = ""
	user.TOTPEnabled = false

	if err := s.db.UpdateUser(user); err != nil {
		return ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "user.mfa_disabled",
		TargetType: "user",
		TargetID:   user.Email,
		Details:    "",
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return nil
}

// MFAVerify completes the 2FA login verification step and issues the final session token.
func (s *portalService) MFAVerify(tempToken, code, ip string) (*db.User, string, error) {
	val, ok := s.portalMap.LoadAndDelete("pre_auth_" + tempToken)
	if !ok {
		return nil, "", ErrUnauthorized
	}

	preAuth, ok := val.(PortalSessionData)
	if !ok {
		return nil, "", ErrInternalError
	}

	if time.Now().After(preAuth.ExpiresAt) {
		return nil, "", ErrUnauthorized
	}

	user, err := s.db.GetUserByEmail(preAuth.Email)
	if err != nil {
		return nil, "", ErrUnauthorized
	}

	if !ValidateTOTP(user.TOTPSecret, code) {
		// Put the pre-auth session back so they can try again (with a fresh 5 minute lifetime)
		preAuth.ExpiresAt = time.Now().Add(5 * time.Minute)
		s.portalMap.Store("pre_auth_"+tempToken, preAuth)
		return nil, "", ErrUnauthorized
	}

	// MFA Validation Success -> Issue Portal Session
	sessionToken, err := generateSecureToken()
	if err != nil {
		return nil, "", ErrInternalError
	}

	var previousLoginAt *time.Time
	if user.LastLoginAt != nil {
		prev := *user.LastLoginAt
		previousLoginAt = &prev
	}

	// Update user login audit metrics
	now := time.Now().UTC()
	user.LastLoginAt = &now
	user.LastLoginIP = ip
	_ = s.db.UpdateUser(user)

	killedPreviousSession := false
	s.portalMap.Range(func(key, value interface{}) bool {
		k := key.(string)
		if strings.HasPrefix(k, "admin_session_") {
			sessionData, ok := value.(PortalSessionData)
			if ok && sessionData.Email == user.Email {
				s.portalMap.Delete(k)
				killedPreviousSession = true
			}
		}
		return true
	})

	s.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:                 user.Email,
		ExpiresAt:             time.Now().Add(s.cfg.PortalSessionDuration),
		ClientIP:              ip,
		PreviousLoginAt:       previousLoginAt,
		KilledPreviousSession: killedPreviousSession,
	})

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "admin.login",
		TargetType: "system",
		TargetID:   "admin",
		Details:    "Admin logged into dashboard via magic link + MFA",
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return user, sessionToken, nil
}
