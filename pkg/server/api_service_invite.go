package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"lfr-tunnel/pkg/db"
)

func generateToken(length int) string {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

func (s *portalService) ListInvitations(user *db.User) ([]*db.GuestInvitation, error) {
	if user.Role == "admin" || user.Role == "owner" {
		return s.db.ListAllGuestInvitations()
	}
	return s.db.ListGuestInvitationsByCreator(user.Email)
}

func (s *portalService) CreateInvitation(user *db.User, subdomain, domain, name, email string, validityDays int, ip, portalURL, scheme, host string) (*db.GuestInvitation, string, error) {
	if validityDays <= 0 {
		validityDays = 7
	}
	if validityDays > 365 {
		validityDays = 365
	}

	res, err := s.db.GetSubdomainReservationByName(subdomain, domain)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, "", ErrNotFound
		}
		return nil, "", ErrInternalError
	}

	if res.UserID != user.ID && user.Role != "admin" && user.Role != "owner" {
		return nil, "", ErrForbidden
	}

	token := generateToken(16)
	expiresAt := time.Now().AddDate(0, 0, validityDays)

	invite := &db.GuestInvitation{
		Token:     token,
		Subdomain: subdomain,
		Domain:    domain,
		Name:      name,
		Email:     email,
		ExpiresAt: expiresAt,
		CreatedBy: user.Email,
	}

	if err := s.db.CreateGuestInvitation(invite); err != nil {
		return nil, "", ErrInternalError
	}

	baseURL := portalURL
	if baseURL == "" {
		baseURL = fmt.Sprintf("%s://%s", scheme, host)
	}
	claimURL := fmt.Sprintf("%s/api/portal/invitations/claim?token=%s", baseURL, token)

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    user.Email,
		Action:     "invitation.created",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", subdomain, domain),
		Details:    fmt.Sprintf("Guest invitation created for %s (%s), claim URL: %s", name, email, claimURL),
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return invite, claimURL, nil
}

func (s *portalService) DeleteInvitation(user *db.User, idStr, ip string) error {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return ErrInvalidRequest
	}

	invite, err := s.db.GetGuestInvitation(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return ErrNotFound
		}
		return ErrInternalError
	}

	if invite.CreatedBy != user.Email && user.Role != "admin" && user.Role != "owner" {
		return ErrForbidden
	}

	if err := s.db.DeleteGuestInvitation(id); err != nil {
		return ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    user.Email,
		Action:     "invitation.deleted",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", invite.Subdomain, invite.Domain),
		Details:    fmt.Sprintf("Guest invitation revoked for %s (%s)", invite.Name, invite.Email),
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return nil
}

func (s *portalService) ClaimInvitation(token, pfxPassword, ip string) ([]byte, *db.GuestInvitation, error) {
	invite, err := s.db.GetGuestInvitationByToken(token)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, ErrInternalError
	}

	if invite.ExpiresAt.Before(time.Now()) {
		return nil, invite, errors.New("invitation expired")
	}
	if invite.ClaimedAt != nil {
		return nil, invite, errors.New("invitation already claimed")
	}
	if s.caCert == nil || s.caKey == nil {
		return nil, invite, errors.New("server CA not initialized")
	}

	validityDays := int(time.Until(invite.ExpiresAt).Hours() / 24)
	if validityDays <= 0 {
		validityDays = 1
	}

	identity := "guest:" + token
	pfxBytes, err := GenerateClientP12(s.caCert, s.caKey, identity, invite.Email, invite.Name, validityDays, pfxPassword)
	if err != nil {
		return nil, invite, ErrInternalError
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
		return nil, invite, ErrInternalError
	}

	_ = s.db.MarkGuestInvitationClaimed(token) //nolint:errcheck

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    invite.Email,
		Action:     "invitation.claimed",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", invite.Subdomain, invite.Domain),
		Details:    fmt.Sprintf("Guest invitation claimed by %s (%s) using identity CN %s", invite.Name, invite.Email, identity),
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return pfxBytes, invite, nil
}

func (s *portalService) CSRSignInvitation(token string, csr []byte, ip string) ([]byte, *db.GuestInvitation, error) {
	invite, err := s.db.GetGuestInvitationByToken(token)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, ErrInternalError
	}

	if invite.ExpiresAt.Before(time.Now()) {
		return nil, invite, errors.New("invitation expired")
	}
	if invite.ClaimedAt != nil {
		return nil, invite, errors.New("invitation already claimed")
	}
	if s.caCert == nil || s.caKey == nil {
		return nil, invite, errors.New("server CA not initialized")
	}

	validityDays := int(time.Until(invite.ExpiresAt).Hours() / 24)
	if validityDays <= 0 {
		validityDays = 1
	}

	identity := "guest:" + token
	certBytes, err := SignClientCSR(s.caCert, s.caKey, csr, identity, validityDays)
	if err != nil {
		return nil, invite, fmt.Errorf("csr signature failed: %w", err)
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
		return nil, invite, ErrInternalError
	}

	_ = s.db.MarkGuestInvitationClaimed(token) //nolint:errcheck

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    invite.Email,
		Action:     "invitation.csr_claimed",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", invite.Subdomain, invite.Domain),
		Details:    fmt.Sprintf("Guest CSR signed for %s (%s) using identity CN %s", invite.Name, invite.Email, identity),
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return certBytes, invite, nil
}
