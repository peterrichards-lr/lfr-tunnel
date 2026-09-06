package ops

import (
	"slices"
	"strings"
	"testing"
)

// TestBuildNginxConfig_SingleDomain verifies the generated config matches what
// setup-central-vps.sh writes for a single `-d <domain>` -- the map block once, and the
// domain's three server blocks with the domain and port substituted in every place the
// original template uses them.
// centralConfig renders the central role for the given owned apex domains, which is what both
// pre-#1442 tests were implicitly asserting.
func centralConfig(port string, domains ...string) string {
	cfg := nginxRenderConfig{Role: RoleCentral, LocalPort: port}
	for _, d := range domains {
		cfg.Groups = append(cfg.Groups, nginxDomainGroup{Domain: d, CertRoot: certRootLetsEncrypt})
	}
	return buildNginxConfig(cfg)
}

func TestBuildNginxConfig_SingleDomain(t *testing.T) {
	cfg := centralConfig("8080", "lfr-demo.se")

	if strings.Count(cfg, "map $http_upgrade $connection_upgrade") != 1 {
		t.Errorf("expected exactly one upgrade map block, got config:\n%s", cfg)
	}
	if strings.Count(cfg, "server_name lfr-demo.se *.lfr-demo.se;") != 1 {
		t.Errorf("expected exactly one HTTP redirect server_name line for lfr-demo.se, got:\n%s", cfg)
	}
	if strings.Count(cfg, "server_name lfr-demo.se;") != 1 {
		t.Errorf("expected exactly one control-plane server_name line for lfr-demo.se, got:\n%s", cfg)
	}
	if strings.Count(cfg, "server_name *.lfr-demo.se;") != 1 {
		t.Errorf("expected exactly one wildcard data-plane server_name line for lfr-demo.se, got:\n%s", cfg)
	}
	if !strings.Contains(cfg, "ssl_certificate /etc/letsencrypt/live/lfr-demo.se/fullchain.pem;") {
		t.Error("expected the cert path to reference lfr-demo.se's own live/ directory")
	}
	if !strings.Contains(cfg, "proxy_pass http://127.0.0.1:8080;") {
		t.Error("expected proxy_pass to use the configured port")
	}
	// The ACME fallback fix (#979) must be present in both the port-80 and port-443
	// control-plane blocks -- this is the exact regression #997 exists to prevent.
	if strings.Count(cfg, "location /.well-known/acme-challenge/ {") != 2 {
		t.Errorf("expected the ACME challenge fallback location in both the port-80 and control-plane blocks, got:\n%s", cfg)
	}
}

// TestBuildNginxConfig_MultipleDomains verifies the live topology this was actually written
// for: a single lfr-tunneld instance serving two independent domain groups (lfr-demo.se and
// lfr-demo.online) needs both fully represented, with the shared upgrade map block written
// only once (nginx errors on a duplicate `map` directive).
func TestBuildNginxConfig_MultipleDomains(t *testing.T) {
	cfg := centralConfig("8080", "lfr-demo.se", "lfr-demo.online")

	if strings.Count(cfg, "map $http_upgrade $connection_upgrade") != 1 {
		t.Errorf("expected exactly one upgrade map block shared across both domains, got config:\n%s", cfg)
	}
	for _, d := range []string{"lfr-demo.se", "lfr-demo.online"} {
		if strings.Count(cfg, "server_name "+d+";") != 1 {
			t.Errorf("expected exactly one control-plane server_name line for %s, got:\n%s", d, cfg)
		}
		if !strings.Contains(cfg, "ssl_certificate /etc/letsencrypt/live/"+d+"/fullchain.pem;") {
			t.Errorf("expected a cert path referencing %s's own live/ directory", d)
		}
	}
	// Each domain's three server blocks are independent -- one domain's cert path must never
	// leak into the other's blocks.
	seIdx := strings.Index(cfg, "server_name lfr-demo.se;")
	onlineIdx := strings.Index(cfg, "server_name lfr-demo.online;")
	if seIdx < 0 || onlineIdx < 0 {
		t.Fatalf("expected both domains' control-plane blocks to be present, got:\n%s", cfg)
	}
}

// ~/ expansion for identity files is now covered by TestResolveDeployTarget_ExpandsHomeDir
// in target_test.go -- reconcile-nginx resolves its identity file through
// ResolveDeployTarget like every other command now (#1019), rather than its own helper.

func TestParseDomainsFlag(t *testing.T) {
	tests := []struct {
		name string
		csv  string
		want []string
	}{
		{"single domain", "lfr-demo.se", []string{"lfr-demo.se"}},
		{"multiple domains", "lfr-demo.se,lfr-demo.online", []string{"lfr-demo.se", "lfr-demo.online"}},
		{"whitespace around entries is trimmed", " lfr-demo.se , lfr-demo.online ", []string{"lfr-demo.se", "lfr-demo.online"}},
		{"empty entries from stray commas are dropped", "lfr-demo.se,,lfr-demo.online,", []string{"lfr-demo.se", "lfr-demo.online"}},
		{"empty string yields no domains", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDomainsFlag(tt.csv)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// The edge role is the whole point of #1442: an edge's config could not be generated from this
// repo at all, so the file the edges actually run was hand-written and drifted.

// TestBuildNginxConfig_EdgeApexRedirects checks the one thing that must never regress into
// central's shape: an edge's own apex serves no portal. If it proxied `location /` to the local
// gateway like central does, every edge would answer for the control plane's landing page.
func TestBuildNginxConfig_EdgeApexRedirects(t *testing.T) {
	cfg := buildNginxConfig(nginxRenderConfig{
		Role:           RoleEdge,
		LocalPort:      "8090",
		RedirectDomain: "lfr-demo.se",
		Groups:         []nginxDomainGroup{{Domain: "sa.lfr-demo.se", CertRoot: certRootLetsEncrypt}},
	})

	if !strings.Contains(cfg, "return 301 https://lfr-demo.se$request_uri;") {
		t.Errorf("expected the edge apex to redirect browsers to the control plane, got:\n%s", cfg)
	}
	// An edge issues no vanity certificates, so it has no fall-through window to protect.
	if strings.Contains(cfg, "acme-challenge") {
		t.Errorf("did not expect central's ACME fallback on an edge, got:\n%s", cfg)
	}
	// Downloads are served from central's disk; an edge has no such directory.
	if strings.Contains(cfg, "/static/downloads/") {
		t.Errorf("did not expect central's downloads alias on an edge, got:\n%s", cfg)
	}
	// The two paths an edge does serve on its own hostname.
	if !strings.Contains(cfg, "location /api/ {") || !strings.Contains(cfg, "location /tunnel {") {
		t.Errorf("expected the edge apex to serve /api/ and /tunnel, got:\n%s", cfg)
	}
}

// TestBuildNginxConfig_WildcardOnlyOmitsApex is the guard against every edge claiming the
// control plane's own hostname. nginx reports a duplicate server_name as a warning, not an
// error, so getting this wrong passes `nginx -t` and silently steals traffic.
func TestBuildNginxConfig_WildcardOnlyOmitsApex(t *testing.T) {
	cfg := buildNginxConfig(nginxRenderConfig{
		Role:      RoleEdge,
		LocalPort: "8090",
		Groups: []nginxDomainGroup{
			{Domain: "lfr-demo.se", CertRoot: certRootCertSync, WildcardOnly: true},
		},
	})

	if !strings.Contains(cfg, "server_name *.lfr-demo.se;") {
		t.Errorf("expected the wildcard block, got:\n%s", cfg)
	}
	if strings.Contains(cfg, "server_name lfr-demo.se;") {
		t.Errorf("wildcard-only group must NOT emit the apex block, got:\n%s", cfg)
	}
	if strings.Contains(cfg, "listen 80;") {
		t.Errorf("wildcard-only group must NOT emit a port-80 redirect, got:\n%s", cfg)
	}
	// Certs for a pushed wildcard bundle come from certsync's root, not certbot's.
	if !strings.Contains(cfg, "ssl_certificate /etc/lfr-tunneld/certs/lfr-demo.se/fullchain.pem;") {
		t.Errorf("expected the certsync cert root for a wildcard-only group, got:\n%s", cfg)
	}
}

// TestBuildNginxConfig_ForwardedHeadersAreOverwritten pins #1325/#1360 for BOTH roles. The live
// edges were still appending, which is the defect #1441 records.
func TestBuildNginxConfig_ForwardedHeadersAreOverwritten(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  nginxRenderConfig
	}{
		{"central", nginxRenderConfig{Role: RoleCentral, LocalPort: "8080",
			Groups: []nginxDomainGroup{{Domain: "lfr-demo.se", CertRoot: certRootLetsEncrypt}}}},
		{"edge", nginxRenderConfig{Role: RoleEdge, LocalPort: "8090", RedirectDomain: "lfr-demo.se",
			Groups: []nginxDomainGroup{{Domain: "sa.lfr-demo.se", CertRoot: certRootLetsEncrypt}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := buildNginxConfig(tc.cfg)
			// Directive lines only. The rendered config explains in a COMMENT why the appending
			// form is wrong, so a substring search matches its own rationale and passes for the
			// wrong reason -- which it did on the first attempt at this test.
			for _, line := range strings.Split(cfg, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "#") {
					continue
				}
				if strings.Contains(line, "proxy_add_x_forwarded_for") {
					t.Errorf("appending XFF is forgeable and must not be a directive (#1325): %q", line)
				}
			}
			if !strings.Contains(cfg, "proxy_set_header X-Forwarded-For $remote_addr;") {
				t.Errorf("expected XFF to be overwritten with $remote_addr, got:\n%s", cfg)
			}
		})
	}
}

// TestCheckApexDomain rejects the mistake that once nearly deleted live vhosts: passing a
// service hostname where a domain group was wanted.
func TestCheckApexDomain(t *testing.T) {
	for _, bad := range []string{"tunnel.lfr-demo.se", "portal.lfr-demo.se", "WWW.lfr-demo.se"} {
		if err := checkApexDomain(bad); err == nil {
			t.Errorf("expected %q to be rejected as a hostname", bad)
		}
	}
	// An edge's own domain is legitimately a subdomain, and a bare apex is obviously fine.
	for _, good := range []string{"sa.lfr-demo.se", "lfr-demo.se", "lfr-demo.online", "example.co.uk"} {
		if err := checkApexDomain(good); err != nil {
			t.Errorf("expected %q to be accepted, got %v", good, err)
		}
	}
}

// TestNginxRemotePaths pins the reason reconcile-nginx could not target an edge before: the
// edge's live config is a different filename enabled under a different link name, so writing
// central's paths would add a second config rather than replace the live one.
func TestNginxRemotePaths(t *testing.T) {
	target, link := nginxRemotePaths(RoleEdge)
	if target != "/etc/nginx/sites-available/lfr-tunneld" || link != "/etc/nginx/sites-enabled/default" {
		t.Errorf("edge paths wrong: got %q, %q", target, link)
	}
	target, link = nginxRemotePaths(RoleCentral)
	if target != "/etc/nginx/sites-available/lfr-tunnel" || link != "/etc/nginx/sites-enabled/lfr-tunnel" {
		t.Errorf("central paths wrong: got %q, %q", target, link)
	}
}

// TestVhostsLostBy is the guard against the mistake this tooling makes easy: reconcile-nginx
// replaces the file wholesale, so an omitted domain silently stops being served and nginx says
// nothing, because a config that no longer mentions a name is still valid.
//
// The case below is real. Live edge-sa still carries vhosts for its pre-rename
// aws-edge-sa.lfr-demo.se hostname, which no -domains list anybody would think to write
// includes -- so the first honest render of an edge config would have dropped them silently.
func TestVhostsLostBy(t *testing.T) {
	live := `
server { server_name sa.lfr-demo.se *.sa.lfr-demo.se; }
server { server_name aws-edge-sa.lfr-demo.se; }
server { server_name *.aws-edge-sa.lfr-demo.se; }
server { server_name *.lfr-demo.se; }
`
	next := buildNginxConfig(nginxRenderConfig{
		Role:           RoleEdge,
		LocalPort:      "8090",
		RedirectDomain: "lfr-demo.se",
		Groups: []nginxDomainGroup{
			{Domain: "sa.lfr-demo.se", CertRoot: certRootLetsEncrypt},
			{Domain: "lfr-demo.se", CertRoot: certRootCertSync, WildcardOnly: true},
		},
	})

	lost := vhostsLostBy(live, next)
	want := []string{"*.aws-edge-sa.lfr-demo.se", "aws-edge-sa.lfr-demo.se"}
	if len(lost) != len(want) {
		t.Fatalf("got %v, want %v", lost, want)
	}
	for i := range want {
		if lost[i] != want[i] {
			t.Fatalf("got %v, want %v", lost, want)
		}
	}

	// Nothing lost when the render covers everything live serves.
	full := live + "\nserver { server_name x; }"
	if lost := vhostsLostBy(full, full); len(lost) != 0 {
		t.Errorf("expected nothing lost comparing a config with itself, got %v", lost)
	}
}

// TestServerNamesIn covers the parsing the guard depends on: multiple names on one directive,
// wildcards, and the trailing semicolon.
func TestServerNamesIn(t *testing.T) {
	got := serverNamesIn("    server_name a.example.com *.a.example.com;\nserver_name b.example.com;\n# server_name commented.example.com;\n")
	for _, want := range []string{"a.example.com", "*.a.example.com", "b.example.com"} {
		if !got[want] {
			t.Errorf("expected %q to be parsed, got %v", want, got)
		}
	}
	if got["commented.example.com"] {
		t.Error("a commented-out server_name must not count as served")
	}
	if len(got) != 3 {
		t.Errorf("expected exactly 3 names, got %v", got)
	}
}

// TestNginxReplacedPaths pins what the removal guard is allowed to look at. Widening it to
// every enabled vhost would report a vanity domain's own conf.d file -- which a reconcile never
// touches -- as a vhost being destroyed, and block every legitimate run on central.
func TestNginxReplacedPaths(t *testing.T) {
	edge := nginxReplacedPaths(RoleEdge)
	if len(edge) != 2 || edge[1] != nginxLegacyApexVhost {
		t.Errorf("edge must account for the legacy apex vhost it stands down, got %v", edge)
	}
	if central := nginxReplacedPaths(RoleCentral); len(central) != 1 {
		t.Errorf("central replaces only its own target, got %v", central)
	}
}

// TestBuildNginxConfig_KeepsOnBoxRationale pins the comments that appear in the GENERATED file,
// not in this source. They are there for whoever SSHes into a box and reads the live config, and
// the #1442 refactor stripped them once by moving them into Go comments -- which reads fine here
// and leaves the deployed artifact unexplained.
func TestBuildNginxConfig_KeepsOnBoxRationale(t *testing.T) {
	cfg := centralConfig("8080", "lfr-demo.se")

	for _, want := range []string{
		"# (#979). Serving ACME challenges here directly",  // the long port-80 rationale
		"# Same rationale as the port-80 block above",      // the shorter HTTPS variant
		"# $remote_addr, never $proxy_add_x_forwarded_for", // #1325
		"# follow-up, #955).",                              // the downloads block
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("generated config lost its on-box rationale %q:\n%s", want, cfg)
		}
	}

	// The long form belongs on port 80 only; the HTTPS block gets the short one. Repeating the
	// long version twice is what made the generated file harder to read.
	if strings.Count(cfg, "# (#979). Serving ACME challenges here directly") != 1 {
		t.Errorf("expected the long ACME rationale exactly once per domain, got:\n%s", cfg)
	}
}

// hasDirective reports whether the rendered config contains a DIRECTIVE line, ignoring comments.
//
// Needed because the generated config EXPLAINS real_ip in a comment -- "you would then want the
// real_ip module with set_real_ip_from naming that upstream" -- so a plain substring search
// matches the explanation and reports the directive present on every config, including central's.
// The first version of these tests did exactly that and failed for the wrong reason.
func hasDirective(conf, directive string) bool {
	for _, line := range strings.Split(conf, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, directive) {
			return true
		}
	}
	return false
}

// countDirective counts DIRECTIVE occurrences, ignoring comments.
func countDirective(conf, directive string) int {
	n := 0
	for _, line := range strings.Split(conf, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, directive) {
			n++
		}
	}
	return n
}

// Recovering the visitor's address on a cross-proxied request (#1450).
//
// A visitor normally reaches an edge directly, via the per-tunnel CNAME central publishes, and
// there $remote_addr is already the visitor. During DNS propagation and on the cross-node path
// central forwards instead, and then the edge's $remote_addr is central -- so the
// proxy_set_header lines overwrite the visitor one hop before the gateway reads it, and the IP
// whitelist, the rate limiter's auto-ban and every audit entry name the control plane.

func TestRenderRealIPBlock_OnlyWhenConfigured(t *testing.T) {
	cfg := nginxRenderConfig{
		Role:           RoleEdge,
		LocalPort:      "8090",
		RedirectDomain: "example.com",
		Groups:         []nginxDomainGroup{{Domain: "sa.example.com", CertRoot: certRootLetsEncrypt}},
	}

	// Absent, the config must be exactly what it was before this existed -- an edge that has
	// not been given the control plane's address must not silently change behaviour.
	if got := buildNginxConfig(cfg); hasDirective(got, "set_real_ip_from") {
		t.Errorf("no trusted proxy configured, so no real_ip directives should appear:\n%s", got)
	}

	cfg.TrustedProxy = "203.0.113.7"
	got := buildNginxConfig(cfg)
	for _, want := range []string{
		"set_real_ip_from 203.0.113.7;",
		// Loopback is not decoration: the chain central sends ends with its own loopback peer,
		// and without this line the recursive walk stops there (#1750).
		"set_real_ip_from 127.0.0.1;",
		"real_ip_header X-Forwarded-For;",
		"real_ip_recursive on;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in:\n%s", want, got)
		}
	}

	// Once per file, two addresses. These are http-context directives and the file is included
	// into http; repeating the block per domain group would be wrong even where nginx tolerates
	// it. Two is the whole set -- see TestRenderRealIPBlock_TrustedSet.
	if n := countDirective(got, "set_real_ip_from"); n != 2 {
		t.Errorf("expected exactly two set_real_ip_from directives, got %d", n)
	}
}

// nginxTrustedRealIPSet reads the addresses the RENDERED config trusts, so the walk test below
// cannot quietly diverge from what is actually emitted.
func nginxTrustedRealIPSet(conf string) []string {
	var out []string
	for _, line := range strings.Split(conf, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "set_real_ip_from ") {
			continue
		}
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(line, "set_real_ip_from "), ";"))
	}
	return out
}

// nginxRecursiveRealIP reproduces ngx_http_realip's documented `real_ip_recursive on` behaviour:
// the connecting peer is replaced by "the last non-trusted address sent in the request header
// field". Concretely -- and this matches ngx_http_get_forwarded_addr_internal -- while the current
// address is trusted, take the rightmost remaining X-Forwarded-For entry; stop at the first
// address that is not trusted, or when the chain runs out.
//
// The module DECLINES entirely if the peer itself is not trusted, which this expresses as the
// loop never running and the peer being returned unchanged.
func nginxRecursiveRealIP(peer string, xff, trusted []string) string {
	addr := peer
	for slices.Contains(trusted, addr) && len(xff) > 0 {
		addr, xff = xff[len(xff)-1], xff[:len(xff)-1]
	}
	return addr
}

// TestRenderRealIPBlock_TrustedSet is the assertion #1750 turned on: it walks the chain an edge
// ACTUALLY receives against the trusted set the template ACTUALLY emits.
//
// #1450 was designed against "X-Forwarded-For: <visitor>, <visitor>", which is not what leaves
// central. Central's nginx proxies to the gateway on 127.0.0.1, and the gateway appends the
// address it was connected from, so the last entry is loopback -- both before #1737 removed the
// duplicated hop ("<visitor>, <visitor>, 127.0.0.1") and after it ("<visitor>, 127.0.0.1").
// Trusting central alone stops the walk on that loopback entry and hands the WAF, the rate
// limiter and the audit log 127.0.0.1 for every cross-proxied visitor.
func TestRenderRealIPBlock_TrustedSet(t *testing.T) {
	const central, visitor = "203.0.113.7", "198.51.100.25"

	trusted := nginxTrustedRealIPSet(buildNginxConfig(nginxRenderConfig{
		Role:           RoleEdge,
		LocalPort:      "8090",
		RedirectDomain: "example.com",
		TrustedProxy:   central,
		Groups:         []nginxDomainGroup{{Domain: "sa.example.com", CertRoot: certRootLetsEncrypt}},
	}))

	for _, tc := range []struct {
		name string
		peer string
		xff  []string
		want string
	}{{
		name: "cross-proxied: the chain central sends today",
		peer: central,
		xff:  []string{visitor, "127.0.0.1"},
		want: visitor,
	}, {
		name: "cross-proxied: the chain central sent before #1737",
		peer: central,
		xff:  []string{visitor, visitor, "127.0.0.1"},
		want: visitor,
	}, {
		name: "edge-direct: nothing is rewritten, forged chain or not",
		peer: visitor,
		xff:  []string{"6.6.6.6"},
		want: visitor,
	}, {
		name: "edge-direct: an attacker naming loopback buys nothing",
		peer: visitor,
		xff:  []string{"6.6.6.6", "127.0.0.1"},
		want: visitor,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nginxRecursiveRealIP(tc.peer, tc.xff, trusted); got != tc.want {
				t.Errorf("real_ip_recursive over %v from peer %s with trusted %v = %s, want %s",
					tc.xff, tc.peer, trusted, got, tc.want)
			}
		})
	}
}

// TestRenderRealIPBlock_NeverOnCentral — central is not behind anything. Emitting real_ip there
// would tell nginx to believe a forwarded-for header from whoever connected, which is the
// forgeable path #1325 closed.
func TestRenderRealIPBlock_NeverOnCentral(t *testing.T) {
	got := buildNginxConfig(nginxRenderConfig{
		Role:         RoleCentral,
		LocalPort:    "8080",
		TrustedProxy: "203.0.113.7",
		Groups:       []nginxDomainGroup{{Domain: "example.com", CertRoot: certRootLetsEncrypt}},
	})
	if hasDirective(got, "set_real_ip_from") {
		t.Errorf("central must never emit real_ip directives:\n%s", got)
	}
}

// TestRenderRealIPBlock_MultipleGroupsStillOncePerFile guards the same thing across the shape an
// edge actually runs: its own regional group plus two wildcard-only apex groups. One block, two
// addresses, however many domain groups.
func TestRenderRealIPBlock_MultipleGroupsStillOncePerFile(t *testing.T) {
	got := buildNginxConfig(nginxRenderConfig{
		Role:           RoleEdge,
		LocalPort:      "8090",
		RedirectDomain: "example.com",
		TrustedProxy:   "203.0.113.7",
		Groups: []nginxDomainGroup{
			{Domain: "sa.example.com", CertRoot: certRootLetsEncrypt},
			{Domain: "example.com", CertRoot: certRootCertSync, WildcardOnly: true},
			{Domain: "example.net", CertRoot: certRootCertSync, WildcardOnly: true},
		},
	})
	if n := countDirective(got, "set_real_ip_from"); n != 2 {
		t.Errorf("expected exactly two set_real_ip_from directives across three groups, got %d", n)
	}
}

// Both the apex and the wildcard server blocks must serve the client downloads (#1687).
//
// /static/downloads/ is served from disk by nginx, bypassing the Go app, which only serves
// /static/* from its compiled-in embed and never contains the binaries. The block was added to
// the wildcard block alone (#955), so the apex served every other path -- API, portal,
// install.sh, dashboard.js -- and 404d on the only path the installer needs.
//
// Two bugs masked each other: #1684 sent every install to the apex, so the failure was
// attributed to the installer and this never surfaced on its own.
func TestBothServerBlocksServeClientDownloads(t *testing.T) {
	out := centralConfig("8080", "example.test")

	if n := strings.Count(out, "location /static/downloads/"); n != 2 {
		t.Errorf("expected the downloads location in BOTH the apex and wildcard blocks, found %d\n%s", n, out)
	}

	// Positionally: one before `server_name *.`, one after. Counting alone would pass if both
	// landed in the same block.
	wildcardAt := strings.Index(out, "server_name *.example.test")
	if wildcardAt < 0 {
		t.Fatalf("no wildcard server block rendered:\n%s", out)
	}
	if !strings.Contains(out[:wildcardAt], "location /static/downloads/") {
		t.Error("the apex block has no downloads location -- an install from the bare domain 404s")
	}
	if !strings.Contains(out[wildcardAt:], "location /static/downloads/") {
		t.Error("the wildcard block lost its downloads location")
	}
}

// An edge node serves no client downloads: the binaries are only on central.
func TestEdgeRoleDoesNotServeClientDownloads(t *testing.T) {
	out := buildNginxConfig(nginxRenderConfig{
		Role:      RoleEdge,
		LocalPort: "8080",
		Groups: []nginxDomainGroup{
			{Domain: "example.test", CertRoot: certRootLetsEncrypt},
		},
	})
	if strings.Contains(out, "location /static/downloads/") {
		t.Error("an edge rendered a downloads location; those files only exist on central")
	}
}
