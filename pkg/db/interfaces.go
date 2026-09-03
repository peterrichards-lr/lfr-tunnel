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

// AcknowledgementRepository stores the versioned-acknowledgement records behind the
// policy re-consent gate (#1707).
//
// Note what is absent: no update, no delete. The acceptance history is append-only, and
// that is enforced by there being no method that could do otherwise rather than by a
// convention a later caller could break.
type AcknowledgementRepository interface {
	RecordAcknowledgement(a *Acknowledgement) error
	HasAcknowledged(userID, documentID, version string) (bool, error)
	ListAcknowledgements(userID string) ([]*Acknowledgement, error)
	RecordFirstSeen(userID, documentID, version string, at time.Time) (time.Time, error)
	GetFirstSeen(userID, documentID, version string) (time.Time, error)
	MarkWarningNotified(userID, documentID, version string) (bool, error)
	ListPendingWarnings(documentID, version string, cutoff time.Time) ([]string, error)
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

	// Anonymous geographic distribution (#1152). Neither signature is capable of
	// carrying a user, which is the point: the aggregate is anonymous because nothing
	// in this layer can express the pairing, not because the current caller declines to.
	UpsertLocationStats(period string, stats []LocationStat) error
	GetLocationStats(period string) (string, []LocationStat, error)

	RecordGatewayStart(startTime time.Time) error
	RecordGatewayCleanShutdown() error
	GetGatewayRuns(limit int) ([]*GatewayRun, error)
}

// RegionProbeRepository stores the latency measurements clients already take and used to throw
// away, so edge placement can be judged on what users experience rather than on geography
// (#1151).
type RegionProbeRepository interface {
	RecordRegionProbes(userID string, samples []RegionProbeSample, at time.Time) error
	GetRegionLatency(days int) (*RegionLatencyReport, error)
}

// PortalSessionRepository persists logged-in portal sessions so a restart -- including a
// routine deploy -- does not sign every user out (#1304).
type PortalSessionRepository interface {
	UpsertPortalSession(sess *PortalSession) error
	GetPortalSession(tokenHash string) (*PortalSession, error)
	DeletePortalSession(tokenHash string) error
	DeletePortalSessionsForEmail(email string) (int64, error)
	PrunePortalSessions() (int64, error)
	CountActivePortalSessions() (int, error)
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
	AddAutoBan(ip, reason string, duration time.Duration, factor float64, maxDuration time.Duration) (*BlacklistEntry, error)
	RemoveBlacklistIP(ip string) error
	IsBlacklisted(ip string) (bool, error)
	ListBlacklistedIPs() ([]*BlacklistEntry, error)
	PruneBlacklist(retention time.Duration) (int64, error)
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
	// DeleteVanityDomainStatus removes tracking for a domain entirely -- called when a
	// lease is removed, so a domain no longer in use doesn't keep showing stale "live"
	// status from before it was torn down.
	DeleteVanityDomainStatus(fullHost string) error
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
