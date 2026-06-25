package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
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
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
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
				_, _ = w.Write([]byte(`{"error":"invalid token"}`))
				return
			}

			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"user_id": "test-user-id",
				"subdomain_prefix": "my-validated-sub",
				"rate_limit": 50
			}`))
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
