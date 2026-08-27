package ops

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func specForEdges(t *testing.T) DNSSpec {
	t.Helper()
	spec, err := parseDNSSpec([]byte(testSpec))
	if err != nil {
		t.Fatalf("fixture spec did not parse: %v", err)
	}
	return spec
}

// TestRenderEdgeNodesBlock_DerivesURLs is the whole point: the operator supplies an id and a
// token, and the address comes from the committed spec rather than from typing.
func TestRenderEdgeNodesBlock_DerivesURLs(t *testing.T) {
	entries, err := ParseEdgeNodesFile([]byte("edge-us,plaintext-token-us\nedge-apac,plaintext-token-apac\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	block, err := RenderEdgeNodesBlock(entries, specForEdges(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		`id: "edge-us"`,
		`url: "https://us.example.com"`,
		`id: "edge-apac"`,
		`url: "https://apac.example.com"`,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("expected %q in:\n%s", want, block)
		}
	}
}

// TestRenderEdgeNodesBlock_RejectsTheRealBug is #1449 in miniature. The operator types a
// plausible hostname that has no record of its own; it would resolve through the wildcard, to
// central. Deriving instead of trusting is what stops it, and a mismatch must be loud.
func TestRenderEdgeNodesBlock_RejectsTheRealBug(t *testing.T) {
	entries, err := ParseEdgeNodesFile([]byte("edge-us,token,https://aws-edge-us.example.com\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = RenderEdgeNodesBlock(entries, specForEdges(t))
	if err == nil {
		t.Fatal("a hand-typed url that disagrees with the DNS spec must be rejected")
	}
	if !strings.Contains(err.Error(), "authoritative") {
		t.Errorf("the error should say which source wins, got: %v", err)
	}
}

// TestDeriveEdgeURL_RefusesControlPlaneAndUnknown — the two ways an address can be wrong.
func TestDeriveEdgeURL_RefusesControlPlaneAndUnknown(t *testing.T) {
	spec := specForEdges(t)

	// A label the spec declares as the control plane is not an edge, whatever the node is called.
	if _, err := DeriveEdgeURL("edge-tunnel", spec); err == nil {
		t.Error("a label pointing at the control plane must be refused")
	} else if !strings.Contains(err.Error(), "control plane") {
		t.Errorf("expected the reason to name the control plane, got: %v", err)
	}

	// A label with no record at all: the important part of the message is WHY it is not safe to
	// guess, because guessing is exactly what produced #1449.
	_, err := DeriveEdgeURL("edge-nowhere", spec)
	if err == nil {
		t.Fatal("an unknown label must be refused rather than guessed")
	}
	if !strings.Contains(err.Error(), "wildcard") {
		t.Errorf("expected the message to explain the wildcard trap, got: %v", err)
	}
}

// TestNormaliseNodeID mirrors the prefix stripping pkg/server already does when advertising
// regions, so `edge-us` and the `us` record are understood as the same place.
func TestNormaliseNodeID(t *testing.T) {
	for in, want := range map[string]string{
		"edge-us":     "us",
		"aws-edge-us": "us",
		"EDGE-APAC":   "apac",
		"us":          "us",
		"  edge-in  ": "in",
	} {
		if got := normaliseNodeID(in); got != want {
			t.Errorf("normaliseNodeID(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestParseEdgeNodesFile_HashesAndNeverKeepsPlaintext is the secrecy requirement. The parsed
// entry must carry only the hash, so no later code path can print the plaintext even by mistake.
func TestParseEdgeNodesFile_HashesAndNeverKeepsPlaintext(t *testing.T) {
	const plaintext = "edge-us-super-secret-token-value"
	entries, err := ParseEdgeNodesFile([]byte("edge-us," + plaintext + "\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}

	sum := sha256.Sum256([]byte(plaintext))
	if entries[0].TokenHash != hex.EncodeToString(sum[:]) {
		t.Error("token was not hashed with SHA-256 as edge_nodes.txt.example promises")
	}

	// The rendered block is what reaches a terminal or a file, so assert on that too.
	block, err := RenderEdgeNodesBlock(entries, specForEdges(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(block, plaintext) {
		t.Error("the plaintext token reached the rendered output")
	}
}

// TestParseEdgeNodesFile_AcceptsAnExistingHash is what makes today's deployment reproducible.
// Central stores only hashes, and the plaintext for hand-registered nodes may exist nowhere --
// so demanding plaintext would mean the running config could never be rendered from this repo,
// which is most of what #1452 asks for.
func TestParseEdgeNodesFile_AcceptsAnExistingHash(t *testing.T) {
	const existing = "deadbeefcafebabe1111222233334444555566667777888899990000aaaabbbb"
	entries, err := ParseEdgeNodesFile([]byte("edge-us,sha256:" + existing + "\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entries[0].TokenHash != existing {
		t.Errorf("an existing hash must pass through unchanged, got %q", entries[0].TokenHash)
	}

	// A malformed hash must fail loudly. Silently hashing "sha256:oops" as though it were a
	// plaintext token would produce a config that looks fine and rejects the edge forever.
	if _, err := ParseEdgeNodesFile([]byte("edge-us,sha256:tooshort\n")); err == nil {
		t.Error("a malformed sha256: value must be an error, not treated as plaintext")
	}
}

// TestParseEdgeNodesFile_FormatHandling covers what edge_nodes.txt.example documents: comments,
// blanks, and both separators -- including that a colon-separated line does not split the url's
// own "://".
func TestParseEdgeNodesFile_FormatHandling(t *testing.T) {
	in := `
# a comment
edge-us,token-us,https://us.example.com

edge-apac:token-apac:https://apac.example.com
`
	entries, err := ParseEdgeNodesFile([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(entries), entries)
	}
	if entries[1].DeclaredURL != "https://apac.example.com" {
		t.Errorf("colon-separated url was mangled: %q", entries[1].DeclaredURL)
	}

	// A file with nothing usable must be an error, not an empty block that silently deletes
	// every edge from central's config.
	if _, err := ParseEdgeNodesFile([]byte("# only comments\n\n")); err == nil {
		t.Error("a file with no entries must be an error, not an empty registry")
	}
	if _, err := ParseEdgeNodesFile([]byte("edge-us\n")); err == nil {
		t.Error("a line with no token must be an error")
	}
}
