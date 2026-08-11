package ops

import (
	"flag"
	"fmt"
	"os"
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

// nginxDomainBlockTemplate is the exact per-domain nginx config setup-central-vps.sh writes
// for a single `-d <domain>` -- see that script for the full rationale behind each location
// block (in particular the #979 ACME-fallback comment). Kept as a literal copy here rather
// than a shared source file so `lfr-tunnel-ops reconcile-nginx` (Go, runs from an operator's
// machine) and setup-central-vps.sh (bash, runs during initial provisioning) don't need to
// share a build/runtime dependency on each other -- but a change to one MUST be mirrored in
// the other, since #997 exists precisely because the two had drifted apart.
const nginxDomainBlockTemplate = `
# HTTP -> HTTPS redirect
server {
    listen 80;
    listen [::]:80;
    server_name %[1]s *.%[1]s;

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

    location / {
        return 301 https://$host$request_uri;
    }
}

# Control plane / portal
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name %[1]s;

    ssl_certificate /etc/letsencrypt/live/%[1]s/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/%[1]s/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

    # Same rationale as the port-80 block above -- a vanity domain's own HTTPS vhost
    # doesn't exist until lfr-vanity-hook.sh has actually issued its certificate, and
    # until then this is the implicit default for a Host header nothing else matches. If
    # the HTTP-01 validator (or a real visitor) reaches this over HTTPS having already
    # been redirected from port 80, this stops the same fall-through into the tunnel (#979).
    location /.well-known/acme-challenge/ {
        root /var/www/lfr-tunnel-vanity;
        try_files $uri =404;
    }

    location / {
        proxy_pass http://127.0.0.1:%[2]s;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location /tunnel {
        proxy_pass http://127.0.0.1:%[2]s;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}

# Wildcard data plane
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name *.%[1]s;

    ssl_certificate /etc/letsencrypt/live/%[1]s/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/%[1]s/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

    # Signed client binaries/checksums (populated by lfr-tunnel-ops deploy-clients) are served
    # directly from disk here, bypassing the Go app entirely -- it only ever serves /static/*
    # from its own compiled-in embed.FS, which never contains these. Without this block,
    # /install's own download links 404 even though the files exist on disk (see #949's
    # follow-up, #955).
    location /static/downloads/ {
        alias /var/www/lfr-tunnel/static/downloads/;
        autoindex off;
        add_header Content-Disposition 'attachment';
    }

    location / {
        proxy_pass http://127.0.0.1:%[2]s;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header X-Forwarded-Proto https;

        client_max_body_size 500M;
        proxy_connect_timeout 120s;
        proxy_send_timeout 120s;
        proxy_read_timeout 120s;
    }
}
`

// buildNginxConfig assembles the full sites-available/lfr-tunnel content for one or more
// domain groups sharing a single lfr-tunneld instance on localPort -- the live central
// (lfr-demo.se) has run with two domain groups (lfr-demo.se and lfr-demo.online) since the
// AWS migration, so this must support more than one even though setup-central-vps.sh's own
// -d flag only ever provisions one at a time.
func buildNginxConfig(domains []string, localPort string) string {
	var b strings.Builder
	b.WriteString(nginxUpgradeMapBlock)
	for _, d := range domains {
		fmt.Fprintf(&b, nginxDomainBlockTemplate, d, localPort)
	}
	return b.String()
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
	domainsFlag := fs.String("domains", "", "comma-separated domain groups this central serves, e.g. lfr-demo.se,lfr-demo.online (falls back to nginx.domains in lfr-tunnel-ops.yaml)")
	port := fs.String("port", "", "local port lfr-tunneld binds to -- must match the live server-config.yaml's http_bind_addr (falls back to nginx.port in lfr-tunnel-ops.yaml)")
	identityFile := fs.String("i", "", "path to SSH private key file (falls back to VPS_USER,VPS_IP,LFT_IDENTITY_FILE env vars / lfr-tunnel-ops.yaml)")
	fs.Usage = func() {
		fmt.Println("Usage: lfr-tunnel-ops reconcile-nginx [-domains <d1,d2,...>] [-port <port>] [-i identity_file]")
		fmt.Println("\nRegenerates /etc/nginx/sites-available/lfr-tunnel from the same template")
		fmt.Println("setup-central-vps.sh uses for initial provisioning, and pushes it to the")
		fmt.Println("central VPS over SSH. Backs up the existing config, swaps in the new one,")
		fmt.Println("runs `nginx -t`, and only reloads nginx if that passes -- otherwise restores")
		fmt.Println("the backup and reloads that instead. Real, live effect on production")
		fmt.Println("traffic; safe to re-run repeatedly. The target (and domains/port, if not")
		fmt.Println("passed as flags) is resolved from env vars / lfr-tunnel-ops.yaml -- see")
		fmt.Println("lfr-tunnel-ops.yaml.example.")
	}
	if IsHelpRequest(args) {
		fs.Usage()
		return
	}
	if err := fs.Parse(args); err != nil {
		CheckFatal(err, "Failed to parse arguments")
	}

	var flagDomains []string
	for _, d := range strings.Split(*domainsFlag, ",") {
		d = strings.TrimSpace(d)
		if d != "" {
			flagDomains = append(flagDomains, d)
		}
	}

	nginxTarget, err := ResolveNginxTarget(flagDomains, *port)
	CheckFatal(err, "Failed to resolve nginx target")
	domains := nginxTarget.Domains

	target, err := ResolveDeployTarget("", "", *identityFile)
	CheckFatal(err, "Failed to resolve deployment target")
	sshTarget := fmt.Sprintf("%s@%s", target.User, target.Host)

	fmt.Printf("=== Reconciling nginx config on %s for domains: %s ===\n", sshTarget, strings.Join(domains, ", "))

	config := buildNginxConfig(domains, nginxTarget.Port)

	tmpPath := fmt.Sprintf("/tmp/lfr-tunneld-nginx-reconcile-%d.conf", time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, []byte(config), 0644); err != nil {
		CheckFatal(err, "Failed to write local temp nginx config")
	}
	defer os.Remove(tmpPath) //nolint:errcheck

	remoteTmp := "/home/" + target.User + "/lfr-tunneld-nginx-reconcile.conf"
	fmt.Println("Uploading generated nginx config...")
	err = RunCommand("scp", "-i", target.IdentityFile, tmpPath, sshTarget+":"+remoteTmp)
	CheckFatal(err, "Failed to SCP generated nginx config")

	remoteScript := `
set -e
TARGET=/etc/nginx/sites-available/lfr-tunnel
NEW="` + remoteTmp + `"
STAMP=$(date +%Y%m%d-%H%M%S)
BACKUP="$TARGET.backup-$STAMP"

if [ -f "$TARGET" ]; then
	sudo cp "$TARGET" "$BACKUP"
	echo "Backed up existing config to $BACKUP"
fi

sudo cp "$NEW" "$TARGET"
sudo ln -sf "$TARGET" /etc/nginx/sites-enabled/lfr-tunnel
rm -f "$NEW"

if sudo nginx -t; then
	sudo systemctl reload nginx
	echo "RECONCILE_OK"
else
	echo "New config failed nginx -t -- rolling back to the previous config."
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

	fmt.Println("=== Nginx Reconcile Complete! ===")
}
