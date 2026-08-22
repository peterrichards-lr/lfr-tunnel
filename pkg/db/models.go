package db

import (
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
)

type AuditEntry struct {
	ID         int64     `json:"id"`
	ActorID    string    `json:"actor_id"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	Details    string    `json:"details"`
	IPAddress  string    `json:"ip_address"`
	CreatedAt  time.Time `json:"created_at"`
}

type TunnelMetric struct {
	ID              int64     `json:"id"`
	UserID          string    `json:"user_id"`
	SubdomainPrefix string    `json:"subdomain_prefix"`
	FullHost        string    `json:"full_host"`
	BytesIn         int64     `json:"bytes_in"`
	BytesOut        int64     `json:"bytes_out"`
	ConnectedAt     time.Time `json:"connected_at"`
	RecordedAt      time.Time `json:"recorded_at"`
	NodeID          string    `json:"node_id"`
}

type MagicLink struct {
	ID        int        `json:"id"`
	Email     string     `json:"email"`
	TokenHash string     `json:"-"`
	ClientIP  string     `json:"client_ip"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at"`
}

// ActionPortalVisit records that someone loaded the portal. It is analytics rather than an
// administrative or security event, which is why the audit log hides it by default (#1208).
const ActionPortalVisit = "portal.visit"

type AuditFilter struct {
	ActorID  string
	Action   string
	TargetID string
	Limit    int
	Offset   int
	// IncludeRoutine keeps routine page-view entries in the results. Off by default: the
	// audit log is a record of administrative and security events, and dozens of near
	// identical visit rows bury the events someone opened it to find.
	IncludeRoutine bool
}

type User struct {
	ID                 string     `json:"id"`
	Email              string     `json:"email"`
	FirstName          string     `json:"first_name"`
	LastName           string     `json:"last_name"`
	PreferredName      string     `json:"preferred_name"`
	Role               string     `json:"role"`
	Status             string     `json:"status"`
	VerificationToken  string     `json:"-"`
	ApprovalToken      string     `json:"-"`
	ClaimToken         string     `json:"-"`
	Timezone           string     `json:"timezone"`
	AuthMethod         string     `json:"auth_method"`
	PreferredDomain    string     `json:"preferred_domain"`
	ThemePreference    string     `json:"theme_preference"`
	NotificationPrefs  string     `json:"notification_prefs"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	LastLoginAt        *time.Time `json:"last_login_at"`
	LastLoginIP        string     `json:"last_login_ip"`
	LastClientVersion  string     `json:"last_client_version"`
	LastClientOS       string     `json:"last_client_os"`
	TOTPSecret         string     `json:"-"`
	TOTPEnabled        bool       `json:"totp_enabled"`
	PolicyConsentAt    *time.Time `json:"policy_consent_at,omitempty"`
	LanguagePreference string     `json:"language_preference"`
	SubdomainStyle     string     `json:"subdomain_style"`
	RateLimit          int        `json:"rate_limit"`
	MaxReservations    *int       `json:"max_reservations,omitempty"`
	MaxCustomDomains   *int       `json:"max_custom_domains,omitempty"`
	MaxTunnels         *int       `json:"max_tunnels,omitempty"`
	OnboardingStatus   string     `json:"onboarding_status"`
	OnboardingLastStep string     `json:"onboarding_last_step"`
	OnboardingReruns   int        `json:"onboarding_reruns"`
}

type SubdomainReservation struct {
	ID                 int64      `json:"id"`
	UserID             string     `json:"user_id"`
	Subdomain          string     `json:"subdomain"`
	Domain             string     `json:"domain"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	ExtensionRequested bool       `json:"extension_requested"`
	Passcode           string     `json:"passcode"`
	WhitelistIPs       string     `json:"whitelist_ips"`
	AccessMode         string     `json:"access_mode"`
	ExpiryWarningSent  int        `json:"expiry_warning_sent"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type SubdomainACL struct {
	ID        int64      `json:"id"`
	Subdomain string     `json:"subdomain"`
	Domain    string     `json:"domain"`
	Identity  string     `json:"identity"`
	Name      string     `json:"name"`
	Email     string     `json:"email"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type GuestInvitation struct {
	ID        int64      `json:"id"`
	Token     string     `json:"token"`
	Subdomain string     `json:"subdomain"`
	Domain    string     `json:"domain"`
	Name      string     `json:"name"`
	Email     string     `json:"email"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedBy string     `json:"created_by"`
	ClaimedAt *time.Time `json:"claimed_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type PersonalAccessToken struct {
	ID          int64      `json:"id"`
	UserID      string     `json:"user_id"`
	TokenHash   string     `json:"-"`
	TokenPrefix string     `json:"token_prefix"`
	Name        string     `json:"name"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type GatewayRun struct {
	ID        int64      `json:"id"`
	StartTime time.Time  `json:"start_time"`
	EndTime   *time.Time `json:"end_time"`
}

type BlacklistEntry struct {
	IPAddress string    `json:"ip_address"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// VanityDomainStatus tracks a custom/vanity domain's provisioning progress through the
// stages runVanityDomainHook and lfr-vanity-hook.sh actually go through (see issue #964).
// Each stage field is nil until that stage is reached -- the portal renders that as an
// open/grey icon, and a set timestamp as a green tick (with the timestamp itself shown in
// the hover tooltip). One row per domain: a fresh "add" attempt resets every field below
// RequestedAt rather than appending a new row, so this always reflects the current attempt,
// not a historical log of every past one.
type VanityDomainStatus struct {
	FullHost      string     `json:"full_host"`
	UserID        string     `json:"user_id"`
	RequestedAt   *time.Time `json:"requested_at"`
	NginxConfigAt *time.Time `json:"nginx_config_at"`
	CertIssuedAt  *time.Time `json:"cert_issued_at"`
	LiveAt        *time.Time `json:"live_at"`
	FailedStage   string     `json:"failed_stage,omitempty"`
	ErrorMessage  string     `json:"error_message,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type DailyBandwidth struct {
	Date     string `json:"date"`
	BytesIn  int64  `json:"bytes_in"`
	BytesOut int64  `json:"bytes_out"`
}

type UserBandwidth struct {
	Email    string `json:"email"`
	BytesIn  int64  `json:"bytes_in"`
	BytesOut int64  `json:"bytes_out"`
}

type TunnelBandwidth struct {
	FullHost string `json:"full_host"`
	BytesIn  int64  `json:"bytes_in"`
	BytesOut int64  `json:"bytes_out"`
}

type PortalUsageStats struct {
	Version string `json:"version"`
	Count   int    `json:"count"`
}

type GlobalAnalytics struct {
	Daily            []DailyBandwidth   `json:"daily"`
	TopUsers         []UserBandwidth    `json:"top_users"`
	TopTunnels       []TunnelBandwidth  `json:"top_tunnels"`
	PortalStats      []PortalUsageStats `json:"portal_stats"`
	NodeDistribution map[string]int     `json:"node_distribution"`
}

type UserAnalytics struct {
	Daily   []DailyBandwidth  `json:"daily"`
	Tunnels []TunnelBandwidth `json:"tunnels"`
}

type ClientVersionStats struct {
	Version string `json:"version"`
	OS      string `json:"os"`
	Count   int    `json:"count"`
}
