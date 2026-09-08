package ops

import (
	"os"
	"path/filepath"
	"slices"
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

// Central with NO fleet declared has nothing to trust and nothing to report -- the single-node
// shape a deployment outside Liferay's own has. Not because "nothing forwards to central": the
// check below asserts that central WITH a fleet is reported on, which is #1767.
func TestCheckGatewayRealIP_CentralWithNoFleetIsSilent(t *testing.T) {
	if f := checkGatewayRealIP("", DNSSpec{}, "server { listen 443; }"); len(f) != 0 {
		t.Errorf("central with no fleet declared must produce no finding, got %+v", f)
	}
}

// centralConf is what central's live nginx looks like, with the trusted addresses substituted.
// Its server_names are the apex and its wildcard -- no edge's fully-qualified hostname among
// them, which is what makes checkRealIPCoversPeerEdges count every edge as one central must
// trust without needing a "which node am I" input.
func centralConf(trusted ...string) string {
	conf := "server {\n    server_name example.com *.example.com;\n}\n"
	for _, a := range trusted {
		conf += "set_real_ip_from " + a + ";\n"
	}
	return conf
}

// Central with a fleet declared and no real_ip block at all is the live state #1767 describes,
// and the check used to return early on exactly it -- so the one box that had no block was also
// the one box drift could not report on.
func TestCheckGatewayRealIP_CentralWithNoBlockIsReported(t *testing.T) {
	f := checkGatewayRealIP("", twoEdgeSpec(t), centralConf())
	if len(f) != 1 {
		t.Fatalf("expected one finding for a central with no real_ip block, got %+v", f)
	}
	if f[0].Severity != severityWarning {
		t.Errorf("expected a warning, got %q", f[0].Severity)
	}
	// It must say what breaks and how to fix it, and must NOT send the operator after
	// -trusted-proxy, which central refuses.
	for _, want := range []string{"#1767", "cross-proxies", "reconcile-nginx -role central", "-dns-spec"} {
		if !strings.Contains(f[0].Message, want) {
			t.Errorf("central finding should mention %q, got %q", want, f[0].Message)
		}
	}
}

// Every edge in the spec must be trusted on central, and the loopback gap #1750 found applies
// here for the same reason: the forwarding edge's gateway appends its own loopback peer.
func TestCheckGatewayRealIP_CentralMissingEdgesAndLoopback(t *testing.T) {
	// Central trusting one edge only: the other edge and loopback are both missing.
	f := checkGatewayRealIP("", twoEdgeSpec(t), centralConf("18.0.0.1"))

	var sawLoopback, sawPeers bool
	for _, finding := range f {
		if strings.Contains(finding.Message, "#1750") {
			sawLoopback = true
		}
		if strings.Contains(finding.Message, "3.0.0.1") && strings.Contains(finding.Message, "2600:db8::1") {
			sawPeers = true
		}
		// Central has no control plane, so it must never be told its control plane moved.
		if strings.Contains(finding.Message, "#1450") {
			t.Errorf("central has no control plane to check against, got %q", finding.Message)
		}
	}
	if !sawLoopback {
		t.Errorf("expected the #1750 loopback finding on central, got %+v", f)
	}
	if !sawPeers {
		t.Errorf("expected both of the untrusted edge's addresses named, got %+v", f)
	}
}

// A fully reconciled central: both edge addresses plus loopback, and no control-plane entry.
func TestCheckGatewayRealIP_CentralFullyTrustedIsClean(t *testing.T) {
	conf := centralConf("18.0.0.1", "3.0.0.1", "2600:db8::1", nginxLoopbackTrustedProxy)
	if f := checkGatewayRealIP("", twoEdgeSpec(t), conf); len(f) != 0 {
		t.Errorf("a fully reconciled central must be clean, got %+v", f)
	}
}

func TestCheckEdgeRealIP_MissingDirectiveIsReported(t *testing.T) {
	f := checkGatewayRealIP("https://tunnel.example.com", DNSSpec{}, "server { listen 443; }")
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
	conf := "set_real_ip_from 203.0.113.7;\nset_real_ip_from 127.0.0.1;\nreal_ip_header X-Forwarded-For;\n"
	f := checkGatewayRealIP("https://nonexistent.invalid", DNSSpec{}, conf)
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
	f := checkGatewayRealIP("https://tunnel.example.com", DNSSpec{}, conf)
	if len(f) != 1 || !strings.Contains(f[0].Message, "does not trust") {
		t.Errorf("a commented directive must count as absent, got %+v", f)
	}
}

// An edge reconciled before #1750 trusts the control plane and nothing else, which the
// control-plane comparison above reads as entirely correct. nginx's recursive walk still stops at
// the loopback entry central appends, so the visitor is attributed to 127.0.0.1 -- and nginx
// config is not re-rendered by a restart, so nothing else would ever surface it.
func TestCheckEdgeRealIP_MissingLoopbackIsReported(t *testing.T) {
	conf := "set_real_ip_from 203.0.113.7;\nreal_ip_header X-Forwarded-For;\nreal_ip_recursive on;\n"
	f := checkGatewayRealIP("https://nonexistent.invalid", DNSSpec{}, conf)

	var loopback *DriftFinding
	for i := range f {
		if strings.Contains(f[i].Message, "#1750") {
			loopback = &f[i]
		}
	}
	if loopback == nil {
		t.Fatalf("expected a #1750 finding for a config that trusts central alone, got %+v", f)
	}
	if loopback.Severity != severityWarning {
		t.Errorf("expected a warning, got %q", loopback.Severity)
	}
	// The consequence, not just the absence -- an operator has to know what is being mis-attributed.
	if !strings.Contains(loopback.Message, "127.0.0.1") {
		t.Errorf("expected the address it wrongly attributes to, got %q", loopback.Message)
	}
	if !strings.Contains(loopback.Message, "reconcile-nginx") {
		t.Errorf("expected the remedy named, got %q", loopback.Message)
	}
}

// A config carrying both addresses must produce no #1750 finding, in either emission order --
// reading only the first match is exactly how the old single-match regexp would have broken.
func TestCheckEdgeRealIP_LoopbackPresentInEitherOrder(t *testing.T) {
	for _, conf := range []string{
		"set_real_ip_from 203.0.113.7;\nset_real_ip_from 127.0.0.1;\n",
		"set_real_ip_from 127.0.0.1;\nset_real_ip_from 203.0.113.7;\n",
	} {
		for _, f := range checkGatewayRealIP("https://nonexistent.invalid", DNSSpec{}, conf) {
			if strings.Contains(f.Message, "#1750") {
				t.Errorf("both addresses are trusted, so no #1750 finding is due; got %q for:\n%s",
					f.Message, conf)
			}
		}
	}
}

// trustedRealIPAddrs must return EVERY address, in order. The old FindStringSubmatch read the
// first directive only, so once the template emitted two, whether the control-plane comparison
// saw central at all depended on which line renderRealIPBlock happened to write first.
func TestTrustedRealIPAddrs_ReadsEveryDirective(t *testing.T) {
	conf := "# set_real_ip_from 10.0.0.1;\nset_real_ip_from 127.0.0.1;\nset_real_ip_from 203.0.113.7;\n"
	got := trustedRealIPAddrs(conf)
	want := []string{"127.0.0.1", "203.0.113.7"}
	if !slices.Equal(got, want) {
		t.Errorf("expected %v (the commented line does not count), got %v", want, got)
	}
	if len(trustedRealIPAddrs("server { listen 443; }")) != 0 {
		t.Error("a config with no directive must trust nothing")
	}
}

// An edge is cross-proxied to by its PEERS as well as by central (#1757).
//
// Same treatment as the loopback gap #1750 added, and for the same reason: an edge reconciled
// before the fix satisfies every check above, and the symptom -- visitors attributed to a sibling
// edge -- is indistinguishable from ordinary traffic in the audit log.

// twoEdgeSpec declares this node (sa) and one peer (us), which is the smallest shape that can
// tell "trusts the fleet" apart from "trusts everything the spec mentions".
func twoEdgeSpec(t *testing.T) DNSSpec {
	t.Helper()
	spec, err := parseDNSSpec([]byte(`
domains:
  - zone: example.com
    records:
      - {name: "@", type: A, value: "${IPV4}"}
      - {name: "*", type: A, value: "${IPV4}"}
      - {name: tunnel, type: A, value: "${IPV4}"}
      - {name: sa, type: A, value: "18.0.0.1"}
      - {name: us, type: A, value: "3.0.0.1"}
      - {name: us, type: AAAA, value: "2600:db8::1"}
`))
	if err != nil {
		t.Fatalf("fixture spec: %v", err)
	}
	return spec
}

// saEdgeConf is what the sa edge's live nginx looks like, with the trusted addresses substituted.
func saEdgeConf(trusted ...string) string {
	conf := "server {\n    server_name sa.example.com *.sa.example.com;\n}\n"
	for _, a := range trusted {
		conf += "set_real_ip_from " + a + ";\n"
	}
	return conf
}

func TestCheckEdgeRealIP_MissingPeerEdgesAreReported(t *testing.T) {
	// An edge reconciled at #1750: central and loopback, no peers.
	conf := saEdgeConf("203.0.113.7", nginxLoopbackTrustedProxy)
	f := checkGatewayRealIP("https://nonexistent.invalid", twoEdgeSpec(t), conf)

	var peers *DriftFinding
	for i := range f {
		if strings.Contains(f[i].Message, "#1757") {
			peers = &f[i]
		}
	}
	if peers == nil {
		t.Fatalf("expected a #1757 finding for an edge that trusts no peer, got %+v", f)
	}
	if peers.Severity != severityWarning {
		t.Errorf("expected a warning, got %q", peers.Severity)
	}
	// Both families: an edge dialling a peer over IPv6 presents its IPv6 source address, and a
	// v4-only trusted set silently does nothing for those requests.
	for _, want := range []string{"3.0.0.1", "2600:db8::1", "us.example.com"} {
		if !strings.Contains(peers.Message, want) {
			t.Errorf("expected the untrusted peer %q named, got %q", want, peers.Message)
		}
	}
	// The consequence, not just the absence. "GATEWAY" rather than "EDGE" since #1767: the same
	// finding is now raised on central, where the forwarder is an edge and the node reading it is
	// not one.
	if !strings.Contains(peers.Message, "FORWARDING GATEWAY") {
		t.Errorf("expected the mis-attribution spelled out, got %q", peers.Message)
	}
	if !strings.Contains(peers.Message, "reconcile-nginx") {
		t.Errorf("expected the remedy named, got %q", peers.Message)
	}
}

// The node's OWN address is not a peer. Demanding it would report a finding on every correctly
// reconciled edge, and a check that cries wolf gets switched off.
func TestCheckEdgeRealIP_OwnAddressIsNotDemanded(t *testing.T) {
	conf := saEdgeConf("203.0.113.7", "3.0.0.1", "2600:db8::1", nginxLoopbackTrustedProxy)
	for _, f := range checkGatewayRealIP("https://nonexistent.invalid", twoEdgeSpec(t), conf) {
		if strings.Contains(f.Message, "#1757") {
			t.Errorf("every peer is trusted, so no #1757 finding is due; got %q", f.Message)
		}
	}
	// And its own address (18.0.0.1) must not be what silenced it -- it is excluded by the
	// server_name the live config serves, so adding it changes nothing either way.
	if strings.Contains(saEdgeConf("203.0.113.7"), "18.0.0.1") {
		t.Fatal("fixture is wrong: the node's own address must not be in its trusted set")
	}
}

// A spec that declares no fleet has nothing to compare against, and saying so on every run would
// be noise. This is the shape a deployment outside Liferay's own has.
func TestCheckEdgeRealIP_NoFleetDeclaredIsSilent(t *testing.T) {
	conf := saEdgeConf("203.0.113.7", nginxLoopbackTrustedProxy)
	for _, f := range checkGatewayRealIP("https://nonexistent.invalid", DNSSpec{}, conf) {
		if strings.Contains(f.Message, "#1757") {
			t.Errorf("no fleet declared, so no peer finding is possible; got %q", f.Message)
		}
	}
}

// edgeAddresses must never hand an unsubstituted placeholder to set_real_ip_from: nginx rejects
// it at reload, so a whole edge would stop serving on the next reconcile.
func TestDNSSpec_EdgeAddressesExcludeTheControlPlane(t *testing.T) {
	got := twoEdgeSpec(t).edgeAddresses()
	if _, ok := got["tunnel.example.com"]; ok {
		t.Errorf("a record pointing at the control plane is not an edge, got %v", got)
	}
	if _, ok := got["@.example.com"]; ok {
		t.Errorf("the apex must never be treated as a host, got %v", got)
	}
	if !slices.Equal(got["us.example.com"], []string{"3.0.0.1", "2600:db8::1"}) {
		t.Errorf("expected both families for us.example.com, got %v", got)
	}
	for host, addrs := range got {
		for _, a := range addrs {
			if strings.HasPrefix(a, "${") {
				t.Errorf("%s: placeholder %q must never reach a trusted set", host, a)
			}
		}
	}
}

// An over-wide set_real_ip_from entry on a LIVE box (#1792).
//
// The render-side refusal added in the same change stops a NEW config being written with one and
// says nothing whatever about a box already carrying one -- provisioned by hand, or by an older
// copy of this tool, or edited on the box. Every other check here is satisfied by such a box: the
// block is present, the control plane is trusted, loopback is trusted, every peer is trusted. What
// it also trusts is everything else. A guard that lives only at render time makes the dangerous
// state unwritable and invisible at the same time, which is the worse half of the two.
func TestCheckGatewayRealIP_ReportsOverWideEntry(t *testing.T) {
	for _, entry := range []string{"0.0.0.0/0", "::/0", "203.0.113.0/24", "10.0.0.0/7"} {
		t.Run(entry, func(t *testing.T) {
			// Otherwise a fully reconciled central -- the same config
			// TestCheckGatewayRealIP_CentralFullyTrustedIsClean asserts produces NO findings --
			// with one entry widened. So the only thing this can report is the width.
			conf := centralConf(entry, "18.0.0.1", "3.0.0.1", "2600:db8::1", nginxLoopbackTrustedProxy)
			f := checkGatewayRealIP("", twoEdgeSpec(t), conf)
			if len(f) != 1 {
				t.Fatalf("expected exactly one finding, the width one, got %+v", f)
			}
			// Assert the cause. This config is one edited line away from the absent-block, the
			// missing-loopback and the missing-peer findings, all of which are DriftFindings on
			// the same key -- so "a finding was produced" is satisfied by any of them.
			if !strings.Contains(f[0].Message, entry) {
				t.Errorf("the finding must NAME the over-wide entry, got %q", f[0].Message)
			}
			if !strings.Contains(f[0].Message, "#1792") {
				t.Errorf("the finding must point at the rule it enforces, got %q", f[0].Message)
			}
			// Unlike every other real_ip finding here. The others are warnings because they rest
			// on resolution from the operator's machine or on a spec that may be mid-edit; the
			// width of a prefix in a file this tool just read is not a matter of opinion.
			if f[0].Severity != severityError {
				t.Errorf("expected %q, got %q", severityError, f[0].Severity)
			}
		})
	}
}

// The other half of the rule, on the drift side: a fleet reconciled with exact addresses -- which
// is what the four production edges and central actually carry -- must not start reporting an
// error, and a genuine private proxy subnet must not either. Without this the check could be
// "report every prefix" and every assertion above would still pass.
func TestCheckGatewayRealIP_ExactAndPrivateEntriesAreNotReportedAsWide(t *testing.T) {
	conf := centralConf("18.0.0.1", "3.0.0.1", "2600:db8::1", "10.20.0.0/16", "192.0.2.5/32", nginxLoopbackTrustedProxy)
	if f := checkGatewayRealIP("", twoEdgeSpec(t), conf); len(f) != 0 {
		t.Errorf("exact addresses, a /32 and a private subnet are all within the rule, got %+v", f)
	}
}

// The SECOND forwarded-header trust boundary on a live box (#1792).
//
// #1792 was reported against nginx's set_real_ip_from, but that is not the only hand-configured
// set in front of the resolved client address: trusted_proxies in the server config decides whose
// X-Real-IP / X-Forwarded-For clientIPFrom will believe at all, it takes CIDRs by design, and
// nothing here looked at it before. A gateway carrying `trusted_proxies: ["0.0.0.0/0"]` honours a
// forged header from any caller on the internet whatever nginx in front of it does -- so fixing
// only the nginx half would have fixed the instance and left the class.
func TestCheckConfigTrustedProxies(t *testing.T) {
	// The default (empty), the documented loopback pair, and a private proxy subnet are all
	// within the rule and must stay silent -- otherwise this check would fire on every gateway
	// that has ever been configured correctly.
	for _, entries := range [][]string{
		nil,
		{"127.0.0.1/32", "::1/128"},
		{"127.0.0.1/32", "10.20.0.0/16"},
		{"203.0.113.7", "203.0.113.8/32"},
	} {
		if f := checkConfigTrustedProxies(entries); len(f) != 0 {
			t.Errorf("%v is within the rule, got %+v", entries, f)
		}
	}

	for _, entry := range []string{"0.0.0.0/0", "::/0", "203.0.113.0/24", "10.0.0.0/7"} {
		t.Run(entry, func(t *testing.T) {
			f := checkConfigTrustedProxies([]string{"127.0.0.1/32", entry})
			if len(f) != 1 {
				t.Fatalf("expected exactly one finding for %s, got %+v", entry, f)
			}
			if !strings.Contains(f[0].Message, entry) {
				t.Errorf("the finding must NAME the over-wide entry, got %q", f[0].Message)
			}
			// The key has to distinguish the two files. Both boundaries produce an error-severity
			// finding with the same remedy text, so without this an operator reading the output
			// cannot tell which one to edit.
			if f[0].Key != trustedProxiesKey {
				t.Errorf("expected key %q so the operator knows which file is wrong, got %q", trustedProxiesKey, f[0].Key)
			}
			if f[0].Severity != severityError {
				t.Errorf("expected %q, got %q", severityError, f[0].Severity)
			}
		})
	}
}
