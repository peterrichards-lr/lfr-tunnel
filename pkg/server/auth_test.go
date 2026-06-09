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
