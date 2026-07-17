package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

func TestEdgeValidationDeregisterAndAuditProxy(t *testing.T) {
	var receivedEdgeRegister bool
	var receivedEdgeDeregister bool
	var receivedEdgeAudit bool
	var forwardedAuditDetails string

	// 1. Start a mock Control Plane HTTP server
	mockControlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Authenticate edge token
		edgeToken := r.Header.Get("X-Edge-Token")
		if edgeToken != "my-edge-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			if _, err := w.Write([]byte(`{"error":"unauthorized"}`)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/internal/edge-register" {
			receivedEdgeRegister = true

			var edgeReq struct {
				AuthToken       string   `json:"auth_token"`
				SubdomainPrefix string   `json:"subdomain_prefix"`
				Domains         []string `json:"domains"`
			}
			_ = json.NewDecoder(r.Body).Decode(&edgeReq)

			if edgeReq.AuthToken != "user-pat-token" {
				w.WriteHeader(http.StatusUnauthorized)
				if _, err := w.Write([]byte(`{"error":"invalid token"}`)); err != nil {
					log.Printf("[Warning] Failed to write response: %v", err)
				}
				return
			}

			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`{
				"user_id": "test-user-id",
				"subdomain_prefix": "my-validated-sub",
				"rate_limit": 50
			}`)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/internal/edge-deregister" {
			receivedEdgeDeregister = true
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/api/internal/edge-audit-log" {
			receivedEdgeAudit = true
			var auditReq struct {
				Details string `json:"details"`
			}
			_ = json.NewDecoder(r.Body).Decode(&auditReq)
			forwardedAuditDetails = auditReq.Details
			w.WriteHeader(http.StatusOK)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockControlPlane.Close()

	// 2. Start our stateless Edge server
	edgeCfg := &config.ServerConfig{
		Domains:         []string{"lfr-demo.online"},
		BindAddr:        ":0",
		ChiselBindAddr:  ":0",
		ControlPlaneURL: mockControlPlane.URL,
		EdgeToken:       "my-edge-secret",
	}

	edgeSrv, err := NewServer(edgeCfg)
	if err != nil {
		t.Fatalf("failed to initialize edge server: %v", err)
	}

	edgeHttpServer := httptest.NewServer(edgeSrv)
	defer edgeHttpServer.Close()

	// 3. Perform client register request to the Edge server
	regPayload := []byte(`{
		"subdomain_prefix": "random",
		"auth_token": "user-pat-token",
		"ports": [{"local_port": 8080}]
	}`)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(edgeHttpServer.URL+"/api/register", "application/json", bytes.NewReader(regPayload))
	if err != nil {
		t.Fatalf("failed to perform register request: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected register status 200, got %d", resp.StatusCode)
	}

	var regResp RegisterResponse
	_ = json.NewDecoder(resp.Body).Decode(&regResp)

	if regResp.Status != "success" || regResp.SubdomainPrefix != "my-validated-sub" {
		t.Fatalf("unexpected register response: %+v", regResp)
	}

	if !receivedEdgeRegister {
		t.Fatal("Control Plane did not receive edge-register request")
	}

	// 4. Manually trigger writeAudit to verify audit forwarding
	edgeSrv.writeAudit("actor-id", "test-action", "subdomain", "123", "Edge event detailed logs", nil)

	// Wait a moment for async audit notification
	time.Sleep(100 * time.Millisecond)

	if !receivedEdgeAudit {
		t.Fatal("Control Plane did not receive edge-audit-log request")
	}
	if forwardedAuditDetails != "Edge event detailed logs" {
		t.Fatalf("expected audit details 'Edge event detailed logs', got '%s'", forwardedAuditDetails)
	}

	// 5. Manually trigger lease cleanup to verify edge-deregister notification
	leases := edgeSrv.registry.ListLeases()
	if len(leases) != 1 {
		t.Fatalf("expected 1 active lease on edge node, got %d", len(leases))
	}

	edgeSrv.registry.CleanLease(leases[0].SessionToken)

	// Wait a moment for async cleanup notification
	time.Sleep(100 * time.Millisecond)

	if !receivedEdgeDeregister {
		t.Fatal("Control Plane did not receive edge-deregister request on lease cleanup")
	}
}

func TestEdgeMetricsAndKickIntegration(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lfr-tunnel-test-edge-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	// Compute a hash of the edge secret
	hash := sha256.Sum256([]byte("my-edge-secret"))
	hashStr := hex.EncodeToString(hash[:])

	// 1. Create a real Control Plane server configuration
	controlCfg := config.DefaultServerConfig()
	controlCfg.DBPath = filepath.Join(tmpDir, "control.db")
	controlCfg.Domains = []string{"control.lfr-demo.se"}
	controlCfg.DisableBackupScheduler = true
	controlCfg.EdgeNodes = []config.EdgeNodeConfig{
		{ID: "us-edge", TokenHash: hashStr},
	}

	controlSrv, err := NewServer(controlCfg)
	if err != nil {
		t.Fatalf("failed to initialize control plane: %v", err)
	}
	defer controlSrv.Stop()
	defer time.Sleep(50 * time.Millisecond) // prevent SQLite cleanup races

	// Create a user in the control plane DB
	email := "dev-user@liferay.com"
	err = controlSrv.db.CreateUser(&db.User{
		ID:     "test-user-id",
		Email:  email,
		Role:   "user",
		Status: "approved",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Make sure we have a valid OIDC/PAT token registered for that user in DB
	patTokenVal := "pat-123456"
	patHash := sha256.Sum256([]byte(patTokenVal))
	patHashStr := hex.EncodeToString(patHash[:])
	if err = controlSrv.db.CreatePAT(&db.PersonalAccessToken{
		UserID:      "test-user-id",
		TokenHash:   patHashStr,
		TokenPrefix: "pat-12",
		Name:        "Test Token",
	}); err != nil {
		t.Fatalf("failed to create PAT: %v", err)
	}
	// Create a subdomain reservation for the test
	err = controlSrv.db.CreateSubdomainReservation(&db.SubdomainReservation{
		UserID:    "test-user-id",
		Subdomain: "peter-dev",
		Domain:    "us.lfr-demo.se",
	})
	if err != nil {
		t.Fatalf("failed to create reservation: %v", err)
	}

	// 2. Mock Edge Registration call to Control Plane
	regPayload := []byte(`{
		"subdomain_prefix": "peter-dev",
		"auth_token": "pat-123456",
		"ports": [{"local_port": 8080}],
		"domains": ["us.lfr-demo.se"],
		"client_ip": "8.8.8.8",
		"client_version": "v1.2.3",
		"client_os": "darwin"
	}`)

	req := httptest.NewRequest("POST", "http://control.lfr-demo.se/api/internal/edge-register", bytes.NewReader(regPayload))
	req.Header.Set("X-Edge-Token", "my-edge-secret")
	rec := httptest.NewRecorder()
	controlSrv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected edge registration 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	// Verify the lease was created in s.edgeLeases on the Control Plane
	controlSrv.edgeLeasesMu.Lock()
	leases, ok := controlSrv.edgeLeases["test-user-id"]
	controlSrv.edgeLeasesMu.Unlock()
	if !ok || len(leases) != 1 {
		t.Fatalf("expected 1 active edge lease on control plane, got %d", len(leases))
	}
	el := leases[0]
	if el.NodeID != "us-edge" || el.Subdomain != "peter-dev" || el.ClientIP != "8.8.8.8" || el.ClientVersion != "v1.2.3" {
		t.Errorf("unexpected edge lease fields populated: %+v", el)
	}

	// 3. Test active lease merging in handleAdminListLeases
	reqList := httptest.NewRequest("GET", "http://control.lfr-demo.se/api/admin/leases", nil)
	recListDirect := httptest.NewRecorder()
	controlSrv.handleAdminListLeases(recListDirect, reqList, "admin-user")

	var activeLeases []*TunnelLease
	err = json.NewDecoder(recListDirect.Body).Decode(&activeLeases)
	if err != nil {
		t.Fatalf("failed to decode merged leases: %v", err)
	}
	if len(activeLeases) != 1 {
		t.Fatalf("expected 1 active merged lease, got %d", len(activeLeases))
	}
	ml := activeLeases[0]
	if ml.SubdomainPrefix != "peter-dev" || ml.NodeID != "us-edge" || ml.ClientIP != "8.8.8.8" {
		t.Errorf("unexpected merged lease fields: %+v", ml)
	}

	// 4. Test Edge Metrics forwarding to `/api/internal/edge-metrics`
	metricsPayload := []byte(`[
		{
			"user_id": "test-user-id",
			"subdomain_prefix": "peter-dev",
			"full_host": "peter-dev.us.lfr-demo.se",
			"bytes_in": 1000,
			"bytes_out": 2000,
			"connected_at": "2026-06-25T12:00:00Z"
		}
	]`)

	reqMetrics := httptest.NewRequest("POST", "http://control.lfr-demo.se/api/internal/edge-metrics", bytes.NewReader(metricsPayload))
	reqMetrics.Header.Set("X-Edge-Token", "my-edge-secret")
	recMetrics := httptest.NewRecorder()
	controlSrv.ServeHTTP(recMetrics, reqMetrics)

	if recMetrics.Code != http.StatusOK {
		t.Fatalf("expected edge metrics POST 200, got %d", recMetrics.Code)
	}

	// Verify that the metrics were written to control plane's database
	analytics, err := controlSrv.db.GetUserAnalytics("test-user-id", 30)
	if err != nil {
		t.Fatalf("failed to get user analytics: %v", err)
	}
	if len(analytics.Tunnels) != 1 {
		t.Fatalf("expected 1 tunnel analytics entry in DB, got %d", len(analytics.Tunnels))
	}
	tb := analytics.Tunnels[0]
	if tb.FullHost != "peter-dev.us.lfr-demo.se" || tb.BytesIn != 1000 || tb.BytesOut != 2000 {
		t.Errorf("unexpected DB analytics data: %+v", tb)
	}

	// Verify in-memory edgeLeases statistics were updated dynamically
	controlSrv.edgeLeasesMu.Lock()
	updatedLease := controlSrv.edgeLeases["test-user-id"][0]
	controlSrv.edgeLeasesMu.Unlock()
	if updatedLease.BytesIn != 1000 || updatedLease.BytesOut != 2000 {
		t.Errorf("expected edgeLease in-memory stats updated to 1000/2000, got %d/%d", updatedLease.BytesIn, updatedLease.BytesOut)
	}

	// 5. Test Admin Kick proxying for edge lease
	// Set up a mock Edge Server to receive the proxied kick call
	mockEdgeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/internal/edge-kick" {
			// Verify signature header
			th := r.Header.Get("X-Edge-Token-Hash")
			if th != hashStr {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`{"status":"success"}`)); err != nil {
				log.Printf("[Warning] Failed to write response: %v", err)
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockEdgeServer.Close()

	// Update the FullHost in our in-memory lease to route to the mock edge server
	controlSrv.edgeLeasesMu.Lock()
	controlSrv.edgeLeases["test-user-id"][0].FullHost = "peter-dev." + mockEdgeServer.Listener.Addr().String()
	controlSrv.edgeLeasesMu.Unlock()

	reqKick := httptest.NewRequest("DELETE", "http://control.lfr-demo.se/api/admin/leases/peter-dev", nil)
	recKick := httptest.NewRecorder()
	controlSrv.handleAdminKickLease(recKick, reqKick, "admin-user")

	if recKick.Code != http.StatusOK {
		t.Fatalf("expected admin kick response 200, got %d. Body: %s", recKick.Code, recKick.Body.String())
	}
}
