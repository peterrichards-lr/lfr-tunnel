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

usage() {
  echo "Usage: $0 -s <vps_ip> -t <edge_token> -r <redirect_domain> -i <identity_file> -u <ssh_user> -d <domains> -c <control_plane_url> -p <port>"
  echo "  -s: VPS Public IP address (required)"
  echo "  -t: Plaintext Edge Token for Control Plane validation (required)"
  echo "  -r: Domain to redirect root browser traffic to (required), e.g. your control plane's landing page"
  echo "  -i: Path to SSH private key file (required)"
  echo "  -u: SSH username (required)"
  echo "  -d: Comma-separated list of edge domains (required)"
  echo "  -c: Control Plane URL (required)"
  echo "  -p: Port for lfr-tunneld to bind to on Edge node (required)"
  exit 1
}

# Parse parameters
while getopts "s:t:i:u:d:c:p:r:" opt; do
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
    *) usage ;;
  esac
done

if [ -z "$VPS_IP" ] || [ -z "$EDGE_TOKEN" ] || [ -z "$REDIRECT_DOMAIN" ] || [ -z "$KEY_PATH" ] || \
   [ -z "$SSH_USER" ] || [ -z "$DOMAINS" ] || [ -z "$CONTROL_PLANE_URL" ] || [ -z "$EDGE_PORT" ]; then
  echo "❌ Error: -s, -t, -r, -i, -u, -d, -c, and -p are all required parameters."
  usage
fi

echo "=== Starting Edge VPS Automation for IP: $VPS_IP ==="

# 0. Refuse to proceed unless the Cloudflare token is already in place — this script never
#    handles that secret itself, so this is a hard prerequisite, not something to work around.
#    (Automated --dns-cloudflare below replaces the old interactive --manual DNS-01 flow,
#    which required a human to add a TXT record mid-run and can't be driven unattended.)
echo "=> Checking for /etc/letsencrypt/cloudflare.ini on $VPS_IP..."
if ! ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP "sudo test -s /etc/letsencrypt/cloudflare.ini"; then
  echo "❌ Error: /etc/letsencrypt/cloudflare.ini is missing or empty on $VPS_IP."
  echo "   Place your real Cloudflare API token there yourself first (see setup_guide.md §3.2):"
  echo "     sudo mkdir -p /etc/letsencrypt"
  echo "     sudo nano /etc/letsencrypt/cloudflare.ini   # dns_cloudflare_api_token = <token>"
  echo "     sudo chmod 600 /etc/letsencrypt/cloudflare.ini"
  exit 1
fi

# 1. Build Linux amd64 binary locally (compatible with standard GCP e2-micro / AWS t3.nano x86_64)
VERSION="$(grep -oE 'Version = "[^"]+"' pkg/config/version.go | cut -d'"' -f2)"
echo "=> Compiling lfr-tunneld for Linux (amd64) with Version=$VERSION..."
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o bin/lfr-tunneld-edge-linux ./cmd/lfr-tunneld

# 2. Update and install packages on the remote VPS (including security hardening packages)
echo "=> Connecting to $VPS_IP to install dependencies (Nginx, Certbot, UFW, Fail2ban)..."
ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP << 'REMOTE_SSH'
  sudo apt-get update
  sudo apt-get install -y nginx certbot python3-certbot-dns-cloudflare curl jq ufw fail2ban unattended-upgrades
REMOTE_SSH

# 3. Request wildcard Let's Encrypt certificates using Certbot's automated Cloudflare DNS-01
#    plugin — fully non-interactive, since /etc/letsencrypt/cloudflare.ini was verified above.
IFS=',' read -r -a DOMAIN_ARRAY <<< "$DOMAINS"
for DOMAIN in "${DOMAIN_ARRAY[@]}"; do
  echo "=========================================================="
  echo "=> Provisioning Wildcard SSL Certificate for $DOMAIN & *.$DOMAIN"
  echo "=========================================================="

  ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP "sudo certbot certonly \
    --dns-cloudflare \
    --dns-cloudflare-credentials /etc/letsencrypt/cloudflare.ini \
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
# from this file rather than having them hardcoded (see cloudflare-ddns-edge.sh).
scp $SSH_KEY_ARG scripts/common/cloudflare-ddns-edge.sh $SSH_USER@$VPS_IP:/home/$SSH_USER/cloudflare-ddns-edge.sh

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

  # Cloudflare DDNS configuration
  sudo mv /home/$SSH_USER/cloudflare-ddns-edge.sh /usr/local/bin/cloudflare-ddns-edge.sh
  sudo chmod 700 /usr/local/bin/cloudflare-ddns-edge.sh
  sudo chown root:root /usr/local/bin/cloudflare-ddns-edge.sh

  sudo mkdir -p /etc/lfr-tunneld
  sudo mv /home/$SSH_USER/ddns-domains.txt /etc/lfr-tunneld/ddns-domains.txt
  sudo chmod 644 /etc/lfr-tunneld/ddns-domains.txt
  sudo chown root:root /etc/lfr-tunneld/ddns-domains.txt

  # Create a placeholder cloudflare.ini if it does not exist
  sudo mkdir -p /etc/letsencrypt
  if [ ! -f /etc/letsencrypt/cloudflare.ini ]; then
    echo "Creating placeholder /etc/letsencrypt/cloudflare.ini..."
    sudo tee /etc/letsencrypt/cloudflare.ini > /dev/null << EOF
dns_cloudflare_api_token = PLACEHOLDER_API_TOKEN
EOF
    sudo chmod 600 /etc/letsencrypt/cloudflare.ini
  fi

  # Create DDNS systemd service
  sudo tee /etc/systemd/system/cloudflare-ddns-edge.service > /dev/null << EOF
[Unit]
Description=Cloudflare Dynamic DNS (Edge Subdomains) Updater
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/cloudflare-ddns-edge.sh
User=root
Group=root
EOF

  # Create DDNS systemd timer
  sudo tee /etc/systemd/system/cloudflare-ddns-edge.timer > /dev/null << EOF
[Unit]
Description=Trigger Cloudflare Dynamic DNS (Edge Subdomains) update every 5 minutes

[Timer]
OnBootSec=1min
OnUnitActiveSec=5min

[Install]
WantedBy=timers.target
EOF

  sudo chown root:root /etc/systemd/system/cloudflare-ddns-edge.service /etc/systemd/system/cloudflare-ddns-edge.timer

  # Reload services
  sudo systemctl daemon-reload
  
  # Start services
  sudo systemctl enable lfr-tunneld
  sudo systemctl restart lfr-tunneld
  
  sudo systemctl restart nginx
  
  sudo systemctl enable --now gateway-watchdog.timer
  sudo systemctl start gateway-watchdog.service

  # Enable DDNS timer (it will trigger but log a credential error until API token is updated)
  sudo systemctl enable --now cloudflare-ddns-edge.timer

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
echo "Cloudflare DDNS service is active (placeholder created at /etc/letsencrypt/cloudflare.ini)."
echo "=========================================================="
