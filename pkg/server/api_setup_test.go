package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

func TestHandleSetupPage(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/setup", nil)
	w := httptest.NewRecorder()

	srv.handleSetupPage(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w.Result().StatusCode)
	}

	cType := w.Header().Get("Content-Type")
	if cType != "text/html; charset=utf-8" {
		t.Errorf("expected HTML content type, got %s", cType)
	}

	body := w.Body.String()
	if !strings.Contains(body, "<html") {
		t.Errorf("expected setup.html content, got: %s", body)
	}
}

func TestHandleCompleteSetup(t *testing.T) {
	srv, mockMail, cleanup := setupTestServer(t)
	defer cleanup()

	originalDB := srv.db
	defer func() { srv.db = originalDB }()

	// Configure expirations and notifications
	srv.cfg.VerificationLinkExpiry = 2 * time.Hour
	srv.cfg.AdminNotificationEmail = "admin@example.com"

	tests := []struct {
		name           string
		payload        interface{}
		setup          func() (string, func())
		expectedStatus int
		verifyBody     func(t *testing.T, body string)
	}{
		{
			name:    "Failure_DBNotConfigured",
			payload: map[string]interface{}{"token": "tok1"},
			setup: func() (string, func()) {
				srv.db = nil
				return "tok1", func() { srv.db = originalDB }
			},
			expectedStatus: http.StatusNotImplemented,
		},
		{
			name:           "Failure_InvalidJSON",
			payload:        "corrupt-json",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:    "Failure_PolicyConsentRejected",
			payload: map[string]interface{}{"token": "tok_consent", "policy_consent": false},
			setup: func() (string, func()) {
				srv.cfg.EnforcePolicyConsent = true
				return "tok_consent", func() { srv.cfg.EnforcePolicyConsent = false }
			},
			expectedStatus: http.StatusBadRequest,
			verifyBody: func(t *testing.T, body string) {
				if !strings.Contains(body, "must acknowledge and agree to the Privacy Policy") {
					t.Errorf("expected policy consent error, got: %s", body)
				}
			},
		},
		{
			name:           "Failure_InvalidToken",
			payload:        map[string]interface{}{"token": "non_existent_token", "policy_consent": true},
			expectedStatus: http.StatusBadRequest,
			verifyBody: func(t *testing.T, body string) {
				if !strings.Contains(body, "Invalid or expired token") {
					t.Errorf("expected invalid token error, got: %s", body)
				}
			},
		},
		{
			name: "Failure_UserAlreadyVerified",
			setup: func() (string, func()) {
				u := &db.User{
					ID:                "already_verified@example.com",
					Email:             "already_verified@example.com",
					Status:            "pending", // already verified setup
					VerificationToken: "token_verified",
					CreatedAt:         time.Now().UTC(),
				}
				_ = srv.db.CreateUser(u)
				return "token_verified", nil
			},
			payload:        map[string]interface{}{"policy_consent": true},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Failure_ExpiredToken",
			setup: func() (string, func()) {
				u := &db.User{
					ID:                "expired@example.com",
					Email:             "expired@example.com",
					Status:            "unverified",
					VerificationToken: "token_expired",
					CreatedAt:         time.Now().UTC().Add(-3 * time.Hour), // expired (expiry is 2h)
				}
				_ = srv.db.CreateUser(u)
				return "token_expired", nil
			},
			payload:        map[string]interface{}{"policy_consent": true},
			expectedStatus: http.StatusBadRequest,
			verifyBody: func(t *testing.T, body string) {
				if !strings.Contains(body, "Verification link has expired") {
					t.Errorf("expected expired error, got: %s", body)
				}
			},
		},
		{
			name: "Success_PreferredNameFallback_And_NotifyAdmin",
			setup: func() (string, func()) {
				mockMail.sentTo = "" // reset mail
				u := &db.User{
					ID:                "success@example.com",
					Email:             "success@example.com",
					Status:            "unverified",
					VerificationToken: "token_success",
					CreatedAt:         time.Now().UTC(),
					ApprovalToken:     "approve_token_123",
				}
				_ = srv.db.CreateUser(u)
				return "token_success", nil
			},
			payload: map[string]interface{}{
				"first_name":     "John",
				"last_name":      "Doe",
				"preferred_name": "", // should fallback to John
				"policy_consent": true,
			},
			expectedStatus: http.StatusOK,
			verifyBody: func(t *testing.T, body string) {
				u, err := srv.db.GetUser("success@example.com")
				if err != nil {
					t.Fatalf("failed to get user: %v", err)
				}
				if u.Status != "pending" {
					t.Errorf("expected status pending, got %s", u.Status)
				}
				if u.PreferredName != "John" {
					t.Errorf("expected preferred name fallback to John, got %s", u.PreferredName)
				}
				if u.VerificationToken != "" {
					t.Errorf("expected verification token to be cleared")
				}
				if u.PolicyConsentAt == nil {
					t.Errorf("expected PolicyConsentAt to be populated")
				}

				// Wait a tiny bit for async email routine
				time.Sleep(50 * time.Millisecond)
				if mockMail.sentTo != "admin@example.com" {
					t.Errorf("expected admin email notification, got to: %s", mockMail.sentTo)
				}
				if !strings.Contains(mockMail.sentSubject, "New User Registration") && !strings.Contains(mockMail.sentTextBody, "John Doe") {
					t.Errorf("email subject/body mismatch, got subject: %s", mockMail.sentSubject)
				}
			},
		},
		{
			name: "Success_AdminNotificationDisabled",
			setup: func() (string, func()) {
				mockMail.sentTo = "" // reset mail
				// Register admin who has disabled notification preferences
				admin := &db.User{
					ID:                "admin@example.com",
					Email:             "admin@example.com",
					Role:              "admin",
					Status:            "approved",
					NotificationPrefs: "disabled",
				}
				_ = srv.db.CreateUser(admin)

				u := &db.User{
					ID:                "success_no_notify@example.com",
					Email:             "success_no_notify@example.com",
					Status:            "unverified",
					VerificationToken: "token_no_notify",
					CreatedAt:         time.Now().UTC(),
				}
				_ = srv.db.CreateUser(u)
				return "token_no_notify", func() {
					_ = srv.db.DeleteUser("admin@example.com")
				}
			},
			payload: map[string]interface{}{
				"first_name":     "Jane",
				"last_name":      "Doe",
				"preferred_name": "J",
				"policy_consent": true,
			},
			expectedStatus: http.StatusOK,
			verifyBody: func(t *testing.T, body string) {
				time.Sleep(50 * time.Millisecond)
				if mockMail.sentTo != "" {
					t.Errorf("expected no email to be sent since admin disabled it, but sent to: %s", mockMail.sentTo)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := ""
			var cleanupFn func()
			if tt.setup != nil {
				var tok string
				tok, cleanupFn = tt.setup()
				token = tok
			}
			if cleanupFn != nil {
				defer cleanupFn()
			}

			var bodyBytes []byte
			if m, ok := tt.payload.(map[string]interface{}); ok {
				if token != "" {
					m["token"] = token
				}
				bodyBytes, _ = json.Marshal(m)
			} else if s, ok := tt.payload.(string); ok {
				bodyBytes = []byte(s)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/complete-setup", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			srv.handleCompleteSetup(w, req)

			if w.Result().StatusCode != tt.expectedStatus {
				t.Errorf("expected status %d, got %d. Body: %s", tt.expectedStatus, w.Result().StatusCode, w.Body.String())
			}

			if tt.verifyBody != nil {
				tt.verifyBody(t, w.Body.String())
			}
		})
	}
}
