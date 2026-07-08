package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

func TestHandleDeleteToken(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	// Create test users
	userA := &db.User{ID: "userA", Email: "usera@example.com", Role: "developer", Status: "approved"}
	if err := srv.db.CreateUser(userA); err != nil {
		t.Fatalf("failed to create userA: %v", err)
	}

	userB := &db.User{ID: "userB", Email: "userb@example.com", Role: "developer", Status: "approved"}
	if err := srv.db.CreateUser(userB); err != nil {
		t.Fatalf("failed to create userB: %v", err)
	}

	adminUser := &db.User{ID: "adminUser", Email: "admin@example.com", Role: "admin", Status: "approved"}
	if err := srv.db.CreateUser(adminUser); err != nil {
		t.Fatalf("failed to create adminUser: %v", err)
	}

	// Create a PAT for userA
	exp := time.Now().Add(24 * time.Hour)
	patA := &db.PersonalAccessToken{
		UserID:    "userA",
		TokenHash: "hash_patA",
		ExpiresAt: &exp,
	}
	if err := srv.db.CreatePAT(patA); err != nil {
		t.Fatalf("failed to create patA: %v", err)
	}

	// Create session tokens
	sessionTokenA := "session_a"
	srv.portalMap.Store("admin_session_"+sessionTokenA, PortalSessionData{
		Email:     userA.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	sessionTokenB := "session_b"
	srv.portalMap.Store("admin_session_"+sessionTokenB, PortalSessionData{
		Email:     userB.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	sessionTokenAdmin := "session_admin"
	srv.portalMap.Store("admin_session_"+sessionTokenAdmin, PortalSessionData{
		Email:     adminUser.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	tests := []struct {
		name           string
		tokenIDPath    string
		sessionToken   string
		expectedStatus int
	}{
		{
			name:           "Success_Self",
			tokenIDPath:    fmt.Sprintf("%d", patA.ID),
			sessionToken:   sessionTokenA,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Success_AdminOverride",
			tokenIDPath:    fmt.Sprintf("%d", patA.ID),
			sessionToken:   sessionTokenAdmin,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Failure_Unauthorized",
			tokenIDPath:    fmt.Sprintf("%d", patA.ID),
			sessionToken:   "",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Failure_InvalidID",
			tokenIDPath:    "notanumber",
			sessionToken:   sessionTokenA,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Failure_NotOwner",
			tokenIDPath:    fmt.Sprintf("%d", patA.ID),
			sessionToken:   sessionTokenB,
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetTokenID := tt.tokenIDPath
			if tt.name == "Success_AdminOverride" || tt.name == "Failure_NotOwner" {
				freshPAT := &db.PersonalAccessToken{
					UserID:    "userA",
					TokenHash: "hash_fresh_" + tt.name,
					ExpiresAt: &exp,
				}
				if err := srv.db.CreatePAT(freshPAT); err != nil {
					t.Fatalf("failed to create fresh PAT: %v", err)
				}
				targetTokenID = fmt.Sprintf("%d", freshPAT.ID)
			}

			req := httptest.NewRequest(http.MethodDelete, "http://example.com/api/tokens/"+targetTokenID, nil)
			if tt.sessionToken != "" {
				req.AddCookie(&http.Cookie{Name: "lfr_session", Value: tt.sessionToken})
			}
			w := httptest.NewRecorder()

			srv.handleDeleteToken(w, req)

			if w.Result().StatusCode != tt.expectedStatus {
				t.Errorf("expected status %d, got %d. Body: %s", tt.expectedStatus, w.Result().StatusCode, w.Body.String())
			}

			if tt.expectedStatus == http.StatusOK {
				var idVal int64
				if _, err := fmt.Sscanf(targetTokenID, "%d", &idVal); err != nil {
					t.Fatalf("failed to parse token ID: %v", err)
				}
				pats, err := srv.db.ListPATs("userA")
				if err != nil {
					t.Fatalf("failed to list PATs: %v", err)
				}
				found := false
				for _, p := range pats {
					if p.ID == idVal {
						found = true
						if p.RevokedAt == nil {
							t.Errorf("expected token %d to be revoked", idVal)
						}
					}
				}
				if !found {
					t.Errorf("token %d not found in DB at all", idVal)
				}
			}
		})
	}
}

func TestHandleAdminDeleteToken(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	// Create test user and token
	user := &db.User{ID: "test_user", Email: "test@example.com", Role: "developer"}
	if err := srv.db.CreateUser(user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	exp := time.Now().Add(24 * time.Hour)
	pat := &db.PersonalAccessToken{
		UserID:    "test_user",
		TokenHash: "hash_admin_del",
		ExpiresAt: &exp,
	}
	if err := srv.db.CreatePAT(pat); err != nil {
		t.Fatalf("failed to create PAT: %v", err)
	}

	originalDB := srv.db
	defer func() { srv.db = originalDB }()

	tests := []struct {
		name           string
		patIDPath      string
		setup          func()
		teardown       func()
		expectedStatus int
		verifyResponse func(t *testing.T, w *httptest.ResponseRecorder)
	}{
		{
			name:           "Success",
			patIDPath:      fmt.Sprintf("%d", pat.ID),
			expectedStatus: http.StatusOK,
			verifyResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				var resp map[string]string
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to unmarshal: %v", err)
				}
				if resp["status"] != "success" {
					t.Errorf("expected status=success, got %v", resp["status"])
				}
			},
		},
		{
			name:           "Success_Idempotent_NotFound",
			patIDPath:      "99999",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Failure_InvalidID",
			patIDPath:      "abc",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:      "Failure_DBNotConfigured",
			patIDPath: fmt.Sprintf("%d", pat.ID),
			setup: func() {
				srv.db = nil
			},
			teardown: func() {
				srv.db = originalDB
			},
			expectedStatus: http.StatusNotImplemented,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}
			if tt.teardown != nil {
				defer tt.teardown()
			}

			req := httptest.NewRequest(http.MethodDelete, "/api/admin/tokens/"+tt.patIDPath, nil)
			w := httptest.NewRecorder()

			srv.handleAdminDeleteToken(w, req, "admin@example.com")

			if w.Result().StatusCode != tt.expectedStatus {
				t.Errorf("expected status %d, got %d. Body: %s", tt.expectedStatus, w.Result().StatusCode, w.Body.String())
			}

			if tt.verifyResponse != nil {
				tt.verifyResponse(t, w)
			}
		})
	}
}
