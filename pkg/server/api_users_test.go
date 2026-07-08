package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/db"
)

func TestHandleAdminListUsers(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	// Configure Owner in srv.cfg
	srv.cfg.Owner.UserID = "owner@example.com"
	srv.cfg.Owner.Name = "Owner User"

	// Create test users in DB
	owner := &db.User{ID: "owner@example.com", Email: "owner@example.com", Role: "owner", Status: "approved"}
	if err := srv.db.CreateUser(owner); err != nil {
		t.Fatalf("failed to create owner: %v", err)
	}

	adminA := &db.User{ID: "admin_a@example.com", Email: "admin_a@example.com", Role: "admin", Status: "approved"}
	if err := srv.db.CreateUser(adminA); err != nil {
		t.Fatalf("failed to create adminA: %v", err)
	}

	devB := &db.User{ID: "dev_b@example.com", Email: "dev_b@example.com", Role: "developer", Status: "approved"}
	if err := srv.db.CreateUser(devB); err != nil {
		t.Fatalf("failed to create devB: %v", err)
	}

	devC := &db.User{ID: "dev_c@example.com", Email: "dev_c@example.com", Role: "developer", Status: "approved"}
	if err := srv.db.CreateUser(devC); err != nil {
		t.Fatalf("failed to create devC: %v", err)
	}

	// 1. Mock standard lease for devB in srv.registry
	srv.registry.Lock()
	srv.registry.leases["dev-b.example.com"] = &TunnelLease{
		UserID:          "dev_b@example.com",
		FullHost:        "dev-b.example.com",
		SubdomainPrefix: "dev-b",
		LocalPort:       8080,
	}
	srv.registry.Unlock()

	// 2. Mock edge lease for devC in srv.edgeLeases
	srv.edgeLeasesMu.Lock()
	srv.edgeLeases["dev_c@example.com"] = []EdgeLease{
		{
			NodeID:    "edge-node-1",
			Subdomain: "dev-c",
			UserID:    "dev_c@example.com",
			FullHost:  "dev-c.example.com",
			LocalPort: 9000,
			ClientIP:  "192.168.1.1",
			CreatedAt: time.Now(),
		},
	}
	srv.edgeLeasesMu.Unlock()

	// 3. Mock portal activity for devB (makes portalActive true)
	srv.portalActivityMu.Lock()
	srv.lastPortalActivity["dev_b@example.com"] = time.Now()
	srv.portalActivityMu.Unlock()

	originalDB := srv.db
	defer func() { srv.db = originalDB }()

	type AdminUserResponse struct {
		*db.User
		PortalActive  bool           `json:"portal_active"`
		ActiveTunnels []*TunnelLease `json:"active_tunnels"`
	}

	tests := []struct {
		name           string
		actor          string
		setup          func()
		teardown       func()
		expectedStatus int
		verifyResponse func(t *testing.T, body []byte)
	}{
		{
			name:           "Success_AsOwner",
			actor:          "owner@example.com",
			expectedStatus: http.StatusOK,
			verifyResponse: func(t *testing.T, body []byte) {
				var resp []*AdminUserResponse
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("failed to unmarshal: %v", err)
				}
				if len(resp) != 4 {
					t.Errorf("expected 4 users for owner, got %d", len(resp))
				}

				for _, r := range resp {
					if r.Email == "dev_b@example.com" {
						if !r.PortalActive {
							t.Errorf("expected dev_b to be portal_active")
						}
						if len(r.ActiveTunnels) != 1 || r.ActiveTunnels[0].SubdomainPrefix != "dev-b" {
							t.Errorf("expected 1 standard tunnel for dev_b, got %d", len(r.ActiveTunnels))
						}
					}
					if r.Email == "dev_c@example.com" {
						if r.PortalActive {
							t.Errorf("expected dev_c to not be portal_active")
						}
						if len(r.ActiveTunnels) != 1 || r.ActiveTunnels[0].SubdomainPrefix != "dev-c" {
							t.Errorf("expected 1 edge tunnel for dev_c, got %d", len(r.ActiveTunnels))
						}
					}
				}
			},
		},
		{
			name:           "Success_AsAdmin",
			actor:          "admin_a@example.com",
			expectedStatus: http.StatusOK,
			verifyResponse: func(t *testing.T, body []byte) {
				var resp []*AdminUserResponse
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("failed to unmarshal: %v", err)
				}
				if len(resp) != 3 {
					t.Errorf("expected 3 users for admin, got %d", len(resp))
				}
				for _, r := range resp {
					if r.Email == "owner@example.com" {
						t.Errorf("admin should not see owner")
					}
				}
			},
		},
		{
			name:  "DB_NotConfigured",
			actor: "owner@example.com",
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

			req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
			w := httptest.NewRecorder()

			srv.handleAdminListUsers(w, req, tt.actor)

			if w.Result().StatusCode != tt.expectedStatus {
				t.Errorf("expected status %d, got %d. Body: %s", tt.expectedStatus, w.Result().StatusCode, w.Body.String())
			}

			if tt.verifyResponse != nil && tt.expectedStatus == http.StatusOK {
				tt.verifyResponse(t, w.Body.Bytes())
			}
		})
	}
}
