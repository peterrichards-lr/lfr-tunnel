package server

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"lfr-tunnel/pkg/db"
)

// ListReservations returns the reservations, quota limit, and used count.
func (s *portalService) ListReservations(user *db.User) ([]*db.SubdomainReservation, int, int, error) {
	list, err := s.db.ListSubdomainReservationsByUserID(user.ID)
	if err != nil {
		return nil, 0, 0, ErrInternalError
	}

	usedCount := len(list)
	limit := s.getUserMaxReservations(user)
	return list, limit, usedCount, nil
}

// CreateReservation validates and persists a new subdomain reservation.
func (s *portalService) CreateReservation(user *db.User, subdomain, domain, ip string) (*db.SubdomainReservation, error) {
	subdomain = strings.ToLower(strings.TrimSpace(subdomain))
	domain = strings.ToLower(strings.TrimSpace(domain))

	if subdomain == "" || domain == "" {
		return nil, ErrInvalidRequest
	}

	if !isValidSubdomain(subdomain) {
		return nil, ErrInvalidRequest
	}

	domainSupported := false
	for _, d := range s.cfg.Domains {
		if strings.EqualFold(d, domain) {
			domainSupported = true
			break
		}
	}
	if !domainSupported {
		return nil, ErrInvalidRequest
	}

	limit := s.getUserMaxReservations(user)
	list, err := s.db.ListSubdomainReservationsByUserID(user.ID)
	if err != nil {
		return nil, ErrInternalError
	}

	activeCount := 0
	for _, res := range list {
		if res.ExpiresAt == nil || res.ExpiresAt.After(time.Now()) {
			activeCount++
		}
	}

	if limit >= 0 && activeCount >= limit {
		return nil, ErrQuotaReached
	}

	existing, err := s.db.GetSubdomainReservationByName(subdomain, domain)
	if err == nil && existing != nil {
		if existing.ExpiresAt != nil && existing.ExpiresAt.Before(time.Now()) {
			quarantineCutoff := existing.ExpiresAt.AddDate(0, 0, s.cfg.SubdomainQuarantineDays)
			if time.Now().Before(quarantineCutoff) {
				if existing.UserID != user.ID {
					return nil, ErrConflict
				}
				_ = s.db.DeleteSubdomainReservation(existing.ID) //nolint:errcheck
			} else {
				_ = s.db.DeleteSubdomainReservation(existing.ID) //nolint:errcheck
			}
		} else {
			return nil, ErrConflict
		}
	}

	expiry := s.getUserSubdomainExpiry(user)
	res := &db.SubdomainReservation{
		UserID:    user.ID,
		Subdomain: subdomain,
		Domain:    domain,
		ExpiresAt: expiry,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.db.CreateSubdomainReservation(res); err != nil {
		return nil, ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    user.Email,
		Action:     "subdomain.reserved",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", subdomain, domain),
		Details:    fmt.Sprintf("Subdomain reserved. ExpiresAt: %v", expiry),
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return res, nil
}

// DeleteReservation removes a reservation securely.
func (s *portalService) DeleteReservation(user *db.User, idStr, ip string) error {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return ErrInvalidRequest
	}

	res, err := s.db.GetSubdomainReservation(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return ErrNotFound
		}
		return ErrInternalError
	}

	if res.UserID != user.ID && user.Role != "admin" && user.Role != "owner" {
		return ErrForbidden
	}

	if err := s.db.DeleteSubdomainReservation(id); err != nil {
		return ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    user.Email,
		Action:     "subdomain.released",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", res.Subdomain, res.Domain),
		Details:    "Subdomain reservation deleted / released by owner",
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return nil
}

// RequestExtension marks a reservation for extension.
func (s *portalService) RequestExtension(user *db.User, idStr, ip string) (*db.SubdomainReservation, error) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return nil, ErrInvalidRequest
	}

	res, err := s.db.GetSubdomainReservation(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, ErrInternalError
	}

	if res.UserID != user.ID && user.Role != "admin" && user.Role != "owner" {
		return nil, ErrForbidden
	}

	if res.ExpiresAt == nil {
		return nil, ErrInvalidRequest
	}

	res.ExtensionRequested = true
	if err := s.db.UpdateSubdomainReservation(res); err != nil {
		return nil, ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    user.Email,
		Action:     "subdomain.extension_requested",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", res.Subdomain, res.Domain),
		Details:    "Extension requested for subdomain reservation",
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	if s.mailer != nil {
		s.mailer.SendAdminAlert("alert_notify_extension_requested", "LFR Tunnel Alert: Subdomain Extension Requested",
			fmt.Sprintf("User %s has requested an extension for subdomain %s.%s.", user.Email, res.Subdomain, res.Domain))
	}

	return res, nil
}

// PromoteReservation promotes a tunnel lease to a reservation.
func (s *portalService) PromoteReservation(user *db.User, subdomain, domain, ip string) (*db.SubdomainReservation, error) {
	limit := s.getUserMaxReservations(user)

	list, err := s.db.ListSubdomainReservationsByUserID(user.ID)
	if err != nil {
		return nil, ErrInternalError
	}

	activeCount := 0
	for _, res := range list {
		if res.ExpiresAt == nil || res.ExpiresAt.After(time.Now()) {
			activeCount++
		}
	}

	if limit >= 0 && activeCount >= limit {
		return nil, ErrQuotaReached
	}

	existing, err := s.db.GetSubdomainReservationByName(subdomain, domain)
	if err == nil && existing != nil {
		if existing.ExpiresAt == nil || existing.ExpiresAt.After(time.Now()) {
			if existing.UserID != user.ID {
				return nil, ErrConflict
			}
			return existing, nil
		}
		_ = s.db.DeleteSubdomainReservation(existing.ID) //nolint:errcheck
	}

	expiry := s.getUserSubdomainExpiry(user)
	res := &db.SubdomainReservation{
		UserID:    user.ID,
		Subdomain: subdomain,
		Domain:    domain,
		ExpiresAt: expiry,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.db.CreateSubdomainReservation(res); err != nil {
		return nil, ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    user.Email,
		Action:     "subdomain.promoted",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", subdomain, domain),
		Details:    "Subdomain promoted from active random lease to standard reservation",
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return res, nil
}

// UpdateReservationAccessControl updates access controls for a reservation.
func (s *portalService) UpdateReservationAccessControl(user *db.User, subdomain, domain, accessMode, passcode, whitelistIPs, ip string) error {
	res, err := s.db.GetSubdomainReservationByName(subdomain, domain)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return ErrNotFound
		}
		return ErrInternalError
	}

	if res.UserID != user.ID && user.Role != "admin" && user.Role != "owner" {
		return ErrForbidden
	}

	if passcode != "" {
		res.Passcode = HashPasscode(passcode)
	} else {
		res.Passcode = ""
	}
	res.WhitelistIPs = whitelistIPs
	if accessMode != "" {
		res.AccessMode = accessMode
	} else {
		res.AccessMode = "or"
	}

	if err := s.db.UpdateSubdomainReservation(res); err != nil {
		return ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    user.Email,
		Action:     "subdomain.access_control_updated",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", subdomain, domain),
		Details:    fmt.Sprintf("Access controls updated: Mode=%s, Passcode=[MASKED], IPs=%s", res.AccessMode, res.WhitelistIPs),
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return nil
}

// AdminListExtensions lists all reservations that have requested an extension.
func (s *portalService) AdminListExtensions() ([]*db.SubdomainReservation, error) {
	all, err := s.db.ListAllSubdomainReservations()
	if err != nil {
		return nil, ErrInternalError
	}

	var list []*db.SubdomainReservation
	for _, res := range all {
		if res.ExtensionRequested {
			list = append(list, res)
		}
	}

	return list, nil
}

// AdminApproveExtension approves an extension request.
func (s *portalService) AdminApproveExtension(actor, idStr string, days int, permanent bool, ip string) (*db.SubdomainReservation, error) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return nil, ErrInvalidRequest
	}

	res, err := s.db.GetSubdomainReservation(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, ErrInternalError
	}

	res.ExtensionRequested = false
	if permanent {
		res.ExpiresAt = nil
	} else {
		baseTime := time.Now()
		if res.ExpiresAt != nil && res.ExpiresAt.After(time.Now()) {
			baseTime = *res.ExpiresAt
		}
		extended := baseTime.AddDate(0, 0, days)
		res.ExpiresAt = &extended
	}
	res.ExpiryWarningSent = 0

	if err := s.db.UpdateSubdomainReservation(res); err != nil {
		return nil, ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    actor,
		Action:     "subdomain.extension_approved",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", res.Subdomain, res.Domain),
		Details:    fmt.Sprintf("Extension approved. Permanent: %t, Days: %d", permanent, days),
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return res, nil
}

// AdminDemoteReservation demotes a permanent reservation.
func (s *portalService) AdminDemoteReservation(actor, idStr, ip string) (*db.SubdomainReservation, error) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return nil, ErrInvalidRequest
	}

	res, err := s.db.GetSubdomainReservation(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, ErrInternalError
	}

	resOwner, err := s.db.GetUser(res.UserID)
	if err != nil {
		return nil, ErrInternalError
	}

	res.ExpiresAt = s.getUserSubdomainExpiry(resOwner)
	res.ExtensionRequested = false
	res.ExpiryWarningSent = 0

	if err := s.db.UpdateSubdomainReservation(res); err != nil {
		return nil, ErrInternalError
	}

	_ = s.db.WriteAuditEntry(&db.AuditEntry{ //nolint:errcheck
		ActorID:    actor,
		Action:     "subdomain.demoted",
		TargetType: "subdomain",
		TargetID:   fmt.Sprintf("%s.%s", res.Subdomain, res.Domain),
		Details:    "Subdomain demoted to standard temporary lease",
		IPAddress:  ip,
		CreatedAt:  time.Now(),
	})

	return res, nil
}

// getUserMaxReservations helper method matching the existing one in api.go
func (s *portalService) getUserMaxReservations(u *db.User) int {
	if s.cfg.RoleSettings != nil {
		if rs, ok := s.cfg.RoleSettings[u.Role]; ok && rs.MaxReservations != nil {
			return *rs.MaxReservations
		}
	}
	if u.Role == "admin" && s.cfg.AdminMaxReservations != nil {
		return *s.cfg.AdminMaxReservations
	}
	if u.Role == "owner" && s.cfg.OwnerMaxReservations != nil {
		return *s.cfg.OwnerMaxReservations
	}
	return s.cfg.DefaultMaxReservations
}

// getUserSubdomainExpiry helper method
func (s *portalService) getUserSubdomainExpiry(u *db.User) *time.Time {
	days := 7
	if s.cfg.RoleSettings != nil {
		if rs, ok := s.cfg.RoleSettings[u.Role]; ok && rs.SubdomainExpiryDays != nil {
			if *rs.SubdomainExpiryDays <= 0 {
				return nil
			}
			days = *rs.SubdomainExpiryDays
		}
	}
	t := time.Now().AddDate(0, 0, days)
	return &t
}
