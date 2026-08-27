package server

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lfr-tunnel/pkg/config"
)

// Revoking an edge token without restarting the control plane (#1309).
//
// An edge token is a long-lived shared secret held in plaintext on four unattended
// internet-facing hosts, and presenting one authorises writes to central's audit log among other
// things. Withdrawing one used to mean editing server-config.yaml and restarting lfr-tunneld,
// which the edge setup guide warns "briefly interrupts every currently active tunnel across
// every edge node" -- so an operator responding to a suspected leak had to choose between
// revoking the credential and keeping the fleet up.
//
// SIGHUP now re-reads edge_nodes and nothing else. These tests cover the two halves of that: the
// new list takes effect immediately, and a config that will not parse does NOT.

// setEdgeNodesForTest swaps the running edge node list the way a reload does.
//
// Tests used to assign srv.cfg.EdgeNodes directly. That stopped working when readers moved
// behind edgeNodes(): cfg holds the startup value and nothing reads it after construction, so
// the assignment would silently do nothing and the test would pass or fail for the wrong reason.
func setEdgeNodesForTest(t *testing.T, srv *Server, nodes []config.EdgeNodeConfig) {
	t.Helper()
	srv.edgeNodesMu.Lock()
	defer srv.edgeNodesMu.Unlock()
	srv.edgeNodesCurrent = nodes
}

func hashOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// writeConfig puts a minimal server config on disk with the given edge_nodes block.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server-config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the test config: %v", err)
	}
	return path
}

// TestReloadEdgeNodesRevokesImmediately is the property the issue asks for: a token removed from
// the file stops authenticating on reload, with no restart and no dropped tunnels.
func TestReloadEdgeNodesRevokesImmediately(t *testing.T) {
	srv := setupTestServerForAPI(t)

	const leaked, kept = "edge-token-leaked", "edge-token-kept"
	setEdgeNodesForTest(t, srv, []config.EdgeNodeConfig{
		{ID: "edge-us", TokenHash: hashOf(leaked), URL: "https://us.example.com"},
		{ID: "edge-in", TokenHash: hashOf(kept), URL: "https://in.example.com"},
	})

	if _, ok := srv.authorisedEdgeNode(leaked); !ok {
		t.Fatal("the token must authenticate before it is withdrawn, or the test proves nothing")
	}

	path := writeConfig(t, `
domains:
  - example.com
edge_nodes:
  - id: "edge-in"
    token_hash: "`+hashOf(kept)+`"
    url: "https://in.example.com"
`)

	if err := srv.ReloadEdgeNodes(path); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	if _, ok := srv.authorisedEdgeNode(leaked); ok {
		t.Error("a token removed from the config still authenticates -- the reload revoked nothing")
	}
	if node, ok := srv.authorisedEdgeNode(kept); !ok || node.ID != "edge-in" {
		t.Errorf("the surviving node must keep working; got ok=%v id=%q", ok, node.ID)
	}
}

// TestReloadEdgeNodesAddsANode — the other direction, which is what makes a rotation possible
// without a flag day (#1491): the incoming token starts working as soon as it is in the file.
func TestReloadEdgeNodesAddsANode(t *testing.T) {
	srv := setupTestServerForAPI(t)
	setEdgeNodesForTest(t, srv, nil)

	const token = "edge-token-new-node"
	path := writeConfig(t, `
domains:
  - example.com
edge_nodes:
  - id: "edge-sa"
    token_hash: "`+hashOf(token)+`"
    url: "https://sa.example.com"
`)

	if err := srv.ReloadEdgeNodes(path); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	node, ok := srv.authorisedEdgeNode(token)
	if !ok {
		t.Fatal("a node added by reload must authenticate without a restart")
	}
	if node.URL != "https://sa.example.com" {
		t.Errorf("the reloaded node must carry its url, got %q", node.URL)
	}
}

// TestReloadEdgeNodesKeepsTheOldListOnABadConfig is the failure mode that matters most.
//
// An operator revoking a credential under time pressure is exactly the person likely to send
// SIGHUP against a half-saved file. If a parse failure emptied the list, that keystroke would
// de-authenticate the entire fleet -- turning an incident response into an outage.
func TestReloadEdgeNodesKeepsTheOldListOnABadConfig(t *testing.T) {
	srv := setupTestServerForAPI(t)

	const token = "edge-token-still-valid"
	setEdgeNodesForTest(t, srv, []config.EdgeNodeConfig{
		{ID: "edge-us", TokenHash: hashOf(token), URL: "https://us.example.com"},
	})

	path := writeConfig(t, "domains:\n  - example.com\nedge_nodes:\n  - id: \"edge-us\"\n   token_hash: [unclosed\n")

	err := srv.ReloadEdgeNodes(path)
	if err == nil {
		t.Fatal("a config that does not parse must be reported as a failed reload")
	}
	if !strings.Contains(err.Error(), "keeping the edge nodes already in force") {
		t.Errorf("the error must say the running config was kept, got: %v", err)
	}

	if _, ok := srv.authorisedEdgeNode(token); !ok {
		t.Error("a failed reload emptied the edge node list; a half-saved file would take the " +
			"whole fleet offline")
	}
}

// TestReloadEdgeNodesMissingFile — same contract when the path is wrong entirely.
func TestReloadEdgeNodesMissingFile(t *testing.T) {
	srv := setupTestServerForAPI(t)

	const token = "edge-token-survives"
	setEdgeNodesForTest(t, srv, []config.EdgeNodeConfig{{ID: "edge-us", TokenHash: hashOf(token)}})

	if err := srv.ReloadEdgeNodes(filepath.Join(t.TempDir(), "does-not-exist.yaml")); err == nil {
		t.Error("reloading a missing file must fail rather than quietly clear the list")
	}
	if _, ok := srv.authorisedEdgeNode(token); !ok {
		t.Error("a missing config file must leave the running edge nodes alone")
	}
}

// TestDescribeEdgeNodeChangesNeverLogsAHash — the reload summary goes to the journal, which is
// read and pasted around far more freely than the config file it came from. It may say that a
// node's accepted hashes changed; it must never say what they are.
func TestDescribeEdgeNodeChangesNeverLogsAHash(t *testing.T) {
	oldHash, newHash := hashOf("old-token"), hashOf("new-token")

	before := []config.EdgeNodeConfig{
		{ID: "edge-us", TokenHash: oldHash, URL: "https://us.example.com"},
		{ID: "edge-gone", TokenHash: hashOf("retired-token")},
	}
	after := []config.EdgeNodeConfig{
		{ID: "edge-us", TokenHash: newHash, URL: "https://us.example.com"},
		{ID: "edge-new", TokenHash: hashOf("fresh-token")},
	}

	report := strings.Join(describeEdgeNodeChanges(before, after), "\n")

	for _, secret := range []string{oldHash, newHash, hashOf("retired-token"), hashOf("fresh-token")} {
		if strings.Contains(report, secret) {
			t.Errorf("a token hash reached the log:\n%s", report)
		}
	}

	for _, want := range []string{`"edge-us" now accepts a different set`, `"edge-gone" removed`, `"edge-new" added`} {
		if !strings.Contains(report, want) {
			t.Errorf("the summary must report %q so an operator can see the revocation took effect, got:\n%s", want, report)
		}
	}

	// The narrowness has to be stated where the operator is looking, or they will assume the
	// rest of the file was applied too (#1454).
	if !strings.Contains(report, "#1454") {
		t.Errorf("the summary must say what was NOT reloaded, got:\n%s", report)
	}
}

// TestDescribeEdgeNodeChangesIgnoresReordering — moving a node up the file is not a credential
// change, and reporting it as one would train operators to ignore the message that matters.
func TestDescribeEdgeNodeChangesIgnoresReordering(t *testing.T) {
	a := config.EdgeNodeConfig{ID: "edge-us", TokenHash: hashOf("t1"), AdditionalTokenHashes: []string{hashOf("t2")}}
	b := config.EdgeNodeConfig{ID: "edge-in", TokenHash: hashOf("t3")}

	report := strings.Join(describeEdgeNodeChanges([]config.EdgeNodeConfig{a, b}, []config.EdgeNodeConfig{b, a}), "\n")

	if !strings.Contains(report, "no change") {
		t.Errorf("reordering edge_nodes is not a change, got:\n%s", report)
	}
}
