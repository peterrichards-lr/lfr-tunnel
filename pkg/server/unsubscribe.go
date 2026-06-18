package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GenerateUnsubscribeToken generates a secure, stateless, and signed 1-click unsubscribe token.
func (s *Server) GenerateUnsubscribeToken(email string) string {
	// Token expires in 90 days
	expiry := time.Now().Add(90 * 24 * time.Hour).Unix()
	payload := fmt.Sprintf("%s:%d", email, expiry)

	// Use your server's shared secret as the HMAC signing key
	mac := hmac.New(sha256.New, []byte(s.unsubscribeSecret))
	mac.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	token := fmt.Sprintf("%s:%s", payload, signature)
	return base64.RawURLEncoding.EncodeToString([]byte(token))
}

// VerifyUnsubscribeToken decodes and cryptographically verifies the unsubscribe token.
func (s *Server) VerifyUnsubscribeToken(token string) (string, error) {
	decodedBytes, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", errors.New("invalid token encoding")
	}

	parts := strings.Split(string(decodedBytes), ":")
	if len(parts) != 3 {
		return "", errors.New("invalid token format")
	}

	email := parts[0]
	expiryStr := parts[1]
	signature := parts[2]

	// Verify expiration
	expiry, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil {
		return "", errors.New("invalid expiration format")
	}

	if time.Now().Unix() > expiry {
		return "", errors.New("unsubscribe token has expired")
	}

	// Verify cryptographic signature
	payload := fmt.Sprintf("%s:%s", email, expiryStr)
	mac := hmac.New(sha256.New, []byte(s.unsubscribeSecret))
	mac.Write([]byte(payload))
	expectedSignature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
		return "", errors.New("invalid token signature")
	}

	return email, nil
}

// handleUnsubscribe processes the 1-click unsubscribe GET request and deactivates emails.
func (s *Server) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "Missing unsubscribe token", http.StatusBadRequest)
		return
	}

	email, err := s.VerifyUnsubscribeToken(token)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `<html><head><title>Unsubscribe Failed</title><style>body{font-family:sans-serif;text-align:center;padding:50px;color:#333;background:#f8fafc;}h1{color:#ef4444;}</style></head><body><h1>Unsubscribe Failed ❌</h1><p>%s</p><p><a href="/">Return to Portal</a></p></body></html>`, htmlEscape(err.Error()))
		return
	}

	if s.db != nil {
		user, err := s.db.GetUserByEmail(email)
		if err == nil && user != nil {
			user.NotificationPrefs = "disabled"
			_ = s.db.UpdateUser(user)
			s.writeAudit("system", "user.unsubscribed", "user", user.Email, "User unsubscribed via one-click email footer link", r)
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, `<html><head><title>Unsubscribed Successfully</title><style>body{font-family:sans-serif;text-align:center;padding:50px;color:#333;background:#f8fafc;}h1{color:#10b981;}</style></head><body><h1>Successfully Unsubscribed! ✅</h1><p>Your email preferences have been updated. You will no longer receive optional administrative notifications, broadcasts, or lease alert emails.</p><p>You can opt-back-in at any time from your Account Settings panel on the dashboard.</p><p><a href="/">Return to Portal</a></p></body></html>`)
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}
