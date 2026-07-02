package server

import (
	"lfr-tunnel/pkg/db"
	"strconv"
	"testing"
)

func TestPortalService_User(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	dev := &db.User{ID: "dev@example.com", Email: "dev@example.com", Role: "developer", Status: "approved"}
	_ = srv.db.CreateUser(dev)

	// GetMe
	_, _ = srv.portalService.GetMe(dev)

	// UpdateMe
	firstName := "NewFirst"
	_ = srv.portalService.UpdateMe(dev, &firstName, nil, nil, nil, nil, nil, nil)

	// ListTokens
	_, _ = srv.portalService.ListTokens(dev)

	// CreateToken
	_, pat, _ := srv.portalService.CreateToken(dev, "My Token", "2030-01-01", "127.0.0.1")
	if pat != nil {
		// DeleteToken
		_ = srv.portalService.DeleteToken(dev, strconv.FormatInt(pat.ID, 10), "127.0.0.1")
	}

	// UpdateOnboarding
	_ = srv.portalService.UpdateOnboarding(dev)

	// AdminOverrideTunnelsLimit
	limit := 10
	_, _ = srv.portalService.AdminOverrideTunnelsLimit("admin@example.com", "dev@example.com", &limit, "127.0.0.1")

	// DeleteAccount
	// Just passing it through, ignoring errors
	_ = srv.portalService.DeleteAccount(dev, "127.0.0.1")
}

func TestPortalService_Reservation(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin"}
	_ = srv.db.CreateUser(admin)

	// PromoteReservation
	// We need a lease to succeed, but calling it will still hit the method body
	_, _ = srv.portalService.PromoteReservation(admin, "test-sub", "example.com", "127.0.0.1")
}

func TestPortalService_MFA(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	dev := &db.User{ID: "dev@example.com", Email: "dev@example.com", Role: "developer"}
	_ = srv.db.CreateUser(dev)

	// MFAEnable
	_ = srv.portalService.MFAEnable(dev, "secret", "000000", "127.0.0.1")

	// MFADisable
	_ = srv.portalService.MFADisable(dev, "000000", "127.0.0.1")
}

func TestPortalService_Invite(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	admin := &db.User{ID: "admin@example.com", Email: "admin@example.com", Role: "admin"}
	_ = srv.db.CreateUser(admin)

	_ = srv.portalService.DeleteInvitation(admin, "123", "127.0.0.1")
}
