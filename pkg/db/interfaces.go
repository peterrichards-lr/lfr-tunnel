package db

import (
	"database/sql"
	"time"
)

type UserRepository interface {
	CreateUser(u *User) error
	GetUser(id string) (*User, error)
	GetUserByEmail(email string) (*User, error)
	GetUserByVerificationToken(token string) (*User, error)
	GetUserByApprovalToken(token string) (*User, error)
	GetUserByClaimToken(token string) (*User, error)
	DeleteUser(id string) error
	UpdateUser(u *User) error
	UpdateUserOnboarding(userID string, status string, lastStep string, incReruns bool) error
	ListUsers() ([]*User, error)
	CountAdmins() (int, error)
	AnonymizeUserData(userID, anonymizedID string) error
}

type PATRepository interface {
	CreatePAT(pat *PersonalAccessToken) error
	GetPATByHash(hash string) (*PersonalAccessToken, error)
	ListPATs(userID string) ([]*PersonalAccessToken, error)
	RevokePAT(patID int64) error
	UpdatePATUsed(patID int64) error
	UpdatePATExpiry(patID int64, expiresAt *time.Time) error
	ListAllPATs() ([]*PersonalAccessToken, error)
	PruneExpiredOrRevokedPATs(retentionDays int) error
}

type SubdomainRepository interface {
	CreateSubdomainReservation(r *SubdomainReservation) error
	GetSubdomainReservation(id int64) (*SubdomainReservation, error)
	GetSubdomainReservationByName(subdomain, domain string) (*SubdomainReservation, error)
	ListSubdomainReservationsByUserID(userID string) ([]*SubdomainReservation, error)
	ListAllSubdomainReservations() ([]*SubdomainReservation, error)
	UpdateSubdomainReservation(r *SubdomainReservation) error
	DeleteSubdomainReservation(id int64) error
	GetExpiringSubdomainReservations(now time.Time, before time.Time) ([]*SubdomainReservation, error)
	DeleteExpiredSubdomainReservations(cutoff time.Time) error

	CreateSubdomainACL(acl *SubdomainACL) error
	GetSubdomainACL(id int64) (*SubdomainACL, error)
	GetSubdomainACLByName(subdomain, domain, identity string) (*SubdomainACL, error)
	ListSubdomainACL(subdomain, domain string) ([]*SubdomainACL, error)
	DeleteSubdomainACL(id int64) error
	DeleteExpiredSubdomainACLs(cutoff time.Time) error
}

type AuditRepository interface {
	WriteAuditEntry(e *AuditEntry) error
	ListAuditEntries(f AuditFilter) ([]*AuditEntry, error)
}

type MetricRepository interface {
	RecordTunnelMetric(m *TunnelMetric) error
	GetGlobalAnalytics(days int) (*GlobalAnalytics, error)
	GetUserAnalytics(userID string, days int) (*UserAnalytics, error)
	GetClientVersionStats() ([]ClientVersionStats, error)

	RecordGatewayStart(startTime time.Time) error
	RecordGatewayCleanShutdown() error
	GetGatewayRuns(limit int) ([]*GatewayRun, error)
}

type MagicLinkRepository interface {
	CreateMagicLink(email, tokenHash, clientIP string, expiresAt time.Time) error
	GetMagicLink(tokenHash string) (*MagicLink, error)
	PruneExpiredMagicLinks() error
	MarkMagicLinkUsed(id int) error
	InvalidateOtherMagicLinks(email string, excludeID int) error
	ListMagicLinks() ([]*MagicLink, error)
}

type BlacklistRepository interface {
	AddBlacklistIP(ip, reason string) error
	RemoveBlacklistIP(ip string) error
	IsBlacklisted(ip string) (bool, error)
	ListBlacklistedIPs() ([]*BlacklistEntry, error)
}

type VanityDomainStatusRepository interface {
	// StartVanityDomainAttempt records a fresh "add" attempt: sets requested_at to now and
	// clears every later stage/failure field, so a retry doesn't show stale state left over
	// from a previous failed attempt.
	StartVanityDomainAttempt(fullHost, userID string) error
	// MarkVanityDomainStage sets the given stage's timestamp to now. stage must be one of
	// "nginx_config", "cert_issued", or "live".
	MarkVanityDomainStage(fullHost, stage string) error
	// MarkVanityDomainFailed records which stage failed and why, without touching whichever
	// earlier stage timestamps already succeeded.
	MarkVanityDomainFailed(fullHost, failedStage, errorMessage string) error
	GetVanityDomainStatus(fullHost string) (*VanityDomainStatus, error)
	ListVanityDomainStatusForUser(userID string) ([]*VanityDomainStatus, error)
	ListAllVanityDomainStatus() ([]*VanityDomainStatus, error)
}

type GuestInviteRepository interface {
	CreateGuestInvitation(invite *GuestInvitation) error
	GetGuestInvitation(id int64) (*GuestInvitation, error)
	GetGuestInvitationByToken(token string) (*GuestInvitation, error)
	MarkGuestInvitationClaimed(token string) error
	ListGuestInvitationsByCreator(createdBy string) ([]*GuestInvitation, error)
	DeleteGuestInvitation(id int64) error
	ListAllGuestInvitations() ([]*GuestInvitation, error)
}

type SettingsRepository interface {
	GetAdminSetting(key string) (string, error)
	SetAdminSetting(key, value string) error
	GetAdminSettingOptional(key string) (string, bool, error)
}

type SystemRepository interface {
	Close() error
	GetConnection() *sql.DB
}

type QueuedWebhookMessage struct {
	ID          int64
	Title       string
	Description string
	Color       string
	Facts       string // JSON string
	CreatedAt   time.Time
}

type WebhookQueueRepository interface {
	EnqueueWebhookMessage(title, description, color, factsJSON string) error
	DequeueWebhookMessages(limit int) ([]*QueuedWebhookMessage, error)
	DeleteWebhookMessages(ids []int64) error
}
