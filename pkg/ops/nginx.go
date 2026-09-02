package ops

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// nginxUpgradeMapBlock is written once at the top of the generated config, regardless of
// how many domains it covers -- nginx errors on a duplicate `map` block if this were
// repeated per domain.
const nginxUpgradeMapBlock = `map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}
`

// renderRealIPBlock recovers the visitor's address on a request the CONTROL PLANE forwarded here.
//
// A visitor normally reaches an edge directly -- central publishes a per-tunnel CNAME to the edge
// -- and there $remote_addr is the visitor and the headers below are already correct. But during
// DNS propagation, and on the cross-node path generally (#1249), central proxies the request
// instead. Then the edge's $remote_addr is CENTRAL, and the proxy_set_header lines overwrite the
// visitor's address with it one hop before the gateway reads it: the per-tunnel IP whitelist, the
// rate limiter's auto-ban and every audit entry then name the control plane (#1450).
//
// central forwards the visitor correctly in X-Forwarded-For (see the director in
// pkg/server/proxy.go), so the address is present on the wire -- it is this nginx that discards
// it. real_ip puts it back BEFORE proxy_set_header runs, so no Go-side change is needed and
// clientIPFrom keeps its current, well-tested resolution order.
//
// Not spoofable: real_ip rewrites only when the immediate peer is in set_real_ip_from. A visitor
// arriving directly is not central, so nothing is rewritten and #1325's guarantee stands. That is
// also why the trusted address must be exact -- widen it and the forgeable path reopens.
func renderRealIPBlock(trustedProxy string) string {
	if strings.TrimSpace(trustedProxy) == "" {
		return ""
	}
	return fmt.Sprintf(`
# Recover the visitor's address on requests forwarded by the control plane (#1450).
set_real_ip_from %s;
real_ip_header X-Forwarded-For;
real_ip_recursive on;
`, strings.TrimSpace(trustedProxy))
}

// The forwarding-header rule, stated once (#1325, #1360).
//
// $remote_addr, never $proxy_add_x_forwarded_for: the latter APPENDS to whatever the caller
// sent, so the leftmost entry is caller-supplied and forgeable. X-Real-IP and X-Forwarded-For
// are both overwritten at this hop, so the gateway can trust what it receives. clientIPFrom in
// pkg/server/client_ip.go reads X-Real-IP first and only then walks X-Forwarded-For right to
// left, so both have to be set here for the resolved address to be honest.
//
// If a CDN or load balancer is ever placed IN FRONT of an nginx rendered from this template,
// revisit: you would want the real_ip module with set_real_ip_from naming that upstream.
//
// Known gap, deliberately not papered over here (#1450): when CENTRAL cross-proxies to an edge,
// the edge's $remote_addr is central, so overwriting attributes the request to central rather
// than to the visitor. Fixing that needs trusted_proxies on the edge and a change to resolution
// order, not a different directive in this file.
const nginxProxyHeaders = `        proxy_set_header Host $host;
        # $remote_addr, never $proxy_add_x_forwarded_for: the latter APPENDS to whatever the
        # client sent, so the leftmost entry is caller-supplied and forgeable. Both headers are
        # overwritten here, so the gateway can trust what it receives from this hop (#1325).
        # If a CDN or load balancer is ever placed IN FRONT of this nginx, revisit -- you would
        # then want the real_ip module with set_real_ip_from naming that upstream.
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;`

// nginxRole is what a node's config is for. The two roles share the wildcard data plane and
// differ in what the apex hostname does and who serves the client downloads.
type nginxRole string

const (
	// RoleCentral is the control plane: it serves the portal on the apex, owns the ACME
	// fallback for vanity domains, and serves signed client binaries from disk.
	RoleCentral nginxRole = "central"
	// RoleEdge is a regional data-plane node. Its apex is a redirect to the control plane;
	// it serves no portal and no downloads.
	RoleEdge nginxRole = "edge"
)

// certRootLetsEncrypt is where certbot puts a certificate it issued on the box itself, which
// is how central and an edge's own regional hostname are covered.
const certRootLetsEncrypt = "/etc/letsencrypt/live"

// certRootCertSync is where scripts/common/lfr-install-certs.sh installs a wildcard bundle
// pushed from central (#1302). Central holds the DNS-write credential and renews the apex
// wildcards; an edge only receives them, so an edge's apex vhost reads from here rather than
// from /etc/letsencrypt. Keep in step with INSTALL_ROOT in that script.
const certRootCertSync = "/etc/lfr-tunneld/certs"

// nginxClientDownloads serves the signed client binaries and checksums (populated by
// lfr-tunnel-ops deploy-clients) directly from disk, bypassing the Go app entirely -- it only
// ever serves /static/* from its own compiled-in embed.FS, which never contains these. Without
// it, /install's own download links 404 even though the files exist on disk (#949's follow-up,
// #955).
//
// One constant used by BOTH the apex and the wildcard server blocks (#1687). It was written out
// in the wildcard block alone, so the apex 404d on the only path the installer needs while
// serving every other path identically -- a difference invisible unless you compare the two
// blocks side by side. Shared so they cannot drift apart again.
const nginxClientDownloads = `
    # Signed client binaries/checksums (populated by lfr-tunnel-ops deploy-clients) are served
    # directly from disk here, bypassing the Go app entirely -- it only ever serves /static/*
    # from its own compiled-in embed.FS, which never contains these. Without this block,
    # /install's own download links 404 even though the files exist on disk (see #949's
    # follow-up, #955).
    #
    # Emitted in BOTH the apex and wildcard server blocks (#1687). It was in the wildcard alone,
    # so the bare domain served every other path and 404d on the only one the installer needs.
    location /static/downloads/ {
        alias /var/www/lfr-tunnel/static/downloads/;
        autoindex off;
        add_header Content-Disposition 'attachment';
    }
`

// nginxDomainGroup is one server_name family on a node.
//
// The two shapes exist because a node can serve a domain it does not own the apex of. An edge
// serves *.lfr-demo.se so a visitor URL can be answered edge-direct, while central keeps
// lfr-demo.se itself -- so the edge must emit the wildcard block ALONE. Emitting the apex
// blocks too would have every edge claim the control plane's own hostname, and nginx reports a
// duplicate server_name as a warning rather than an error, so it would be silently wrong.
type nginxDomainGroup struct {
	// Domain is the apex, e.g. lfr-demo.se or sa.lfr-demo.se -- never a hostname like
	// tunnel.lfr-demo.se, which would render server_name tunnel.lfr-demo.se
	// *.tunnel.lfr-demo.se and serve nothing anybody asks for.
	Domain string
	// CertRoot is the directory holding <Domain>/fullchain.pem and <Domain>/privkey.pem.
	CertRoot string
	// WildcardOnly emits just the *.Domain server block: no port-80 redirect and no apex
	// block, because another node owns the apex.
	WildcardOnly bool
}

// nginxRenderConfig is the whole input to a rendered config.
type nginxRenderConfig struct {
	Role      nginxRole
	Groups    []nginxDomainGroup
	LocalPort string
	// RedirectDomain is where an edge sends browser traffic that arrives on its own apex --
	// the control plane's landing page. Ignored for RoleCentral.
	RedirectDomain string
	// TrustedProxy is the control plane's address, as an IP or CIDR. Set on an edge so a
	// request the control plane forwarded is attributed to the visitor rather than to central
	// (#1450). Empty renders exactly what was rendered before this existed.
	//
	// Meaningless on central: nothing forwards to it, so there is no upstream to trust.
	TrustedProxy string
}

// nginxACMEFallback is central-only. A vanity domain (e.g. dev.solaramoto.com) added later by
// lfr-vanity-hook.sh has no vhost of its own until its certificate is issued, so until then
// requests for it fall through to whichever block nginx treats as the implicit default for the
// listen socket. Without this location, ACME's own HTTP-01 validation (and any real visitor
// arriving in that window) was redirected to HTTPS, proxied to the Go backend and on into the
// WS tunnel to whichever client held that lease -- surfacing as a 502 in the CLIENT's request
// log, since nothing local listens on that path (#979). Serving challenges here, from the same
// shared webroot the per-domain vhosts use, closes the window whether or not a vanity vhost
// exists yet. Harmless 404s if the vanity hook is never configured: an absent
// /var/www/lfr-tunnel-vanity is an empty webroot, not an nginx config error.
//
// An edge never issues a vanity certificate, so it has nothing to fall through to and does not
// get this block.
const nginxACMEFallbackHTTP = `
    # Neither of these server blocks has an explicit server_name for a vanity/custom
    # domain (e.g. dev.solaramoto.com) added later via lfr-vanity-hook.sh -- until that
    # domain's own conf.d/*.conf vhost exists and nginx has reloaded, requests for it fall
    # through to whichever server block nginx treats as the implicit default for this
    # listen socket, which is this one. Without this location, that meant ACME's own
    # HTTP-01 validation request (and any real visitor hitting the domain during that same
    # window) got redirected to HTTPS and then proxied straight through to the Go backend
    # and on into the WS tunnel to whichever client holds that lease -- surfacing as a 502
    # in the CLIENT's own request log, since nothing local is listening on that path
    # (#979). Serving ACME challenges here directly, from the same shared webroot
    # lfr-vanity-hook.sh's own per-domain vhosts use, closes that window regardless of
    # whether a domain-specific vhost has been created yet. Harmless 404s if the vanity
    # hook is never configured -- /var/www/lfr-tunnel-vanity not existing isn't an nginx
    # config error, just an empty webroot.
    location /.well-known/acme-challenge/ {
        root /var/www/lfr-tunnel-vanity;
        try_files $uri =404;
    }
`

// nginxACMEFallbackHTTPS is the same location on the HTTPS block. Shorter comment on purpose:
// the full rationale is on the port-80 block a few lines above it in the rendered file, and
// repeating it verbatim there made the generated config harder to read, not easier.
const nginxACMEFallbackHTTPS = `
    # Same rationale as the port-80 block above -- a vanity domain's own HTTPS vhost
    # doesn't exist until lfr-vanity-hook.sh has actually issued its certificate, and
    # until then this is the implicit default for a Host header nothing else matches. If
    # the HTTP-01 validator (or a real visitor) reaches this over HTTPS having already
    # been redirected from port 80, this stops the same fall-through into the tunnel (#979).
    location /.well-known/acme-challenge/ {
        root /var/www/lfr-tunnel-vanity;
        try_files $uri =404;
    }
`

// buildNginxConfig assembles the full sites-available content for a node.
//
// This is the ONE template both roles render from, and the one both entry points use:
// setup-central-vps.sh and setup-edge-vps.sh at provisioning time (via render-nginx-config)
// and reconcile-nginx against an already-provisioned box. #997 existed because central had two
// copies that drifted; #1442 because the edge had a third that no command could reach at all.
func buildNginxConfig(cfg nginxRenderConfig) string {
	var b strings.Builder
	b.WriteString(nginxUpgradeMapBlock)
	if cfg.Role == RoleEdge {
		b.WriteString(renderRealIPBlock(cfg.TrustedProxy))
	}
	for _, g := range cfg.Groups {
		if !g.WildcardOnly {
			b.WriteString(renderHTTPRedirect(cfg.Role, g))
			b.WriteString(renderApexBlock(cfg, g))
		}
		b.WriteString(renderWildcardBlock(cfg, g))
	}
	return b.String()
}

// renderHTTPRedirect writes the port-80 block: everything goes to HTTPS, except central's ACME
// fallback which has to be answerable over plain HTTP for HTTP-01 to work at all.
func renderHTTPRedirect(role nginxRole, g nginxDomainGroup) string {
	var b strings.Builder
	fmt.Fprintf(&b, `
# HTTP -> HTTPS redirect
server {
    listen 80;
    listen [::]:80;
    server_name %[1]s *.%[1]s;
`, g.Domain)
	if role == RoleCentral {
		b.WriteString(nginxACMEFallbackHTTP)
	}
	b.WriteString(`
    location / {
        return 301 https://$host$request_uri;
    }
}
`)
	return b.String()
}

// renderTLS writes the certificate stanza shared by every HTTPS block.
func renderTLS(g nginxDomainGroup) string {
	return fmt.Sprintf(`
    ssl_certificate %[2]s/%[1]s/fullchain.pem;
    ssl_certificate_key %[2]s/%[1]s/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;
`, g.Domain, g.CertRoot)
}

// renderApexBlock writes the block for the bare domain. On central that is the portal. On an
// edge it is a redirect to the control plane, plus the two paths an edge genuinely serves on
// its own hostname: /api/ for its control API and /tunnel for client connections.
func renderApexBlock(cfg nginxRenderConfig, g nginxDomainGroup) string {
	var b strings.Builder
	if cfg.Role == RoleCentral {
		b.WriteString("\n# Control plane / portal\n")
	} else {
		b.WriteString("\n# Edge apex: browsers go to the control plane, clients stay here\n")
	}
	fmt.Fprintf(&b, `server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name %s;
`, g.Domain)
	b.WriteString(renderTLS(g))

	if cfg.Role == RoleCentral {
		b.WriteString(nginxACMEFallbackHTTPS)
		// The same downloads block the wildcard server gets below (#1687). It was added to the
		// wildcard only (#955), so the apex served every other path -- the API, the portal,
		// install.sh, dashboard.js -- and 404d on the one path the installer needs. Two bugs
		// masked each other: #1684 sent every install to the apex, so the failure looked like
		// the installer's and this block's absence never surfaced on its own.
		b.WriteString(nginxClientDownloads)
		fmt.Fprintf(&b, `
    location / {
        proxy_pass http://127.0.0.1:%s;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
%s
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
`, cfg.LocalPort, nginxProxyHeaders)
	} else {
		fmt.Fprintf(&b, `
    # Nothing to serve a browser here -- the portal lives on the control plane.
    location / {
        return 301 https://%s$request_uri;
    }

    location /api/ {
        proxy_pass http://127.0.0.1:%s;
%s
        proxy_set_header X-Forwarded-Proto $scheme;
    }
`, cfg.RedirectDomain, cfg.LocalPort, nginxProxyHeaders)
	}

	fmt.Fprintf(&b, `
    location /tunnel {
        proxy_pass http://127.0.0.1:%s;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
%s
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
`, cfg.LocalPort, nginxProxyHeaders)
	return b.String()
}

// renderWildcardBlock writes the *.domain data plane, which is where tunnel traffic is
// actually served on both roles. Central additionally serves the signed client binaries from
// disk here: the Go app only ever serves /static/* from its compiled-in embed.FS, which never
// contains them, so without this /install's own download links 404 even though the files are
// on disk (#955).
func renderWildcardBlock(cfg nginxRenderConfig, g nginxDomainGroup) string {
	var b strings.Builder
	fmt.Fprintf(&b, `
# Wildcard data plane
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name *.%s;
`, g.Domain)
	b.WriteString(renderTLS(g))

	if cfg.Role == RoleCentral {
		b.WriteString(nginxClientDownloads)
	}

	fmt.Fprintf(&b, `
    location / {
        proxy_pass http://127.0.0.1:%s;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
%s
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header X-Forwarded-Proto https;

        client_max_body_size 500M;
        proxy_connect_timeout 120s;
        proxy_send_timeout 120s;
        proxy_read_timeout 120s;
    }
}
`, cfg.LocalPort, nginxProxyHeaders)
	return b.String()
}

// nginxFlags is the flag set shared by reconcile-nginx and render-nginx-config, so the two
// cannot grow different spellings or defaults for the same concept.
type nginxFlags struct {
	role              *string
	domains           *string
	apexDomains       *string
	certRoot          *string
	apexCertRoot      *string
	redirectDomain    *string
	trustedProxy      *string
	allowVhostRemoval *bool
}

func registerNginxFlags(fs *flag.FlagSet) nginxFlags {
	return nginxFlags{
		role: fs.String("role", string(RoleCentral),
			"node role: central (portal, ACME fallback, downloads) or edge (apex redirects to the control plane)"),
		domains: fs.String("domains", "",
			"comma-separated APEX domains this node owns, e.g. lfr-demo.se,lfr-demo.online -- never a hostname like tunnel.lfr-demo.se"),
		apexDomains: fs.String("apex-domains", "",
			"comma-separated apex domains served WILDCARD-ONLY here because another node owns the apex, e.g. an edge answering *.lfr-demo.se edge-direct"),
		certRoot: fs.String("cert-root", certRootLetsEncrypt,
			"directory holding <domain>/fullchain.pem for -domains (certbot's own live directory)"),
		apexCertRoot: fs.String("apex-cert-root", certRootCertSync,
			"directory holding <domain>/fullchain.pem for -apex-domains (where lfr-install-certs.sh puts a bundle pushed from central)"),
		redirectDomain: fs.String("redirect-domain", "",
			"edge only: where browser traffic arriving on the edge's own apex is sent, i.e. the control plane"),
		trustedProxy: fs.String("trusted-proxy", "",
			"edge only: the control plane's address (IP or CIDR), so a request it forwards is attributed to the visitor rather than to central (#1450)"),
		allowVhostRemoval: fs.Bool("allow-vhost-removal", false,
			"proceed even if the new config stops serving a server_name the live config serves (reconcile-nginx only)"),
	}
}

// nginxServiceLabels are first labels that mean "a host", not "a domain group". Rendering
// `server_name <label>.example.com *.<label>.example.com` for any of these is always a mistake:
// the vhost then answers only for names nobody uses, while the real ones fall through to
// whichever block nginx picked as the default.
var nginxServiceLabels = map[string]bool{
	"tunnel": true,
	"portal": true,
	"www":    true,
	"api":    true,
	"status": true,
	"mail":   true,
}

// checkApexDomain rejects a domain that is really a service hostname.
func checkApexDomain(d string) error {
	label, _, found := strings.Cut(d, ".")
	if !found {
		return nil
	}
	if nginxServiceLabels[strings.ToLower(label)] {
		return fmt.Errorf(
			"%q looks like a hostname, not a domain group: this renders `server_name %[1]s *.%[1]s`, "+
				"which serves nothing anybody requests. Pass the apex (e.g. %s) instead", d, strings.SplitN(d, ".", 2)[1])
	}
	return nil
}

// buildRenderConfigFromFlags validates the flags and assembles the render input.
//
// Refuses a service hostname where an apex is wanted. The template renders
// `server_name X *.X`, so passing tunnel.lfr-demo.se yields *.tunnel.lfr-demo.se and serves
// nothing anybody requests -- a mistake that looks like a perfectly successful reconcile, which
// is how it nearly deleted live vhosts once. Label-counting cannot tell an apex from a subdomain
// (co.uk, github.io) and an edge's own domain IS a subdomain (sa.lfr-demo.se), so this rejects
// only the shape that is always wrong: a first label that names a service rather than a place.
func buildRenderConfigFromFlags(f nginxFlags, port string) (nginxRenderConfig, error) {
	role := nginxRole(strings.TrimSpace(*f.role))
	if role != RoleCentral && role != RoleEdge {
		return nginxRenderConfig{}, fmt.Errorf("unknown -role %q: want %q or %q", *f.role, RoleCentral, RoleEdge)
	}

	owned := parseDomainsFlag(*f.domains)
	wildcardOnly := parseDomainsFlag(*f.apexDomains)
	if len(owned) == 0 && len(wildcardOnly) == 0 {
		return nginxRenderConfig{}, fmt.Errorf("at least one of -domains or -apex-domains is required")
	}
	if port == "" {
		return nginxRenderConfig{}, fmt.Errorf("-port is required and must match the live server-config.yaml's http_bind_addr")
	}
	if role == RoleEdge && len(owned) > 0 && strings.TrimSpace(*f.redirectDomain) == "" {
		return nginxRenderConfig{}, fmt.Errorf("-redirect-domain is required for -role edge: the edge's own apex has no portal to serve")
	}

	cfg := nginxRenderConfig{
		Role:           role,
		LocalPort:      port,
		RedirectDomain: strings.TrimSpace(*f.redirectDomain),
		TrustedProxy:   strings.TrimSpace(*f.trustedProxy),
	}
	// Refuse rather than silently ignore. Passing it for central reads as "central will now
	// attribute forwarded requests correctly", which is not a thing central does -- nothing
	// forwards to it.
	if role == RoleCentral && cfg.TrustedProxy != "" {
		return nginxRenderConfig{}, fmt.Errorf(
			"-trusted-proxy applies to -role edge only: it exists so a request the CONTROL PLANE " +
				"forwards is attributed to the visitor, and nothing forwards to central")
	}
	for _, d := range owned {
		if err := checkApexDomain(d); err != nil {
			return nginxRenderConfig{}, err
		}
		cfg.Groups = append(cfg.Groups, nginxDomainGroup{Domain: d, CertRoot: *f.certRoot})
	}
	for _, d := range wildcardOnly {
		if err := checkApexDomain(d); err != nil {
			return nginxRenderConfig{}, err
		}
		cfg.Groups = append(cfg.Groups, nginxDomainGroup{Domain: d, CertRoot: *f.apexCertRoot, WildcardOnly: true})
	}
	return cfg, nil
}

// serverNameDirective matches a server_name directive wherever it appears, including inline
// inside a `server { ... }` on one line. Deliberately not anchored to the start of a line: the
// configs this reads are hand-written ones found on live boxes, not only ones this tool wrote.
var serverNameDirective = regexp.MustCompile(`server_name\s+([^;]+);`)

// serverNamesIn extracts every server_name token from an nginx config, ignoring commented
// lines -- a name that is commented out is not being served, and counting it would make the
// removal guard report a loss that is not real.
func serverNamesIn(conf string) map[string]bool {
	names := map[string]bool{}
	for _, line := range strings.Split(conf, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, m := range serverNameDirective.FindAllStringSubmatch(line, -1) {
			for _, tok := range strings.Fields(m[1]) {
				if tok != "" {
					names[tok] = true
				}
			}
		}
	}
	return names
}

// vhostsLostBy returns the server_names the live config serves that the new one would not.
//
// reconcile-nginx replaces the file wholesale, so a -domains list that is merely INCOMPLETE
// silently stops serving whatever it omitted -- and nginx reports nothing, because a config
// that no longer mentions a name is perfectly valid. This is not hypothetical: the live edges
// still carry vhosts for their pre-rename aws-edge-* hostnames, which no -domains list anybody
// would think to write includes.
func vhostsLostBy(liveConf, newConf string) []string {
	live, next := serverNamesIn(liveConf), serverNamesIn(newConf)
	var lost []string
	for name := range live {
		if !next[name] {
			lost = append(lost, name)
		}
	}
	sort.Strings(lost)
	return lost
}

// nginxLegacyApexVhost is the hand-written apex-wildcard vhost found on the live edges, which
// reconcile stands down because the rendered config now contains those server blocks (#1443).
// It is listed here, not just in the remote script, because the removal guard has to account
// for it: this operation removes its content too, so its server_names are in scope.
const nginxLegacyApexVhost = "/etc/nginx/sites-enabled/lfr-apex-wildcards.conf"

// nginxReplacedPaths are the files whose content THIS operation removes -- the target it
// overwrites, plus any legacy vhost it stands down.
//
// Deliberately not every enabled vhost. A vanity domain's own conf.d/*.conf (written by
// lfr-vanity-hook.sh) is untouched by a reconcile, so counting its server_names would report
// every reconcile as destroying vhosts it never had anything to do with.
func nginxReplacedPaths(role nginxRole) []string {
	target, _ := nginxRemotePaths(role)
	if role == RoleEdge {
		return []string{target, nginxLegacyApexVhost}
	}
	return []string{target}
}

// nginxRemotePaths returns where a role's config lives on the box and what the enabling symlink
// is called. These are not cosmetic: an edge provisioned by setup-edge-vps.sh has its config at
// sites-available/lfr-tunneld enabled as sites-enabled/default, so writing central's
// sites-available/lfr-tunnel there would add a second config rather than replace the live one.
// That is exactly why reconcile-nginx could not be pointed at an edge before (#1442).
func nginxRemotePaths(role nginxRole) (target, link string) {
	if role == RoleEdge {
		return "/etc/nginx/sites-available/lfr-tunneld", "/etc/nginx/sites-enabled/default"
	}
	return "/etc/nginx/sites-available/lfr-tunnel", "/etc/nginx/sites-enabled/lfr-tunnel"
}

// resolveReconcileRenderConfig applies lfr-tunnel-ops.yaml's nginx: fallback and then validates
// through the same path an explicit -domains takes. Feeding the resolved list back through the
// shared flags matters: otherwise flag-supplied and file-supplied domains reach the template by
// different routes and only one of them gets checked.
func resolveReconcileRenderConfig(nf nginxFlags, port, flagTarget string) nginxRenderConfig {
	nginxTarget, err := ResolveNginxTarget(parseDomainsFlag(*nf.domains), port, flagTarget)
	CheckFatal(err, "Failed to resolve nginx target")

	resolved := strings.Join(nginxTarget.Domains, ",")
	nf.domains = &resolved

	renderCfg, err := buildRenderConfigFromFlags(nf, nginxTarget.Port)
	CheckFatal(err, "Invalid nginx configuration")
	return renderCfg
}

// describeServed renders the group list for the operator, marking wildcard-only groups so it is
// obvious from the output that the apex is NOT being claimed here.
func describeServed(groups []nginxDomainGroup) string {
	served := make([]string, 0, len(groups))
	for _, g := range groups {
		if g.WildcardOnly {
			served = append(served, "*."+g.Domain)
			continue
		}
		served = append(served, g.Domain)
	}
	return strings.Join(served, ", ")
}

// preflightVhostRemoval refuses a reconcile that would silently stop serving something.
//
// The generated config replaces the whole file, so a -domains list that is merely incomplete
// takes vhosts away -- and nginx cannot flag it, because a config that no longer mentions a
// name is perfectly valid and applies cleanly. Exits non-zero rather than returning an error:
// there is nothing useful to do with it further up, and continuing is the failure mode.
func preflightVhostRemoval(target DeployTarget, sshTarget string, role nginxRole, newConf string, allow bool) {
	liveConf, err := RunCommandCaptureOutput("ssh", "-i", target.IdentityFile, sshTarget,
		"sudo cat "+strings.Join(nginxReplacedPaths(role), " ")+" 2>/dev/null || true")
	if err != nil {
		fmt.Printf("WARNING: could not read the live config to compare against (%v).\n", err)
		fmt.Println("Proceeding blind: if the domain list is incomplete, this will stop serving whatever it omits.")
		return
	}

	lost := vhostsLostBy(liveConf, newConf)
	if len(lost) == 0 {
		return
	}

	fmt.Println("\nThis config would STOP SERVING server_names the box serves today:")
	for _, name := range lost {
		fmt.Println("  - " + name)
	}
	if allow {
		fmt.Println("\nProceeding anyway: -allow-vhost-removal was passed.")
		return
	}
	fmt.Println("\nRefusing to proceed. nginx cannot warn about this -- a config that no longer")
	fmt.Println("mentions a name is perfectly valid -- so the check has to happen here.")
	fmt.Println("Add the missing domains to -domains/-apex-domains, or pass -allow-vhost-removal")
	fmt.Println("if dropping them is genuinely what you want.")
	os.Exit(1)
}

// ReconcileNginxCommand regenerates the core lfr-tunneld nginx config (see #997) from the
// same template setup-central-vps.sh uses for initial provisioning, and pushes it to an
// already-running central -- closing the gap where a fix to that template (like #979's
// ACME-fallback location block) only ever reached a box on its first provision, never on a
// normal `deploy`. Safe to re-run repeatedly: the remote side backs up the existing config,
// swaps in the new one, and only reloads nginx if `nginx -t` passes -- otherwise it restores
// the backup and reloads that instead, so a bad reconcile can't leave the box without a
// working nginx config (the exact failure mode that took lfr-demo.se's ACME fallback down
// until it was manually SSH-patched).
func ReconcileNginxCommand(args []string) {
	fs := flag.NewFlagSet("reconcile-nginx", flag.ExitOnError)
	nf := registerNginxFlags(fs)
	port := fs.String("port", "", "local port lfr-tunneld binds to -- must match the live server-config.yaml's http_bind_addr (falls back to nginx.port in lfr-tunnel-ops.yaml)")
	identityFile := fs.String("i", "", "path to SSH private key file (falls back to VPS_USER,VPS_IP,LFT_IDENTITY_FILE env vars / lfr-tunnel-ops.yaml)")
	flagUser := fs.String("u", "", "SSH username on the central VPS (falls back to VPS_USER env var / lfr-tunnel-ops.yaml)")
	flagHost := fs.String("s", "", "SSH host of the central VPS (falls back to VPS_IP env var / lfr-tunnel-ops.yaml)")
	flagTarget := fs.String("target", "", "named target to use from a multi-target lfr-tunnel-ops.yaml (#1028)")
	fs.Usage = func() {
		fmt.Println("Usage: lfr-tunnel-ops reconcile-nginx [-role central|edge] [-domains <d1,d2,...>]")
		fmt.Println("       [-apex-domains <d1,d2,...>] [-redirect-domain <d>] [-port <port>]")
		fmt.Println("       [-allow-vhost-removal] [-i identity_file] [-u user] [-s host] [-target name]")
		fmt.Println("\nRegenerates the node's nginx config from the same template the provisioning")
		fmt.Println("scripts use, and pushes it over SSH. Works for a central OR an edge (#1442):")
		fmt.Println("-role picks which shape and which file on the box, since an edge's config lives")
		fmt.Println("at sites-available/lfr-tunneld enabled as sites-enabled/default.")
		fmt.Println("\n-domains takes APEX domains this node owns. -apex-domains takes domains served")
		fmt.Println("wildcard-only here because another node owns the apex -- an edge answering")
		fmt.Println("*.example.com edge-direct while the control plane keeps example.com itself.")
		fmt.Println("Their certificates come from different roots (-cert-root/-apex-cert-root),")
		fmt.Println("because a pushed wildcard bundle is not in certbot's live directory.")
		fmt.Println("\nBefore writing anything it compares against what the box serves today and")
		fmt.Println("REFUSES if the new config would stop serving a name the old one served --")
		fmt.Println("nginx cannot warn about that, since a config omitting a name is still valid.")
		fmt.Println("Override with -allow-vhost-removal only when dropping them is intended.")
		fmt.Println("\nBacks up the existing config, swaps in the new one,")
		fmt.Println("runs `nginx -t`, and only reloads nginx if that passes -- otherwise restores")
		fmt.Println("the backup and reloads that instead. Real, live effect on production")
		fmt.Println("traffic; safe to re-run repeatedly. The target (and domains/port, if not")
		fmt.Println("passed as flags) is resolved from env vars / lfr-tunnel-ops.yaml -- see")
		fmt.Println("lfr-tunnel-ops.yaml.example. -target/LFT_OPS_TARGET selects which named")
		fmt.Println("target to use from a multi-target config file.")
	}
	if IsHelpRequest(args) {
		fs.Usage()
		return
	}
	if err := fs.Parse(args); err != nil {
		CheckFatal(err, "Failed to parse arguments")
	}

	renderCfg := resolveReconcileRenderConfig(nf, *port, *flagTarget)

	target, err := ResolveDeployTarget(*flagUser, *flagHost, *identityFile, *flagTarget)
	CheckFatal(err, "Failed to resolve deployment target")
	sshTarget := fmt.Sprintf("%s@%s", target.User, target.Host)

	fmt.Printf("=== Reconciling nginx config on %s (role: %s) for: %s ===\n",
		sshTarget, renderCfg.Role, describeServed(renderCfg.Groups))

	config := buildNginxConfig(renderCfg)

	preflightVhostRemoval(target, sshTarget, renderCfg.Role, config, *nf.allowVhostRemoval)

	applyNginxConfigRemotely(target, sshTarget, renderCfg.Role, config)
	fmt.Println("=== Nginx Reconcile Complete! ===")
}

// RenderNginxConfigCommand prints the nginx config for one or more domain groups to
// stdout, using the exact same buildNginxConfig template ReconcileNginxCommand uses.
// Purely local -- no SSH, no deployment target needed. This exists so
// scripts/common/setup-central-vps.sh (bash, initial provisioning) and reconcile-nginx (Go,
// re-syncing an already-provisioned box) generate nginx config from exactly one source of
// truth instead of two independently hand-maintained copies drifting apart, which is
// precisely why #997 existed in the first place (#1026).
func RenderNginxConfigCommand(args []string) {
	fs := flag.NewFlagSet("render-nginx-config", flag.ExitOnError)
	nf := registerNginxFlags(fs)
	port := fs.String("port", "", "local port lfr-tunneld binds to (required)")
	fs.Usage = func() {
		fmt.Println("Usage: lfr-tunnel-ops render-nginx-config [-role central|edge] -domains <d1,d2,...>")
		fmt.Println("       [-apex-domains <d1,d2,...>] [-redirect-domain <d>] -port <port>")
		fmt.Println("\nPrints a node's nginx config to stdout -- the exact same template")
		fmt.Println("reconcile-nginx uses to re-sync an already-provisioned box. Purely local: no")
		fmt.Println("SSH, no deployment target involved. Used by setup-central-vps.sh AND")
		fmt.Println("setup-edge-vps.sh during initial provisioning, so provisioning and reconciling")
		fmt.Println("cannot drift apart -- which is what left the live edges running a hand-written")
		fmt.Println("config with no source in this repo (#1442, #1443).")
	}
	if IsHelpRequest(args) {
		fs.Usage()
		return
	}
	if err := fs.Parse(args); err != nil {
		CheckFatal(err, "Failed to parse arguments")
	}

	renderCfg, err := buildRenderConfigFromFlags(nf, *port)
	if err != nil {
		fmt.Println("ERROR: " + err.Error())
		fs.Usage()
		os.Exit(1)
	}

	fmt.Print(buildNginxConfig(renderCfg))
}

// parseDomainsFlag splits a comma-separated -domains flag value into a trimmed,
// empty-entry-free list, shared by reconcile-nginx and render-nginx-config so their
// (identical) handling of the same flag can't drift apart from each other.
func parseDomainsFlag(csv string) []string {
	var domains []string
	for _, d := range strings.Split(csv, ",") {
		d = strings.TrimSpace(d)
		if d != "" {
			domains = append(domains, d)
		}
	}
	return domains
}

// applyNginxConfigRemotely stages the rendered config, swaps it in behind a backup, and only
// reloads nginx if `nginx -t` passes -- restoring the backup and reloading that if it does not,
// so a bad reconcile cannot leave the box with no working config. Extracted from
// ReconcileNginxCommand unchanged; the rollback shape is the part that matters and it is the
// reason this is safe to re-run.
func applyNginxConfigRemotely(target DeployTarget, sshTarget string, role nginxRole, config string) {
	tmpPath := fmt.Sprintf("/tmp/lfr-tunneld-nginx-reconcile-%d.conf", time.Now().UnixNano())
	// 0600: a generated nginx config staged in a world-readable /tmp, read only by the scp
	// below (#1408). The predictable filename is a separate matter, untouched here.
	if err := os.WriteFile(tmpPath, []byte(config), 0o600); err != nil {
		CheckFatal(err, "Failed to write local temp nginx config")
	}
	defer os.Remove(tmpPath) //nolint:errcheck

	remoteTmp := "/home/" + target.User + "/lfr-tunneld-nginx-reconcile.conf"
	fmt.Println("Uploading generated nginx config...")
	err := RunCommand("scp", "-i", target.IdentityFile, tmpPath, sshTarget+":"+remoteTmp)
	CheckFatal(err, "Failed to SCP generated nginx config")

	remoteTarget, remoteLink := nginxRemotePaths(role)
	remoteScript := `
set -e
TARGET=` + remoteTarget + `
LINK=` + remoteLink + `
NEW="` + remoteTmp + `"
STAMP=$(date +%Y%m%d-%H%M%S)
BACKUP="$TARGET.backup-$STAMP"

if [ -f "$TARGET" ]; then
	sudo cp "$TARGET" "$BACKUP"
	echo "Backed up existing config to $BACKUP"
fi

sudo cp "$NEW" "$TARGET"
sudo ln -sf "$TARGET" "$LINK"
rm -f "$NEW"

# A node that was hand-patched with a separate apex-wildcard vhost now has those server blocks
# in $TARGET as well, and nginx treats a duplicate server_name as a WARNING -- nginx -t passes
# and one block silently wins. So the old vhost has to be stood down in the same operation, not
# left to be noticed later (#1443). Renamed rather than deleted, and restored by the rollback
# path below if the new config does not hold.
LEGACY=/etc/nginx/sites-enabled/lfr-apex-wildcards.conf
RETIRED=/etc/nginx/sites-available/lfr-apex-wildcards.conf.retired-$STAMP
if [ -L "$LEGACY" ] || [ -f "$LEGACY" ]; then
	# OUT of sites-enabled, not renamed within it. nginx.conf includes that directory with a
	# bare glob and no extension filter, so a renamed file is still
	# loaded and both copies of the apex blocks stay active -- which reproduced the very
	# duplicate-server_name hazard this retirement exists to prevent (#1470). sites-available is
	# not included, so the file survives as a record without being served. mv moves the symlink
	# itself when it is one, so this works either way.
	sudo mv "$LEGACY" "$RETIRED"
	echo "Stood down legacy apex vhost -> $RETIRED"
fi

NGINX_TEST=$(sudo nginx -t 2>&1)
echo "$NGINX_TEST"
# A conflicting server_name is reported by nginx as a WARNING, not an error, so nginx -t
# still exits 0 and says "test is successful" while one of the two blocks is being silently
# ignored. Gating on the exit status alone therefore accepts exactly the failure this whole
# change exists to prevent (#1470), so success requires BOTH conditions -- and anything else
# falls through to the same rollback, rather than exiting early under set -e and leaving the
# retired vhost stood down.
if echo "$NGINX_TEST" | grep -q "test is successful" && ! echo "$NGINX_TEST" | grep -q "conflicting server name"; then
	sudo systemctl reload nginx
	echo "RECONCILE_OK"
else
	if echo "$NGINX_TEST" | grep -q "conflicting server name"; then
		echo "New config declares a server_name another vhost already serves; nginx would keep"
		echo "whichever it loaded first and ignore the other silently. Rolling back."
	else
		echo "New config failed nginx -t -- rolling back to the previous config."
	fi
	if [ -L "$RETIRED" ] || [ -f "$RETIRED" ]; then
		sudo mv "$RETIRED" "$LEGACY"
		echo "Restored the legacy apex vhost."
	fi
	if [ -f "$BACKUP" ]; then
		sudo cp "$BACKUP" "$TARGET"
		if sudo nginx -t; then
			sudo systemctl reload nginx
			echo "Rolled back successfully; nginx is running the previous config."
		else
			echo "WARNING: the previous config also failed nginx -t after rollback -- manual intervention required on $TARGET."
		fi
	else
		echo "WARNING: no previous config to roll back to -- removing the broken one. nginx is now running with no site config for lfr-tunnel."
		sudo rm -f "$TARGET"
	fi
	exit 1
fi
`
	fmt.Println("Applying config on the remote host (backup, swap, nginx -t, reload -- with automatic rollback on failure)...")
	err = RunCommand("ssh", "-i", target.IdentityFile, sshTarget, remoteScript)
	CheckFatal(err, "Reconcile failed -- see output above; the remote side should already have rolled back to its previous working config")
}
