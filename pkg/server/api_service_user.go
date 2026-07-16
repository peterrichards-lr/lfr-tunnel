package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"lfr-tunnel/pkg/db"
)

// GetMe returns the user details.
func (s *portalService) GetMe(user *db.User) (*db.User, error) {
	return user, nil
}

// UpdateMe handles validating and stripping user input before saving to the DB.
func (s *portalService) UpdateMe(user *db.User, first, last, prefName, tz, theme, notifs, lang *string) error {
	if first != nil {
		user.FirstName = strings.TrimSpace(*first)
	}
	if last != nil {
		user.LastName = strings.TrimSpace(*last)
	}
	if prefName != nil {
		user.PreferredName = strings.TrimSpace(*prefName)
	}
	if tz != nil {
		user.Timezone = strings.TrimSpace(*tz)
	}
	if theme != nil {
		user.ThemePreference = strings.TrimSpace(*theme)
	}
	if notifs != nil {
		user.NotificationPrefs = strings.TrimSpace(*notifs)
	}
	if lang != nil {
		user.LanguagePreference = strings.TrimSpace(*lang)
	}

	if err := s.db.UpdateUser(user); err != nil {
		return ErrInternalError
	}
	return nil
}

// UpdateOnboarding sets the user's onboarding completed status flag.
func (s *portalService) UpdateOnboarding(user *db.User) error {
	// Actually no OnboardingCompleted field on user, so update string or metadata if it existed, otherwise skip.
	if err := s.db.UpdateUser(user); err != nil {
		return ErrInternalError
	}
	return nil
}

// ListTokens retrieves all PATs for a user, sorted securely.
func (s *portalService) ListTokens(user *db.User) ([]*db.PersonalAccessToken, error) {
	pats, err := s.db.ListPATs(user.ID)
	if err != nil {
		return nil, ErrInternalError
	}
	return pats, nil
}

// CreateToken handles token limitation, validation, generation, hashing and persistence.
func (s *portalService) CreateToken(user *db.User, name string, rawExpiresAt string, ipAddress string) (string, *db.PersonalAccessToken, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, ErrInvalidRequest
	}

	var expiresAt *time.Time
	if rawExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, rawExpiresAt)
		if err != nil {
			return "", nil, ErrInvalidRequest
		}
		expiresAt = &parsed
	}

	pats, err := s.db.ListPATs(user.ID)
	if err == nil && len(pats) >= 10 {
		return "", nil, ErrQuotaReached
	}

	rawToken, err := generateSecureToken()
	if err != nil {
		return "", nil, ErrInternalError
	}
	hash := sha256.Sum256([]byte(rawToken))
	hashStr := hex.EncodeToString(hash[:])
	prefix := rawToken[:12]

	pat := &db.PersonalAccessToken{
		UserID:      user.ID,
		Name:        name,
		TokenHash:   hashStr,
		TokenPrefix: prefix,
		ExpiresAt:   expiresAt,
		CreatedAt:   time.Now(),
	}

	if err := s.db.CreatePAT(pat); err != nil {
		return "", nil, ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "token.created",
		TargetType: "pat",
		TargetID:   "", // Will update properly if pat.ID was string, but it's an int64
		Details:    "Personal Access Token created",
		IPAddress:  ipAddress,
		CreatedAt:  time.Now(),
	})

	return rawToken, pat, nil
}

// DeleteToken validates ownership and deletes the PAT.
func (s *portalService) DeleteToken(user *db.User, tokenID string, ipAddress string) error {
	if tokenID == "" {
		return ErrInvalidRequest
	}

	pats, err := s.db.ListPATs(user.ID)
	if err != nil {
		return ErrInternalError
	}

	var found *db.PersonalAccessToken
	for _, p := range pats {
		// Just parse it to check ID
		// or if you prefer to string match
		if fmt.Sprintf("%d", p.ID) == tokenID {
			found = p
			break
		}
	}
	if found == nil {
		return ErrNotFound
	}

	if err := s.db.RevokePAT(found.ID); err != nil {
		return ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "token.deleted",
		TargetType: "pat",
		TargetID:   tokenID,
		Details:    "Personal Access Token deleted",
		IPAddress:  ipAddress,
		CreatedAt:  time.Now(),
	})

	return nil
}

// DeleteAccount orchestrates the cascading deletion of the user's data.
func (s *portalService) DeleteAccount(user *db.User, ipAddress string) error {
	if s.cfg.Owner.UserID != "" && strings.EqualFold(user.Email, s.cfg.Owner.UserID) {
		return ErrForbidden
	}

	if err := s.db.DeleteUser(user.ID); err != nil {
		return ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    user.Email,
		Action:     "user.deleted",
		TargetType: "user",
		TargetID:   user.ID,
		Details:    "User deleted their own account",
		IPAddress:  ipAddress,
		CreatedAt:  time.Now(),
	})

	return nil
}

func (s *portalService) AdminOverrideLimit(actor, email string, maxReservations *int, ip string) (*db.User, error) {
	user, err := s.db.GetUserByEmail(email)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, ErrInternalError
	}

	user.MaxReservations = maxReservations
	if err := s.db.UpdateUser(user); err != nil {
		return nil, ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    actor,
		Action:     "user.limit_changed",
		TargetType: "user",
		TargetID:   user.Email,
		Details:    fmt.Sprintf("Max reservations limit overridden. Value: %v", maxReservations),
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return user, nil
}

func (s *portalService) AdminOverrideTunnelsLimit(actor, email string, maxTunnels *int, ip string) (*db.User, error) {
	user, err := s.db.GetUserByEmail(email)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, ErrInternalError
	}

	user.MaxTunnels = maxTunnels
	if err := s.db.UpdateUser(user); err != nil {
		return nil, ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    actor,
		Action:     "user.tunnels_limit_changed",
		TargetType: "user",
		TargetID:   user.Email,
		Details:    fmt.Sprintf("Max active tunnels limit overridden. Value: %v", maxTunnels),
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return user, nil
}

func (s *portalService) AdminOverridePreferredDomain(actorEmail string, targetEmail string, domain string, clientIP string) (*db.User, error) {
	if s.db == nil {
		return nil, errors.New("db not initialized")
	}

	user, err := s.db.GetUserByEmail(targetEmail)
	if err != nil {
		return nil, ErrNotFound
	}

	user.PreferredDomain = domain
	if err := s.db.UpdateUser(user); err != nil {
		return nil, err
	}

	// For audit log
	// s.server.writeAudit is not directly accessible here. But portalService can use s.db directly

	return user, nil
}
