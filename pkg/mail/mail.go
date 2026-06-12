package mail

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"time"
)

type Config struct {
	SMTPHost           string `yaml:"smtp_host"`
	SMTPPort           int    `yaml:"smtp_port"`
	SMTPUsername       string `yaml:"smtp_username"`
	SMTPPassword       string `yaml:"smtp_password"`
	SMTPFromAddress    string `yaml:"smtp_from_address"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

type Sender interface {
	Send(to string, subject string, htmlBody string, plainBody string) error
}

type SMTPClient struct {
	cfg *Config
}

// NewSMTPClient initializes and returns an SMTPClient instance.
func NewSMTPClient(cfg *Config) *SMTPClient {
	return &SMTPClient{cfg: cfg}
}

// Send sends an HTML email using SMTP.
func (s *SMTPClient) Send(to string, subject string, body string, plainBody string) error {
	addr := net.JoinHostPort(s.cfg.SMTPHost, fmt.Sprintf("%d", s.cfg.SMTPPort))
	var conn net.Conn
	var err error

	tlsConfig := &tls.Config{
		ServerName:         s.cfg.SMTPHost,
		InsecureSkipVerify: s.cfg.InsecureSkipVerify,
	}

	if s.cfg.SMTPPort == 465 {
		conn, err = tls.Dial("tcp", addr, tlsConfig)
	} else {
		conn, err = net.DialTimeout("tcp", addr, 10*time.Second)
	}
	if err != nil {
		return fmt.Errorf("failed to dial smtp server: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	c, err := smtp.NewClient(conn, s.cfg.SMTPHost)
	if err != nil {
		return fmt.Errorf("failed to create smtp client: %v", err)
	}
	defer c.Close() //nolint:errcheck

	if s.cfg.SMTPPort != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("failed to start tls handshake: %v", err)
			}
		}
	}

	if s.cfg.SMTPUsername != "" {
		auth := smtp.PlainAuth("", s.cfg.SMTPUsername, s.cfg.SMTPPassword, s.cfg.SMTPHost)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("failed to authenticate: %v", err)
		}
	}

	fromAddr := s.cfg.SMTPFromAddress
	if parsed, err := mail.ParseAddress(s.cfg.SMTPFromAddress); err == nil {
		fromAddr = parsed.Address
	}
	if err := c.Mail(fromAddr); err != nil {
		return fmt.Errorf("failed to set mail sender: %v", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("failed to add mail recipient: %v", err)
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("failed to initiate data stream: %v", err)
	}
	defer w.Close() //nolint:errcheck

	headers := make(map[string]string)
	headers["From"] = s.cfg.SMTPFromAddress
	headers["To"] = to
	headers["Subject"] = subject
	headers["MIME-Version"] = "1.0"
	headers["Content-Type"] = `multipart/alternative; boundary="lfr-tunnel-boundary-12345"`

	var msg string
	for k, v := range headers {
		msg += fmt.Sprintf("%s: %s\r\n", k, v)
	}

	msg += "\r\n"

	// Plain text part
	if plainBody != "" {
		msg += "--lfr-tunnel-boundary-12345\r\n"
		msg += "Content-Type: text/plain; charset=UTF-8\r\n\r\n"
		msg += plainBody + "\r\n\r\n"
	}

	// HTML part
	if body != "" {
		msg += "--lfr-tunnel-boundary-12345\r\n"
		msg += "Content-Type: text/html; charset=UTF-8\r\n\r\n"
		msg += body + "\r\n\r\n"
	}

	msg += "--lfr-tunnel-boundary-12345--\r\n"

	_, err = w.Write([]byte(msg))
	if err != nil {
		return fmt.Errorf("failed to write message body: %v", err)
	}

	return nil
}
