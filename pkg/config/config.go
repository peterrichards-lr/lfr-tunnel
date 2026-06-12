package config

import (
	"os"
	"path/filepath"
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

// ServerConfig holds configuration settings for the lfr-tunneld server.
type ServerConfig struct {
	Domains                []string         `yaml:"domains"`
	BindAddr               string           `yaml:"bind_addr"`
	HTTPBindAddr           string           `yaml:"http_bind_addr"`
	ChiselBindAddr         string           `yaml:"chisel_bind_addr"`
	SSLCertFile            string           `yaml:"ssl_cert_file"`
	SSLKeyFile             string           `yaml:"ssl_key_file"`
	DBPath                 string           `yaml:"db_path"`
	SMTPServer             SMTPServerConfig `yaml:"smtp_server"`
	AdminNotificationEmail string           `yaml:"admin_notification_email"`
	InsecureSkipVerify     bool             `yaml:"insecure_skip_verify"`
	Owner                  OwnerConfig      `yaml:"owner"`
	AllowedEmailDomains    []string         `yaml:"allowed_email_domains"`
	IPBlacklist            []string         `yaml:"ip_blacklist"`
	MaxTunnelRateLimit     int              `yaml:"max_tunnel_rate_limit"`
	EnableUserPortal       bool             `yaml:"enable_user_portal"`
	PortalSessionDuration  time.Duration    `yaml:"portal_session_duration"`
	MinClientVersion       string           `yaml:"min_client_version"`
	DocumentationURL       string           `yaml:"documentation_url"`
	RepositoryURL          string           `yaml:"repository_url"`
	PruneInterval          time.Duration    `yaml:"prune_interval"`
	MagicLinkExpiry        time.Duration    `yaml:"magic_link_expiry"`
	InviteLinkExpiry       time.Duration    `yaml:"invite_link_expiry"`
	VerificationLinkExpiry time.Duration    `yaml:"verification_link_expiry"`

	// Dynamic SSO/OIDC Providers
	SSOProviders []SSOProviderConfig `yaml:"sso_providers"`
}

type SSOProviderConfig struct {
	ID           string `yaml:"id"`
	Name         string `yaml:"name"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	IssuerURL    string `yaml:"issuer_url"`
	Icon         string `yaml:"icon"`
}

// ClientConfig holds configuration settings for the lfr-tunnel client.
type ClientConfig struct {
	ServerURL string `yaml:"server_url"`
	AuthToken string `yaml:"auth_token"`
	Subdomain string `yaml:"subdomain"`
	Ports     []int  `yaml:"ports"`
	TokenFile string `yaml:"token_file"`
	RateLimit int    `yaml:"rate_limit"`
	BasicAuth string `yaml:"basic_auth"`
}

// DefaultServerConfig returns a ServerConfig with sensible default values.
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		BindAddr:               ":443",
		HTTPBindAddr:           ":80",
		ChiselBindAddr:         ":8081",
		MaxTunnelRateLimit:     100,
		EnableUserPortal:       true,
		PortalSessionDuration:  24 * time.Hour,
		MinClientVersion:       "v1.0.0",
		DocumentationURL:       "https://github.com/peterrichards-lr/lfr-tunnel/tree/master/docs",
		RepositoryURL:          "https://github.com/peterrichards-lr/lfr-tunnel",
		PruneInterval:          1 * time.Hour,
		MagicLinkExpiry:        15 * time.Minute,
		InviteLinkExpiry:       7 * 24 * time.Hour,
		VerificationLinkExpiry: 24 * time.Hour,
	}
}

// DefaultClientConfig returns a ClientConfig with sensible default values.
func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		ServerURL: "https://tunnel.lfr-demo.se",
		Ports:     []int{8080},
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
	if val := os.Getenv("LFT_PORTAL_SESSION_DURATION"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			cfg.PortalSessionDuration = d
		}
	}
	if val := os.Getenv("LFT_MIN_CLIENT_VERSION"); val != "" {
		cfg.MinClientVersion = val
	}

	return cfg, nil
}

// LoadClientConfig loads the client configuration from a YAML file and/or environment variables.
func LoadClientConfig(path string) (*ClientConfig, error) {
	cfg := DefaultClientConfig()

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
				cfg.AuthToken = strings.TrimSpace(string(data))
			}
		}
	}

	// Environment variable overrides
	if val := os.Getenv("LFT_CLIENT_SERVER"); val != "" {
		cfg.ServerURL = val
	}
	if val := os.Getenv("LFT_CLIENT_TOKEN"); val != "" {
		cfg.AuthToken = val
	}
	if val := os.Getenv("LFT_CLIENT_SUBDOMAIN"); val != "" {
		cfg.Subdomain = val
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

	return cfg, nil
}
