package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The DNS spec fixture mirrors the real one's shape: literal per-edge addresses, and ${IPV4} for
// records pointing at the control plane. Deliberately a fixture and not the committed file --
// production currently has no drift (#1449 is fixed), so testing against it would assert nothing
// and would start failing the day an edge is legitimately added.
const testSpec = `
domains:
  - zone: example.com
    records:
      - {name: "@", type: A, value: "${IPV4}"}
      - {name: "*", type: A, value: "${IPV4}"}
      - {name: tunnel, type: A, value: "${IPV4}"}
      - {name: us, type: A, value: "203.0.113.10"}
      - {name: apac, type: A, value: "203.0.113.11"}
  - zone: example.net
    records:
      - {name: "*", type: A, value: "${IPV4}"}
`

// TestCheckEdgeNodeDrift_CleanConfig is the baseline: a correct config must be silent, or the
// check is noise and gets ignored.
func TestCheckEdgeNodeDrift_CleanConfig(t *testing.T) {
	cfg := `
edge_nodes:
  - id: "edge-us"
    url: "https://us.example.com"
  - id: "edge-apac"
    url: "https://apac.example.com"
`
	findings, err := CheckEdgeNodeDrift([]byte(cfg), []byte(testSpec))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for a correct config, got %+v", findings)
	}
}

// TestCheckEdgeNodeDrift_CatchesTheRealBug reproduces #1449 exactly: a url naming a host with no
// record of its own. It resolves -- through the zone wildcard, to the control plane -- so any
// check that merely asked "does this resolve?" would have passed it, which is why it survived
// weeks in production.
func TestCheckEdgeNodeDrift_CatchesTheRealBug(t *testing.T) {
	cfg := `
edge_nodes:
  - id: "edge-us"
    url: "https://aws-edge-us.example.com"
  - id: "edge-apac"
    url: "https://apac.example.com"
`
	findings, err := CheckEdgeNodeDrift([]byte(cfg), []byte(testSpec))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected exactly one finding, got %+v", findings)
	}
	if findings[0].Severity != "error" {
		t.Errorf("a url that routes an edge to central is an error, got %q", findings[0].Severity)
	}
	if !strings.Contains(findings[0].Key, "edge-us") {
		t.Errorf("finding should name the offending node, got %q", findings[0].Key)
	}
	if !strings.Contains(findings[0].Message, "wildcard") {
		t.Errorf("the message should explain WHY it resolves anyway, got %q", findings[0].Message)
	}
}

// TestCheckEdgeNodeDrift_CatchesCentralAddressedAsEdge covers the second shape: the host IS
// declared, but the spec says it is the control plane. Distinct from the case above, and just as
// wrong.
func TestCheckEdgeNodeDrift_CatchesCentralAddressedAsEdge(t *testing.T) {
	cfg := `
edge_nodes:
  - id: "edge-us"
    url: "https://tunnel.example.com"
`
	findings, err := CheckEdgeNodeDrift([]byte(cfg), []byte(testSpec))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 || findings[0].Severity != "error" {
		t.Fatalf("expected one error finding, got %+v", findings)
	}
	if !strings.Contains(findings[0].Message, "control plane") {
		t.Errorf("expected the message to say the host is the control plane, got %q", findings[0].Message)
	}
}

// TestCheckEdgeNodeDrift_MissingAndUnparseableURLs — an edge with no url cannot be routed to at
// all, which is worth saying rather than skipping silently.
func TestCheckEdgeNodeDrift_MissingAndUnparseableURLs(t *testing.T) {
	cfg := `
edge_nodes:
  - id: "edge-none"
    url: ""
  - id: "edge-broken"
    url: "http://[::1"
`
	findings, err := CheckEdgeNodeDrift([]byte(cfg), []byte(testSpec))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected two findings, got %+v", findings)
	}
	var sawWarning, sawError bool
	for _, f := range findings {
		if f.Severity == "warning" && strings.Contains(f.Key, "edge-none") {
			sawWarning = true
		}
		if f.Severity == "error" && strings.Contains(f.Key, "edge-broken") {
			sawError = true
		}
	}
	if !sawWarning || !sawError {
		t.Errorf("expected a warning for the empty url and an error for the unparseable one, got %+v", findings)
	}
}

// TestCheckEdgeNodeDrift_RefusesAnEmptySpec — a spec that declares nothing would make every
// config look clean, which is the worst possible failure for a check like this: silent, and
// reassuring. It has to refuse instead.
func TestCheckEdgeNodeDrift_RefusesAnEmptySpec(t *testing.T) {
	cfg := `
edge_nodes:
  - id: "edge-us"
    url: "https://us.example.com"
`
	if _, err := CheckEdgeNodeDrift([]byte(cfg), []byte("domains: []")); err == nil {
		t.Error("an empty DNS spec must be an error, not a clean bill of health")
	}
}

// TestNoSecretIsEverEchoed is the hard requirement. The file this check reads holds token
// hashes, SMTP credentials and webhook URLs; a finding that quoted one would put it in CI logs
// and in whatever terminal an operator ran it in.
//
// Asserts on the OUTPUT rather than trusting the redaction helper: the risk is a code path that
// forgets to call it, not the helper being wrong.
func TestNoSecretIsEverEchoed(t *testing.T) {
	const (
		fakeHash    = "deadbeefcafebabe1111222233334444555566667777888899990000aaaabbbb"
		fakePass    = "S3cret-SMTP-Passw0rd"
		fakeWebhook = "https://hooks.slack.com/services/T00000/B00000/XXXXXXXXXXXX"
		fakeToken   = "edge-token-plaintext-should-never-appear"
	)
	cfg := `
smtp_server:
  password: "` + fakePass + `"
webhooks:
  slack_url: "` + fakeWebhook + `"
edge_token: "` + fakeToken + `"
edge_nodes:
  - id: "edge-us"
    token_hash: "` + fakeHash + `"
    url: "https://aws-edge-us.example.com"
`
	findings, err := CheckEdgeNodeDrift([]byte(cfg), []byte(testSpec))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected the drifted url to be reported, so there is output to inspect")
	}

	var all strings.Builder
	for _, f := range findings {
		all.WriteString(f.Severity)
		all.WriteString(f.Key)
		all.WriteString(f.Message)
	}
	got := all.String()
	for name, secret := range map[string]string{
		"token_hash":    fakeHash,
		"smtp password": fakePass,
		"slack webhook": fakeWebhook,
		"edge_token":    fakeToken,
	} {
		if strings.Contains(got, secret) {
			t.Errorf("%s leaked into the findings output", name)
		}
	}
}

// TestRedact covers the helper directly, including that it never returns the value it was given
// for a secret-ish key even when short.
func TestRedact(t *testing.T) {
	for _, key := range []string{"token_hash", "smtp_password", "slack_url", "api_key", "edge_token", "ADMIN_EMAIL"} {
		if got := redact(key, "sensitive"); strings.Contains(got, "sensitive") {
			t.Errorf("redact(%q) leaked the value: %q", key, got)
		}
	}
	// Non-secret keys must pass through, or a drift report becomes unreadable.
	if got := redact("http_bind_addr", "127.0.0.1:8080"); got != "127.0.0.1:8080" {
		t.Errorf("expected a non-secret value to pass through, got %q", got)
	}
	if got := redact("token_hash", ""); got != "<empty>" {
		t.Errorf("expected an empty secret to report as empty, got %q", got)
	}
}

// TestUnknownTopLevelKeys — yaml.v3 ignores unknown keys by default, so a typo is silently inert
// and an operator believes a setting is applied when it never was.
func TestUnknownTopLevelKeys(t *testing.T) {
	cfg := `
http_bind_addr: "127.0.0.1:8080"
force_mfa: true
frce_ip_whitelist: true
edge_nodes: []
`
	known := map[string]bool{"http_bind_addr": true, "force_mfa": true, "force_ip_whitelist": true, "edge_nodes": true}
	unknown, err := UnknownTopLevelKeys([]byte(cfg), known)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(unknown) != 1 || unknown[0] != "frce_ip_whitelist" {
		t.Errorf("expected the typo'd key to be reported, got %v", unknown)
	}
}

// TestExpectedConfigOwner derives the owner from the committed unit rather than hardcoding it,
// because the setup guide is explicit that a deployment may run the daemon as another user.
func TestExpectedConfigOwner(t *testing.T) {
	dir := t.TempDir()
	unit := filepath.Join(dir, "unit.service")

	if err := os.WriteFile(unit, []byte("[Service]\nUser=lfr-tunnel\nGroup=lfr-tunnel\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := expectedConfigOwner(unit); got != "lfr-tunnel:lfr-tunnel" {
		t.Errorf("got %q, want lfr-tunnel:lfr-tunnel", got)
	}

	// Group defaults to the user when the unit names only User=, which is a legal unit.
	if err := os.WriteFile(unit, []byte("[Service]\nUser=gateway\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := expectedConfigOwner(unit); got != "gateway:gateway" {
		t.Errorf("got %q, want gateway:gateway", got)
	}

	// An unreadable unit must return empty so the caller SKIPS the check loudly. Returning a
	// guess would be worse than not checking: it would report a false error on every run, and
	// the check would be turned off.
	if got := expectedConfigOwner(filepath.Join(dir, "nope.service")); got != "" {
		t.Errorf("a missing unit must yield no expectation, got %q", got)
	}
	// A unit with no User= at all is the same case.
	if err := os.WriteFile(unit, []byte("[Service]\nExecStart=/usr/local/bin/lfr-tunneld\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := expectedConfigOwner(unit); got != "" {
		t.Errorf("a unit without User= must yield no expectation, got %q", got)
	}
}

// TestExpectedConfigOwner_MatchesTheCommittedUnit guards the default the command ships with. If
// the unit's User= changes and this check keeps comparing against the old value, it reports a
// false error on every run -- and a check that cries wolf gets ignored.
func TestExpectedConfigOwner_MatchesTheCommittedUnit(t *testing.T) {
	got := expectedConfigOwner("../../resources/server/lfr-tunneld.service")
	if got == "" {
		t.Fatal("could not read the committed unit; the command's default -unit path is wrong")
	}
	if !strings.Contains(got, ":") {
		t.Errorf("expected user:group, got %q", got)
	}
}

// Verifying an edge still trusts the control plane's address (#1450).
//
// The address is baked into each edge's nginx, which is precisely the shape that goes stale
// unnoticed -- the same shape as the edge_nodes urls that named retired hosts for weeks (#1449).

func TestCheckEdgeRealIP_CentralIsSkipped(t *testing.T) {
	// Nothing forwards to central, so there is no upstream to trust and nothing to report.
	if f := checkEdgeRealIP("", "server { listen 443; }"); len(f) != 0 {
		t.Errorf("central must produce no finding, got %+v", f)
	}
}

func TestCheckEdgeRealIP_MissingDirectiveIsReported(t *testing.T) {
	f := checkEdgeRealIP("https://tunnel.example.com", "server { listen 443; }")
	if len(f) != 1 {
		t.Fatalf("expected one finding, got %+v", f)
	}
	if f[0].Severity != severityWarning {
		t.Errorf("expected a warning, got %q", f[0].Severity)
	}
	// The message has to say what goes wrong, not just that something is absent -- an operator
	// reading this needs to know traffic is being attributed to the wrong address.
	if !strings.Contains(f[0].Message, "attributed to CENTRAL") {
		t.Errorf("expected the consequence spelled out, got %q", f[0].Message)
	}
	if !strings.Contains(f[0].Message, "-trusted-proxy") {
		t.Errorf("expected the remedy named, got %q", f[0].Message)
	}
}

// A host that cannot be resolved must be reported rather than silently treated as agreeing --
// that would turn the check into a rubber stamp on exactly the machines where DNS is broken.
func TestCheckEdgeRealIP_UnresolvableHostIsReported(t *testing.T) {
	conf := "set_real_ip_from 203.0.113.7;\nreal_ip_header X-Forwarded-For;\n"
	f := checkEdgeRealIP("https://nonexistent.invalid", conf)
	if len(f) != 1 || f[0].Severity != severityWarning {
		t.Fatalf("expected one warning, got %+v", f)
	}
	if !strings.Contains(f[0].Message, "could not be resolved") {
		t.Errorf("expected the resolution failure to be named, got %q", f[0].Message)
	}
}

// A commented-out directive is not in effect, and must not read as configured.
func TestCheckEdgeRealIP_CommentedDirectiveDoesNotCount(t *testing.T) {
	conf := "# set_real_ip_from 203.0.113.7;\nserver { listen 443; }\n"
	f := checkEdgeRealIP("https://tunnel.example.com", conf)
	if len(f) != 1 || !strings.Contains(f[0].Message, "does not trust") {
		t.Errorf("a commented directive must count as absent, got %+v", f)
	}
}
