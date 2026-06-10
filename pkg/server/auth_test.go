package server

import (
	"strings"
	"testing"

	"github.com/jpillora/chisel/server"
)

func TestRegistryRegister(t *testing.T) {
	// Initialize real Chisel server for integration test
	chiselServer, err := chserver.NewServer(&chserver.Config{
		Reverse: true,
	})
	if err != nil {
		t.Fatalf("failed to create chisel server: %v", err)
	}

	reg := NewRegistry(chiselServer)

	domains := []string{"liferay.com", "liferay-tunnel.com"}
	ports := []PortMapping{
		{LocalPort: 8080},
		{LocalPort: 3000, NameSuffix: "assets"},
	}

	// Register
	token, remotes, err := reg.Register("alpha-se", ports, domains)
	if err != nil {
		t.Fatalf("failed to register: %v", err)
	}

	if token == "" {
		t.Error("expected non-empty session token")
	}

	// 2 ports requested -> we expect 2 remotes returned
	if len(remotes) != 2 {
		t.Errorf("expected 2 remotes, got %d", len(remotes))
	}

	// Verify remotes format (e.g. "R:10001:localhost:8080")
	for _, remote := range remotes {
		if !strings.HasPrefix(remote, "R:") {
			t.Errorf("unexpected remote format: %s", remote)
		}
	}

	// Verify leases are present in registry
	p1, exists1 := reg.GetBackendPort("alpha-se.liferay.com")
	if !exists1 {
		t.Error("expected alpha-se.liferay.com to exist")
	}

	p2, exists2 := reg.GetBackendPort("alpha-se-assets.liferay-tunnel.com")
	if !exists2 {
		t.Error("expected alpha-se-assets.liferay-tunnel.com to exist")
	}

	if p1 == p2 {
		t.Errorf("expected different local ports for different targets, got %d for both", p1)
	}
}

func TestRegistryDuplicateSubdomain(t *testing.T) {
	chiselServer, _ := chserver.NewServer(&chserver.Config{Reverse: true})
	reg := NewRegistry(chiselServer)
	domains := []string{"liferay.com"}

	_, _, err := reg.Register("beta-se", []PortMapping{{LocalPort: 8080}}, domains)
	if err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	_, _, err = reg.Register("beta-se", []PortMapping{{LocalPort: 8080}}, domains)
	if err == nil {
		t.Error("expected second registration with same subdomain to fail")
	}
}

func TestRegistryCleanup(t *testing.T) {
	chiselServer, _ := chserver.NewServer(&chserver.Config{Reverse: true})
	reg := NewRegistry(chiselServer)
	domains := []string{"liferay.com"}

	token, _, err := reg.Register("gamma-se", []PortMapping{{LocalPort: 8080}}, domains)
	if err != nil {
		t.Fatalf("registration failed: %v", err)
	}

	// Verify it exists
	_, exists := reg.GetBackendPort("gamma-se.liferay.com")
	if !exists {
		t.Fatal("expected lease to exist")
	}

	// Clean up
	reg.CleanLease(token)

	// Verify it is gone
	_, exists = reg.GetBackendPort("gamma-se.liferay.com")
	if exists {
		t.Error("expected lease to be cleaned up")
	}
}

func TestRegistryValidation(t *testing.T) {
	chiselServer, _ := chserver.NewServer(&chserver.Config{Reverse: true})
	reg := NewRegistry(chiselServer)
	domains := []string{"liferay.com"}
	ports := []PortMapping{{LocalPort: 8080}}

	tests := []struct {
		subdomain  string
		shouldPass bool
	}{
		{"valid-sub", true},
		{"ok123", true},
		{"a", false},           // Too short
		{"invalid_sub", false}, // Has underscore
		{"sub-", false},        // Ends with hyphen
		{"-sub", false},        // Starts with hyphen
		{"www", false},         // Reserved
		{"admin", false},       // Reserved
		{"api", false},         // Reserved
		{"portal", false},      // Reserved
	}

	for _, tt := range tests {
		_, _, err := reg.Register(tt.subdomain, ports, domains)
		if tt.shouldPass && err != nil {
			t.Errorf("expected subdomain %s to pass, but got error: %v", tt.subdomain, err)
		}
		if !tt.shouldPass && err == nil {
			t.Errorf("expected subdomain %s to be rejected, but it succeeded", tt.subdomain)
		}
	}
}

func TestRegistryCheckSubdomain(t *testing.T) {
	chiselServer, _ := chserver.NewServer(&chserver.Config{Reverse: true})
	reg := NewRegistry(chiselServer)
	domains := []string{"liferay.com"}

	// Verify an empty prefix
	available, reason := reg.CheckSubdomain("", domains)
	if available || reason != "empty subdomain" {
		t.Errorf("expected empty subdomain check to fail, got available=%v, reason=%q", available, reason)
	}

	// Verify short prefix
	available, reason = reg.CheckSubdomain("ab", domains)
	if available || !strings.Contains(reason, "length") {
		t.Errorf("expected short subdomain check to fail, got available=%v, reason=%q", available, reason)
	}

	// Verify reserved name
	available, reason = reg.CheckSubdomain("admin", domains)
	if available || !strings.Contains(reason, "reserved") {
		t.Errorf("expected reserved subdomain check to fail, got available=%v, reason=%q", available, reason)
	}

	// Verify invalid characters
	available, reason = reg.CheckSubdomain("foo_bar", domains)
	if available || !strings.Contains(reason, "invalid characters") {
		t.Errorf("expected invalid chars check to fail, got available=%v, reason=%q", available, reason)
	}

	// Verify valid and free subdomain
	available, reason = reg.CheckSubdomain("alpha-se", domains)
	if !available || reason != "" {
		t.Errorf("expected alpha-se to be available, got available=%v, reason=%q", available, reason)
	}

	// Register the subdomain
	_, _, err := reg.Register("alpha-se", []PortMapping{{LocalPort: 8080}}, domains)
	if err != nil {
		t.Fatalf("registration failed: %v", err)
	}

	// Verify it is now taken
	available, reason = reg.CheckSubdomain("alpha-se", domains)
	if available || !strings.Contains(reason, "already taken") {
		t.Errorf("expected registered subdomain to be taken, got available=%v, reason=%q", available, reason)
	}
}

func TestRegistryGenerateSuggestions(t *testing.T) {
	chiselServer, _ := chserver.NewServer(&chserver.Config{Reverse: true})
	reg := NewRegistry(chiselServer)
	domains := []string{"liferay.com"}

	// Check suggestions for valid prefix that is taken
	_, _, _ = reg.Register("alpha-se", []PortMapping{{LocalPort: 8080}}, domains)

	suggestions := reg.GenerateSuggestions("alpha-se", domains)
	if len(suggestions) != 3 {
		t.Errorf("expected 3 suggestions, got %d: %v", len(suggestions), suggestions)
	}

	// Ensure suggestions do not contain the registered one
	for _, sugg := range suggestions {
		if sugg == "alpha-se" {
			t.Errorf("suggestion list should not contain taken subdomain: %s", sugg)
		}
		// Validate that the suggestion is actually reported as available
		available, _ := reg.CheckSubdomain(sugg, domains)
		if !available {
			t.Errorf("suggested subdomain %s should be available", sugg)
		}
	}
}
