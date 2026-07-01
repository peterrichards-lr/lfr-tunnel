package server

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSignClientCSR(t *testing.T) {
	// 1. Setup temp CA
	tmpDir, err := os.MkdirTemp("", "ca-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	certPath := filepath.Join(tmpDir, "ca.crt")
	keyPath := filepath.Join(tmpDir, "ca.key")

	caCert, caKey, err := LoadOrCreateCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateCA failed: %v", err)
	}

	// 2. Generate a client key and CSR
	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate client key: %v", err)
	}

	csrTemplate := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: "guest:some-temp-token",
		},
	}
	csrBytes, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, clientKey)
	if err != nil {
		t.Fatalf("failed to create CSR bytes: %v", err)
	}

	csrPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrBytes,
	})

	// 3. Sign CSR
	identity := "guest:approved-identity"
	certPEM, err := SignClientCSR(caCert, caKey, csrPEM, identity, 7)
	if err != nil {
		t.Fatalf("SignClientCSR failed: %v", err)
	}

	// 4. Verify signed cert
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("invalid PEM output")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse output cert: %v", err)
	}

	if cert.Subject.CommonName != identity {
		t.Errorf("expected CN to be %s, got %s", identity, cert.Subject.CommonName)
	}
}

func TestInvitationAPIEndpoints(t *testing.T) {
	// 1. Setup DB
	tmpFile, err := os.CreateTemp("", "invite-test-*.db")
	if err != nil {
		t.Fatalf("failed to create temp db: %v", err)
	}
	defer os.Remove(tmpFile.Name()) //nolint:errcheck

	database, err := db.Open(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close() //nolint:errcheck

	// Seed User
	userID := "dev-user"
	userEmail := "dev@liferay.com"
	_ = database.CreateUser(&db.User{
		ID:        userID,
		Email:     userEmail,
		Role:      "user",
		Status:    "active",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	// Seed Reservation
	_ = database.CreateSubdomainReservation(&db.SubdomainReservation{
		UserID:    userID,
		Subdomain: "invite-sub",
		Domain:    "liferay.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	// 2. Setup Server CA
	tmpDir, err := os.MkdirTemp("", "api-ca-test-*")
	if err != nil {
		t.Fatalf("failed to create temp CA dir: %v", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	caCert, caKey, err := LoadOrCreateCA(filepath.Join(tmpDir, "ca.crt"), filepath.Join(tmpDir, "ca.key"))
	if err != nil {
		t.Fatalf("LoadOrCreateCA failed: %v", err)
	}

	cfg := config.DefaultServerConfig()
	srv := &Server{
		db:        database,
		caCert:    caCert,
		caKey:     caKey,
		cfg:       cfg,
		startTime: time.Now(),
	}
	srv.portalService = NewPortalService(srv.db, srv.cfg, nil, &srv.portalMap, srv.caCert, srv.caKey)

	// Mock session loader
	sessionToken := "test-session-token"
	srv.portalMap.Store("admin_session_"+sessionToken, PortalSessionData{
		Email: userEmail,
	})

	// Helper to add auth cookie
	addAuthCookie := func(req *http.Request) {
		req.AddCookie(&http.Cookie{
			Name:  "lfr_session",
			Value: sessionToken,
		})
	}

	var inviteToken string

	t.Run("CreateInvitation_Success", func(t *testing.T) {
		payload := map[string]interface{}{
			"subdomain":     "invite-sub",
			"domain":        "liferay.com",
			"name":          "My Guest Colleague",
			"email":         "colleague@guest.com",
			"validity_days": 5,
		}
		body, _ := json.Marshal(payload)

		req := httptest.NewRequest("POST", "/api/portal/invitations", bytes.NewReader(body))
		addAuthCookie(req)
		rec := httptest.NewRecorder()

		srv.handleCreateInvitation(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created, got %d. Body: %s", rec.Code, rec.Body.String())
		}

		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)

		claimToken := res["invitation"].(map[string]interface{})["token"].(string)
		inviteToken = claimToken

		if inviteToken == "" {
			t.Error("expected claim token in response")
		}
	})

	t.Run("ListInvitations", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/portal/invitations", nil)
		addAuthCookie(req)
		rec := httptest.NewRecorder()

		srv.handleListInvitations(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}

		var list []interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &list)
		if len(list) != 1 {
			t.Errorf("expected 1 invitation, got %d", len(list))
		}
	})

	t.Run("ClaimInvitation_P12", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/portal/invitations/claim?token="+inviteToken, nil)
		rec := httptest.NewRecorder()

		srv.handleClaimInvitation(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
		}

		contentType := rec.Header().Get("Content-Type")
		if contentType != "application/x-pkcs12" {
			t.Errorf("expected application/x-pkcs12 header, got %s", contentType)
		}

		if len(rec.Body.Bytes()) == 0 {
			t.Error("expected certificate bundle in output")
		}
	})

	t.Run("ClaimInvitation_AlreadyClaimed", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/portal/invitations/claim?token="+inviteToken, nil)
		rec := httptest.NewRecorder()

		srv.handleClaimInvitation(rec, req)
		if rec.Code != http.StatusConflict {
			t.Errorf("expected 409 Conflict, got %d", rec.Code)
		}
	})

	t.Run("ClaimInvitation_CSR", func(t *testing.T) {
		// Seed another invitation to claim via CSR
		invite := &db.GuestInvitation{
			Token:     "csr-invite-token",
			Subdomain: "invite-sub",
			Domain:    "liferay.com",
			Name:      "My Guest CSR Colleague",
			Email:     "colleague-csr@guest.com",
			ExpiresAt: time.Now().AddDate(0, 0, 7),
			CreatedBy: userEmail,
		}
		_ = database.CreateGuestInvitation(invite)

		// Generate key and CSR
		clientKey, _ := rsa.GenerateKey(rand.Reader, 2048)
		csrTemplate := &x509.CertificateRequest{
			Subject: pkix.Name{
				CommonName: "guest:csr-invite-token",
			},
		}
		csrBytes, _ := x509.CreateCertificateRequest(rand.Reader, csrTemplate, clientKey)
		csrPEM := pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE REQUEST",
			Bytes: csrBytes,
		})

		req := httptest.NewRequest("POST", "/api/portal/csr/sign?token=csr-invite-token", bytes.NewReader(csrPEM))
		rec := httptest.NewRecorder()

		srv.handleCSRSignInvitation(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
		}

		contentType := rec.Header().Get("Content-Type")
		if contentType != "application/x-pem-file" {
			t.Errorf("expected application/x-pem-file header, got %s", contentType)
		}

		// Verify output PEM certificate
		block, _ := pem.Decode(rec.Body.Bytes())
		if block == nil || block.Type != "CERTIFICATE" {
			t.Fatalf("invalid certificate PEM returned")
		}
	})
}
