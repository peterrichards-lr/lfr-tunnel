package server

import (
	"testing"

	chserver "github.com/jpillora/chisel/server"
	"lfr-tunnel/pkg/config"
)

// TestEdgeNodeIDFromToken pins the derivation now that two call sites depend on it. The
// control channel and the lease registry must not disagree about who this gateway is.
func TestEdgeNodeIDFromToken(t *testing.T) {
	cases := []struct {
		token string
		want  string
	}{
		{"apacedge-mysecrettokenvalue", "apacedge"},
		{"edge-in-somesecret", "edge-in"},
		{"edge-us-east-2-secret", "edge-us-east-2"},
		{"singleword", "singleword"},
		{"", "edge"},
	}

	for _, tc := range cases {
		if got := edgeNodeIDFromToken(tc.token); got != tc.want {
			t.Errorf("edgeNodeIDFromToken(%q) = %q, want %q", tc.token, got, tc.want)
		}
	}
}

// TestLeaseCarriesTheServingNodeID is the regression test for #1167. Every lease used to
// be stamped "control" regardless of which gateway created it, so the portal reported all
// tunnels on the control plane even when the client's own TUI showed a regional edge.
func TestLeaseCarriesTheServingNodeID(t *testing.T) {
	newRegistry := func(t *testing.T) *Registry {
		t.Helper()
		chiselServer, err := chserver.NewServer(&chserver.Config{Reverse: true})
		if err != nil {
			t.Fatalf("failed to create chisel server: %v", err)
		}
		return NewRegistry(chiselServer)
	}

	t.Run("central keeps the control default", func(t *testing.T) {
		reg := newRegistry(t)
		if _, _, err := reg.Register("u@example.com", "alpha",
			[]PortMapping{{LocalPort: 8080}}, []string{"example.se"}, 0, "127.0.0.1", "", nil); err != nil {
			t.Fatalf("registration failed: %v", err)
		}
		for _, l := range reg.ListLeases() {
			if l.NodeID != "control" {
				t.Errorf("expected control, got %q", l.NodeID)
			}
		}
	})

	t.Run("an edge stamps itself", func(t *testing.T) {
		reg := newRegistry(t)
		reg.SetNodeID("edge-in")
		if _, _, err := reg.Register("u@example.com", "beta",
			[]PortMapping{{LocalPort: 8080}}, []string{"example.se"}, 0, "127.0.0.1", "", nil); err != nil {
			t.Fatalf("registration failed: %v", err)
		}
		leases := reg.ListLeases()
		if len(leases) == 0 {
			t.Fatal("expected a lease")
		}
		for _, l := range leases {
			if l.NodeID != "edge-in" {
				t.Errorf("expected the serving edge to be recorded, got %q -- this is what makes the portal's Node column wrong", l.NodeID)
			}
		}
	})
}

// TestServerSetsRegistryNodeIDInEdgeMode covers the wiring, which is the part that was
// missing rather than the stamping itself.
func TestServerSetsRegistryNodeIDInEdgeMode(t *testing.T) {
	edgeCfg := config.DefaultServerConfig()
	edgeCfg.DBPath = ""
	edgeCfg.ControlPlaneURL = "https://tunnel.example"
	edgeCfg.EdgeToken = "edge-in-secretvalue"
	edgeCfg.DisableBackupScheduler = true

	srv, err := NewServer(edgeCfg)
	if err != nil {
		t.Fatalf("failed to create edge server: %v", err)
	}
	defer srv.Stop()

	if got := srv.registry.localNodeID(); got != "edge-in" {
		t.Errorf("an edge's registry should identify as edge-in, got %q", got)
	}
}
