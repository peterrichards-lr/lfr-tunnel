package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

func TestServer_Register(t *testing.T) {
	cfg := &config.ServerConfig{
		Domains:                    []string{"example.com"},
		DisableBackupScheduler:     true,
		AllowClientAutoReservation: true,
	}

	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(t.TempDir(), "test.db")
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer func() {
		time.Sleep(50 * time.Millisecond)
		srv.Stop()
	}()
	_ = srv.db.CreateUser(&db.User{ID: "peter.richards@liferay.com", Email: "peter.richards@liferay.com", Role: "owner", Status: "approved"})
	_ = srv.db.CreateUser(&db.User{ID: "test@example.com", Email: "test@example.com", Role: "admin", Status: "approved"})
	patHashBytes := sha256.Sum256([]byte("lfr_pat_mysecret"))
	_ = srv.db.CreatePAT(&db.PersonalAccessToken{UserID: "test@example.com", TokenHash: hex.EncodeToString(patHashBytes[:]), TokenPrefix: "lfr_pat_myse"})

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
		AuthToken:       "lfr_pat_mysecret",
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
		Domains:                []string{"example.com"},
		DisableBackupScheduler: true,
	}

	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(t.TempDir(), "test.db")
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()
	_ = srv.db.CreateUser(&db.User{ID: "peter.richards@liferay.com", Email: "peter.richards@liferay.com", Role: "owner", Status: "approved"})
	_ = srv.db.CreateUser(&db.User{ID: "test@example.com", Email: "test@example.com", Role: "admin", Status: "approved"})
	patHashBytes := sha256.Sum256([]byte("lfr_pat_mysecret"))
	_ = srv.db.CreatePAT(&db.PersonalAccessToken{UserID: "test@example.com", TokenHash: hex.EncodeToString(patHashBytes[:]), TokenPrefix: "lfr_pat_myse"})

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Liferay Tunnel Identity") {
		t.Error("expected dashboard landing page content")
	}
}

func TestServer_Domains(t *testing.T) {
	cfg := &config.ServerConfig{
		Domains:                []string{"example.se", "example.online"},
		DisableBackupScheduler: true,
	}

	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(t.TempDir(), "test.db")
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()
	_ = srv.db.CreateUser(&db.User{ID: "peter.richards@liferay.com", Email: "peter.richards@liferay.com", Role: "owner", Status: "approved"})
	_ = srv.db.CreateUser(&db.User{ID: "test@example.com", Email: "test@example.com", Role: "admin", Status: "approved"})
	patHashBytes := sha256.Sum256([]byte("lfr_pat_mysecret"))
	_ = srv.db.CreatePAT(&db.PersonalAccessToken{UserID: "test@example.com", TokenHash: hex.EncodeToString(patHashBytes[:]), TokenPrefix: "lfr_pat_myse"})

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
		Domains:                []string{"example.com"},
		DisableBackupScheduler: true,
	}

	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(t.TempDir(), "test.db")
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer func() {
		time.Sleep(50 * time.Millisecond)
		srv.Stop()
	}()
	_ = srv.db.CreateUser(&db.User{ID: "peter.richards@liferay.com", Email: "peter.richards@liferay.com", Role: "owner", Status: "approved"})
	_ = srv.db.CreateUser(&db.User{ID: "test@example.com", Email: "test@example.com", Role: "admin", Status: "approved"})
	patHashBytes := sha256.Sum256([]byte("lfr_pat_mysecret"))
	_ = srv.db.CreatePAT(&db.PersonalAccessToken{UserID: "test@example.com", TokenHash: hex.EncodeToString(patHashBytes[:]), TokenPrefix: "lfr_pat_myse"})

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
	req = httptest.NewRequest("GET", "http://example.com/api/check-subdomain?token=lfr_pat_mysecret", nil)
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rec.Code)
	}

	// 4. Good token, available subdomain
	req = httptest.NewRequest("GET", "http://example.com/api/check-subdomain?subdomain=beta-dev&token=lfr_pat_mysecret", nil)
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
	req = httptest.NewRequest("GET", "http://example.com/api/check-subdomain?subdomain=admin&token=lfr_pat_mysecret", nil)
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

	// 6. Test check subdomain using database Personal Access Token (PAT)
	tmpDir, err := os.MkdirTemp("", "lfr-tunnel-check-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	dbPath := filepath.Join(tmpDir, "test.db")
	cfgDb := &config.ServerConfig{
		Domains: []string{"example.com"},
		DBPath:  dbPath,
		Owner:   config.OwnerConfig{UserID: "peter.richards@liferay.com"},
	}

	srvDb, err := NewServer(cfgDb)
	if err != nil {
		t.Fatalf("failed to create server with DB: %v", err)
	}
	defer func() {
		time.Sleep(50 * time.Millisecond)
		srvDb.Stop()
	}()

	// Seed user and PAT for check-subdomain test
	_ = srvDb.db.CreateUser(&db.User{
		ID:     "peter.richards@liferay.com",
		Email:  "peter.richards@liferay.com",
		Role:   "admin",
		Status: "approved",
	})
	patHashBytes = sha256.Sum256([]byte("lfr_pat_peter_token_abc"))
	_ = srvDb.db.CreatePAT(&db.PersonalAccessToken{
		UserID:      "peter.richards@liferay.com",
		TokenHash:   hex.EncodeToString(patHashBytes[:]),
		TokenPrefix: "lfr_pat_pete",
	})

	// Query check subdomain using the seeded PAT token
	req = httptest.NewRequest("GET", "http://example.com/api/check-subdomain?subdomain=beta-dev", nil)
	req.Header.Set("Authorization", "Bearer lfr_pat_peter_token_abc")
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	srvDb.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK with PAT token, got %d", rec.Code)
	}
	var patCheckResp CheckSubdomainResponse
	if err := json.NewDecoder(rec.Body).Decode(&patCheckResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !patCheckResp.Available {
		t.Errorf("expected beta-dev to be available under PAT token query")
	}
}

type mockMailSender struct {
	sentTo       string
	sentSubject  string
	sentTextBody string
	sentHtmlBody string
}

func (m *mockMailSender) Send(to string, subject string, textBody string, htmlBody string) error {
	m.sentTo = to
	m.sentSubject = subject
	m.sentTextBody = textBody
	m.sentHtmlBody = htmlBody
	return nil
}

func TestServer_RegistrationFlow(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lfr-tunnel-server-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &config.ServerConfig{
		Domains:                []string{"example.com"},
		DBPath:                 dbPath,
		AdminNotificationEmail: "admin@example.com",
		DisableBackupScheduler: true,
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

	// Verify user is in DB as unverified
	user, err := srv.db.GetUser("developer@liferay.com")
	if err != nil {
		t.Fatalf("failed to get user: %v", err)
	}
	if user.Status != "unverified" || user.VerificationToken == "" || user.ApprovalToken == "" {
		t.Errorf("expected status 'unverified' and non-empty tokens, got status=%s, vt=%s, at=%s", user.Status, user.VerificationToken, user.ApprovalToken)
	}

	// Verify developer verification email was sent
	time.Sleep(50 * time.Millisecond)
	if mockMail.sentTo != "developer@liferay.com" || !strings.Contains(mockMail.sentTextBody, "/setup?token=") {
		t.Errorf("developer verification email not sent correctly, got to=%s, body=%s", mockMail.sentTo, mockMail.sentTextBody)
	}

	// 1.5. Developer completes setup
	payload := `{"token":"` + user.VerificationToken + `", "first_name":"Dev", "last_name":"User", "theme_preference":"dark"}`
	verifyReq := httptest.NewRequest("POST", "http://example.com/api/complete-setup", strings.NewReader(payload))
	verifyReq.Header.Set("Content-Type", "application/json")
	verifyReq.Host = "example.com"
	verifyRec := httptest.NewRecorder()
	srv.ServeHTTP(verifyRec, verifyReq)

	if verifyRec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for email verify, got %d", verifyRec.Code)
	}

	// Verify user is in DB as pending
	user, err = srv.db.GetUser("developer@liferay.com")
	if err != nil {
		t.Fatalf("failed to get user: %v", err)
	}
	if user.Status != "pending" {
		t.Errorf("expected status 'pending', got status=%s", user.Status)
	}

	// Verify admin notification email was sent
	time.Sleep(50 * time.Millisecond)
	if mockMail.sentTo != "admin@example.com" || (!strings.Contains(mockMail.sentTextBody, "/api/admin/approve") && !strings.Contains(mockMail.sentTextBody, "has verified their email")) {
		t.Errorf("admin notification email not sent correctly, got to=%s, body=%s", mockMail.sentTo, mockMail.sentTextBody)
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
	time.Sleep(50 * time.Millisecond)
	if mockMail.sentTo != "developer@liferay.com" || !strings.Contains(mockMail.sentTextBody, "/api/claim") {
		t.Errorf("developer email not sent correctly, got to=%s, body=%s", mockMail.sentTo, mockMail.sentTextBody)
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

	// Insert reservation so standard user registration works
	resExpiry := time.Now().AddDate(0, 0, 7)
	err = srv.db.CreateSubdomainReservation(&db.SubdomainReservation{
		UserID:    user.ID,
		Subdomain: "dev-tunnel",
		Domain:    "example.com",
		ExpiresAt: &resExpiry,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to create subdomain reservation for test: %v", err)
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

func TestServer_DomainSeparation(t *testing.T) {
	cfg := &config.ServerConfig{
		Domains:                    []string{"example.se", "example.online"},
		DisableBackupScheduler:     true,
		AllowClientAutoReservation: true,
	}

	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(t.TempDir(), "test.db")
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()
	_ = srv.db.CreateUser(&db.User{ID: "peter.richards@liferay.com", Email: "peter.richards@liferay.com", Role: "owner", Status: "approved"})
	_ = srv.db.CreateUser(&db.User{ID: "test@example.com", Email: "test@example.com", Role: "admin", Status: "approved"})
	patHashBytes := sha256.Sum256([]byte("lfr_pat_mysecret"))
	_ = srv.db.CreatePAT(&db.PersonalAccessToken{UserID: "test@example.com", TokenHash: hex.EncodeToString(patHashBytes[:]), TokenPrefix: "lfr_pat_myse"})

	// 1. Register with Host example.se
	payload, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "peter-dev",
		Ports:           []PortMapping{{LocalPort: 8080}},
		AuthToken:       "lfr_pat_mysecret",
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
	reqCheck1 := httptest.NewRequest("GET", "http://tunnel.example.se/api/check-subdomain?subdomain=peter-dev&token=lfr_pat_mysecret", nil)
	reqCheck1.Host = "tunnel.example.se"
	recCheck1 := httptest.NewRecorder()
	srv.ServeHTTP(recCheck1, reqCheck1)

	var respCheck1 CheckSubdomainResponse
	if err := json.NewDecoder(recCheck1.Body).Decode(&respCheck1); err != nil {
		t.Fatalf("failed to decode check1 response: %v", err)
	}
	if respCheck1.Available {
		t.Error("expected peter-dev.example.se to be unavailable")
	}

	// Checking peter-dev on example.online should say available
	reqCheck2 := httptest.NewRequest("GET", "http://tunnel.example.online/api/check-subdomain?subdomain=peter-dev&token=lfr_pat_mysecret", nil)
	reqCheck2.Host = "tunnel.example.online"
	recCheck2 := httptest.NewRecorder()
	srv.ServeHTTP(recCheck2, reqCheck2)

	var respCheck2 CheckSubdomainResponse
	if err := json.NewDecoder(recCheck2.Body).Decode(&respCheck2); err != nil {
		t.Fatalf("failed to decode check2 response: %v", err)
	}
	if !respCheck2.Available {
		t.Error("expected peter-dev.example.online to be available")
	}

	// Wait 50ms for asynchronous background tasks (like audit logs or PAT updates) to finish
	time.Sleep(50 * time.Millisecond)
}

func TestAdminEndpoints(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lfr-tunnel-admin-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	dbPath := filepath.Join(tmpDir, "test.db")
	cfg := &config.ServerConfig{
		Domains: []string{"example.com"},
		DBPath:  dbPath,
		Owner:   config.OwnerConfig{UserID: "admin@liferay.com"},
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()
	_ = srv.db.CreateUser(&db.User{ID: "peter.richards@liferay.com", Email: "peter.richards@liferay.com", Role: "owner", Status: "approved"})
	_ = srv.db.CreateUser(&db.User{ID: "test@example.com", Email: "test@example.com", Role: "admin", Status: "approved"})
	patHashBytes := sha256.Sum256([]byte("lfr_pat_mysecret"))
	_ = srv.db.CreatePAT(&db.PersonalAccessToken{UserID: "test@example.com", TokenHash: hex.EncodeToString(patHashBytes[:]), TokenPrefix: "lfr_pat_myse"})

	userAdmin := &db.User{
		ID:     "admin@liferay.com",
		Email:  "admin@liferay.com",
		Role:   "admin",
		Status: "approved",
	}
	_ = srv.db.CreateUser(userAdmin)

	adminToken := "lfr_pat_admin_static_token"
	adminHashBytes := sha256.Sum256([]byte(adminToken))
	_ = srv.db.CreatePAT(&db.PersonalAccessToken{
		UserID:      "admin@liferay.com",
		TokenHash:   hex.EncodeToString(adminHashBytes[:]),
		TokenPrefix: "lfr_pat_admi",
		Name:        "admin token",
	})

	userToken := "lfr_pat_user_static_token"
	userHashBytes := sha256.Sum256([]byte(userToken))

	// Seed DB with a test user
	user := &db.User{
		ID:     "u1",
		Email:  "testuser@liferay.com",
		Role:   "user",
		Status: "approved",
	}
	_ = srv.db.CreateUser(user)

	pat := &db.PersonalAccessToken{
		UserID:      "u1",
		TokenHash:   hex.EncodeToString(userHashBytes[:]),
		TokenPrefix: "lfr_pat_user",
		Name:        "test token",
	}
	_ = srv.db.CreatePAT(pat)

	// 1. Test unauthorized access (No token)
	req1 := httptest.NewRequest("GET", "http://tunnel.example.com/api/admin/users", nil)
	rec1 := httptest.NewRecorder()
	srv.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for no token, got %d", rec1.Code)
	}

	// 2. Test unauthorized access (Non-admin token)
	req2 := httptest.NewRequest("GET", "http://tunnel.example.com/api/admin/users", nil)
	req2.Header.Set("Authorization", "Bearer lfr_pat_user_static_token")
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for non-admin token, got %d", rec2.Code)
	}

	// 3. Test list users (Admin)
	req3 := httptest.NewRequest("GET", "http://tunnel.example.com/api/admin/users", nil)
	req3.Header.Set("Authorization", "Bearer lfr_pat_admin_static_token")
	rec3 := httptest.NewRecorder()
	srv.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Errorf("expected 200 for admin token, got %d", rec3.Code)
	}

	// 4. Test patch user
	patchBody := `{"role":"admin"}`
	req4 := httptest.NewRequest("PATCH", "http://tunnel.example.com/api/admin/users/testuser@liferay.com", strings.NewReader(patchBody))
	req4.Header.Set("Authorization", "Bearer lfr_pat_admin_static_token")
	rec4 := httptest.NewRecorder()
	srv.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusOK {
		t.Errorf("expected 200 for patch user, got %d", rec4.Code)
	}
	u, _ := srv.db.GetUserByEmail("testuser@liferay.com")
	if u.Role != "admin" {
		t.Errorf("expected user role to be admin, got %s", u.Role)
	}

	// 5. Test audit log
	// Sleep briefly to ensure async audit log write completes
	time.Sleep(100 * time.Millisecond)
	req5 := httptest.NewRequest("GET", "http://tunnel.example.com/api/admin/audit?action=user.role_changed", nil)
	req5.Header.Set("Authorization", "Bearer lfr_pat_admin_static_token")
	rec5 := httptest.NewRecorder()
	srv.ServeHTTP(rec5, req5)
	if rec5.Code != http.StatusOK {
		t.Errorf("expected 200 for audit log, got %d", rec5.Code)
	}
	var auditResp []db.AuditEntry
	_ = json.NewDecoder(rec5.Body).Decode(&auditResp)
	if len(auditResp) == 0 {
		t.Error("expected at least 1 audit entry for role change")
	}

	// 6. Test delete PAT
	req6 := httptest.NewRequest("DELETE", fmt.Sprintf("http://tunnel.example.com/api/admin/tokens/%d", pat.ID), nil)
	req6.Header.Set("Authorization", "Bearer lfr_pat_admin_static_token")
	rec6 := httptest.NewRecorder()
	srv.ServeHTTP(rec6, req6)
	if rec6.Code != http.StatusOK {
		t.Errorf("expected 200 for delete PAT, got %d", rec6.Code)
	}

	deletedPat, _ := srv.db.GetPATByHash(hex.EncodeToString(userHashBytes[:]))
	if deletedPat.RevokedAt == nil {
		t.Error("expected PAT to have a revoked_at timestamp")
	}
}

func TestDefenseMiddleware(t *testing.T) {
	cfg := &config.ServerConfig{
		Domains:                []string{"example.com"},
		IPBlacklist:            []string{"192.168.1.100"},
		DisableBackupScheduler: true,
	}

	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(t.TempDir(), "test.db")
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()
	_ = srv.db.CreateUser(&db.User{ID: "peter.richards@liferay.com", Email: "peter.richards@liferay.com", Role: "owner", Status: "approved"})
	_ = srv.db.CreateUser(&db.User{ID: "test@example.com", Email: "test@example.com", Role: "admin", Status: "approved"})
	patHashBytes := sha256.Sum256([]byte("lfr_pat_mysecret"))
	_ = srv.db.CreatePAT(&db.PersonalAccessToken{UserID: "test@example.com", TokenHash: hex.EncodeToString(patHashBytes[:]), TokenPrefix: "lfr_pat_myse"})

	// 1. Test IP Blacklist
	reqBlacklisted := httptest.NewRequest("GET", "http://tunnel.example.com/api/register", nil)
	reqBlacklisted.RemoteAddr = "192.168.1.100:12345"
	recBlacklisted := httptest.NewRecorder()
	srv.ServeHTTP(recBlacklisted, reqBlacklisted)
	if recBlacklisted.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for blacklisted IP, got %d", recBlacklisted.Code)
	}

	// 2. Test Rate Limiter (Burst of 20 allowed, 21st should fail)
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest("GET", "http://tunnel.example.com/api/register", nil)
		req.RemoteAddr = "10.0.0.1:54321"
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			t.Errorf("expected request %d to be allowed, got 429", i+1)
		}
	}

	// 21st request should hit the rate limit and get forbidden/blacklisted
	reqRateLimited := httptest.NewRequest("GET", "http://tunnel.example.com/api/register", nil)
	reqRateLimited.RemoteAddr = "10.0.0.1:54321"
	recRateLimited := httptest.NewRecorder()
	srv.ServeHTTP(recRateLimited, reqRateLimited)
	if recRateLimited.Code != http.StatusTooManyRequests && recRateLimited.Code != http.StatusForbidden {
		t.Errorf("expected 429 Too Many Requests or 403 Forbidden, got %d", recRateLimited.Code)
	}
}

func TestServer_UnsubscribeAndMaintenance(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lfr-tunnel-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	cfg := config.DefaultServerConfig()
	cfg.DBPath = filepath.Join(tmpDir, "test.db")
	cfg.Domains = []string{"example.com"}
	cfg.DisableBackupScheduler = true
	cfg.DockerImage = "peterrichardslr/lfr-tunnel"

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()
	defer time.Sleep(50 * time.Millisecond) // prevent SQLite cleanup races

	email := "dev-user@liferay.com"
	_ = srv.db.CreateUser(&db.User{ID: email, Email: email, Role: "user", Status: "approved"})

	// 1. Test stateless unsubscribe token generation and verification
	token := srv.GenerateUnsubscribeToken(email)
	if token == "" {
		t.Fatal("expected unsubscribe token to be non-empty")
	}

	parsedEmail, err := srv.VerifyUnsubscribeToken(token)
	if err != nil {
		t.Fatalf("expected token verification to succeed, got error: %v", err)
	}
	if parsedEmail != email {
		t.Errorf("expected parsed email %q, got %q", email, parsedEmail)
	}

	// 2. Test unsubscribe GET request endpoint
	req := httptest.NewRequest("GET", "http://example.com/api/unsubscribe?token="+token, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for unsubscribe endpoint, got %d", rec.Code)
	}

	u, err := srv.db.GetUserByEmail(email)
	if err != nil {
		t.Fatalf("failed to get user: %v", err)
	}
	if u.NotificationPrefs != "disabled" {
		t.Errorf("expected user notification_prefs to be 'disabled', got %q", u.NotificationPrefs)
	}

	// 3. Test scheduling maintenance mode with a countdown
	srv.maintMutex.Lock()
	srv.maintenanceMode = false
	srv.maintScheduledAt = time.Now().Add(5 * time.Minute)
	srv.maintMutex.Unlock()

	reqVer := httptest.NewRequest("GET", "http://example.com/api/version", nil)
	recVer := httptest.NewRecorder()
	srv.ServeHTTP(recVer, reqVer)

	if recVer.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for api/version, got %d", recVer.Code)
	}

	var verResp map[string]interface{}
	if err := json.NewDecoder(recVer.Body).Decode(&verResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if verResp["maintenance_mode"] != "pending" {
		t.Errorf("expected maintenance_mode to be 'pending', got %q", verResp["maintenance_mode"])
	}
	if verResp["docker_image"] != "peterrichardslr/lfr-tunnel" {
		t.Errorf("expected docker_image to be 'peterrichardslr/lfr-tunnel', got %q", verResp["docker_image"])
	}
}

func TestServer_GDPRDeleteAndAnonymization(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lfr-tunnel-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	cfg := config.DefaultServerConfig()
	cfg.DBPath = filepath.Join(tmpDir, "test.db")
	cfg.Domains = []string{"example.com"}
	cfg.DisableBackupScheduler = true

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()
	defer time.Sleep(50 * time.Millisecond) // prevent SQLite cleanup races

	email := "gdpr-user@example.com"
	_ = srv.db.CreateUser(&db.User{ID: email, Email: email, Role: "user", Status: "approved"})

	// Create some audit entries for this user
	_ = srv.db.WriteAuditEntry(&db.AuditEntry{
		ActorID:    email,
		Action:     "tunnel.connected",
		TargetType: "tunnel",
		TargetID:   "gamma",
	})

	// Run anonymization directly
	anonymizedID := "gdpr-deleted-user-hash123"
	err = srv.db.AnonymizeUserData(email, anonymizedID)
	if err != nil {
		t.Fatalf("AnonymizeUserData failed: %v", err)
	}

	// Verify audit logs are anonymized
	entries, err := srv.db.ListAuditEntries(db.AuditFilter{})
	if err != nil {
		t.Fatalf("failed to list audit entries: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least 1 audit entry")
	}

	for _, entry := range entries {
		if entry.ActorID == email {
			t.Errorf("found un-anonymized actor_id %q in audit log", entry.ActorID)
		}
	}
}

func TestServer_I18nLanguageHandling(t *testing.T) {
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"example.com"}
	cfg.DisableBackupScheduler = true

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()
	defer time.Sleep(50 * time.Millisecond) // prevent SQLite cleanup races

	// 1. Test GetTranslation matching
	subjectFR := srv.GetTranslation("fr", "magic_link_subject")
	expectedFR := "Votre lien magique de connexion"
	if subjectFR != expectedFR {
		t.Errorf("expected French translation %q, got %q", expectedFR, subjectFR)
	}

	// 2. Test GetTranslation default fallback
	subjectEN := srv.GetTranslation("ru", "magic_link_subject") // unsupported language falls back to 'en'
	expectedEN := "Your magic login link"
	if subjectEN != expectedEN {
		t.Errorf("expected Fallback English translation %q, got %q", expectedEN, subjectEN)
	}

	// 3. Test ResolveLocale headers
	req, _ := http.NewRequest("GET", "http://example.com/", nil)
	req.Header.Set("Accept-Language", "es-ES,es;q=0.9,en;q=0.8")

	lang := srv.ResolveLocale(req)
	if lang != "es" {
		t.Errorf("expected resolved locale 'es', got %q", lang)
	}
}

func TestParseProperties(t *testing.T) {
	mockContent := `# This is a test comment
! Another comment type

portal.welcome = Bine ai venit
  btn_send_magic_link  =  Trimite Link-ul Magic  
label_email:Adresă de E-mail

# Empty line below

`
	props := parseProperties(mockContent)

	if len(props) != 3 {
		t.Fatalf("expected exactly 3 properties, got %d", len(props))
	}
}

// setupTestServer sets up a temporary server instance with a mock mail sender and a clean DB path.
func setupTestServer(t *testing.T) (*Server, *mockMailSender, func()) {
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"example.com"}
	cfg.DBPath = filepath.Join(t.TempDir(), "test.db")
	cfg.DisableBackupScheduler = true

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	mockMail := &mockMailSender{}
	srv.mailSender = mockMail

	cleanup := func() {
		srv.Stop()
		time.Sleep(50 * time.Millisecond) // prevent SQLite cleanup races
	}

	return srv, mockMail, cleanup
}

func TestServer_GetMeLanguagePreference(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Create a user in the database with Romanian language preference
	email := "test_i18n@example.com"
	u := &db.User{
		ID:                 email,
		Email:              email,
		Role:               "user",
		Status:             "approved",
		LanguagePreference: "ro",
		TOTPEnabled:        true,
	}
	if err := srv.db.CreateUser(u); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// 2. Create an active portal session
	sessionToken := "test-session-token-i18n-123"
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		ClientIP:  "127.0.0.1",
	})

	// 3. Forge a GET request to /api/me
	req, _ := http.NewRequest("GET", "http://example.com/api/me", nil)
	req.AddCookie(&http.Cookie{
		Name:  "lfr_session",
		Value: sessionToken,
	})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	// 4. Assert status OK
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d", rec.Code)
	}

	// 5. Unmarshal and assert language_preference field
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	langVal, ok := resp["language_preference"]
	if !ok {
		t.Error("expected 'language_preference' field in /api/me response, but it was missing")
	} else if langVal != "ro" {
		t.Errorf("expected 'language_preference' to be %q, got %q", "ro", langVal)
	}

	totpVal, ok := resp["totp_enabled"]
	if !ok {
		t.Error("expected 'totp_enabled' field in /api/me response, but it was missing")
	} else if totpVal != true {
		t.Errorf("expected 'totp_enabled' to be true, got %v", totpVal)
	}
}

func TestServer_WelcomePageLanguageOverride(t *testing.T) {
	srv, mockMail, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Create a user in the DB with English language preference
	email := "test_override@example.com"
	u := &db.User{
		ID:                 email,
		Email:              email,
		Role:               "user",
		Status:             "approved",
		LanguagePreference: "en",
	}
	if err := srv.db.CreateUser(u); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// 2. Forge a POST request to /api/auth/magic-link?lang=ro (Romanian welcome screen selection)
	payload, _ := json.Marshal(map[string]string{"email": email})
	req, _ := http.NewRequest("POST", "http://example.com/api/auth/magic-link?lang=ro", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	// Sleep 50ms to let the background email-sending goroutine execute and complete!
	time.Sleep(50 * time.Millisecond)

	// 3. Assert status OK
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d", rec.Code)
	}

	// 4. Assert that the intercepted email is in Romanian (respecting ?lang=ro instead of DB's "en"!)
	interceptedBody := mockMail.sentTextBody
	if !strings.Contains(interceptedBody, "Salut") {
		t.Error("expected email body to be translated to Romanian ('Salut'), but it was not")
	}
	if !strings.Contains(interceptedBody, "Conectează-te la Portal") {
		t.Error("expected email button to be translated to Romanian ('Conectează-te la Portal'), but it was not")
	}
}

func TestServer_MagicLinkInstantRequestInvalidation(t *testing.T) {
	srv, mockMail, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Create approved user
	email := "test_invalidation@example.com"
	u := &db.User{
		ID:     email,
		Email:  email,
		Role:   "user",
		Status: "approved",
	}
	if err := srv.db.CreateUser(u); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// 2. Request first magic link
	payload, _ := json.Marshal(map[string]string{"email": email})
	req1, _ := http.NewRequest("POST", "http://example.com/api/auth/magic-link", bytes.NewReader(payload))
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	srv.ServeHTTP(rec1, req1)
	time.Sleep(30 * time.Millisecond) // wait for goroutine

	// Extract first token from intercepted body
	firstBody := mockMail.sentTextBody
	firstToken := extractTokenFromBody(firstBody)
	if firstToken == "" {
		t.Fatal("failed to extract first magic token from email body")
	}

	// 3. Request second magic link
	req2, _ := http.NewRequest("POST", "http://example.com/api/auth/magic-link", bytes.NewReader(payload))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, req2)
	time.Sleep(30 * time.Millisecond) // wait for goroutine

	// Extract second token from intercepted body
	secondBody := mockMail.sentTextBody
	secondToken := extractTokenFromBody(secondBody)
	if secondToken == "" {
		t.Fatal("failed to extract second magic token from email body")
	}

	// 4. Verify first token is now INVALID (denied 401)
	verifyPayload1, _ := json.Marshal(map[string]string{"token": firstToken})
	reqV1, _ := http.NewRequest("POST", "http://example.com/api/auth/verify", bytes.NewReader(verifyPayload1))
	reqV1.Header.Set("Content-Type", "application/json")
	recV1 := httptest.NewRecorder()
	srv.ServeHTTP(recV1, reqV1)

	if recV1.Code != http.StatusUnauthorized {
		t.Errorf("expected first (older) token to be unauthorized (401), got %d", recV1.Code)
	}

	// 5. Verify second token is VALID (accepts 200)
	verifyPayload2, _ := json.Marshal(map[string]string{"token": secondToken})
	reqV2, _ := http.NewRequest("POST", "http://example.com/api/auth/verify", bytes.NewReader(verifyPayload2))
	reqV2.Header.Set("Content-Type", "application/json")
	recV2 := httptest.NewRecorder()
	srv.ServeHTTP(recV2, reqV2)

	if recV2.Code != http.StatusOK {
		t.Errorf("expected second (latest) token to be successfully authorized (200), got %d", recV2.Code)
	}
}

// helper to extract token from URL in body
func extractTokenFromBody(body string) string {
	idx := strings.Index(body, "token=")
	if idx == -1 {
		return ""
	}
	start := idx + 6
	end := start
	for end < len(body) && body[end] != '"' && body[end] != ' ' && body[end] != '\n' && body[end] != '&' && body[end] != '<' {
		end++
	}
	return body[start:end]
}

func TestServer_MagicLinkLanguagePersistence(t *testing.T) {
	srv, mockMail, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Create an approved user with English language preference
	email := "test_lang_persist@example.com"
	u := &db.User{
		ID:                 email,
		Email:              email,
		Role:               "user",
		Status:             "approved",
		LanguagePreference: "en",
	}
	if err := srv.db.CreateUser(u); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// 2. Request magic link with Romanian language selection (lang=ro)
	payload, _ := json.Marshal(map[string]string{"email": email})
	req1, _ := http.NewRequest("POST", "http://example.com/api/auth/magic-link?lang=ro", bytes.NewReader(payload))
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	srv.ServeHTTP(rec1, req1)
	time.Sleep(30 * time.Millisecond) // wait for goroutine

	// Extract the magic token containing the &lang=ro URL query parameter!
	bodyText := mockMail.sentTextBody
	idxToken := strings.Index(bodyText, "token=")
	if idxToken == -1 {
		t.Fatal("failed to find token= in email body")
	}
	// Verify that the link carries &lang=ro!
	if !strings.Contains(bodyText, "lang=ro") {
		t.Error("expected generated magic link to carry '&lang=ro', but it did not")
	}

	// Extract the actual token hash (64 hex characters)
	tokenStart := idxToken + 6
	tokenEnd := tokenStart
	for tokenEnd < len(bodyText) && bodyText[tokenEnd] != '"' && bodyText[tokenEnd] != '&' && bodyText[tokenEnd] != ' ' {
		tokenEnd++
	}
	token := bodyText[tokenStart:tokenEnd]

	// 3. Forge a POST request to /api/auth/verify containing BOTH the token and the lang override
	verifyPayload, _ := json.Marshal(map[string]string{
		"token": token,
		"lang":  "ro",
	})
	reqVerify, _ := http.NewRequest("POST", "http://example.com/api/auth/verify", bytes.NewReader(verifyPayload))
	reqVerify.Header.Set("Content-Type", "application/json")
	recVerify := httptest.NewRecorder()
	srv.ServeHTTP(recVerify, reqVerify)

	// 4. Assert status OK
	if recVerify.Code != http.StatusOK {
		t.Fatalf("expected verification to succeed (200), got %d, body: %s", recVerify.Code, recVerify.Body.String())
	}

	// 5. Fetch the user from the SQLite database and assert their language_preference has been dynamically updated to "ro"!
	dbUser, err := srv.db.GetUserByEmail(email)
	if err != nil {
		t.Fatalf("failed to fetch user from DB: %v", err)
	}
	if dbUser.LanguagePreference != "ro" {
		t.Errorf("expected user language_preference to be dynamically updated to %q, got %q", "ro", dbUser.LanguagePreference)
	}
}

func TestServer_InvitationLanguagePersistence(t *testing.T) {
	srv, mockMail, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Create an active admin user and session to satisfy the auth middleware
	adminEmail := "admin_test@example.com"
	adminUser := &db.User{
		ID:     adminEmail,
		Email:  adminEmail,
		Role:   "admin",
		Status: "approved",
	}
	if err := srv.db.CreateUser(adminUser); err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}

	sessionToken := "admin-test-session-token-987"
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     adminEmail,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		ClientIP:  "127.0.0.1",
	})

	// 2. Forge an administrative POST request to invite a new user with Romanian language preference
	payload, _ := json.Marshal(map[string]string{
		"email":               "test_invite_lang@example.com",
		"first_name":          "Dev",
		"last_name":           "Liferay",
		"language_preference": "ro",
	})
	req, _ := http.NewRequest("POST", "http://example.com/api/admin/invite", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{
		Name:  "lfr_session",
		Value: sessionToken,
	})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	time.Sleep(30 * time.Millisecond) // wait for goroutine

	// 3. Assert status OK
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d, body: %s", rec.Code, rec.Body.String())
	}

	// 3. Verify that the created user record has 'LanguagePreference' saved as 'ro'
	dbUser, err := srv.db.GetUserByEmail("test_invite_lang@example.com")
	if err != nil {
		t.Fatalf("failed to fetch invited user from DB: %v", err)
	}
	if dbUser.LanguagePreference != "ro" {
		t.Errorf("expected user language_preference to be saved as 'ro', got %q", dbUser.LanguagePreference)
	}

	// 4. Assert that the sent invitation email is translated into Romanian
	interceptedBody := mockMail.sentTextBody
	if !strings.Contains(interceptedBody, "Acceptă Invitația") {
		t.Error("expected invitation email button to be translated to Romanian ('Acceptă Invitația'), but it was not")
	}
}

func TestServer_AuditLogCSVExport(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Create a dummy audit entry
	email := "audit_test@example.com"
	s := "test action"
	dummyReq, _ := http.NewRequest("GET", "/", nil)
	srv.writeAudit(email, s, "user", "target", "details", dummyReq)
	time.Sleep(50 * time.Millisecond) // wait for goroutine

	// 2. Create an active admin session
	adminEmail := "admin_test@example.com"
	adminUser := &db.User{ID: adminEmail, Email: adminEmail, Role: "admin", Status: "approved"}
	if err := srv.db.CreateUser(adminUser); err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}
	sessionToken := "admin-audit-token-123"
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{Email: adminEmail, ExpiresAt: time.Now().Add(1 * time.Hour)})

	// 3. Forge a GET request to /api/admin/audit/export
	req, _ := http.NewRequest("GET", "http://example.com/api/admin/audit/export", nil)
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionToken})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	// 4. Assert status OK and Content-Type
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "text/csv" {
		t.Errorf("expected Content-Type text/csv, got %s", rec.Header().Get("Content-Type"))
	}

	// 5. Assert CSV content contains our test action
	csvContent := rec.Body.String()
	if !strings.Contains(csvContent, s) {
		t.Errorf("expected CSV to contain action %q, got: %s", s, csvContent)
	}
}

func TestServer_RateLimitingEnforcements(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Create a user with a specific DB rate limit quota of 10 RPS
	email := "throttled_user@example.com"
	u := &db.User{
		ID:                 email,
		Email:              email,
		Role:               "user",
		Status:             "approved",
		RateLimit:          10, // Quota!
		LanguagePreference: "en",
	}
	if err := srv.db.CreateUser(u); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create a valid personal access token (PAT) inside the SQLite database for this user
	token := "test-client-token-123"
	hashBytes := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(hashBytes[:])

	pat := &db.PersonalAccessToken{
		UserID:      email,
		TokenHash:   tokenHash,
		TokenPrefix: "lfr_pat_test",
		Name:        "Test Token",
	}
	if err := srv.db.CreatePAT(pat); err != nil {
		t.Fatalf("failed to create PAT: %v", err)
	}

	// Insert reservation so standard user registration works
	resExpiry := time.Now().AddDate(0, 0, 7)
	err := srv.db.CreateSubdomainReservation(&db.SubdomainReservation{
		UserID:    email,
		Subdomain: "throttle-dev",
		Domain:    "example.com",
		ExpiresAt: &resExpiry,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("failed to create subdomain reservation for test: %v", err)
	}

	// Mock register request from client requesting a much higher rate limit (100 RPS)
	payloadRegister, _ := json.Marshal(RegisterRequest{
		AuthToken:       token, // Pass the valid token!
		SubdomainPrefix: "throttle-dev",
		RateLimit:       100, // Client requests 100 RPS
		Ports:           []PortMapping{{LocalPort: 8080}},
	})

	reqReg, _ := http.NewRequest("POST", "http://example.com/api/register", bytes.NewReader(payloadRegister))
	reqReg.Header.Set("Content-Type", "application/json")
	recReg := httptest.NewRecorder()
	srv.ServeHTTP(recReg, reqReg)

	if recReg.Code != http.StatusOK {
		t.Fatalf("expected register OK (200), got %d, body: %s", recReg.Code, recReg.Body.String())
	}

	// Verify that the active lease's RateLimit was cleanly and dynamically capped at the user's DB quota of 10 RPS!
	host := "throttle-dev.example.com"
	lease, ok := srv.registry.GetLease(host)
	if !ok {
		t.Fatalf("expected active lease for %q to exist, but it was missing", host)
	}
	if lease.RateLimit != 10 {
		t.Errorf("expected lease rate_limit to be capped at DB user quota (10), got %d", lease.RateLimit)
	}

	// 2. Perform Administrative Dynamic Rate Limit Override (to 15 RPS)
	adminEmail := "admin_limiter@example.com"
	adminUser := &db.User{
		ID:     adminEmail,
		Email:  adminEmail,
		Role:   "admin",
		Status: "approved",
	}
	if err := srv.db.CreateUser(adminUser); err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}

	sessionToken := "admin-limiter-session-token-456"
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email:     adminEmail,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		ClientIP:  "127.0.0.1",
	})

	// Forge administrative rate-limit override request
	payloadOverride, _ := json.Marshal(map[string]interface{}{
		"host":       host,
		"rate_limit": 15,
	})
	reqOverride, _ := http.NewRequest("PUT", "http://example.com/api/admin/leases/rate-limit", bytes.NewReader(payloadOverride))
	reqOverride.Header.Set("Content-Type", "application/json")
	reqOverride.AddCookie(&http.Cookie{
		Name:  "lfr_session",
		Value: sessionToken,
	})
	recOverride := httptest.NewRecorder()
	srv.ServeHTTP(recOverride, reqOverride)

	// Verify administrative status success
	if recOverride.Code != http.StatusOK {
		t.Fatalf("expected override status 200 OK, got %d, body: %s", recOverride.Code, recOverride.Body.String())
	}

	// Verify that the in-memory lease has been updated instantly to 15 RPS!
	leaseOverride, _ := srv.registry.GetLease(host)
	if leaseOverride.RateLimit != 15 {
		t.Errorf("expected active lease rate_limit to be dynamically updated to 15, got %d", leaseOverride.RateLimit)
	}

	// Verify that our ProxyHandler's rate limiter dynamically updates its limit and burst on the fly!
	limiter := srv.proxyHandler.getRateLimiter(host, leaseOverride.RateLimit)
	if limiter.Limit() != 15 {
		t.Errorf("expected ProxyHandler's rate limiter to adjust dynamically to 15, got %f", limiter.Limit())
	}

	// 3. Test Administrative User-Level Quota PATCH Update via /api/admin/users/:email
	payloadUserPatch, _ := json.Marshal(map[string]interface{}{
		"rate_limit": 50, // Set new DB quota to 50 RPS
	})
	reqUserPatch, _ := http.NewRequest("PATCH", "http://example.com/api/admin/users/"+url.QueryEscape(email), bytes.NewReader(payloadUserPatch))
	reqUserPatch.Header.Set("Content-Type", "application/json")
	reqUserPatch.AddCookie(&http.Cookie{
		Name:  "lfr_session",
		Value: sessionToken,
	})
	recUserPatch := httptest.NewRecorder()
	srv.ServeHTTP(recUserPatch, reqUserPatch)

	// Verify status OK
	if recUserPatch.Code != http.StatusOK {
		t.Fatalf("expected user PATCH status 200 OK, got %d, body: %s", recUserPatch.Code, recUserPatch.Body.String())
	}

	// Verify that the user's RateLimit quota inside the SQLite database was successfully updated to 50 RPS!
	dbUser, err := srv.db.GetUserByEmail(email)
	if err != nil {
		t.Fatalf("failed to fetch user from DB: %v", err)
	}
	if dbUser.RateLimit != 50 {
		t.Errorf("expected user rate_limit quota in DB to be updated to 50, got %d", dbUser.RateLimit)
	}
}

func TestServer_DatabaseBackupScheduler(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Manually trigger a database backup
	time.Sleep(1 * time.Second) // Ensure unique timestamp
	err := srv.BackupDatabase()
	if err != nil {
		t.Fatalf("BackupDatabase failed: %v", err)
	}

	// 2. Locate the backup directory
	backupsDir := filepath.Join(filepath.Dir(srv.cfg.DBPath), "backups")

	// 3. Verify directory exists
	info, err := os.Stat(backupsDir)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected backups directory to exist at %s, but it did not", backupsDir)
	}

	// 4. Verify a backup file was created
	files, err := os.ReadDir(backupsDir)
	if err != nil || len(files) == 0 {
		t.Fatalf("expected at least one backup file in %s, but found none", backupsDir)
	}

	// 5. Verify the file is not empty and matches active DB size
	backupFile := filepath.Join(backupsDir, files[0].Name())
	bInfo, err := os.Stat(backupFile)
	if err != nil {
		t.Fatalf("failed to stat backup file: %v", err)
	}
	if bInfo.Size() == 0 {
		t.Error("backup file is empty")
	}

	// 6. Basic size check: backup should be reasonably close to DB size
	dbInfo, _ := os.Stat(srv.cfg.DBPath)
	if bInfo.Size() < dbInfo.Size() {
		t.Errorf("backup file size (%d) is significantly smaller than active DB size (%d)", bInfo.Size(), dbInfo.Size())
	}
}

func TestServer_BackupSchedulerConfiguration(t *testing.T) {
	// 1. Test case A: Backup Scheduler Disabled
	t.Run("Disabled", func(t *testing.T) {
		cfg := config.DefaultServerConfig()
		cfg.Domains = []string{"example.com"}
		cfg.DBPath = filepath.Join(t.TempDir(), "test.db")
		cfg.DisableBackupScheduler = true

		srv, err := NewServer(cfg)
		if err != nil {
			t.Fatalf("failed to create server: %v", err)
		}
		defer func() {
			srv.Stop()
			time.Sleep(50 * time.Millisecond) // SQLite race protection
		}()

		// Allow brief window to verify no background backup runs
		time.Sleep(50 * time.Millisecond)

		backupsDir := filepath.Join(filepath.Dir(cfg.DBPath), "backups")
		_, err = os.Stat(backupsDir)
		if err == nil {
			t.Error("expected backups directory to not exist when DisableBackupScheduler is true")
		}
	})

	// 2. Test case B: Backup Scheduler Enabled
	t.Run("Enabled", func(t *testing.T) {
		cfg := config.DefaultServerConfig()
		cfg.Domains = []string{"example.com"}
		cfg.DBPath = filepath.Join(t.TempDir(), "test.db")
		cfg.DisableBackupScheduler = false // Enable!

		srv, err := NewServer(cfg)
		if err != nil {
			t.Fatalf("failed to create server: %v", err)
		}
		defer func() {
			srv.Stop()
			time.Sleep(50 * time.Millisecond) // SQLite race protection
		}()

		// Wait briefly for initial background backup to run
		time.Sleep(100 * time.Millisecond)

		backupsDir := filepath.Join(filepath.Dir(cfg.DBPath), "backups")
		info, err := os.Stat(backupsDir)
		if err != nil {
			t.Fatalf("expected backups directory to exist when DisableBackupScheduler is false: %v", err)
		}
		if !info.IsDir() {
			t.Error("expected backups path to be a directory")
		}

		files, err := os.ReadDir(backupsDir)
		if err != nil || len(files) == 0 {
			t.Fatal("expected at least one database backup file to be created")
		}
	})
}

func TestServer_SubdomainReservations(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Setup normal user and admin user
	emailUser := "user@example.com"
	uUser := &db.User{
		ID:                 emailUser,
		Email:              emailUser,
		FirstName:          "Standard",
		Role:               "user",
		Status:             "approved",
		LanguagePreference: "en",
	}
	_ = srv.db.CreateUser(uUser)

	emailAdmin := "admin@example.com"
	uAdmin := &db.User{
		ID:                 emailAdmin,
		Email:              emailAdmin,
		FirstName:          "Admin",
		Role:               "admin",
		Status:             "approved",
		LanguagePreference: "en",
	}
	_ = srv.db.CreateUser(uAdmin)

	// Setup portal sessions (cookies) with valid expiration times
	sessionUser := "user-session-123"
	srv.portalMap.Store("admin_session_"+sessionUser, PortalSessionData{
		Email:     emailUser,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	sessionAdmin := "admin-session-123"
	srv.portalMap.Store("admin_session_"+sessionAdmin, PortalSessionData{
		Email:     emailAdmin,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	// 2. Reserve subdomain (POST /api/portal/reservations)
	payload := map[string]string{
		"subdomain": "my-subdomain",
		"domain":    "example.com",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/portal/reservations", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionUser})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for reservation, got %d, body: %s", rec.Code, rec.Body.String())
	}

	var reservation db.SubdomainReservation
	_ = json.NewDecoder(rec.Body).Decode(&reservation)
	if reservation.Subdomain != "my-subdomain" || reservation.Domain != "example.com" {
		t.Errorf("unexpected reservation values: %+v", reservation)
	}

	// 3. List reservations (GET /api/portal/reservations)
	reqList := httptest.NewRequest("GET", "/api/portal/reservations", nil)
	reqList.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionUser})
	recList := httptest.NewRecorder()
	srv.ServeHTTP(recList, reqList)

	if recList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK listing reservations, got %d", recList.Code)
	}
	var listResp map[string]interface{}
	_ = json.NewDecoder(recList.Body).Decode(&listResp)
	if listResp["used"].(float64) != 1 {
		t.Errorf("expected used count 1, got %v", listResp["used"])
	}

	// 4. Request extension (POST /api/portal/reservations/:id/request-extension)
	reqExt := httptest.NewRequest("POST", fmt.Sprintf("/api/portal/reservations/%d/request-extension", reservation.ID), nil)
	reqExt.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionUser})
	recExt := httptest.NewRecorder()
	srv.ServeHTTP(recExt, reqExt)

	if recExt.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for request-extension, got %d", recExt.Code)
	}

	// Verify extension_requested flag is set in DB
	resDb, _ := srv.db.GetSubdomainReservation(reservation.ID)
	if !resDb.ExtensionRequested {
		t.Error("expected ExtensionRequested to be true")
	}

	// 5. Admin List extensions (GET /api/admin/reservations/extensions)
	reqAdminExt := httptest.NewRequest("GET", "/api/admin/reservations/extensions", nil)
	reqAdminExt.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionAdmin})
	recAdminExt := httptest.NewRecorder()
	srv.ServeHTTP(recAdminExt, reqAdminExt)

	if recAdminExt.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", recAdminExt.Code)
	}
	var extensionsList []db.SubdomainReservation
	_ = json.NewDecoder(recAdminExt.Body).Decode(&extensionsList)
	if len(extensionsList) != 1 {
		t.Errorf("expected 1 extension request, got %d", len(extensionsList))
	}

	// 6. Admin Approve extension (POST /api/admin/reservations/:id/approve-extension)
	approvePayload := map[string]interface{}{
		"days":      30,
		"permanent": false,
	}
	approveBody, _ := json.Marshal(approvePayload)
	reqApprove := httptest.NewRequest("POST", fmt.Sprintf("/api/admin/reservations/%d/approve-extension", reservation.ID), bytes.NewReader(approveBody))
	reqApprove.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionAdmin})
	recApprove := httptest.NewRecorder()
	srv.ServeHTTP(recApprove, reqApprove)

	if recApprove.Code != http.StatusOK {
		t.Fatalf("expected 200 OK approving extension, got %d", recApprove.Code)
	}

	resDb, _ = srv.db.GetSubdomainReservation(reservation.ID)
	if resDb.ExtensionRequested {
		t.Error("expected ExtensionRequested to be reset to false")
	}
	if resDb.ExpiresAt == nil {
		t.Error("expected ExpiresAt to not be nil for standard extension")
	}

	// 7. Admin Demote reservation (POST /api/admin/reservations/:id/demote)
	reqDemote := httptest.NewRequest("POST", fmt.Sprintf("/api/admin/reservations/%d/demote", reservation.ID), nil)
	reqDemote.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionAdmin})
	recDemote := httptest.NewRecorder()
	srv.ServeHTTP(recDemote, reqDemote)

	if recDemote.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for demotion, got %d", recDemote.Code)
	}

	// 8. Test connection check-gates
	// Create PAT for normal user
	patToken := "pat-token-user-1"
	hashBytes := sha256.Sum256([]byte(patToken))
	pat := &db.PersonalAccessToken{
		UserID:      emailUser,
		TokenHash:   hex.EncodeToString(hashBytes[:]),
		TokenPrefix: "lfr_pat_user",
		Name:        "User PAT",
	}
	_ = srv.db.CreatePAT(pat)

	// Connecting using reserved subdomain prefix -> Should succeed
	regPayload1, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "my-subdomain",
		Ports:           []PortMapping{{LocalPort: 8080}},
		AuthToken:       patToken,
	})
	reqReg1 := httptest.NewRequest("POST", "/api/register", bytes.NewReader(regPayload1))
	reqReg1.Host = "example.com"
	recReg1 := httptest.NewRecorder()
	srv.ServeHTTP(recReg1, reqReg1)
	if recReg1.Code != http.StatusOK {
		t.Errorf("expected connection success for reserved subdomain, got %d, body: %s", recReg1.Code, recReg1.Body.String())
	}

	// Connecting using non-reserved custom subdomain prefix -> Should fail (403 Forbidden)
	regPayload2, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "another-subdomain",
		Ports:           []PortMapping{{LocalPort: 8080}},
		AuthToken:       patToken,
	})
	reqReg2 := httptest.NewRequest("POST", "/api/register", bytes.NewReader(regPayload2))
	reqReg2.Host = "example.com"
	recReg2 := httptest.NewRecorder()
	srv.ServeHTTP(recReg2, reqReg2)
	if recReg2.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for non-reserved subdomain, got %d", recReg2.Code)
	}

	// Connecting using random subdomain prefix -> Should succeed and generate prefix
	regPayload3, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "random",
		Ports:           []PortMapping{{LocalPort: 8080}},
		AuthToken:       patToken,
	})
	reqReg3 := httptest.NewRequest("POST", "/api/register", bytes.NewReader(regPayload3))
	reqReg3.Host = "example.com"
	recReg3 := httptest.NewRecorder()
	srv.ServeHTTP(recReg3, reqReg3)
	if recReg3.Code != http.StatusOK {
		t.Errorf("expected 200 OK for random subdomain connection, got %d", recReg3.Code)
	}
	var regResp3 RegisterResponse
	_ = json.NewDecoder(recReg3.Body).Decode(&regResp3)
	if regResp3.SubdomainPrefix == "" || regResp3.SubdomainPrefix == "random" {
		t.Errorf("expected generated unique subdomain, got %s", regResp3.SubdomainPrefix)
	}

	// 9. Restrict 'Never' expiration token option for standard user
	tokenPayload := map[string]interface{}{
		"name":            "Standard Never Token",
		"expires_in_days": 0, // Never
	}
	tokenBody, _ := json.Marshal(tokenPayload)
	reqTok := httptest.NewRequest("POST", "/api/tokens", bytes.NewReader(tokenBody))
	reqTok.AddCookie(&http.Cookie{Name: "lfr_session", Value: sessionUser})
	recTok := httptest.NewRecorder()
	srv.ServeHTTP(recTok, reqTok)
	if recTok.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for standard user creating Never token, got %d", recTok.Code)
	}

	// 10. Test HTTP 410 Gone for quarantined subdomain
	expiredTime := time.Now().Add(-1 * time.Hour)
	quarantineRes := &db.SubdomainReservation{
		UserID:    emailUser,
		Subdomain: "quarantine-sub",
		Domain:    "example.com",
		ExpiresAt: &expiredTime,
		CreatedAt: time.Now().Add(-8 * time.Hour),
		UpdatedAt: time.Now().Add(-1 * time.Hour),
	}
	_ = srv.db.CreateSubdomainReservation(quarantineRes)

	reqGone := httptest.NewRequest("GET", "http://quarantine-sub.example.com/some/path", nil)
	recGone := httptest.NewRecorder()
	srv.ServeHTTP(recGone, reqGone)

	if recGone.Code != http.StatusGone {
		t.Errorf("expected 410 Gone for quarantined subdomain host, got %d, body: %s", recGone.Code, recGone.Body.String())
	}
	if !strings.Contains(recGone.Body.String(), "Subdomain Discontinued") {
		t.Error("expected gone.html to render, but title missing")
	}
}

func TestServer_RoleSubdomainLimitsAndAutoReservation(t *testing.T) {
	infVal := -1
	adminLimit := 2
	cfg := &config.ServerConfig{
		Domains:                    []string{"example.com"},
		DisableBackupScheduler:     true,
		AllowClientAutoReservation: true,
		OwnerMaxReservations:       &infVal,
		AdminMaxReservations:       &adminLimit,
	}

	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(t.TempDir(), "test.db")
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer func() {
		time.Sleep(50 * time.Millisecond)
		srv.Stop()
	}()

	// Create an owner user and an admin user
	ownerEmail := "owner@example.com"
	adminEmail := "admin@example.com"
	_ = srv.db.CreateUser(&db.User{ID: ownerEmail, Email: ownerEmail, Role: "owner", Status: "approved"})
	_ = srv.db.CreateUser(&db.User{ID: adminEmail, Email: adminEmail, Role: "admin", Status: "approved"})

	patOwner := sha256.Sum256([]byte("pat_owner"))
	_ = srv.db.CreatePAT(&db.PersonalAccessToken{UserID: ownerEmail, TokenHash: hex.EncodeToString(patOwner[:]), TokenPrefix: "pat_owner"})
	patAdmin := sha256.Sum256([]byte("pat_admin"))
	_ = srv.db.CreatePAT(&db.PersonalAccessToken{UserID: adminEmail, TokenHash: hex.EncodeToString(patAdmin[:]), TokenPrefix: "pat_admin"})

	// 1. Verify owner has infinity limit (-1) resolved
	ownerRec, _ := srv.db.GetUser(ownerEmail)
	ownerLimit := srv.getUserMaxReservations(ownerRec)
	if ownerLimit != -1 {
		t.Errorf("expected owner limit to be -1 (infinity), got %d", ownerLimit)
	}

	// 2. Verify admin has limit of 2 resolved
	adminRec, _ := srv.db.GetUser(adminEmail)
	admLimit := srv.getUserMaxReservations(adminRec)
	if admLimit != 2 {
		t.Errorf("expected admin limit to be 2, got %d", admLimit)
	}

	// 3. Test owner client-side auto-reservation works without reservation pre-created
	payload1, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "owner-auto-1",
		Ports:           []PortMapping{{LocalPort: 8080}},
		AuthToken:       "pat_owner",
	})
	req1 := httptest.NewRequest("POST", "http://tunnel.example.com/api/register", bytes.NewReader(payload1))
	req1.Host = "tunnel.example.com"
	rec1 := httptest.NewRecorder()
	srv.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Errorf("expected owner auto-reservation connection to succeed, got status %d", rec1.Code)
	}

	// Verify the reservation was actually created in the DB
	res, err := srv.db.GetSubdomainReservationByName("owner-auto-1", "example.com")
	if err != nil || res == nil {
		t.Error("expected subdomain reservation to be auto-created in the database, got nil or error")
	}

	// 4. Test admin client-side auto-reservation enforces the quota of 2
	// Connect first tunnel (takes 1st quota)
	payloadA1, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "admin-auto-1",
		Ports:           []PortMapping{{LocalPort: 8080}},
		AuthToken:       "pat_admin",
	})
	reqA1 := httptest.NewRequest("POST", "http://tunnel.example.com/api/register", bytes.NewReader(payloadA1))
	reqA1.Host = "tunnel.example.com"
	recA1 := httptest.NewRecorder()
	srv.ServeHTTP(recA1, reqA1)
	if recA1.Code != http.StatusOK {
		t.Errorf("expected admin first connection to succeed, got %d", recA1.Code)
	}

	// Connect second tunnel (takes 2nd quota)
	payloadA2, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "admin-auto-2",
		Ports:           []PortMapping{{LocalPort: 8080}},
		AuthToken:       "pat_admin",
	})
	reqA2 := httptest.NewRequest("POST", "http://tunnel.example.com/api/register", bytes.NewReader(payloadA2))
	reqA2.Host = "tunnel.example.com"
	recA2 := httptest.NewRecorder()
	srv.ServeHTTP(recA2, reqA2)
	if recA2.Code != http.StatusOK {
		t.Errorf("expected admin second connection to succeed, got %d", recA2.Code)
	}

	// Connect third tunnel (should exceed quota of 2 and fail)
	payloadA3, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "admin-auto-3",
		Ports:           []PortMapping{{LocalPort: 8080}},
		AuthToken:       "pat_admin",
	})
	reqA3 := httptest.NewRequest("POST", "http://tunnel.example.com/api/register", bytes.NewReader(payloadA3))
	reqA3.Host = "tunnel.example.com"
	recA3 := httptest.NewRecorder()
	srv.ServeHTTP(recA3, reqA3)
	if recA3.Code != http.StatusForbidden {
		t.Errorf("expected admin third connection to fail with 403 Forbidden (quota reached), got %d", recA3.Code)
	}
}
