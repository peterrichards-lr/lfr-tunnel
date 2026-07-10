package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

func TestServer_GetCurrentUserOrToken(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// 1. Create a user
	user := &db.User{
		ID:     "user@lfr-demo.local",
		Email:  "user@lfr-demo.local",
		Role:   "user",
		Status: "approved",
	}
	_ = srv.db.CreateUser(user)

	// 2. Setup a valid PAT
	rawToken := "my-secret-pat-token-value"
	h := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(h[:])
	pat := &db.PersonalAccessToken{
		UserID:      user.ID,
		Name:        "Test PAT",
		TokenHash:   tokenHash,
		TokenPrefix: "lfr_pat_",
	}
	_ = srv.db.CreatePAT(pat)

	// 3. Test case: No auth info
	req1, _ := http.NewRequest(http.MethodGet, "http://localhost/api/me", nil)
	u1, err := srv.getCurrentUserOrToken(req1)
	if err == nil {
		t.Errorf("expected error for empty auth, got user %v", u1)
	}

	// 4. Test case: Valid session cookie
	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     user.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})
	req2, _ := http.NewRequest(http.MethodGet, "http://localhost/api/me", nil)
	req2.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	u2, err := srv.getCurrentUserOrToken(req2)
	if err != nil || u2.Email != user.Email {
		t.Errorf("expected user %s, got error: %v, user: %v", user.Email, err, u2)
	}

	// 5. Test case: Valid query param "token"
	req3, _ := http.NewRequest(http.MethodGet, "http://localhost/api/me?token="+rawToken, nil)
	u3, err := srv.getCurrentUserOrToken(req3)
	if err != nil || u3.Email != user.Email {
		t.Errorf("expected user %s via token param, got error: %v", user.Email, err)
	}

	// 6. Test case: Valid query param "auth_token"
	req4, _ := http.NewRequest(http.MethodGet, "http://localhost/api/me?auth_token="+rawToken, nil)
	u4, err := srv.getCurrentUserOrToken(req4)
	if err != nil || u4.Email != user.Email {
		t.Errorf("expected user %s via auth_token param, got error: %v", user.Email, err)
	}

	// 7. Test case: Valid X-Auth-Token header
	req5, _ := http.NewRequest(http.MethodGet, "http://localhost/api/me", nil)
	req5.Header.Set("X-Auth-Token", rawToken)
	u5, err := srv.getCurrentUserOrToken(req5)
	if err != nil || u5.Email != user.Email {
		t.Errorf("expected user %s via X-Auth-Token header, got error: %v", user.Email, err)
	}

	// 8. Test case: Valid Authorization bearer header
	req6, _ := http.NewRequest(http.MethodGet, "http://localhost/api/me", nil)
	req6.Header.Set("Authorization", "Bearer "+rawToken)
	u6, err := srv.getCurrentUserOrToken(req6)
	if err != nil || u6.Email != user.Email {
		t.Errorf("expected user %s via Authorization Bearer header, got error: %v", user.Email, err)
	}

	// 9. Test case: Invalid token
	req7, _ := http.NewRequest(http.MethodGet, "http://localhost/api/me?token=invalid-token", nil)
	_, err = srv.getCurrentUserOrToken(req7)
	if err == nil {
		t.Errorf("expected error for invalid token")
	}
}

func TestServer_HandleMFAEnable_Extra(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	user := &db.User{
		ID:     "user@lfr-demo.local",
		Email:  "user@lfr-demo.local",
		Role:   "user",
		Status: "approved",
	}
	_ = srv.db.CreateUser(user)

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     user.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// Test: Invalid Request payload (bad JSON)
	req1, _ := http.NewRequest(http.MethodPost, "http://localhost/api/mfa/enable", bytes.NewBufferString("{bad json}"))
	req1.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w1 := httptest.NewRecorder()
	srv.handleMFAEnable(w1, req1)
	if w1.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w1.Code)
	}

	// Test: Invalid Verification Code (returns ErrInvalidRequest equivalent)
	payload := map[string]string{
		"secret": "JBSWY3DPEHPK3PXP",
		"code":   "000000",
	}
	body, _ := json.Marshal(payload)
	req2, _ := http.NewRequest(http.MethodPost, "http://localhost/api/mfa/enable", bytes.NewBuffer(body))
	req2.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w2 := httptest.NewRecorder()
	srv.handleMFAEnable(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d (code mismatch)", w2.Code)
	}
}

func TestServer_HandleMFAVerify_Extra(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// Test: Bad JSON request
	req1, _ := http.NewRequest(http.MethodPost, "http://localhost/api/mfa/verify", bytes.NewBufferString("{bad json}"))
	w1 := httptest.NewRecorder()
	srv.handleMFAVerify(w1, req1)
	if w1.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w1.Code)
	}

	// Test: Expired or invalid temp token verify (ErrUnauthorized)
	payload := map[string]string{
		"temp_token": "expired_temp_token",
		"code":       "123456",
	}
	body, _ := json.Marshal(payload)
	req2, _ := http.NewRequest(http.MethodPost, "http://localhost/api/mfa/verify", bytes.NewBuffer(body))
	w2 := httptest.NewRecorder()
	srv.handleMFAVerify(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", w2.Code)
	}
}

func TestServer_HandleMFADisable_Extra(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	user := &db.User{
		ID:     "user@lfr-demo.local",
		Email:  "user@lfr-demo.local",
		Role:   "user",
		Status: "approved",
	}
	_ = srv.db.CreateUser(user)

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     user.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// Test: Bad JSON request
	req1, _ := http.NewRequest(http.MethodPost, "http://localhost/api/mfa/disable", bytes.NewBufferString("{bad json}"))
	req1.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w1 := httptest.NewRecorder()
	srv.handleMFADisable(w1, req1)
	if w1.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w1.Code)
	}

	// Test: Invalid verification code
	payload := map[string]string{
		"code": "000000",
	}
	body, _ := json.Marshal(payload)
	req2, _ := http.NewRequest(http.MethodPost, "http://localhost/api/mfa/disable", bytes.NewBuffer(body))
	req2.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w2 := httptest.NewRecorder()
	srv.handleMFADisable(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w2.Code)
	}
}

func TestServer_HandleAdminOverrideTunnelsLimit_Extra(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// 1. Create target user
	targetUser := &db.User{
		ID:     "target@lfr-demo.local",
		Email:  "target@lfr-demo.local",
		Role:   "user",
		Status: "approved",
	}
	_ = srv.db.CreateUser(targetUser)

	// 2. Create admin session
	adminUser := &db.User{
		ID:     "admin@lfr-demo.local",
		Email:  "admin@lfr-demo.local",
		Role:   "admin",
		Status: "approved",
	}
	_ = srv.db.CreateUser(adminUser)

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     adminUser.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// Test case: Non-existent user
	val := 5
	payload := map[string]interface{}{
		"max_tunnels": &val,
	}
	body, _ := json.Marshal(payload)

	req1, _ := http.NewRequest(http.MethodPost, "http://localhost/api/admin/users/nonexistent@lfr-demo.local/tunnels_limit", bytes.NewBuffer(body))
	req1.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w1 := httptest.NewRecorder()
	srv.handleAdminOverrideTunnelsLimit(w1, req1, adminUser.Email)
	if w1.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", w1.Code)
	}

	// Test case: Bad JSON
	req2, _ := http.NewRequest(http.MethodPost, "http://localhost/api/admin/users/target@lfr-demo.local/tunnels_limit", bytes.NewBufferString("{bad json}"))
	req2.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w2 := httptest.NewRecorder()
	srv.handleAdminOverrideTunnelsLimit(w2, req2, adminUser.Email)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w2.Code)
	}

	// Test case: Success override
	req3, _ := http.NewRequest(http.MethodPost, "http://localhost/api/admin/users/target@lfr-demo.local/tunnels_limit", bytes.NewBuffer(body))
	req3.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w3 := httptest.NewRecorder()
	srv.handleAdminOverrideTunnelsLimit(w3, req3, adminUser.Email)
	if w3.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w3.Code)
	}

	// Test case: Invalid path suffix
	req4, _ := http.NewRequest(http.MethodPost, "http://localhost/api/admin/users/target@lfr-demo.local/invalid_suffix", bytes.NewBuffer(body))
	req4.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w4 := httptest.NewRecorder()
	srv.handleAdminOverrideTunnelsLimit(w4, req4, adminUser.Email)
	if w4.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w4.Code)
	}
}

func TestServer_HandleAdminDeleteUser_Extra(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	// 1. Setup system owner configuration
	srv.cfg.Owner = config.OwnerConfig{
		UserID: "owner@lfr-demo.local",
		Name:   "System Owner",
	}

	owner := &db.User{
		ID:     "owner@lfr-demo.local",
		Email:  "owner@lfr-demo.local",
		Role:   "owner",
		Status: "approved",
	}
	_ = srv.db.CreateUser(owner)

	admin := &db.User{
		ID:     "admin@lfr-demo.local",
		Email:  "admin@lfr-demo.local",
		Role:   "admin",
		Status: "approved",
	}
	_ = srv.db.CreateUser(admin)

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     admin.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// Test: Try to delete Owner (Forbidden)
	req1, _ := http.NewRequest(http.MethodDelete, "http://localhost/api/admin/users/owner@lfr-demo.local", nil)
	req1.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w1 := httptest.NewRecorder()
	srv.handleAdminDeleteUser(w1, req1, admin.Email)
	if w1.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden when deleting owner, got %d", w1.Code)
	}

	// Test: Non-existent user delete (NotFound)
	req2, _ := http.NewRequest(http.MethodDelete, "http://localhost/api/admin/users/nonexistent@lfr-demo.local", nil)
	req2.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w2 := httptest.NewRecorder()
	srv.handleAdminDeleteUser(w2, req2, admin.Email)
	if w2.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", w2.Code)
	}
}

func TestServer_HandleUpdateReservationAccessControl_Extra(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	user := &db.User{
		ID:     "user@lfr-demo.local",
		Email:  "user@lfr-demo.local",
		Role:   "user",
		Status: "approved",
	}
	_ = srv.db.CreateUser(user)

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     user.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// Test: Bad JSON request
	req1, _ := http.NewRequest(http.MethodPost, "http://localhost/api/portal/reservations/access-control", bytes.NewBufferString("{bad json}"))
	req1.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w1 := httptest.NewRecorder()
	srv.handleUpdateReservationAccessControl(w1, req1)
	if w1.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w1.Code)
	}

	// Test: Missing subdomain or domain
	payload1 := map[string]interface{}{
		"subdomain": "",
		"domain":    "lfr-demo.local",
	}
	body1, _ := json.Marshal(payload1)
	req2, _ := http.NewRequest(http.MethodPost, "http://localhost/api/portal/reservations/access-control", bytes.NewBuffer(body1))
	req2.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w2 := httptest.NewRecorder()
	srv.handleUpdateReservationAccessControl(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w2.Code)
	}

	// Test: Invalid access mode (must be and, or, or empty)
	payload2 := map[string]interface{}{
		"subdomain":   "my-sub",
		"domain":      "lfr-demo.local",
		"access_mode": "invalid-mode",
	}
	body2, _ := json.Marshal(payload2)
	req3, _ := http.NewRequest(http.MethodPost, "http://localhost/api/portal/reservations/access-control", bytes.NewBuffer(body2))
	req3.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w3 := httptest.NewRecorder()
	srv.handleUpdateReservationAccessControl(w3, req3)
	if w3.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w3.Code)
	}

	// Test: Reservation not found
	payload3 := map[string]interface{}{
		"subdomain":   "nonexistent-sub",
		"domain":      "lfr-demo.local",
		"access_mode": "or",
	}
	body3, _ := json.Marshal(payload3)
	req4, _ := http.NewRequest(http.MethodPost, "http://localhost/api/portal/reservations/access-control", bytes.NewBuffer(body3))
	req4.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w4 := httptest.NewRecorder()
	srv.handleUpdateReservationAccessControl(w4, req4)
	if w4.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", w4.Code)
	}
}

func TestServer_HandleUpdateReservationHeaders_Extra(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	user := &db.User{
		ID:     "user@lfr-demo.local",
		Email:  "user@lfr-demo.local",
		Role:   "user",
		Status: "approved",
	}
	_ = srv.db.CreateUser(user)

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     user.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// Test: Bad JSON request
	req1, _ := http.NewRequest(http.MethodPost, "http://localhost/api/portal/reservations/headers", bytes.NewBufferString("{bad json}"))
	req1.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w1 := httptest.NewRecorder()
	srv.handleUpdateReservationHeaders(w1, req1)
	if w1.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w1.Code)
	}

	// Test: Missing subdomain or domain
	payload1 := map[string]interface{}{
		"subdomain": "",
		"domain":    "lfr-demo.local",
	}
	body1, _ := json.Marshal(payload1)
	req2, _ := http.NewRequest(http.MethodPost, "http://localhost/api/portal/reservations/headers", bytes.NewBuffer(body1))
	req2.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w2 := httptest.NewRecorder()
	srv.handleUpdateReservationHeaders(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w2.Code)
	}

	// Test: Active tunnel lease not found
	payload2 := map[string]interface{}{
		"subdomain": "inactive-sub",
		"domain":    "lfr-demo.local",
	}
	body2, _ := json.Marshal(payload2)
	req3, _ := http.NewRequest(http.MethodPost, "http://localhost/api/portal/reservations/headers", bytes.NewBuffer(body2))
	req3.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w3 := httptest.NewRecorder()
	srv.handleUpdateReservationHeaders(w3, req3)
	if w3.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", w3.Code)
	}
}

func TestServer_HandleInvitations_Extra(t *testing.T) {
	srv := setupTestServerForAPI(t)
	defer srv.Stop()

	user := &db.User{
		ID:     "user@lfr-demo.local",
		Email:  "user@lfr-demo.local",
		Role:   "user",
		Status: "approved",
	}
	_ = srv.db.CreateUser(user)

	sessionToken := generateToken(16)
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     user.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// 1. handleCreateInvitation Bad JSON payload
	req1, _ := http.NewRequest(http.MethodPost, "http://localhost/api/admin/invitations", bytes.NewBufferString("{bad json}"))
	req1.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w1 := httptest.NewRecorder()
	srv.handleCreateInvitation(w1, req1)
	if w1.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for bad JSON in create invitation, got %d", w1.Code)
	}

	// 2. handleDeleteInvitation Not Found
	req2, _ := http.NewRequest(http.MethodDelete, "http://localhost/api/portal/invitations/99999", nil)
	req2.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w2 := httptest.NewRecorder()
	srv.handleDeleteInvitation(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found for delete invitation, got %d", w2.Code)
	}

	// 3. handleCSRSignInvitation Bad JSON payload
	req3, _ := http.NewRequest(http.MethodPost, "http://localhost/api/portal/csr-sign", bytes.NewBufferString("{bad json}"))
	req3.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	w3 := httptest.NewRecorder()
	srv.handleCSRSignInvitation(w3, req3)
	if w3.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for sign csr, got %d", w3.Code)
	}
}
