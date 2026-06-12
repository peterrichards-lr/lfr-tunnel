package server

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
)

const defaultMagicLinkEmailTemplate = `
<p>Hi {{.PreferredName}},</p>
<p>Click the button below to log in to your account. This link will expire in {{.ExpiryMinutes}} minutes and can only be used once.</p>
<p>If the button doesn't work, copy and paste this URL into your browser: {{.MagicLink}}</p>
<br/>
<p>Didn’t request this link?</p>
<p>This request originated from the IP address: {{.IPAddress}}.</p>
<p>If this wasn't you, your account remains secure, but someone else entered your email address. You can safely ignore this email, or you can help our security team by flagging it below:</p>
<p><a href="{{.ReportLink}}">Click here to report this unauthorized request</a></p>
<p>What happens next? Clicking this link will immediately deactivate the login link above. Our system will securely log the request data to help us detect and block automated abuse.</p>
<br/>
<p>Best regards,</p>
<p>Liferay Tunnel Team</p>
`

type MagicLinkEmailData struct {
	PreferredName string
	ExpiryMinutes int
	MagicLink     string
	IPAddress     string
	ReportLink    string
}

func (s *Server) handleAuthReport(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}

	if s.db == nil {
		http.Error(w, "database not configured", http.StatusNotImplemented)
		return
	}

	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:])

	link, err := s.db.GetMagicLink(tokenHash)
	if err != nil {
		http.Error(w, "Link expired or not found", http.StatusGone)
		return
	}

	if link.UsedAt != nil {
		http.Error(w, "Link expired or not found", http.StatusGone)
		return
	}

	// Invalidate the token
	s.db.MarkMagicLinkUsed(link.ID)

	// Audit Log
	s.writeAudit(link.Email, "auth.magic_link_reported", "ip", link.ClientIP, "User reported unauthorized magic link request", r)

	// Blacklist the IP
	s.db.AddBlacklistIP(link.ClientIP, "Reported via Magic Link email")
	s.blacklist.Store(link.ClientIP, true)

	log.Printf("[Auth] Magic link reported by %s. IP %s has been blacklisted.", link.Email, link.ClientIP)

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte("Thank you. This login link has been deactivated, and the request has been reported to our security team."))
}

func (s *Server) handleAuthDecline(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" || s.db == nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:])

	link, err := s.db.GetMagicLink(tokenHash)
	if err != nil || link.UsedAt != nil {
		http.Error(w, "Link expired or not found", http.StatusGone)
		return
	}

	// Invalidate the token
	s.db.MarkMagicLinkUsed(link.ID)

	// Since they declined the invite, let's delete their user record (it was approved initially by the inviter)
	user, err := s.db.GetUserByEmail(link.Email)
	if err == nil {
		s.db.DeleteUser(user.ID)
	}

	s.writeAudit(link.Email, "auth.invite_declined", "user", link.Email, "User declined administrator invitation", r)

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte("Thank you. This invitation has been securely declined and the administrator will be notified."))
}

func (s *Server) handleReportRegistration(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" || s.db == nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	user, err := s.db.GetUserByVerificationToken(token)
	if err != nil {
		http.Error(w, "Token expired or not found", http.StatusGone)
		return
	}

	// They reported the registration request as unauthorized
	s.db.DeleteUser(user.ID)

	s.writeAudit(user.Email, "auth.registration_reported", "user", user.Email, "User reported unauthorized registration request", r)

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte("Thank you. This registration token has been instantly deactivated, preventing anyone from completing the sign-up process."))
}
