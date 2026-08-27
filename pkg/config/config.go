package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type OwnerConfig struct {
	UserID string `yaml:"user_id"`
	Name   string `yaml:"name"`
	Role   string `yaml:"role"`
}

type SMTPServerConfig struct {
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	Username    string `yaml:"username"`
	Password    string `yaml:"password"`
	FromAddress string `yaml:"from_address"`
}

type WebhookConfig struct {
	Enabled              bool   `yaml:"enabled"`
	SlackURL             string `yaml:"slack_url"`
	TeamsURL             string `yaml:"teams_url"`
	BatchIntervalSeconds int    `yaml:"batch_interval_seconds"`
}

// SlackAppConfig holds the OAuth credentials for an installable Slack app (the
// "Add to Slack" / distributable-app flow, issue #909) -- distinct from
// WebhookConfig.SlackURL above, which is a much simpler incoming-webhook URL
// for posting admin alerts and needs no OAuth at all. All fields here are
// normally supplied via LFT_SLACK_* env vars (see secrets.env,
// docs/server/setup_guide.md §4.5) rather than this YAML block directly, same
// as SMTPServerConfig's credentials, since ClientSecret/SigningSecret/
// VerificationToken are real secrets even though AppID/ClientID aren't.
type SlackAppConfig struct {
	AppID             string `yaml:"app_id"`
	ClientID          string `yaml:"client_id"`
	ClientSecret      string `yaml:"client_secret"`
	SigningSecret     string `yaml:"signing_secret"`
	VerificationToken string `yaml:"verification_token"`
}

// ServerConfig holds configuration settings for the lfr-tunneld server.
type ServerConfig struct {
	Domains                []string `yaml:"domains"`
	BindAddr               string   `yaml:"bind_addr"`
	HTTPBindAddr           string   `yaml:"http_bind_addr"`
	ChiselBindAddr         string   `yaml:"chisel_bind_addr"`
	DefaultMaxReservations int      `yaml:"default_max_reservations"`
	AdminMaxReservations   *int     `yaml:"admin_max_reservations"`
	OwnerMaxReservations   *int     `yaml:"owner_max_reservations"`
	// DefaultMaxCustomDomains and its admin/owner overrides are a separate quota from the
	// plain reservation one above -- a custom domain costs meaningfully more than a
	// subdomain on the shared wildcard domain (its own Let's Encrypt certificate, its own
	// nginx vhost, ongoing Certbot renewal) and shouldn't share a counter with the cheaper
	// resource, or a user reserving both hits their limit faster than one who only ever
	// uses subdomains (#1004). Same three-tier resolution as MaxReservations otherwise:
	// per-role RoleSettings entry, then these admin/owner-specific overrides, then this
	// default.
	DefaultMaxCustomDomains    int                       `yaml:"default_max_custom_domains"`
	AdminMaxCustomDomains      *int                      `yaml:"admin_max_custom_domains"`
	OwnerMaxCustomDomains      *int                      `yaml:"owner_max_custom_domains"`
	DefaultMaxActiveTunnels    int                       `yaml:"default_max_active_tunnels"`
	AdminMaxActiveTunnels      *int                      `yaml:"admin_max_active_tunnels"`
	OwnerMaxActiveTunnels      *int                      `yaml:"owner_max_active_tunnels"`
	AllowClientAutoReservation bool                      `yaml:"allow_client_auto_reservation"`
	SubdomainQuarantineDays    int                       `yaml:"subdomain_quarantine_days"`
	SSLCertFile                string                    `yaml:"ssl_cert_file"`
	SSLKeyFile                 string                    `yaml:"ssl_key_file"`
	ClientCAFile               string                    `yaml:"client_ca_file"`
	ClientCAKeyFile            string                    `yaml:"client_ca_key_file"`
	ForceClientCert            bool                      `yaml:"force_client_cert"`
	ForcePasscode              bool                      `yaml:"force_passcode"`
	ForceIPWhitelist           bool                      `yaml:"force_ip_whitelist"`
	ForceMFA                   bool                      `yaml:"force_mfa"`
	DBPath                     string                    `yaml:"db_path"`
	SMTPServer                 SMTPServerConfig          `yaml:"smtp_server"`
	Webhooks                   WebhookConfig             `yaml:"webhooks"`
	SlackApp                   SlackAppConfig            `yaml:"slack_app"`
	AdminNotificationEmail     string                    `yaml:"admin_notification_email"`
	InsecureSkipVerify         bool                      `yaml:"insecure_skip_verify"`
	Owner                      OwnerConfig               `yaml:"owner"`
	AllowedEmailDomains        []string                  `yaml:"allowed_email_domains"`
	IPBlacklist                []string                  `yaml:"ip_blacklist"`
	MaxTunnelRateLimit         int                       `yaml:"max_tunnel_rate_limit"`
	EnableUserPortal           bool                      `yaml:"enable_user_portal"`
	PortalSessionDuration      time.Duration             `yaml:"portal_session_duration"`
	MinClientVersion           string                    `yaml:"min_client_version"`
	LatestClientVersion        string                    `yaml:"latest_client_version"`
	DocumentationURL           string                    `yaml:"documentation_url"`
	RepositoryURL              string                    `yaml:"repository_url"`
	SecureTokenGuideURL        string                    `yaml:"secure_token_guide_url"`
	DockerHubURL               string                    `yaml:"docker_hub_url"`
	StatusPageURL              string                    `yaml:"status_page_url"`
	PruneInterval              time.Duration             `yaml:"prune_interval"`
	MagicLinkExpiry            time.Duration             `yaml:"magic_link_expiry"`
	InviteLinkExpiry           time.Duration             `yaml:"invite_link_expiry"`
	VerificationLinkExpiry     time.Duration             `yaml:"verification_link_expiry"`
	PrivacyPolicyURL           string                    `yaml:"privacy_policy_url"`
	CookiePolicyURL            string                    `yaml:"cookie_policy_url"`
	EnforcePolicyConsent       bool                      `yaml:"enforce_policy_consent"`
	EnableOnboarding           bool                      `yaml:"enable_onboarding"`
	DisableBackupScheduler     bool                      `yaml:"disable_backup_scheduler"`
	DockerImage                string                    `yaml:"docker_image"`
	DockerBypassURL            string                    `yaml:"docker_bypass_url"`
	MaintenanceTriggerPath     string                    `yaml:"maintenance_trigger_path"`
	ClientPlatforms            map[string]PlatformConfig `yaml:"client_platforms"`
	VisitorTimeout             time.Duration             `yaml:"visitor_timeout"`
	PATRetentionDays           int                       `yaml:"pat_retention_days"`
	EnableWAF                  bool                      `yaml:"enable_waf"`
	DisableEmailLogin          bool                      `yaml:"disable_email_login"`
	// DisableNewRegistrations is the umbrella flag gating NEW account creation regardless
	// of method (email registration or first-time SSO login) -- see issue #910. This is
	// distinct from DisableEmailLogin, which only affects the email-specific login/register
	// UI and backend path; without this flag, first-time SSO login always auto-provisioned
	// a new account with no way to close that off independent of disabling SSO entirely.
	DisableNewRegistrations bool          `yaml:"disable_new_registrations"`
	DisableClientDownloads  bool          `yaml:"disable_client_downloads"`
	DisableBrew             bool          `yaml:"disable_brew"`
	DisableScoop            bool          `yaml:"disable_scoop"`
	DisableAPIRateLimit     bool          `yaml:"disable_api_rate_limit"`
	AutoBan                 AutoBanConfig `yaml:"auto_ban"`
	// TrustedProxies lists the CIDRs whose X-Real-IP / X-Forwarded-For headers may be believed
	// (#1325). A header is only as trustworthy as the hop that set it, and the resolved address
	// drives the per-tunnel IP whitelist, the API rate limiter and every audit entry.
	//
	// Empty means loopback, which matches every documented deployment: nginx terminates TLS on
	// the same host and proxies to 127.0.0.1. Widen it only for a proxy you actually run --
	// naming a range you do not control hands anyone inside it the ability to choose their own
	// client address.
	TrustedProxies []string `yaml:"trusted_proxies"`
	PortalURL      string   `yaml:"portal_url"`
	// CentralURL is how the control plane advertises itself to clients in the region map.
	// Empty keeps the historical construction, "https://tunnel." + the first configured
	// domain, which assumes both the scheme and the hostname prefix (#1286). Set it when
	// either is untrue -- a control plane at gateway.example.com, or one behind a proxy
	// terminating TLS elsewhere, otherwise hands clients a URL that does not answer.
	CentralURL      string           `yaml:"central_url"`
	ControlPlaneURL string           `yaml:"control_plane_url"`
	EdgeToken       string           `yaml:"edge_token"`
	EdgeNodes       []EdgeNodeConfig `yaml:"edge_nodes"`
	// EdgeProvisionerURL points at the optional, AWS-specific edge-provisioner
	// sidecar (see cmd/lfr-tunnel-edge-provisioner, issue #888) that this server
	// calls to start/stop/restart edge node instances and manage their stop/start
	// schedules. Empty by default -- when unset, those portal actions are simply
	// absent, not erroring. Never set this to anything but a loopback address.
	EdgeProvisionerURL         string `yaml:"edge_provisioner_url"`
	EdgeProvisionerTokenFile   string `yaml:"edge_provisioner_token_file"`
	EdgeShutdownWarningMinutes int    `yaml:"edge_shutdown_warning_minutes" json:"edge_shutdown_warning_minutes"`
	VanityDomainHook           string `yaml:"vanity_domain_hook"`
	// DNSHook is an operator-supplied executable that publishes and withdraws the DNS record
	// for a tunnel, so a visitor reaches the gateway actually serving it rather than whichever
	// one the wildcard points at (#1247). Empty leaves DNS alone entirely.
	//
	// Contract, mirroring the power hook in pkg/ops:
	//
	//	<hook> upsert <fqdn> <target>
	//	<hook> delete <fqdn>
	//
	// See scripts/common/lfr-dns-hook-route53.sh for a reference implementation. This belongs
	// on the control plane, which is the only gateway that knows which node holds which lease.
	DNSHook string `yaml:"dns_hook"`
	// DNSWithdrawGrace is how long a withdrawal waits before deleting the record. A lease is
	// cleaned up on any disconnect, so deleting immediately would blank the record on every
	// wifi blip. Zero uses the built-in default.
	DNSWithdrawGrace time.Duration          `yaml:"dns_withdraw_grace"`
	ProxyHeaders     map[string]string      `yaml:"proxy_headers"`
	RoleSettings     map[string]RoleSetting `yaml:"role_settings"`

	// Dynamic SSO/OIDC Providers
	SSOProviders []SSOProviderConfig `yaml:"sso_providers"`

	ViewConfigAllowedRole string `yaml:"view_config_allowed_role"`

	DomainAllocationRule string `yaml:"domain_allocation_rule"`
	DefaultDomain        string `yaml:"default_domain"`
	// TunnelDomains restricts which of Domains a tunnel may actually be issued on.
	//
	// Domains is the list this gateway *answers* on, and an edge needs regional names in it
	// -- in.lfr-demo.se, aws-edge-in.lfr-demo.se -- for direct and internal addressing. But a
	// lease issued on one of those puts the serving node into the visitor's URL, so the URL
	// changes the moment the client moves to another gateway, which is exactly what a planned
	// move (#1246) was meant to avoid. The region belongs in DNS resolution, not in the name
	// a visitor types (#1285).
	//
	// Set this to the shared domain(s) on an edge so registration always issues an
	// apex-level host regardless of which node handled it. Empty means every entry in
	// Domains is eligible, which is correct for a single-gateway deployment and for central.
	// Entries not present in Domains are dropped at startup -- a gateway cannot issue a host
	// it does not serve.
	TunnelDomains []string `yaml:"tunnel_domains"`
}

type RoleSetting struct {
	MaxReservations      *int  `yaml:"max_reservations" json:"max_reservations"`
	MaxCustomDomains     *int  `yaml:"max_custom_domains" json:"max_custom_domains"`
	SubdomainExpiryDays  *int  `yaml:"subdomain_expiry_days" json:"subdomain_expiry_days"`
	AllowAutoReservation *bool `yaml:"allow_auto_reservation" json:"allow_auto_reservation"`
}

type PlatformConfig struct {
	URL              string `yaml:"url" json:"url"`
	BinaryName       string `yaml:"binary_name" json:"binary_name"`
	InstallDir       string `yaml:"install_dir" json:"install_dir"`
	SHA256           string `yaml:"sha256" json:"sha256"`
	Cmd              string `yaml:"cmd" json:"cmd"`
	CmdLabel         string `yaml:"cmd_label" json:"cmd_label"`
	CmdFallback      string `yaml:"cmd_fallback" json:"cmd_fallback"`
	CmdFallbackLabel string `yaml:"cmd_fallback_label" json:"cmd_fallback_label"`
	Recommended      string `yaml:"recommended" json:"recommended"` // "cmd", "cmd_fallback", "url"
	ShowDownload     *bool  `yaml:"show_download" json:"show_download"`
	DownloadLabel    string `yaml:"download_label" json:"download_label"`
}

type SSOProviderConfig struct {
	ID              string `yaml:"id"`
	Name            string `yaml:"name"`
	ClientID        string `yaml:"client_id"`
	ClientSecret    string `yaml:"client_secret"`
	IssuerURL       string `yaml:"issuer_url"`
	Icon            string `yaml:"icon"`
	SkipIssuerCheck bool   `yaml:"skip_issuer_check"`
}

// AutoBanConfig governs the automatic bans the API rate limiter issues (#1353).
//
// Modelled on fail2ban, because the failure mode it guards against is the same: an automatic
// ban acts on a noisy signal -- shared NAT, CGNAT, mobile carriers and corporate egress all
// look like one busy address -- so a ban that never lifts eventually punishes someone who did
// nothing. fail2ban answers that with a finite ban time, a longer one for repeat offenders,
// and an ignore list; so does this.
//
// The blacklist is enforced ahead of all routing, so a banned address cannot reach the admin
// API to undo its own ban. That is what makes the expiry matter rather than merely being tidy.
type AutoBanConfig struct {
	// Duration is the ban time for a first offence. Zero means the ban never expires, which
	// was the behaviour before this existed -- available deliberately, but not the default.
	Duration time.Duration `yaml:"duration"`
	// Increment turns on longer bans for repeat offenders (fail2ban's bantime.increment).
	Increment bool `yaml:"increment"`
	// Factor multiplies the ban time per prior ban. 2 doubles it each time.
	Factor float64 `yaml:"factor"`
	// MaxDuration caps the escalation, so a repeat offender cannot reach a ban that is
	// effectively permanent by another route.
	MaxDuration time.Duration `yaml:"max_duration"`
	// HistoryRetention is how long an expired ban is kept so that escalation still sees it.
	// An address only starts from zero once it has stayed clean for this long.
	HistoryRetention time.Duration `yaml:"history_retention"`
	// Ignore lists CIDRs that are never auto-banned -- fail2ban's ignoreip. Loopback is the
	// default: locking an operator out of their own gateway is a worse outcome than a missed
	// ban, and the ban cannot be lifted from the banned address.
	Ignore []string `yaml:"ignore"`
}

// AcceptedTokenHashes returns every hash that authenticates as this node: the current one plus
// any additional hashes configured for a rotation in progress.
//
// One accessor rather than each caller assembling the list, for the reason #1308 exists -- the
// REST endpoints and the control channel must agree on what counts as this node's token, or a
// half-rotated edge passes /api/internal/* and then silently fails to establish its control
// channel, losing schedules, shutdown warnings and lease kicks.
//
// Empty entries are dropped: an unconfigured node has TokenHash "", and a caller presenting ""
// must not match it.
func (e EdgeNodeConfig) AcceptedTokenHashes() []string {
	out := make([]string, 0, 1+len(e.AdditionalTokenHashes))
	if e.TokenHash != "" {
		out = append(out, e.TokenHash)
	}
	for _, h := range e.AdditionalTokenHashes {
		if h != "" {
			out = append(out, h)
		}
	}
	return out
}

type EdgeNodeConfig struct {
	ID        string `yaml:"id"`
	TokenHash string `yaml:"token_hash"`
	// AdditionalTokenHashes are accepted alongside TokenHash, so a token can be rotated without
	// a flag day: add the incoming hash, roll the edges one at a time, then remove the old one
	// (#1491). Without it, changing a token means every edge fails to authenticate until it has
	// been re-provisioned -- which is why rotation is something nobody does.
	//
	// Additive on purpose. Every existing deployment has TokenHash and nothing else, and must
	// keep working untouched.
	AdditionalTokenHashes []string `yaml:"additional_token_hashes,omitempty"`
	URL                   string   `yaml:"url"`
	// Schedule optionally declares when this node stops and starts, for deployments with no
	// edge-provisioner sidecar to ask (#1282). Nil means "ask the provisioner, or treat the
	// node as unscheduled" -- the previous and still the default behaviour.
	//
	// The provisioner keeps precedence where it is configured and working, since it reflects
	// what the scheduler will actually do. This is a fallback, not an override.
	Schedule *EdgeScheduleConfig `yaml:"schedule,omitempty"`
}

// EdgeScheduleConfig is a statically declared stop/start window for an edge node.
//
// Only the *discovery* of a schedule was ever AWS-specific. Everything downstream -- the
// shutdown warning, a client moving ahead of the stop (#1246), the Disabled classification
// (#887), an edge knowing its own window (#1276) -- is provider-neutral, but none of it
// happened without EventBridge because there was no other way to tell central the times.
// Anyone stopping a node with cron can now say so here.
type EdgeScheduleConfig struct {
	Enabled   bool   `yaml:"enabled"`
	StopTime  string `yaml:"stop_time"`  // "HH:MM" in Timezone
	StartTime string `yaml:"start_time"` // "HH:MM" in Timezone
	Timezone  string `yaml:"timezone"`   // IANA name, e.g. "Asia/Kolkata"
}

// ClientHooksConfig defines script paths/commands for client lifecycle hook triggers.
type ClientHooksConfig struct {
	WarningReceived string `yaml:"warning_received" json:"warning_received,omitempty"`
	Stopping        string `yaml:"stopping" json:"stopping,omitempty"`
	Stopped         string `yaml:"stopped" json:"stopped,omitempty"`
	Starting        string `yaml:"starting" json:"starting,omitempty"`
	Started         string `yaml:"started" json:"started,omitempty"`
}

// ClientConfig holds configuration settings for the lfr-tunnel client.
type ClientConfig struct {
	ServerURL          string            `yaml:"server_url"`
	AuthToken          string            `yaml:"auth_token"`
	Subdomain          string            `yaml:"subdomain"`
	CustomDomain       string            `yaml:"custom_domain"`
	Ports              []int             `yaml:"ports"`
	TokenFile          string            `yaml:"token_file"`
	MaintenancePath    string            `yaml:"maintenance_path"`
	RateLimit          int               `yaml:"rate_limit"`
	BasicAuth          string            `yaml:"basic_auth"`
	TargetHost         string            `yaml:"target_host"`
	Passcode           string            `yaml:"passcode"`
	WhitelistIPs       string            `yaml:"whitelist_ips"`
	Region             string            `yaml:"region"`
	Regions            map[string]string `yaml:"regions"`
	Latency            time.Duration     `yaml:"latency"`
	Bandwidth          string            `yaml:"bandwidth"`
	PreserveHost       bool              `yaml:"preserve_host"`
	BypassProxy        bool              `yaml:"bypass_proxy,omitempty"`
	InsecureSkipVerify bool              `yaml:"insecure_skip_verify,omitempty"`
	Theme              string            `yaml:"theme,omitempty"`
	NavPlacement       string            `yaml:"nav_placement,omitempty"`
	// LogDir is where the persistent traffic and error logs are written. Empty means
	// ~/.lfr-tunnel/logs (#1223). A leading ~ is expanded, since this is a value people
	// type by hand.
	LogDir string            `yaml:"log_dir,omitempty"`
	Hooks  ClientHooksConfig `yaml:"hooks" json:"hooks,omitempty"`
	// DisableLatencyReport suppresses reporting the region probe RTTs this client already
	// measures to choose a gateway (#1151). Opt-out rather than opt-in because the figures are
	// only useful in aggregate and a sample nobody sends is a region nobody can justify -- but
	// a self-hosted deployment that would rather send nothing has one switch to throw.
	//
	// What is reported is a region name and a round trip in milliseconds. No IP, no location,
	// nothing derived from either.
	DisableLatencyReport bool `yaml:"disable_latency_report,omitempty"`
}

// DefaultServerConfig returns a ServerConfig with sensible default values.
func DefaultServerConfig() *ServerConfig {
	trueVal := true
	return &ServerConfig{
		BindAddr:                   ":443",
		HTTPBindAddr:               ":80",
		ChiselBindAddr:             ":8081",
		DefaultMaxReservations:     3,
		DefaultMaxCustomDomains:    1,
		DefaultMaxActiveTunnels:    3,
		SubdomainQuarantineDays:    3,
		MaxTunnelRateLimit:         100,
		EdgeShutdownWarningMinutes: 5,
		EnableUserPortal:           true,
		EnableOnboarding:           true,
		PortalSessionDuration:      24 * time.Hour,
		MinClientVersion:           "v1.0.0",
		LatestClientVersion:        "",
		DocumentationURL:           DefaultDocumentationURL,
		RepositoryURL:              DefaultRepositoryURL,
		SecureTokenGuideURL:        DefaultSecureTokenGuideURL,
		DockerHubURL:               DefaultDockerHubURL,
		StatusPageURL:              DefaultStatusPageURL,
		PruneInterval:              1 * time.Hour,
		MagicLinkExpiry:            15 * time.Minute,
		PATRetentionDays:           30,
		InviteLinkExpiry:           7 * 24 * time.Hour,
		VerificationLinkExpiry:     24 * time.Hour,
		DockerImage:                "peterjrichards/lfr-tunnel:latest",
		DockerBypassURL:            DefaultDockerBypassURL,
		VisitorTimeout:             5 * time.Minute,
		EnableWAF:                  true,
		// 24h matches what the ban alert has always told operators, and sits in the range the
		// comparable tools use for an automated block. Escalation is on by default: a genuine
		// repeat offender should be held longer, and it is the presence of escalation that
		// makes a finite first ban safe rather than lenient.
		AutoBan: AutoBanConfig{
			Duration:         24 * time.Hour,
			Increment:        true,
			Factor:           2,
			MaxDuration:      7 * 24 * time.Hour,
			HistoryRetention: 30 * 24 * time.Hour,
			Ignore:           []string{"127.0.0.1/32", "::1/128"},
		},
		RoleSettings: map[string]RoleSetting{
			"admin": {
				AllowAutoReservation: &trueVal,
			},
			"owner": {
				AllowAutoReservation: &trueVal,
			},
		},
		ClientPlatforms: map[string]PlatformConfig{
			"macos_arm64": {
				URL:              "/static/downloads/lfr-tunnel-darwin-arm64",
				BinaryName:       "lfr-tunnel-darwin-arm64",
				Cmd:              "brew tap peterrichards-lr/tap && brew trust peterrichards-lr/tap && brew install lfr-tunnel",
				CmdLabel:         "🚀 Recommended (Package Manager):",
				CmdFallback:      "curl -sSfL https://raw.githubusercontent.com/peterrichards-lr/lfr-tunnel/master/pkg/server/static/install.sh | sh",
				CmdFallbackLabel: "🛠️ Direct Script Fallback:",
				Recommended:      "cmd",
				ShowDownload:     &trueVal,
				DownloadLabel:    "⬇️ Download Binary",
			},
			"macos_amd64": {
				URL:              "/static/downloads/lfr-tunnel-darwin-amd64",
				BinaryName:       "lfr-tunnel-darwin-amd64",
				Cmd:              "brew tap peterrichards-lr/tap && brew trust peterrichards-lr/tap && brew install lfr-tunnel",
				CmdLabel:         "🚀 Recommended (Package Manager):",
				CmdFallback:      "curl -sSfL https://raw.githubusercontent.com/peterrichards-lr/lfr-tunnel/master/pkg/server/static/install.sh | sh",
				CmdFallbackLabel: "🛠️ Direct Script Fallback:",
				Recommended:      "cmd",
				ShowDownload:     &trueVal,
				DownloadLabel:    "⬇️ Download Binary",
			},
			"windows_amd64": {
				URL:              "/static/downloads/lfr-tunnel-windows-amd64.exe",
				BinaryName:       "lfr-tunnel-windows-amd64.exe",
				Cmd:              "scoop bucket add peterrichards-lr https://github.com/peterrichards-lr/scoop-bucket && scoop install lfr-tunnel",
				CmdLabel:         "🚀 Recommended (Package Manager):",
				CmdFallback:      "iwr https://raw.githubusercontent.com/peterrichards-lr/lfr-tunnel/master/pkg/server/static/install.ps1 | iex",
				CmdFallbackLabel: "🛠️ Direct Script Fallback:",
				Recommended:      "cmd",
				ShowDownload:     &trueVal,
				DownloadLabel:    "⬇️ Download Binary",
			},
			"linux_amd64": {
				URL:           "/static/downloads/lfr-tunnel-linux-amd64",
				BinaryName:    "lfr-tunnel-linux-amd64",
				Cmd:           "curl -sSfL https://raw.githubusercontent.com/peterrichards-lr/lfr-tunnel/master/pkg/server/static/install.sh | sh",
				CmdLabel:      "🚀 Recommended (Direct Script):",
				Recommended:   "cmd",
				ShowDownload:  &trueVal,
				DownloadLabel: "⬇️ Download Binary",
			},
		},
	}
}

// DefaultClientConfig returns a ClientConfig with sensible default values.
func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		ServerURL: DefaultServerURL,
		Ports:     []int{8080},
		Regions:   map[string]string{}, // Regions are now fetched dynamically from the Control Plane at runtime
	}
}

// LoadServerConfig loads the server configuration from a YAML file and/or environment variables.
func LoadServerConfig(path string) (*ServerConfig, error) {
	cfg := DefaultServerConfig()

	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close() //nolint:errcheck

		dec := yaml.NewDecoder(file)
		if err := dec.Decode(cfg); err != nil {
			return nil, err
		}
	}

	// Environment variable overrides
	if val := os.Getenv("LFT_DOMAINS"); val != "" {
		domains := strings.Split(val, ",")
		for i, d := range domains {
			domains[i] = strings.ToLower(strings.TrimSpace(d))
		}
		cfg.Domains = domains
	}
	if val := os.Getenv("LFT_BIND_ADDR"); val != "" {
		cfg.BindAddr = val
	}
	if val := os.Getenv("LFT_HTTP_BIND_ADDR"); val != "" {
		cfg.HTTPBindAddr = val
	}
	if val := os.Getenv("LFT_CHISEL_BIND_ADDR"); val != "" {
		cfg.ChiselBindAddr = val
	}
	if val := os.Getenv("LFT_SSL_CERT"); val != "" {
		cfg.SSLCertFile = val
	}
	if val := os.Getenv("LFT_SSL_KEY"); val != "" {
		cfg.SSLKeyFile = val
	}
	if val := os.Getenv("LFT_CLIENT_CA_FILE"); val != "" {
		cfg.ClientCAFile = val
	}
	if val := os.Getenv("LFT_CLIENT_CA_KEY_FILE"); val != "" {
		cfg.ClientCAKeyFile = val
	}
	if val := os.Getenv("LFT_FORCE_CLIENT_CERT"); val != "" {
		cfg.ForceClientCert = strings.ToLower(val) == "true" || val == "1"
	}
	if val := os.Getenv("LFT_FORCE_PASSCODE"); val != "" {
		cfg.ForcePasscode = strings.ToLower(val) == "true" || val == "1"
	}
	if val := os.Getenv("LFT_FORCE_IP_WHITELIST"); val != "" {
		cfg.ForceIPWhitelist = strings.ToLower(val) == "true" || val == "1"
	}
	if val := os.Getenv("LFT_FORCE_MFA"); val != "" {
		cfg.ForceMFA = strings.ToLower(val) == "true" || val == "1"
	}
	if val := os.Getenv("LFT_DB_PATH"); val != "" {
		cfg.DBPath = val
	}
	if val := os.Getenv("LFT_SMTP_HOST"); val != "" {
		cfg.SMTPServer.Host = val
	}
	if val := os.Getenv("LFT_SMTP_PORT"); val != "" {
		if p, err := strconv.Atoi(val); err == nil {
			cfg.SMTPServer.Port = p
		}
	}
	if val := os.Getenv("LFT_SMTP_USERNAME"); val != "" {
		cfg.SMTPServer.Username = val
	}
	if val := os.Getenv("LFT_SMTP_PASSWORD"); val != "" {
		cfg.SMTPServer.Password = val
	}
	if val := os.Getenv("LFT_SMTP_FROM"); val != "" {
		cfg.SMTPServer.FromAddress = val
	}
	if val := os.Getenv("LFT_SLACK_APP_ID"); val != "" {
		cfg.SlackApp.AppID = val
	}
	if val := os.Getenv("LFT_SLACK_CLIENT_ID"); val != "" {
		cfg.SlackApp.ClientID = val
	}
	if val := os.Getenv("LFT_SLACK_CLIENT_SECRET"); val != "" {
		cfg.SlackApp.ClientSecret = val
	}
	if val := os.Getenv("LFT_SLACK_SIGNING_SECRET"); val != "" {
		cfg.SlackApp.SigningSecret = val
	}
	if val := os.Getenv("LFT_SLACK_VERIFICATION_TOKEN"); val != "" {
		cfg.SlackApp.VerificationToken = val
	}
	if val := os.Getenv("LFT_ADMIN_EMAIL"); val != "" {
		cfg.AdminNotificationEmail = val
	}
	if val := os.Getenv("LFT_OWNER_USER_ID"); val != "" {
		cfg.Owner.UserID = strings.ToLower(strings.TrimSpace(val))
	}
	if val := os.Getenv("LFT_OWNER_NAME"); val != "" {
		cfg.Owner.Name = val
	}
	if val := os.Getenv("LFT_OWNER_ROLE"); val != "" {
		cfg.Owner.Role = val
	}
	if val := os.Getenv("LFT_ALLOWED_DOMAINS"); val != "" {
		domains := strings.Split(val, ",")
		for i, d := range domains {
			domains[i] = strings.ToLower(strings.TrimSpace(d))
		}
		cfg.AllowedEmailDomains = domains
	}

	if val := os.Getenv("LFT_INSECURE_SKIP_VERIFY"); val != "" {
		cfg.InsecureSkipVerify = strings.ToLower(val) == "true" || val == "1"
	}
	if val := os.Getenv("LFT_ENABLE_USER_PORTAL"); val != "" {
		cfg.EnableUserPortal = strings.ToLower(val) == "true" || val == "1"
	}
	if val := os.Getenv("LFT_PORTAL_URL"); val != "" {
		cfg.PortalURL = val
	}
	if val := os.Getenv("LFT_CONTROL_PLANE_URL"); val != "" {
		cfg.ControlPlaneURL = val
	}
	if val := os.Getenv("LFT_EDGE_TOKEN"); val != "" {
		cfg.EdgeToken = val
	}
	if val := os.Getenv("LFT_EDGE_SHUTDOWN_WARNING_MINUTES"); val != "" {
		if m, err := strconv.Atoi(val); err == nil {
			cfg.EdgeShutdownWarningMinutes = m
		}
	}
	if val := os.Getenv("LFT_PORTAL_SESSION_DURATION"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			cfg.PortalSessionDuration = d
		}
	}
	if val := os.Getenv("LFT_MIN_CLIENT_VERSION"); val != "" {
		cfg.MinClientVersion = val
	}
	if val := os.Getenv("LFT_LATEST_CLIENT_VERSION"); val != "" {
		cfg.LatestClientVersion = val
	}
	if val := os.Getenv("LFT_DOCKER_IMAGE"); val != "" {
		cfg.DockerImage = val
	}
	if val := os.Getenv("LFT_DEFAULT_MAX_RESERVATIONS"); val != "" {
		if limit, err := strconv.Atoi(val); err == nil {
			cfg.DefaultMaxReservations = limit
		}
	}
	if val := os.Getenv("LFT_DEFAULT_MAX_CUSTOM_DOMAINS"); val != "" {
		if limit, err := strconv.Atoi(val); err == nil {
			cfg.DefaultMaxCustomDomains = limit
		}
	}
	if val := os.Getenv("LFT_DEFAULT_MAX_ACTIVE_TUNNELS"); val != "" {
		if limit, err := strconv.Atoi(val); err == nil {
			cfg.DefaultMaxActiveTunnels = limit
		}
	}
	if val := os.Getenv("LFT_SUBDOMAIN_QUARANTINE_DAYS"); val != "" {
		if days, err := strconv.Atoi(val); err == nil {
			cfg.SubdomainQuarantineDays = days
		}
	}
	if val := os.Getenv("LFT_VISITOR_TIMEOUT"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			cfg.VisitorTimeout = d
		}
	}
	if val := os.Getenv("LFT_PAT_RETENTION_DAYS"); val != "" {
		if days, err := strconv.Atoi(val); err == nil {
			cfg.PATRetentionDays = days
		}
	}
	if val := os.Getenv("LFT_ENABLE_WAF"); val != "" {
		cfg.EnableWAF = strings.ToLower(val) == "true" || val == "1"
	}
	if val := os.Getenv("LFT_DISABLE_EMAIL_LOGIN"); val != "" {
		cfg.DisableEmailLogin = strings.ToLower(val) == "true" || val == "1"
	}
	if val := os.Getenv("LFT_DISABLE_NEW_REGISTRATIONS"); val != "" {
		cfg.DisableNewRegistrations = strings.ToLower(val) == "true" || val == "1"
	}
	if val := os.Getenv("LFT_DISABLE_CLIENT_DOWNLOADS"); val != "" {
		cfg.DisableClientDownloads = strings.ToLower(val) == "true" || val == "1"
	}
	if val := os.Getenv("LFT_DISABLE_BREW"); val != "" {
		cfg.DisableBrew = strings.ToLower(val) == "true" || val == "1"
	}
	if val := os.Getenv("LFT_DISABLE_SCOOP"); val != "" {
		cfg.DisableScoop = strings.ToLower(val) == "true" || val == "1"
	}
	if val := os.Getenv("LFT_DISABLE_API_RATE_LIMIT"); val != "" {
		cfg.DisableAPIRateLimit = strings.ToLower(val) == "true" || val == "1"
	}
	if val := os.Getenv("LFT_PROXY_HEADERS"); val != "" {
		var m map[string]string
		if strings.HasPrefix(strings.TrimSpace(val), "{") {
			if err := json.Unmarshal([]byte(val), &m); err == nil {
				cfg.ProxyHeaders = m
			}
		}
		if len(cfg.ProxyHeaders) == 0 {
			m = make(map[string]string)
			pairs := strings.Split(val, ",")
			for _, pair := range pairs {
				kv := strings.SplitN(pair, "=", 2)
				if len(kv) == 2 {
					m[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
				}
			}
			if len(m) > 0 {
				cfg.ProxyHeaders = m
			}
		}
	}

	// Every admin alert -- new registrations, approval requests, IP bans, tunnel
	// offline -- is addressed to AdminNotificationEmail, and each send site returns
	// silently when it is empty. An operator who has configured owner.user_id has
	// already said who runs this gateway, so treat that as the destination rather than
	// notifying nobody and saying nothing about it.
	if cfg.AdminNotificationEmail == "" && cfg.Owner.UserID != "" {
		cfg.AdminNotificationEmail = cfg.Owner.UserID
		slog.Info(fmt.Sprintf("[Config] admin_notification_email is not set; defaulting admin alerts to owner.user_id (%s).", cfg.Owner.UserID))
	}
	if cfg.AdminNotificationEmail == "" {
		slog.Warn("[Config] Neither admin_notification_email nor owner.user_id is set -- admin alerts (new registrations, approval requests, IP bans) will not be sent to anyone.")
	}

	return cfg, nil
}

// SaveClientConfig saves the client configuration to a YAML file.
func SaveClientConfig(path string, cfg *ClientConfig) error {
	if path == "" {
		path = ResolveDefaultConfigPath()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer file.Close() //nolint:errcheck
	enc := yaml.NewEncoder(file)
	defer enc.Close() //nolint:errcheck
	return enc.Encode(cfg)
}

// ResolveDefaultConfigPath returns the canonical path to the user's config file.
func ResolveDefaultConfigPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "client-config.yaml"
	}
	return filepath.Join(homeDir, ".lfr-tunnel", "config.yaml")
}

// LoadClientConfig loads the client configuration from a YAML file and/or environment variables.
func LoadClientConfig(path string) (*ClientConfig, error) {
	cfg := DefaultClientConfig()

	if path == "" {
		defaultPath := ResolveDefaultConfigPath()
		if _, err := os.Stat(defaultPath); err == nil {
			path = defaultPath
		}
	}

	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close() //nolint:errcheck

		dec := yaml.NewDecoder(file)
		if err := dec.Decode(cfg); err != nil {
			return nil, err
		}
	}

	// 2. Load from token file if not set in YAML
	if cfg.AuthToken == "" {
		tokenFilePath := os.Getenv("LFT_TOKEN_FILE")
		if tokenFilePath == "" {
			homeDir, err := os.UserHomeDir()
			if err == nil {
				tokenFilePath = filepath.Join(homeDir, ".lfr-tunnel", "token")
			}
		}
		if tokenFilePath != "" {
			if data, err := os.ReadFile(tokenFilePath); err == nil {
				content := string(data)
				if strings.Contains(content, "LFT_CLIENT_TOKEN") || strings.Contains(content, "LFT_TOKEN") {
					if val, parseErr := parseSecretsFile(tokenFilePath); parseErr == nil && val != "" {
						cfg.AuthToken = val
					}
				} else {
					cfg.AuthToken = strings.TrimSpace(content)
				}

				checkInsecurePermissions(tokenFilePath, "Token")
			}
		}
	}

	// 2b. Load from LDM secrets file if still empty
	if cfg.AuthToken == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			paths := []string{
				filepath.Join(homeDir, ".config", "lfr", "secrets"),
				filepath.Join(homeDir, ".config", "lfr", "secrets.ps1"),
			}
			for _, p := range paths {
				if val, parseErr := parseSecretsFile(p); parseErr == nil && val != "" {
					cfg.AuthToken = val
					checkInsecurePermissions(p, "Secrets")
					break
				}
			}
		}
	}

	// Environment variable overrides
	if val := os.Getenv("LFT_CLIENT_SERVER"); val != "" {
		cfg.ServerURL = val
	} else if val := os.Getenv("LFT_SERVER_URL"); val != "" {
		cfg.ServerURL = val
	} else if val := os.Getenv("LFT_SERVER"); val != "" {
		cfg.ServerURL = val
	}

	if val := os.Getenv("LFT_CLIENT_TOKEN"); val != "" {
		cfg.AuthToken = val
	} else if val := os.Getenv("LFT_TOKEN"); val != "" {
		cfg.AuthToken = val
	}

	if val := os.Getenv("LFT_CLIENT_SUBDOMAIN"); val != "" {
		cfg.Subdomain = cleanSubdomainPrefix(val)
	} else if val := os.Getenv("LFT_SUBDOMAIN"); val != "" {
		cfg.Subdomain = cleanSubdomainPrefix(val)
	}

	if val := os.Getenv("LFT_CLIENT_PORTS"); val != "" {
		parts := strings.Split(val, ",")
		var ports []int
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if p, err := strconv.Atoi(part); err == nil {
				ports = append(ports, p)
			}
		}
		if len(ports) > 0 {
			cfg.Ports = ports
		}
	}

	if val := os.Getenv("LFT_TARGET_HOST"); val != "" {
		cfg.TargetHost = cleanTargetHost(val)
	}

	if val := os.Getenv("LFT_CLIENT_PASSCODE"); val != "" {
		cfg.Passcode = val
	} else if val := os.Getenv("LFT_PASSCODE"); val != "" {
		cfg.Passcode = val
	}

	if val := os.Getenv("LFT_CLIENT_WHITELIST_IPS"); val != "" {
		cfg.WhitelistIPs = val
	} else if val := os.Getenv("LFT_WHITELIST_IPS"); val != "" {
		cfg.WhitelistIPs = val
	}

	if val := os.Getenv("LFT_CLIENT_REGION"); val != "" {
		cfg.Region = val
	} else if val := os.Getenv("LFT_REGION"); val != "" {
		cfg.Region = val
	}

	if val := os.Getenv("LFT_CLIENT_CUSTOM_DOMAIN"); val != "" {
		cfg.CustomDomain = val
	} else if val := os.Getenv("LFT_CUSTOM_DOMAIN"); val != "" {
		cfg.CustomDomain = val
	}

	if val := os.Getenv("LFT_CLIENT_LATENCY"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			cfg.Latency = d
		}
	} else if val := os.Getenv("LFT_LATENCY"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			cfg.Latency = d
		}
	}

	if val := os.Getenv("LFT_CLIENT_BANDWIDTH"); val != "" {
		cfg.Bandwidth = val
	} else if val := os.Getenv("LFT_BANDWIDTH"); val != "" {
		cfg.Bandwidth = val
	}

	return cfg, nil
}

// cleanSubdomainPrefix extracts the subdomain from a URL or hostname.
func cleanSubdomainPrefix(val string) string {
	val = strings.TrimSpace(val)
	val = strings.TrimPrefix(val, "http://")
	val = strings.TrimPrefix(val, "https://")
	if idx := strings.Index(val, "."); idx != -1 {
		val = val[:idx]
	}
	return val
}

// cleanTargetHost extracts the hostname/IP from a URL (e.g. http://liferay:8080 -> liferay)
// or returns the original string if it is already a plain hostname/IP.
func cleanTargetHost(target string) string {
	if target == "" {
		return ""
	}
	// If it doesn't contain a scheme prefix, prepend a dummy scheme to allow url.Parse to work
	uStr := target
	if !strings.Contains(uStr, "://") {
		uStr = "http://" + uStr
	}
	u, err := url.Parse(uStr)
	if err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return target
}

// parseSecretsFile reads a restricted shell script or PowerShell file line by line
// and parses LFT_CLIENT_TOKEN or LFT_TOKEN variables.
func parseSecretsFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close() //nolint:errcheck

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Strip export syntax: export KEY=VALUE
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimPrefix(line, "export ")
			line = strings.TrimSpace(line)
		}

		// Strip PowerShell environment syntax: $env:KEY=VALUE
		if strings.HasPrefix(line, "$env:") {
			line = strings.TrimPrefix(line, "$env:")
			line = strings.TrimSpace(line)
		}

		// Split on first '='
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		if key == "LFT_CLIENT_TOKEN" || key == "LFT_TOKEN" {
			// Strip surrounding quotes
			if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) ||
				(strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
				if len(val) >= 2 {
					val = val[1 : len(val)-1]
				}
			}
			return val, nil
		}
	}
	return "", scanner.Err()
}

// checkInsecurePermissions checks if a file has insecure permissions (0077 mask check) on Unix systems.
func checkInsecurePermissions(path string, label string) {
	if runtime.GOOS == "windows" {
		return
	}
	if info, err := os.Stat(path); err == nil {
		if info.Mode().Perm()&0077 != 0 {
			fmt.Fprintf(os.Stderr, "Warning: %s file %s has insecure permissions %04o. For security, run 'chmod 600 %s'\n", label, path, info.Mode().Perm(), path)
		}
	}
}
