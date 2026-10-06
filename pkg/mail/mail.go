package mail

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
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

	dialer := &net.Dialer{Timeout: 10 * time.Second}

	if s.cfg.SMTPPort == 465 {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	} else {
		conn, err = dialer.Dial("tcp", addr)
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
	toAddr := to
	if parsed, err := mail.ParseAddress(to); err == nil {
		toAddr = parsed.Address
	}
	if err := c.Rcpt(toAddr); err != nil {
		return fmt.Errorf("failed to add mail recipient: %v", err)
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("failed to initiate data stream: %v", err)
	}
	defer w.Close() //nolint:errcheck

	msg, err := buildMessage(s.cfg.SMTPFromAddress, to, subject, body, plainBody)
	if err != nil {
		return err
	}
	_, err = w.Write([]byte(msg))
	if err != nil {
		return fmt.Errorf("failed to write message body: %v", err)
	}

	return nil
}

// buildMessage assembles the RFC 5322 message, separately from the SMTP conversation so its
// safety properties can be tested without a server.
//
// Two of its inputs can carry text a user typed -- a token name reaches the subject and the plain
// body (#2344) -- so it must not let content become structure:
//
//   - Header values have CR and LF removed, and the subject is RFC 2047 encoded. A raw
//     "name\r\nBcc: x" in a subject was a second header; a raw non-ASCII subject was invalid 8-bit.
//   - The multipart boundary is random per message, and regenerated if either body happens to
//     contain it. It used to be a fixed string, so a body containing that string followed by a
//     Content-Type line could close the plain-text part and open an HTML part of its own.
func buildMessage(from, to, subject, htmlBody, plainBody string) (string, error) {
	boundary, err := newBoundary(htmlBody, plainBody)
	if err != nil {
		return "", err
	}

	// Ordered, so the message is deterministic apart from the boundary.
	headers := [][2]string{
		{"From", headerValue(from)},
		{"To", headerValue(to)},
		{"Subject", mime.QEncoding.Encode("UTF-8", headerValue(subject))},
		{"MIME-Version", "1.0"},
		{"Content-Type", `multipart/alternative; boundary="` + boundary + `"`},
	}

	var msg strings.Builder
	for _, h := range headers {
		msg.WriteString(h[0] + ": " + h[1] + "\r\n")
	}
	msg.WriteString("\r\n")

	if plainBody != "" {
		msg.WriteString("--" + boundary + "\r\n")
		msg.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		msg.WriteString(plainBody + "\r\n\r\n")
	}
	if htmlBody != "" {
		msg.WriteString("--" + boundary + "\r\n")
		msg.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
		msg.WriteString(htmlBody + "\r\n\r\n")
	}
	msg.WriteString("--" + boundary + "--\r\n")
	return msg.String(), nil
}

// headerValue removes the characters that would end a header line. Nothing a header legitimately
// carries here needs them, and folding is not used.
func headerValue(v string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(v)
}

// newBoundary returns a random multipart boundary that appears in neither body.
func newBoundary(bodies ...string) (string, error) {
	for {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return "", fmt.Errorf("failed to generate a MIME boundary: %v", err)
		}
		boundary := "lfr-" + hex.EncodeToString(b)
		clash := false
		for _, body := range bodies {
			if strings.Contains(body, boundary) {
				clash = true
				break
			}
		}
		if !clash {
			return boundary, nil
		}
	}
}
