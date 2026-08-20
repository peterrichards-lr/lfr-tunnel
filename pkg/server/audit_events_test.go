package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"lfr-tunnel/pkg/config"
	"lfr-tunnel/pkg/db"
)

func TestTunnelLifecycleAndReservationAuditEvents(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.DefaultServerConfig()
	cfg.Domains = []string{"example.com"}
	cfg.DBPath = filepath.Join(tmpDir, "test.db")
	cfg.DisableBackupScheduler = true

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer srv.Stop()

	time.Sleep(50 * time.Millisecond)

	// Create test user
	user := &db.User{
		ID:        "test-user-id",
		Email:     "audit-tester@example.com",
		Role:      "admin",
		Status:    "approved",
		CreatedAt: time.Now(),
	}
	if err := srv.db.CreateUser(user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	hashBytes := sha256.Sum256([]byte("dev-token"))
	tokenHash := hex.EncodeToString(hashBytes[:])
	pat := &db.PersonalAccessToken{
		UserID:    user.ID,
		TokenHash: tokenHash,
		Name:      "test-pat",
		CreatedAt: time.Now(),
	}
	if err := srv.db.CreatePAT(pat); err != nil {
		t.Fatalf("failed to create pat: %v", err)
	}

	// 1. Test handleRegister emits tunnel.start audit event
	regPayload, _ := json.Marshal(RegisterRequest{
		SubdomainPrefix: "my-audit-sub",
		AuthToken:       "dev-token",
		Ports:           []PortMapping{{LocalPort: 8080}},
	})
	reqReg, _ := http.NewRequest(http.MethodPost, "/api/register", bytes.NewBuffer(regPayload))
	recReg := httptest.NewRecorder()
	srv.handleRegister(recReg, reqReg)

	if recReg.Code != http.StatusOK {
		t.Fatalf("expected 200 from handleRegister, got %d: %s", recReg.Code, recReg.Body.String())
	}

	var regResp RegisterResponse
	if err := json.Unmarshal(recReg.Body.Bytes(), &regResp); err != nil {
		t.Fatalf("failed to parse register response: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Verify tunnel.start in audit entries
	entries, err := srv.db.ListAuditEntries(db.AuditFilter{Action: "tunnel.start"})
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected tunnel.start audit entry, got err=%v, count=%d", err, len(entries))
	}
	if entries[0].TargetID != "my-audit-sub" {
		t.Errorf("expected TargetID 'my-audit-sub', got %s", entries[0].TargetID)
	}

	// 2. Test handleDeregister emits tunnel.stop audit event
	deregPayload, _ := json.Marshal(map[string]string{
		"session_token": regResp.SessionToken,
	})
	reqDereg, _ := http.NewRequest(http.MethodPost, "/api/deregister", bytes.NewBuffer(deregPayload))
	recDereg := httptest.NewRecorder()
	srv.handleDeregister(recDereg, reqDereg)

	if recDereg.Code != http.StatusOK {
		t.Fatalf("expected 200 from handleDeregister, got %d", recDereg.Code)
	}

	time.Sleep(50 * time.Millisecond)

	stopEntries, err := srv.db.ListAuditEntries(db.AuditFilter{Action: "tunnel.stop"})
	if err != nil || len(stopEntries) == 0 {
		t.Fatalf("expected tunnel.stop audit entry, got err=%v, count=%d", err, len(stopEntries))
	}
	if stopEntries[0].TargetID != "my-audit-sub" {
		t.Errorf("expected TargetID 'my-audit-sub', got %s", stopEntries[0].TargetID)
	}
}
