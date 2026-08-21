package server

import (
	"strings"
	"testing"

	chserver "github.com/jpillora/chisel/server"
)

// TestMultiPortHostnamesUseOneDash is the regression test for #1154. The port-derived
// suffix carried its own leading dash while the join added another, producing hosts like
// ngriffin--58081. Client-extension suffixes never had one, so the two kinds of multi-port
// tunnel spelled their hostnames differently.
func TestMultiPortHostnamesUseOneDash(t *testing.T) {
	chiselServer, err := chserver.NewServer(&chserver.Config{Reverse: true})
	if err != nil {
		t.Fatalf("failed to create chisel server: %v", err)
	}
	reg := NewRegistry(chiselServer)

	// Second and subsequent ports get a derived suffix; the first never does.
	_, _, err = reg.Register("user@example.com", "ngriffin",
		[]PortMapping{{LocalPort: 8080}, {LocalPort: 58081}},
		[]string{"lfr-demo.se"}, 0, "127.0.0.1", "", nil)
	if err != nil {
		t.Fatalf("registration failed: %v", err)
	}

	reg.RLock()
	defer reg.RUnlock()

	var hosts []string
	for host := range reg.leases {
		hosts = append(hosts, host)
	}

	for _, host := range hosts {
		if strings.Contains(host, "--") {
			t.Errorf("host %q contains a double dash", host)
		}
	}

	if _, ok := reg.leases["ngriffin.lfr-demo.se"]; !ok {
		t.Errorf("expected the primary host unchanged, got %v", hosts)
	}
	if _, ok := reg.leases["ngriffin-58081.lfr-demo.se"]; !ok {
		t.Errorf("expected ngriffin-58081.lfr-demo.se, got %v", hosts)
	}
}

// TestExplicitSuffixWithLeadingDashIsPreserved covers the mixed-version case. A client
// from before this fix sends "-58081" and prints ngriffin--58081 for itself. Normalising
// that away server-side would serve a host the client never advertised, so the value is
// deliberately used as given.
func TestExplicitSuffixWithLeadingDashIsPreserved(t *testing.T) {
	chiselServer, err := chserver.NewServer(&chserver.Config{Reverse: true})
	if err != nil {
		t.Fatalf("failed to create chisel server: %v", err)
	}
	reg := NewRegistry(chiselServer)

	_, _, err = reg.Register("old@example.com", "legacy",
		[]PortMapping{{LocalPort: 8080}, {LocalPort: 58081, NameSuffix: "-58081"}},
		[]string{"lfr-demo.se"}, 0, "127.0.0.1", "", nil)
	if err != nil {
		t.Fatalf("registration failed: %v", err)
	}

	reg.RLock()
	defer reg.RUnlock()

	if _, ok := reg.leases["legacy--58081.lfr-demo.se"]; !ok {
		var hosts []string
		for host := range reg.leases {
			hosts = append(hosts, host)
		}
		t.Errorf("an older client's host must be served exactly as that client advertises it, got %v", hosts)
	}
}

// TestNamedSuffixUnaffected confirms client-extension suffixes, which never carried a
// dash, still produce a single separator.
func TestNamedSuffixUnaffected(t *testing.T) {
	chiselServer, err := chserver.NewServer(&chserver.Config{Reverse: true})
	if err != nil {
		t.Fatalf("failed to create chisel server: %v", err)
	}
	reg := NewRegistry(chiselServer)

	_, _, err = reg.Register("user@example.com", "alpha",
		[]PortMapping{{LocalPort: 8080}, {LocalPort: 3000, NameSuffix: "assets"}},
		[]string{"lfr-demo.se"}, 0, "127.0.0.1", "", nil)
	if err != nil {
		t.Fatalf("registration failed: %v", err)
	}

	reg.RLock()
	defer reg.RUnlock()

	if _, ok := reg.leases["alpha-assets.lfr-demo.se"]; !ok {
		t.Error("expected alpha-assets.lfr-demo.se to be unchanged by this fix")
	}
}
