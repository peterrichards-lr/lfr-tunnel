#!/usr/bin/env bash
# scripts/common/setup-edge-vps.sh
# Automates setting up a stateless regional edge VPS node for lfr-tunnel.
set -e

# This is a generic, reusable script -- it carries no default values of its
# own. Every parameter must be supplied explicitly by the caller (a
# Liferay-specific wrapper, or you, the operator), which is the only place
# that actually knows the right values for a given deployment.
SSH_USER=""
DOMAINS=""
CONTROL_PLANE_URL=""
EDGE_PORT=""
EDGE_TOKEN=""
SSH_KEY_ARG=""
KEY_PATH=""
VPS_IP=""
REDIRECT_DOMAIN=""
TUNNEL_DOMAINS=""
# Which Certbot DNS-01 plugin proves domain control. No default on purpose: this is the
# generic layer, and which provider is right is a property of the deployment, not of this
# tool (#1015/#1016/#1187).
DNS_AUTHENTICATOR=""
DNS_AUTHENTICATOR_ARGS=""
# Which dynamic-DNS updater to install, if any. Default none: a node with a static or elastic
# address does not need one, and installing an updater that cannot reach its provider is worse
# than installing nothing -- it runs every five minutes, fails, and exits 0, so systemd records
# success while the records go stale (#1300).
DDNS_PROVIDER="none"

usage() {
  echo "Usage: $0 -s <vps_ip> -t <edge_token> -r <redirect_domain> -i <identity_file> -u <ssh_user> -d <domains> -c <control_plane_url> -p <port> [-a <tunnel_domains>]"
  echo "  -s: VPS Public IP address (required)"
  echo "  -t: Plaintext Edge Token for Control Plane validation (required)"
  echo "  -r: Domain to redirect root browser traffic to (required), e.g. your control plane's landing page"
  echo "  -i: Path to SSH private key file (required)"
  echo "  -u: SSH username (required)"
  echo "  -d: Comma-separated list of edge domains (required)"
  echo "  -c: Control Plane URL (required)"
  echo "  -p: Port for lfr-tunneld to bind to on Edge node (required)"
  echo "  -D: Dynamic DNS updater to install: none (default), cloudflare, or route53."
  echo "      Only needed when this node's public address can change. A node with a static"
  echo "      or elastic address should leave this as none."
  echo "  -n: Certbot DNS-01 authenticator to prove domain control with (required), e.g."
  echo "      dns-route53, dns-cloudflare, dns-digitalocean, dns-google. Installs"
  echo "      python3-certbot-<authenticator> and passes --<authenticator> to certbot."
  echo "  -N: Extra arguments for that authenticator (optional), e.g."
  echo "      \"--dns-cloudflare-credentials /etc/letsencrypt/cloudflare.ini\""
  echo "  -a: Comma-separated subset of -d that tunnels may be issued on (optional)."
  echo "      Set this to the shared domain so a tunnel's public URL never names the edge"
  echo "      serving it, and does not change when the client moves (see #1285). The regional"
  echo "      names stay in -d for direct addressing. Omitted, any domain in -d may be used."
  exit 1
}

# Parse parameters
while getopts "s:t:i:u:d:c:p:r:a:n:N:D:" opt; do
  case $opt in
    s) VPS_IP="$OPTARG" ;;
    t) EDGE_TOKEN="$OPTARG" ;;
    i)
      KEY_PATH="$OPTARG"
      if [[ "$KEY_PATH" == "~/"* ]]; then
        KEY_PATH="${HOME}/${KEY_PATH#~/}"
      elif [[ "$KEY_PATH" == "~" ]]; then
        KEY_PATH="${HOME}"
      fi
      SSH_KEY_ARG="-i $KEY_PATH"
      ;;
    u) SSH_USER="$OPTARG" ;;
    d) DOMAINS="$OPTARG" ;;
    c) CONTROL_PLANE_URL="$OPTARG" ;;
    p) EDGE_PORT="$OPTARG" ;;
    r) REDIRECT_DOMAIN="$OPTARG" ;;
    a) TUNNEL_DOMAINS="$OPTARG" ;;
    n) DNS_AUTHENTICATOR="$OPTARG" ;;
    D) DDNS_PROVIDER="$OPTARG" ;;
    N) DNS_AUTHENTICATOR_ARGS="$OPTARG" ;;
    *) usage ;;
  esac
done

if [ -z "$VPS_IP" ] || [ -z "$EDGE_TOKEN" ] || [ -z "$REDIRECT_DOMAIN" ] || [ -z "$KEY_PATH" ] || \
   [ -z "$SSH_USER" ] || [ -z "$DOMAINS" ] || [ -z "$CONTROL_PLANE_URL" ] || [ -z "$EDGE_PORT" ] || \
   [ -z "$DNS_AUTHENTICATOR" ]; then
  echo "❌ Error: -s, -t, -r, -i, -u, -d, -c, -p and -n are all required parameters."
  usage
fi

case "$DDNS_PROVIDER" in
  none|cloudflare|route53) ;;
  *)
    echo "❌ Error: -D must be none, cloudflare or route53 (got '$DDNS_PROVIDER')."
    usage
    ;;
esac

echo "=== Starting Edge VPS Automation for IP: $VPS_IP ==="


# 1. Build Linux amd64 binary locally (compatible with standard GCP e2-micro / AWS t3.nano x86_64)
VERSION="$(grep -oE 'Version = "[^"]+"' pkg/config/version.go | cut -d'"' -f2)"
echo "=> Compiling lfr-tunneld for Linux (amd64) with Version=$VERSION..."
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o bin/lfr-tunneld-edge-linux ./cmd/lfr-tunneld

# 2. Update and install packages on the remote VPS (including security hardening packages)
echo "=> Connecting to $VPS_IP to install dependencies (Nginx, Certbot, UFW, Fail2ban)..."
ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP << 'REMOTE_SSH'
  sudo apt-get update
  sudo apt-get install -y nginx certbot curl jq ufw fail2ban unattended-upgrades
REMOTE_SSH

# 3. Request wildcard Let's Encrypt certificates using whichever Certbot DNS-01 plugin the
#    operator named. The plugin is a parameter, not a constant -- hardcoding --dns-cloudflare
#    here is how #1297 survived the move to another DNS provider unnoticed.
IFS=',' read -r -a DOMAIN_ARRAY <<< "$DOMAINS"
for DOMAIN in "${DOMAIN_ARRAY[@]}"; do
  echo "=========================================================="
  echo "=> Provisioning Wildcard SSL Certificate for $DOMAIN & *.$DOMAIN"
  echo "=========================================================="

  ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP "sudo apt-get install -y python3-certbot-$DNS_AUTHENTICATOR"
  ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP "sudo certbot certonly \
    --$DNS_AUTHENTICATOR $DNS_AUTHENTICATOR_ARGS \
    --agree-tos \
    --non-interactive \
    --register-unsafely-without-email \
    -d '$DOMAIN' \
    -d '*.$DOMAIN'"
done

# 4. Generate stateless server-config.yaml locally and upload
echo "=> Generating server-config.yaml locally..."
CONFIG_TMP="/tmp/edge-server-config.yaml"
cat > "$CONFIG_TMP" << EOF
domains:
EOF

for DOMAIN in "${DOMAIN_ARRAY[@]}"; do
  echo "  - \"$DOMAIN\"" >> "$CONFIG_TMP"
done

if [ -n "$TUNNEL_DOMAINS" ]; then
  echo "tunnel_domains:" >> "$CONFIG_TMP"
  IFS=',' read -ra TUNNEL_DOMAIN_ARRAY <<< "$TUNNEL_DOMAINS"
  for TDOMAIN in "${TUNNEL_DOMAIN_ARRAY[@]}"; do
    echo "  - \"$TDOMAIN\"" >> "$CONFIG_TMP"
  done
fi

cat >> "$CONFIG_TMP" << EOF
http_bind_addr: "127.0.0.1:$EDGE_PORT"
db_path: "" # Stateless Edge mode
control_plane_url: "$CONTROL_PLANE_URL"
edge_token: "$EDGE_TOKEN"
EOF

echo "=> Uploading server-config.yaml..."
scp $SSH_KEY_ARG "$CONFIG_TMP" $SSH_USER@$VPS_IP:/home/$SSH_USER/server-config.yaml
rm -f "$CONFIG_TMP"

# 5. Generate Nginx virtual hosts configuration locally and upload
echo "=> Generating Nginx configuration locally..."
NGINX_TMP="/tmp/nginx-edge.conf"
cat > "$NGINX_TMP" << 'EOF'
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}
EOF

for DOMAIN in "${DOMAIN_ARRAY[@]}"; do
  cat >> "$NGINX_TMP" << EOF

# HTTP redirect to HTTPS
server {
    listen 80;
    listen [::]:80;
    server_name $DOMAIN *.$DOMAIN;
    return 301 https://\$host\$request_uri;
}

# Base domain HTTPS
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name $DOMAIN;

    ssl_certificate /etc/letsencrypt/live/$DOMAIN/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/$DOMAIN/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

    # Redirect root browser traffic to control plane landing page
    location / {
        return 301 https://$REDIRECT_DOMAIN\$request_uri;
    }

    location /api/ {
        proxy_pass http://127.0.0.1:$EDGE_PORT;
        proxy_set_header Host \$http_host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
    }

    location /tunnel {
        proxy_pass http://127.0.0.1:$EDGE_PORT;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$connection_upgrade;
        proxy_set_header Host \$http_host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
    }
}

# Wildcard subdomains HTTPS
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name *.$DOMAIN;

    ssl_certificate /etc/letsencrypt/live/$DOMAIN/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/$DOMAIN/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

    location / {
        proxy_pass http://127.0.0.1:$EDGE_PORT;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$connection_upgrade;
        proxy_set_header Host \$http_host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Host \$http_host;
        proxy_set_header X-Forwarded-Proto https;
    }
}
EOF
done

echo "=> Uploading Nginx configuration..."
scp $SSH_KEY_ARG "$NGINX_TMP" $SSH_USER@$VPS_IP:/home/$SSH_USER/lfr-tunneld-nginx.conf
rm -f "$NGINX_TMP"

# 6. Upload compiled binary and assets
echo "=> Uploading binary to VPS..."
scp $SSH_KEY_ARG bin/lfr-tunneld-edge-linux $SSH_USER@$VPS_IP:/home/$SSH_USER/lfr-tunneld

echo "=> Uploading error pages..."
scp $SSH_KEY_ARG -r resources/server/error_pages $SSH_USER@$VPS_IP:/home/$SSH_USER/

echo "=> Uploading static assets..."
scp $SSH_KEY_ARG -r pkg/server/static $SSH_USER@$VPS_IP:/home/$SSH_USER/

# 7. Upload self-healing watchdog and DDNS scripts. gateway-watchdog.sh itself has no
#    default port -- it requires LFT_BACKEND_PORT to be set, so it's uploaded unmodified;
#    the systemd unit's placeholder env var is what actually supplies this edge's $EDGE_PORT.
echo "=> Uploading watchdog, DDNS, and systemd overrides..."
scp $SSH_KEY_ARG scripts/common/gateway-watchdog.sh $SSH_USER@$VPS_IP:/home/$SSH_USER/gateway-watchdog.sh

sed "s/__BACKEND_PORT__/$EDGE_PORT/" scripts/common/gateway-watchdog.service > /tmp/gateway-watchdog-edge.service
scp $SSH_KEY_ARG /tmp/gateway-watchdog-edge.service $SSH_USER@$VPS_IP:/home/$SSH_USER/gateway-watchdog.service
rm -f /tmp/gateway-watchdog-edge.service

scp $SSH_KEY_ARG scripts/common/nginx-override.conf $SSH_USER@$VPS_IP:/home/$SSH_USER/nginx-override.conf
scp $SSH_KEY_ARG scripts/common/gateway-watchdog.timer $SSH_USER@$VPS_IP:/home/$SSH_USER/

# Upload Edge DDNS Script, plus this instance's own domains file — the DDNS script is
# shared verbatim across every edge, so it reads which domain(s) are actually *its own*
# from this file rather than having them hardcoded (see the *-ddns-edge.sh scripts).
if [ "$DDNS_PROVIDER" != "none" ]; then
  scp $SSH_KEY_ARG "scripts/common/${DDNS_PROVIDER}-ddns-edge.sh" $SSH_USER@$VPS_IP:/home/$SSH_USER/lfr-ddns-edge.sh
fi

DDNS_DOMAINS_TMP="/tmp/ddns-domains.txt"
printf '%s\n' "${DOMAIN_ARRAY[@]}" > "$DDNS_DOMAINS_TMP"
scp $SSH_KEY_ARG "$DDNS_DOMAINS_TMP" $SSH_USER@$VPS_IP:/home/$SSH_USER/ddns-domains.txt
rm -f "$DDNS_DOMAINS_TMP"

# 8. Remotely execute setup and service configurations
echo "=> Registering services and securing files on VPS..."
ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP << REMOTE_SSH
  # Create system user lfr-tunnel
  if ! id "lfr-tunnel" &>/dev/null; then
    echo "Creating system user lfr-tunnel..."
    sudo useradd -r -s /bin/false lfr-tunnel
  fi

  # Binary installation
  sudo mv /home/$SSH_USER/lfr-tunneld /usr/local/bin/lfr-tunneld
  sudo chmod +x /usr/local/bin/lfr-tunneld

  # Static assets
  sudo mkdir -p /var/www/lfr-tunnel/error_pages
  sudo cp -r /home/$SSH_USER/error_pages/* /var/www/lfr-tunnel/error_pages/
  sudo mkdir -p /var/www/lfr-tunnel/static
  sudo cp -r /home/$SSH_USER/static/* /var/www/lfr-tunnel/static/
  rm -rf /home/$SSH_USER/error_pages /home/$SSH_USER/static

  # Config setup
  sudo mkdir -p /etc/lfr-tunneld
  if [ -f /etc/lfr-tunneld/server-config.yaml ]; then
    sudo cp /etc/lfr-tunneld/server-config.yaml /etc/lfr-tunneld/server-config.yaml.backup-\$(date +%Y-%m-%d_%H-%M-%S)
  fi
  sudo mv /home/$SSH_USER/server-config.yaml /etc/lfr-tunneld/server-config.yaml
  sudo chown -R lfr-tunnel:lfr-tunnel /etc/lfr-tunneld
  sudo chmod 700 /etc/lfr-tunneld
  sudo chmod 600 /etc/lfr-tunneld/server-config.yaml

  # Nginx config setup
  sudo mv /home/$SSH_USER/lfr-tunneld-nginx.conf /etc/nginx/sites-available/lfr-tunneld
  sudo ln -sf /etc/nginx/sites-available/lfr-tunneld /etc/nginx/sites-enabled/default
  sudo rm -f /etc/nginx/sites-enabled/default-backup

  # systemd configuration
  echo "Creating systemd configuration for lfr-tunneld..."
  sudo tee /etc/systemd/system/lfr-tunneld.service > /dev/null << EOF
[Unit]
Description=Liferay Tunnel Gateway Daemon (Edge Mode)
After=network.target

[Service]
Type=simple
User=lfr-tunnel
Group=lfr-tunnel
WorkingDirectory=/etc/lfr-tunneld
ExecStart=/usr/local/bin/lfr-tunneld --config /etc/lfr-tunneld/server-config.yaml
Restart=on-failure
RestartSec=5s

# Security Hardening (systemd Sandboxing)
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
NoNewPrivileges=true
CapabilityBoundingSet=
ReadOnlyPaths=/usr/local/bin/lfr-tunneld
ReadWritePaths=/etc/lfr-tunneld

[Install]
WantedBy=multi-user.target
EOF

  # Deploy systemd override for Nginx auto-restart
  sudo mkdir -p /etc/systemd/system/nginx.service.d/
  sudo mv /home/$SSH_USER/nginx-override.conf /etc/systemd/system/nginx.service.d/override.conf
  sudo chown root:root /etc/systemd/system/nginx.service.d/override.conf

  # Active watchdog configuration
  sudo mv /home/$SSH_USER/gateway-watchdog.sh /usr/local/bin/gateway-watchdog.sh
  sudo chmod 700 /usr/local/bin/gateway-watchdog.sh
  sudo chown root:root /usr/local/bin/gateway-watchdog.sh

  sudo mv /home/$SSH_USER/gateway-watchdog.service /etc/systemd/system/gateway-watchdog.service
  sudo mv /home/$SSH_USER/gateway-watchdog.timer /etc/systemd/system/gateway-watchdog.timer
  sudo chown root:root /etc/systemd/system/gateway-watchdog.service /etc/systemd/system/gateway-watchdog.timer

  sudo mkdir -p /etc/lfr-tunneld
  sudo mv /home/$SSH_USER/ddns-domains.txt /etc/lfr-tunneld/ddns-domains.txt
  sudo chmod 644 /etc/lfr-tunneld/ddns-domains.txt
  sudo chown root:root /etc/lfr-tunneld/ddns-domains.txt

  # Dynamic DNS, only when one was asked for (-D). This used to install a Cloudflare updater
  # unconditionally, which is how every edge ended up running one that could not reach its
  # provider after the zones moved: it fired every five minutes, failed, and exited 0, so
  # systemd recorded success for two weeks (#1300).
  if [ "$DDNS_PROVIDER" != "none" ]; then
    sudo mv /home/$SSH_USER/lfr-ddns-edge.sh /usr/local/bin/lfr-ddns-edge.sh
    sudo chmod 700 /usr/local/bin/lfr-ddns-edge.sh
    sudo chown root:root /usr/local/bin/lfr-ddns-edge.sh

    sudo tee /etc/systemd/system/lfr-ddns-edge.service > /dev/null << EOF
[Unit]
Description=Dynamic DNS (Edge Subdomains) Updater -- $DDNS_PROVIDER
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/lfr-ddns-edge.sh
User=root
Group=root
EOF

    sudo tee /etc/systemd/system/lfr-ddns-edge.timer > /dev/null << EOF
[Unit]
Description=Trigger Dynamic DNS (Edge Subdomains) update every 5 minutes

[Timer]
OnBootSec=1min
OnUnitActiveSec=5min
# So a node that was asleep when the timer was due still updates on the next boot, rather
# than waiting a further five minutes with a stale record.
Persistent=true

[Install]
WantedBy=timers.target
EOF

    sudo chown root:root /etc/systemd/system/lfr-ddns-edge.service /etc/systemd/system/lfr-ddns-edge.timer
  fi

  # Any DDNS units from a previous provisioning run are removed, so re-running with -D none
  # actually retires the updater rather than leaving it failing in the background.
  if [ "$DDNS_PROVIDER" = "none" ]; then
    sudo systemctl disable --now lfr-ddns-edge.timer cloudflare-ddns-edge.timer 2>/dev/null || true
    sudo rm -f /etc/systemd/system/lfr-ddns-edge.{service,timer} \
               /etc/systemd/system/cloudflare-ddns-edge.{service,timer} \
               /usr/local/bin/lfr-ddns-edge.sh /usr/local/bin/cloudflare-ddns-edge.sh
  fi

  # Reload services
  sudo systemctl daemon-reload
  
  # Start services
  sudo systemctl enable lfr-tunneld
  sudo systemctl restart lfr-tunneld
  
  sudo systemctl restart nginx
  
  sudo systemctl enable --now gateway-watchdog.timer
  sudo systemctl start gateway-watchdog.service

  # Enable the DDNS timer only when one was installed. The previous version enabled it
  # unconditionally, with the comment "it will trigger but log a credential error until API
  # token is updated" -- an updater expected to fail on every run from the moment it was
  # provisioned. That expectation is why nobody noticed when it started failing for a
  # different reason entirely and stayed broken for two weeks (#1300).
  if [ "$DDNS_PROVIDER" != "none" ]; then
    sudo systemctl enable --now lfr-ddns-edge.timer
  fi

  # 9. Configure Local Security Hardening (UFW, Fail2ban, Auto Upgrades)
  echo "=> Configuring UFW local firewall rules..."
  sudo ufw default deny incoming
  sudo ufw default allow outgoing
  sudo ufw allow 22/tcp
  sudo ufw allow 80/tcp
  sudo ufw allow 443/tcp
  sudo ufw --force enable

  echo "=> Enabling fail2ban service..."
  sudo systemctl enable --now fail2ban

  echo "=> Setting up automated daily security upgrades..."
  echo 'APT::Periodic::Update-Package-Lists "1";' | sudo tee /etc/apt/apt.conf.d/20auto-upgrades
  echo 'APT::Periodic::Unattended-Upgrade "1";' | sudo tee -a /etc/apt/apt.conf.d/20auto-upgrades

  echo "=== Edge VPS Remote Setup Complete! ==="
  echo "=> Checking status of lfr-tunneld:"
  sudo systemctl status lfr-tunneld --no-pager
  
  echo "=> Checking status of Nginx:"
  sudo systemctl status nginx --no-pager
REMOTE_SSH

echo "=========================================================="
echo "🎉 Edge Node Setup Complete!"
echo "Edge server is running and proxying requests to port $EDGE_PORT."
echo "Watchdog, self-healing, and UFW/Fail2ban security guards are active."
if [ "$DDNS_PROVIDER" = "none" ]; then
  echo "Dynamic DNS: not installed (-D none). Correct for a node with a static or elastic address."
else
  echo "Dynamic DNS: $DDNS_PROVIDER updater installed and enabled."
  echo "  Verify it before relying on it:  systemctl start lfr-ddns-edge.service && journalctl -u lfr-ddns-edge -n 20"
  echo "  An updater that cannot reach its provider is worse than none -- it fails every five"
  echo "  minutes while systemd records success (#1300)."
fi
echo "=========================================================="
