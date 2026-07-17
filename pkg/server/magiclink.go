package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"log/slog"
	"net/http"
)

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
	_ = s.db.MarkMagicLinkUsed(link.ID)

	// Audit Log
	s.writeAudit(link.Email, "auth.magic_link_reported", "ip", link.ClientIP, "User reported unauthorized magic link request", r)

	// Blacklist the IP
	_ = s.db.AddBlacklistIP(link.ClientIP, "Reported via Magic Link email")
	s.blacklist.Store(link.ClientIP, true)
	s.webhooks.SendAbuseReportAlert(link.Email, "Unauthorized magic link login attempt", link.ClientIP)

	slog.Info(fmt.Sprintf("[Auth] Magic link reported by %s. IP %s has been blacklisted.", link.Email, link.ClientIP))

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte("Thank you. This login link has been deactivated, and the request has been reported to our security team.")); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
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
	_ = s.db.MarkMagicLinkUsed(link.ID)

	// Since they declined the invite, let's delete their user record (it was approved initially by the inviter)
	user, err := s.db.GetUserByEmail(link.Email)
	if err == nil {
		_ = s.db.DeleteUser(user.ID)
	}

	s.writeAudit(link.Email, "auth.invite_declined", "user", link.Email, "User declined administrator invitation", r)

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte("Thank you. This invitation has been securely declined and the administrator will be notified.")); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
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
	_ = s.db.DeleteUser(user.ID)

	s.writeAudit(user.Email, "auth.registration_reported", "user", user.Email, "User reported unauthorized registration request", r)
	s.webhooks.SendAbuseReportAlert(user.Email, "Unauthorized registration request", getClientIP(r))

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte("Thank you. This registration token has been instantly deactivated, preventing anyone from completing the sign-up process.")); err != nil {
		log.Printf("[Warning] Failed to write response: %v", err)
	}
}
