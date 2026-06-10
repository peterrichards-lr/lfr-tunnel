package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
)

func TestServer_Register(t *testing.T) {
	cfg := &config.ServerConfig{
		Domain1:   "example.com",
		AuthToken: "mysecret",
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()

	// 1. Unauthorized registration
	badPayload, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "alpha",
		Ports:           []PortMapping{{LocalPort: 8080}},
		AuthToken:       "wrong-secret",
	})
	req := httptest.NewRequest("POST", "http://example.com/api/register", bytes.NewReader(badPayload))
	req.Host = "example.com"
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
	}

	// 2. Successful registration
	goodPayload, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "alpha",
		Ports:           []PortMapping{{LocalPort: 8080}},
		AuthToken:       "mysecret",
	})
	req2 := httptest.NewRequest("POST", "http://example.com/api/register", bytes.NewReader(goodPayload))
	req2.Host = "example.com"
	rec2 := httptest.NewRecorder()

	srv.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec2.Code)
	}

	var resp RegisterResponse
	if err := json.NewDecoder(rec2.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "success" {
		t.Errorf("expected status success, got %s", resp.Status)
	}
	if resp.SessionToken == "" {
		t.Error("expected non-empty session token")
	}
	if len(resp.Remotes) != 1 {
		t.Errorf("expected 1 remote, got %d", len(resp.Remotes))
	}
}

func TestServer_ControlWelcomePage(t *testing.T) {
	cfg := &config.ServerConfig{
		Domain1: "example.com",
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Liferay Tunnel Gateway") {
		t.Error("expected welcome landing page content")
	}
}

func TestServer_Domains(t *testing.T) {
	cfg := &config.ServerConfig{
		Domain1: "example.se",
		Domain2: "example.online",
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()

	req := httptest.NewRequest("GET", "http://example.se/api/domains", nil)
	req.Host = "example.se"
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}

	var domains []string
	if err := json.NewDecoder(rec.Body).Decode(&domains); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(domains) != 2 || domains[0] != "example.se" || domains[1] != "example.online" {
		t.Errorf("expected [example.se, example.online], got %v", domains)
	}
}

func TestServer_CheckSubdomain(t *testing.T) {
	cfg := &config.ServerConfig{
		Domain1:   "example.com",
		AuthToken: "mysecret",
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()

	// 1. Missing token (Unauthorized)
	req := httptest.NewRequest("GET", "http://example.com/api/check-subdomain?subdomain=alpha", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
	}

	// 2. Bad token (Unauthorized)
	req = httptest.NewRequest("GET", "http://example.com/api/check-subdomain?subdomain=alpha&token=badsecret", nil)
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
	}

	// 3. Good token, missing subdomain (Bad Request)
	req = httptest.NewRequest("GET", "http://example.com/api/check-subdomain?token=mysecret", nil)
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rec.Code)
	}

	// 4. Good token, available subdomain
	req = httptest.NewRequest("GET", "http://example.com/api/check-subdomain?subdomain=beta-dev&token=mysecret", nil)
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
	var checkResp CheckSubdomainResponse
	if err := json.NewDecoder(rec.Body).Decode(&checkResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !checkResp.Available {
		t.Errorf("expected beta-dev to be available")
	}
	if len(checkResp.Suggestions) != 0 {
		t.Errorf("expected no suggestions for available subdomain, got %v", checkResp.Suggestions)
	}

	// 5. Good token, reserved subdomain (Unavailable)
	req = httptest.NewRequest("GET", "http://example.com/api/check-subdomain?subdomain=admin&token=mysecret", nil)
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
	if err := json.NewDecoder(rec.Body).Decode(&checkResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if checkResp.Available {
		t.Errorf("expected admin to be unavailable")
	}
	if len(checkResp.Suggestions) != 3 {
		t.Errorf("expected 3 suggestions for unavailable subdomain, got %d", len(checkResp.Suggestions))
	}
}

type mockMailSender struct {
	sentTo      string
	sentSubject string
	sentBody    string
}

func (m *mockMailSender) Send(to string, subject string, body string) error {
	m.sentTo = to
	m.sentSubject = subject
	m.sentBody = body
	return nil
}

func TestServer_RegistrationFlow(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lfr-tunnel-server-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &config.ServerConfig{
		Domain1:                "example.com",
		DBPath:                 dbPath,
		AdminNotificationEmail: "admin@example.com",
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer func() {
		time.Sleep(10 * time.Millisecond)
		srv.Stop()
	}()

	mockMail := &mockMailSender{}
	srv.mailSender = mockMail

	// 1. Submit registration request
	reqBody, _ := json.Marshal(RegisterRequestPayload{
		Email:     "developer@liferay.com",
		FirstName: "Dev",
		LastName:  "Liferay",
	})
	req := httptest.NewRequest("POST", "http://example.com/api/register-request", bytes.NewReader(reqBody))
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for register request, got %d, body: %s", rec.Code, rec.Body.String())
	}

	// Verify user is in DB as pending
	user, err := srv.db.GetUser("developer@liferay.com")
	if err != nil {
		t.Fatalf("failed to get user: %v", err)
	}
	if user.Status != "pending" || user.ApprovalToken == "" {
		t.Errorf("expected status 'pending' and non-empty approval token, got status=%s, token=%s", user.Status, user.ApprovalToken)
	}

	// Verify admin notification email was sent
	if mockMail.sentTo != "admin@example.com" || !strings.Contains(mockMail.sentBody, "/api/admin/approve") {
		t.Errorf("admin notification email not sent correctly, got to=%s, body=%s", mockMail.sentTo, mockMail.sentBody)
	}

	// 2. Admin approves user
	approveReq := httptest.NewRequest("GET", fmt.Sprintf("http://example.com/api/admin/approve?email=developer@liferay.com&token=%s", user.ApprovalToken), nil)
	approveReq.Host = "example.com"
	approveRec := httptest.NewRecorder()
	srv.ServeHTTP(approveRec, approveReq)

	if approveRec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for approval, got %d", approveRec.Code)
	}

	// Verify user is approved in DB and has claim token
	user, err = srv.db.GetUser("developer@liferay.com")
	if err != nil {
		t.Fatalf("failed to get user: %v", err)
	}
	if user.Status != "approved" || user.ClaimToken == "" {
		t.Errorf("expected status 'approved' and non-empty claim token, got status=%s, token=%s", user.Status, user.ClaimToken)
	}

	// Verify developer approval email was sent
	if mockMail.sentTo != "developer@liferay.com" || !strings.Contains(mockMail.sentBody, "/api/claim") {
		t.Errorf("developer email not sent correctly, got to=%s, body=%s", mockMail.sentTo, mockMail.sentBody)
	}

	// Extract claim token prefix (before the colon)
	parts := strings.Split(user.ClaimToken, ":")
	claimTokenPrefix := parts[0]

	// 3. Developer claims token
	claimReq := httptest.NewRequest("GET", fmt.Sprintf("http://example.com/api/claim?token=%s", claimTokenPrefix), nil)
	claimReq.Host = "example.com"
	claimRec := httptest.NewRecorder()
	srv.ServeHTTP(claimRec, claimReq)

	if claimRec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for claim, got %d", claimRec.Code)
	}

	var claimResp map[string]string
	if err := json.NewDecoder(claimRec.Body).Decode(&claimResp); err != nil {
		t.Fatalf("failed to decode claim response: %v", err)
	}

	patToken := claimResp["personal_access_token"]
	if !strings.HasPrefix(patToken, "lfr_pat_") {
		t.Errorf("expected claimed token to start with lfr_pat_, got %s", patToken)
	}

	// Verify claim token is cleared in DB
	user, err = srv.db.GetUser("developer@liferay.com")
	if err != nil {
		t.Fatalf("failed to get user: %v", err)
	}
	if user.ClaimToken != "" {
		t.Errorf("expected claim token to be cleared, got %s", user.ClaimToken)
	}

	// 4. Register tunnel using claimed PAT
	registerPayload, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "dev-tunnel",
		Ports:           []PortMapping{{LocalPort: 8080}},
		AuthToken:       patToken,
	})
	registerReq := httptest.NewRequest("POST", "http://example.com/api/register", bytes.NewReader(registerPayload))
	registerReq.Host = "example.com"
	registerRec := httptest.NewRecorder()
	srv.ServeHTTP(registerRec, registerReq)

	if registerRec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for tunnel registration with PAT, got %d, body: %s", registerRec.Code, registerRec.Body.String())
	}

	// 5. Unauthorized register request with wrong PAT
	badRegisterPayload, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "dev-tunnel-2",
		Ports:           []PortMapping{{LocalPort: 8080}},
		AuthToken:       "wrong-pat-token",
	})
	badRegisterReq := httptest.NewRequest("POST", "http://example.com/api/register", bytes.NewReader(badRegisterPayload))
	badRegisterReq.Host = "example.com"
	badRegisterRec := httptest.NewRecorder()
	srv.ServeHTTP(badRegisterRec, badRegisterReq)

	if badRegisterRec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for bad PAT registration, got %d", badRegisterRec.Code)
	}
}

func TestServer_StaticTokenProvisioning(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lfr-tunnel-static-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &config.ServerConfig{
		Domain1: "example.com",
		DBPath:  dbPath,
		StaticTokens: []config.StaticTokenConfig{
			{
				Token:  "dummy_static_token_xyz_value",
				UserID: "st-user@liferay.com",
				Name:   "Static Test User Token",
				Role:   "admin",
			},
		},
	}

	// 1. Initial startup (Should seed token)
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// Verify user is created and approved
	user, err := srv.db.GetUser("st-user@liferay.com")
	if err != nil {
		t.Fatalf("failed to find seeded user: %v", err)
	}
	if user.Status != "approved" || user.Role != "admin" {
		t.Errorf("seeded user role/status mismatch, got status=%s, role=%s", user.Status, user.Role)
	}

	// 2. Validate client registration using seeded static token
	registerPayload, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "static-tunnel",
		Ports:           []PortMapping{{LocalPort: 8080}},
		AuthToken:       "dummy_static_token_xyz_value",
	})
	registerReq := httptest.NewRequest("POST", "http://example.com/api/register", bytes.NewReader(registerPayload))
	registerReq.Host = "example.com"
	registerRec := httptest.NewRecorder()
	srv.ServeHTTP(registerRec, registerReq)

	if registerRec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for tunnel registration with static token, got %d, body: %s", registerRec.Code, registerRec.Body.String())
	}
	time.Sleep(10 * time.Millisecond)
	srv.Stop()

	// 3. Second startup with same DB (Idempotency Check)
	srv2, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to restart server: %v", err)
	}
	defer func() {
		time.Sleep(10 * time.Millisecond)
		srv2.Stop()
	}()

	// Ensure user is still present and there is only 1 user in DB
	users, err := srv2.db.ListUsers()
	if err != nil {
		t.Fatalf("failed to list users: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("expected exactly 1 user, got %d", len(users))
	}

	// Test registration still succeeds
	registerRec2 := httptest.NewRecorder()
	registerReq2 := httptest.NewRequest("POST", "http://example.com/api/register", bytes.NewReader(registerPayload))
	registerReq2.Host = "example.com"
	srv2.ServeHTTP(registerRec2, registerReq2)
	if registerRec2.Code != http.StatusOK {
		t.Errorf("expected 200 OK after restart, got %d", registerRec2.Code)
	}
}

func TestServer_DomainSeparation(t *testing.T) {
	cfg := &config.ServerConfig{
		Domain1:   "example.se",
		Domain2:   "example.online",
		AuthToken: "mysecret",
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()

	// 1. Register with Host example.se
	payload, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "peter-dev",
		Ports:           []PortMapping{{LocalPort: 8080}},
		AuthToken:       "mysecret",
	})
	req := httptest.NewRequest("POST", "http://tunnel.example.se/api/register", bytes.NewReader(payload))
	req.Host = "tunnel.example.se"
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var resp RegisterResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Domains) != 1 || resp.Domains[0] != "example.se" {
		t.Errorf("expected registered domains to be [example.se], got %v", resp.Domains)
	}

	// Verify the lease in registry exists only for example.se
	if _, exists := srv.registry.leases["peter-dev.example.se"]; !exists {
		t.Error("expected lease for peter-dev.example.se to exist")
	}
	if _, exists := srv.registry.leases["peter-dev.example.online"]; exists {
		t.Error("expected lease for peter-dev.example.online to NOT exist")
	}

	// 2. Check subdomain availability using Host header
	// Checking peter-dev on example.se should say unavailable
	reqCheck1 := httptest.NewRequest("GET", "http://tunnel.example.se/api/check-subdomain?subdomain=peter-dev&token=mysecret", nil)
	reqCheck1.Host = "tunnel.example.se"
	recCheck1 := httptest.NewRecorder()
	srv.ServeHTTP(recCheck1, reqCheck1)

	var respCheck1 CheckSubdomainResponse
	json.NewDecoder(recCheck1.Body).Decode(&respCheck1)
	if respCheck1.Available {
		t.Error("expected peter-dev.example.se to be unavailable")
	}

	// Checking peter-dev on example.online should say available
	reqCheck2 := httptest.NewRequest("GET", "http://tunnel.example.online/api/check-subdomain?subdomain=peter-dev&token=mysecret", nil)
	reqCheck2.Host = "tunnel.example.online"
	recCheck2 := httptest.NewRecorder()
	srv.ServeHTTP(recCheck2, reqCheck2)

	var respCheck2 CheckSubdomainResponse
	json.NewDecoder(recCheck2.Body).Decode(&respCheck2)
	if !respCheck2.Available {
		t.Error("expected peter-dev.example.online to be available")
	}
}

