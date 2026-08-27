package db

import (
	"testing"
)

func TestVanityDomainStatus_StartAttempt(t *testing.T) {
	database := setupTestDB(t)

	if err := database.StartVanityDomainAttempt("dev.example.com", "user-1"); err != nil {
		t.Fatalf("StartVanityDomainAttempt failed: %v", err)
	}

	status, err := database.GetVanityDomainStatus("dev.example.com")
	if err != nil {
		t.Fatalf("GetVanityDomainStatus failed: %v", err)
	}
	if status == nil {
		t.Fatal("expected a status row, got nil")
	}
	if status.UserID != "user-1" {
		t.Errorf("expected user-1, got %q", status.UserID)
	}
	if status.RequestedAt == nil {
		t.Error("expected RequestedAt to be set")
	}
	if status.NginxConfigAt != nil || status.CertIssuedAt != nil || status.LiveAt != nil {
		t.Error("expected all later stages to be nil on a fresh attempt")
	}
	if status.FailedStage != "" || status.ErrorMessage != "" {
		t.Error("expected no failure state on a fresh attempt")
	}
}

func TestVanityDomainStatus_MarkStage(t *testing.T) {
	database := setupTestDB(t)

	if err := database.StartVanityDomainAttempt("dev.example.com", "user-1"); err != nil {
		t.Fatalf("StartVanityDomainAttempt failed: %v", err)
	}
	if err := database.MarkVanityDomainStage("dev.example.com", "nginx_config"); err != nil {
		t.Fatalf("MarkVanityDomainStage(nginx_config) failed: %v", err)
	}
	if err := database.MarkVanityDomainStage("dev.example.com", "cert_issued"); err != nil {
		t.Fatalf("MarkVanityDomainStage(cert_issued) failed: %v", err)
	}

	status, err := database.GetVanityDomainStatus("dev.example.com")
	if err != nil {
		t.Fatalf("GetVanityDomainStatus failed: %v", err)
	}
	if status.NginxConfigAt == nil {
		t.Error("expected NginxConfigAt to be set")
	}
	if status.CertIssuedAt == nil {
		t.Error("expected CertIssuedAt to be set")
	}
	if status.LiveAt != nil {
		t.Error("expected LiveAt to still be nil")
	}
}

func TestVanityDomainStatus_MarkStageRejectsUnknownStage(t *testing.T) {
	database := setupTestDB(t)

	if err := database.StartVanityDomainAttempt("dev.example.com", "user-1"); err != nil {
		t.Fatalf("StartVanityDomainAttempt failed: %v", err)
	}
	if err := database.MarkVanityDomainStage("dev.example.com", "not-a-real-stage"); err == nil {
		t.Fatal("expected an error for an unknown stage, got nil")
	}
}

func TestVanityDomainStatus_MarkFailed(t *testing.T) {
	database := setupTestDB(t)

	if err := database.StartVanityDomainAttempt("dev.example.com", "user-1"); err != nil {
		t.Fatalf("StartVanityDomainAttempt failed: %v", err)
	}
	if err := database.MarkVanityDomainStage("dev.example.com", "nginx_config"); err != nil {
		t.Fatalf("MarkVanityDomainStage failed: %v", err)
	}
	if err := database.MarkVanityDomainFailed("dev.example.com", "cert_issued", "502 from acme-challenge"); err != nil {
		t.Fatalf("MarkVanityDomainFailed failed: %v", err)
	}

	status, err := database.GetVanityDomainStatus("dev.example.com")
	if err != nil {
		t.Fatalf("GetVanityDomainStatus failed: %v", err)
	}
	// The earlier successful stage must survive a later failure.
	if status.NginxConfigAt == nil {
		t.Error("expected NginxConfigAt to remain set after a later failure")
	}
	if status.FailedStage != "cert_issued" {
		t.Errorf("expected failed_stage=cert_issued, got %q", status.FailedStage)
	}
	if status.ErrorMessage != "502 from acme-challenge" {
		t.Errorf("unexpected error message: %q", status.ErrorMessage)
	}
}

func TestVanityDomainStatus_RetryResetsFailureState(t *testing.T) {
	database := setupTestDB(t)

	if err := database.StartVanityDomainAttempt("dev.example.com", "user-1"); err != nil {
		t.Fatalf("StartVanityDomainAttempt failed: %v", err)
	}
	if err := database.MarkVanityDomainStage("dev.example.com", "nginx_config"); err != nil {
		t.Fatalf("MarkVanityDomainStage failed: %v", err)
	}
	if err := database.MarkVanityDomainFailed("dev.example.com", "cert_issued", "502 from acme-challenge"); err != nil {
		t.Fatalf("MarkVanityDomainFailed failed: %v", err)
	}

	// A fresh attempt (e.g. the client reconnecting) should wipe the previous failure and
	// stage state clean, not accumulate it.
	if err := database.StartVanityDomainAttempt("dev.example.com", "user-1"); err != nil {
		t.Fatalf("second StartVanityDomainAttempt failed: %v", err)
	}

	status, err := database.GetVanityDomainStatus("dev.example.com")
	if err != nil {
		t.Fatalf("GetVanityDomainStatus failed: %v", err)
	}
	if status.NginxConfigAt != nil {
		t.Error("expected NginxConfigAt to be reset by the new attempt")
	}
	if status.FailedStage != "" || status.ErrorMessage != "" {
		t.Error("expected failure state to be reset by the new attempt")
	}
	if status.RequestedAt == nil {
		t.Error("expected RequestedAt to be set on the new attempt")
	}
}

func TestVanityDomainStatus_GetMissingReturnsNilNoError(t *testing.T) {
	database := setupTestDB(t)

	status, err := database.GetVanityDomainStatus("never-requested.example.com")
	if err != nil {
		t.Fatalf("expected no error for a missing domain, got: %v", err)
	}
	if status != nil {
		t.Error("expected nil status for a missing domain")
	}
}

func TestVanityDomainStatus_ListForUserAndAll(t *testing.T) {
	database := setupTestDB(t)

	if err := database.StartVanityDomainAttempt("dev-a.example.com", "user-1"); err != nil {
		t.Fatalf("StartVanityDomainAttempt failed: %v", err)
	}
	if err := database.StartVanityDomainAttempt("dev-b.example.com", "user-1"); err != nil {
		t.Fatalf("StartVanityDomainAttempt failed: %v", err)
	}
	if err := database.StartVanityDomainAttempt("dev-c.example.com", "user-2"); err != nil {
		t.Fatalf("StartVanityDomainAttempt failed: %v", err)
	}

	user1Domains, err := database.ListVanityDomainStatusForUser("user-1")
	if err != nil {
		t.Fatalf("ListVanityDomainStatusForUser failed: %v", err)
	}
	if len(user1Domains) != 2 {
		t.Errorf("expected 2 domains for user-1, got %d", len(user1Domains))
	}

	allDomains, err := database.ListAllVanityDomainStatus()
	if err != nil {
		t.Fatalf("ListAllVanityDomainStatus failed: %v", err)
	}
	if len(allDomains) != 3 {
		t.Errorf("expected 3 domains total, got %d", len(allDomains))
	}
}
