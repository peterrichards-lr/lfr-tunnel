package config

import (
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ServerConfig holds configuration settings for the lfr-tunneld server.
type ServerConfig struct {
	Domain1                string `yaml:"domain1"`
	Domain2                string `yaml:"domain2"`
	BindAddr               string `yaml:"bind_addr"`
	HTTPBindAddr           string `yaml:"http_bind_addr"`
	ChiselBindAddr         string `yaml:"chisel_bind_addr"`
	AuthToken              string `yaml:"auth_token"`
	SSLCertFile            string `yaml:"ssl_cert_file"`
	SSLKeyFile             string `yaml:"ssl_key_file"`
	DBPath                 string `yaml:"db_path"`
	SMTPHost               string `yaml:"smtp_host"`
	SMTPPort               int    `yaml:"smtp_port"`
	SMTPUsername           string `yaml:"smtp_username"`
	SMTPPassword           string `yaml:"smtp_password"`
	SMTPFromAddress        string `yaml:"smtp_from_address"`
	AdminNotificationEmail string `yaml:"admin_notification_email"`
	InsecureSkipVerify     bool   `yaml:"insecure_skip_verify"`
}

// ClientConfig holds configuration settings for the lfr-tunnel client.
type ClientConfig struct {
	ServerURL string `yaml:"server_url"`
	AuthToken string `yaml:"auth_token"`
	Subdomain string `yaml:"subdomain"`
	Ports     []int  `yaml:"ports"`
}

// DefaultServerConfig returns a ServerConfig with sensible default values.
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		BindAddr:       ":443",
		HTTPBindAddr:   ":80",
		ChiselBindAddr: ":8081",
	}
}

// DefaultClientConfig returns a ClientConfig with sensible default values.
func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		ServerURL: "http://localhost:8081",
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
		defer file.Close()

		dec := yaml.NewDecoder(file)
		if err := dec.Decode(cfg); err != nil {
			return nil, err
		}
	}

	// Environment variable overrides
	if val := os.Getenv("LFT_DOMAIN1"); val != "" {
		cfg.Domain1 = val
	}
	if val := os.Getenv("LFT_DOMAIN2"); val != "" {
		cfg.Domain2 = val
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
	if val := os.Getenv("LFT_AUTH_TOKEN"); val != "" {
		cfg.AuthToken = val
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
		cfg.SMTPHost = val
	}
	if val := os.Getenv("LFT_SMTP_PORT"); val != "" {
		if p, err := strconv.Atoi(val); err == nil {
			cfg.SMTPPort = p
		}
	}
	if val := os.Getenv("LFT_SMTP_USERNAME"); val != "" {
		cfg.SMTPUsername = val
	}
	if val := os.Getenv("LFT_SMTP_PASSWORD"); val != "" {
		cfg.SMTPPassword = val
	}
	if val := os.Getenv("LFT_SMTP_FROM"); val != "" {
		cfg.SMTPFromAddress = val
	}
	if val := os.Getenv("LFT_ADMIN_EMAIL"); val != "" {
		cfg.AdminNotificationEmail = val
	}
	if val := os.Getenv("LFT_INSECURE_SKIP_VERIFY"); val != "" {
		cfg.InsecureSkipVerify = strings.ToLower(val) == "true" || val == "1"
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
		defer file.Close()

		dec := yaml.NewDecoder(file)
		if err := dec.Decode(cfg); err != nil {
			return nil, err
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
