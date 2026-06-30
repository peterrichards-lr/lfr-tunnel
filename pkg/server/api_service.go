package server

import (
	"crypto/rsa"
	"crypto/x509"
	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
	"sync"
)

// PortalService encapsulates the core business logic of the dashboard and API
// decoupling it from HTTP specific objects like ResponseWriter and Request.
type PortalService interface {
	GetMe(user *db.User) (*db.User, error)
	UpdateMe(user *db.User, first, last, prefName, tz, theme, notifs, lang *string) error
	UpdateOnboarding(user *db.User) error
	ListTokens(user *db.User) ([]*db.PersonalAccessToken, error)
	CreateToken(user *db.User, name, expiresAt, ip string) (string, *db.PersonalAccessToken, error)
	DeleteToken(user *db.User, tokenID string, ip string) error
	DeleteAccount(user *db.User, ip string) error

	MFASetup(user *db.User) (string, string, error)
	MFAEnable(user *db.User, secret, code, ip string) error
	MFADisable(user *db.User, code, ip string) error
	MFAVerify(tempToken, code, ip string) (*db.User, string, error)

	ListReservations(user *db.User) ([]*db.SubdomainReservation, int, int, error)
	CreateReservation(user *db.User, subdomain, domain, ip string) (*db.SubdomainReservation, error)
	DeleteReservation(user *db.User, idStr, ip string) error
	RequestExtension(user *db.User, idStr, ip string) (*db.SubdomainReservation, error)
	PromoteReservation(user *db.User, subdomain, domain, ip string) (*db.SubdomainReservation, error)
	UpdateReservationAccessControl(user *db.User, subdomain, domain, accessMode, passcode, whitelistIPs, ip string) error

	ListInvitations(user *db.User) ([]*db.GuestInvitation, error)
	CreateInvitation(user *db.User, subdomain, domain, name, email string, validityDays int, ip, portalURL, scheme, host string) (*db.GuestInvitation, string, error)
	DeleteInvitation(user *db.User, idStr, ip string) error
	ClaimInvitation(token, pfxPassword, ip string) ([]byte, *db.GuestInvitation, error)
	CSRSignInvitation(token string, csr []byte, ip string) ([]byte, *db.GuestInvitation, error)

	AdminListExtensions() ([]*db.SubdomainReservation, error)
	AdminApproveExtension(actor, idStr string, days int, permanent bool, ip string) (*db.SubdomainReservation, error)
	AdminDemoteReservation(actor, idStr, ip string) (*db.SubdomainReservation, error)
	AdminOverrideLimit(actor, email string, maxReservations *int, ip string) (*db.User, error)
	AdminOverrideTunnelsLimit(actor, email string, maxActiveTunnels *int, ip string) (*db.User, error)
}

type portalService struct {
	db        *db.DB
	cfg       *config.ServerConfig
	mailer    *NotificationService
	portalMap *sync.Map
	caCert    *x509.Certificate
	caKey     *rsa.PrivateKey
}

func NewPortalService(database *db.DB, cfg *config.ServerConfig, mailer *NotificationService, pMap *sync.Map, caCert *x509.Certificate, caKey *rsa.PrivateKey) PortalService {
	return &portalService{
		db:        database,
		cfg:       cfg,
		mailer:    mailer,
		portalMap: pMap,
		caCert:    caCert,
		caKey:     caKey,
	}
}
