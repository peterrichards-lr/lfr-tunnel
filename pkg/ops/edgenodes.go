package ops

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
)

// Generating central's edge_nodes registry instead of hand-editing it (#1452).
//
// Registering an edge is a manual step today: edge_setup_guide.md tells the operator to SSH to
// the control plane and add an entry with `id`, `token_hash` and `url` by hand. The `url` is the
// part that rots -- three of four still named retired aws-edge-* hosts, which resolve through the
// zone wildcard to central itself, so central advertised its own address as three regions for
// weeks (#1449).
//
// The url should never have been typed. scripts/liferay/dns/lfr-demo-production.yaml is already
// the authoritative record of which edges exist and where (#941), so it is derived from there
// and a url supplied by hand is only ever CHECKED against it, never trusted over it.
//
// edge_nodes.txt.example already promises this: "Pre-shared plaintext tokens are automatically
// hashed using SHA-256 locally before configuration upload. Plaintext secrets are never
// uploaded." Nothing implemented it. This does.
//
// SECRECY: a plaintext token is read and hashed, and is never printed, logged, or written
// anywhere. The rendered block contains token_hash values, which are credential artefacts -- the
// output goes to stdout for the operator to place on the box, and must not be committed.

// edgeNodeEntry is one line of the operator's gitignored edge_nodes.txt.
type edgeNodeEntry struct {
	ID string
	// TokenHash is the SHA-256 hex of the plaintext token. Held as the hash from the moment the
	// line is parsed, so no later code path can print the plaintext by accident.
	TokenHash string
	// DeclaredURL is a url the operator wrote in the file, if any. Checked against the DNS spec,
	// never preferred over it.
	DeclaredURL string
}

// hashPrefix lets an entry supply an already-computed hash instead of a plaintext token.
//
// This is what makes an existing deployment reproducible. Central stores only hashes, and the
// plaintext tokens for nodes registered by hand may exist nowhere the operator can reach -- so a
// tool that demanded plaintext could not render the config that is running right now, which is
// most of what #1452 is asking for.
const hashPrefix = "sha256:"

// ParseEdgeNodesFile reads the `id,token[,url]` format documented in edge_nodes.txt.example.
//
// Comma or colon separated, because the example documents both. A colon-separated line is
// ambiguous when the url contains "://", so commas are tried first and a colon split only
// happens when there is no comma at all.
func ParseEdgeNodesFile(data []byte) ([]edgeNodeEntry, error) {
	var entries []edgeNodeEntry
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		var fields []string
		if strings.Contains(line, ",") {
			fields = strings.Split(line, ",")
		} else {
			// No comma, so a colon is the separator -- but the url's own "://" must not be
			// split. Limit to three parts and rejoin the remainder.
			fields = strings.SplitN(line, ":", 3)
		}
		for j := range fields {
			fields[j] = strings.TrimSpace(fields[j])
		}

		if len(fields) < 2 || fields[0] == "" || fields[1] == "" {
			return nil, fmt.Errorf("line %d: expected id,token[,url] -- got %d usable field(s)", i+1, len(fields))
		}

		entry := edgeNodeEntry{ID: fields[0]}
		secret := fields[1]
		if strings.HasPrefix(secret, hashPrefix) {
			h := strings.TrimPrefix(secret, hashPrefix)
			if len(h) != 64 {
				return nil, fmt.Errorf("line %d: %s expects 64 hex characters, got %d", i+1, hashPrefix, len(h))
			}
			if _, err := hex.DecodeString(h); err != nil {
				return nil, fmt.Errorf("line %d: %s value is not hex", i+1, hashPrefix)
			}
			entry.TokenHash = strings.ToLower(h)
		} else {
			sum := sha256.Sum256([]byte(secret))
			entry.TokenHash = hex.EncodeToString(sum[:])
		}
		if len(fields) >= 3 && fields[2] != "" {
			entry.DeclaredURL = fields[2]
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no edge nodes found; every line was blank or a comment")
	}
	return entries, nil
}

// normaliseNodeID reduces a node id to the DNS label its record uses.
//
// Mirrors the region-alias logic in pkg/server/server.go, which already strips `aws-` and `edge-`
// when advertising regions to clients -- so `edge-us` and the `us` A record are already
// understood as the same place by the running system.
func normaliseNodeID(id string) string {
	out := strings.ToLower(strings.TrimSpace(id))
	out = strings.TrimPrefix(out, "aws-")
	out = strings.TrimPrefix(out, "edge-")
	return out
}

// DeriveEdgeURL finds the url for a node from the committed DNS spec.
//
// Errors rather than guessing when the label is absent or declared in more than one zone. A
// guess here is what #1449 was: a plausible hostname that resolved to the wrong machine.
func DeriveEdgeURL(id string, spec DNSSpec) (string, error) {
	label := normaliseNodeID(id)
	if label == "" {
		return "", fmt.Errorf("node id %q normalises to nothing", id)
	}

	var matches []string
	for _, d := range spec.Domains {
		for _, r := range d.Records {
			if r.Type != "A" || strings.ToLower(r.Name) != label {
				continue
			}
			// A record pointing at the control plane is not an edge, whatever it is called.
			if r.Value == centralPlaceholder {
				return "", fmt.Errorf(
					"%q resolves to the control plane in the DNS spec, not to an edge -- "+
						"routing an edge there is #1449", label+"."+d.Zone)
			}
			matches = append(matches, "https://"+label+"."+d.Zone)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf(
			"no A record named %q in the DNS spec, so there is no address to derive for node %q. "+
				"Add the record (and apply it) before registering the node -- a name with no record "+
				"of its own still resolves through the zone wildcard, to the control plane", label, id)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf(
			"%q is declared in more than one zone (%s), so the address is ambiguous; "+
				"supply the url explicitly in the third field", label, strings.Join(matches, ", "))
	}
}

// RenderEdgeNodesBlock builds the `edge_nodes:` YAML for central's config.
//
// A url written in the file is validated against the spec, never preferred over it: the whole
// point is that a hand-typed address stops being authoritative.
func RenderEdgeNodesBlock(entries []edgeNodeEntry, spec DNSSpec) (string, error) {
	var b strings.Builder
	b.WriteString("edge_nodes:\n")
	for _, e := range entries {
		derived, err := DeriveEdgeURL(e.ID, spec)
		if err != nil {
			return "", fmt.Errorf("node %q: %w", e.ID, err)
		}
		if e.DeclaredURL != "" && !sameHost(e.DeclaredURL, derived) {
			return "", fmt.Errorf(
				"node %q declares url %q but the DNS spec says %q. The spec is authoritative "+
					"(#941); fix the file, or fix the spec and apply it",
				e.ID, e.DeclaredURL, derived)
		}
		fmt.Fprintf(&b, "  - id: %q\n    token_hash: %q\n    url: %q\n", e.ID, e.TokenHash, derived)
	}
	return b.String(), nil
}

// sameHost compares two urls by hostname only, so a trailing slash or an explicit :443 does not
// read as a mismatch.
func sameHost(a, b string) bool {
	ha, hb := urlHost(a), urlHost(b)
	return ha != "" && ha == hb
}

// RenderEdgeNodesCommand prints the edge_nodes block for central's config.
func RenderEdgeNodesCommand(args []string) {
	fs := flag.NewFlagSet("render-edge-nodes", flag.ExitOnError)
	nodesFile := fs.String("nodes", "edge_nodes.txt",
		"gitignored file of id,token[,url] lines -- see edge_nodes.txt.example")
	specPath := fs.String("dns-spec", "scripts/liferay/dns/lfr-demo-production.yaml",
		"committed DNS spec the urls are derived from")
	fs.Usage = func() {
		fmt.Println("Usage: lfr-tunnel-ops render-edge-nodes [-nodes edge_nodes.txt] [-dns-spec path]")
		fmt.Println("\nPrints the edge_nodes: block for central's server-config.yaml, with each url")
		fmt.Println("DERIVED from the committed DNS spec rather than typed by hand -- which is how")
		fmt.Println("three of four came to name retired hosts that resolved to central itself (#1449).")
		fmt.Println("\nTokens are hashed locally with SHA-256 and the plaintext is never printed,")
		fmt.Println("logged or written anywhere, which is what edge_nodes.txt.example has always")
		fmt.Println("promised. A token may instead be given as sha256:<64 hex> to supply an existing")
		fmt.Println("hash, so a deployment whose plaintext tokens are long gone can still be")
		fmt.Println("reproduced from this repo plus that file.")
		fmt.Println("\nThe OUTPUT contains token hashes. It is for placing on the control plane; do")
		fmt.Println("not commit it.")
	}
	if IsHelpRequest(args) {
		fs.Usage()
		return
	}
	if err := fs.Parse(args); err != nil {
		CheckFatal(err, "Failed to parse arguments")
	}

	nodesData, err := os.ReadFile(*nodesFile)
	CheckFatal(err, "Failed to read "+*nodesFile+" (see edge_nodes.txt.example for the format)")

	specData, err := os.ReadFile(*specPath)
	CheckFatal(err, "Failed to read the DNS spec at "+*specPath)
	spec, err := parseDNSSpec(specData)
	CheckFatal(err, "Failed to parse the DNS spec")

	entries, err := ParseEdgeNodesFile(nodesData)
	CheckFatal(err, "Failed to parse "+*nodesFile)

	block, err := RenderEdgeNodesBlock(entries, spec)
	CheckFatal(err, "Failed to render edge_nodes")

	fmt.Print(block)
}
