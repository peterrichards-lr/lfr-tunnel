#!/usr/bin/env bash
# scripts/common/setup-central-vps.sh
# Automates setting up the central control-plane VPS node for lfr-tunnel.
# Target IP/key/domain/config are all parameters (not hardcoded), so this works against
# any provider — the existing production VPS or a new AWS/other-cloud instance alike.
set -e

# This is a generic, reusable script -- it carries no default values of its
# own. Every parameter must be supplied explicitly by the caller (a
# Liferay-specific wrapper, or you, the operator), which is the only place
# that actually knows the right values for a given deployment.
SSH_USER=""
DOMAIN=""
ADMIN_EMAIL=""
SSH_KEY_ARG=""
KEY_PATH=""
VPS_IP=""
CONFIG_FILE=""
PORT=""

usage() {
  echo "Usage: $0 -s <vps_ip> -d <domain> -f <server-config.yaml path> -i <identity_file> -u <ssh_user> -e <admin_email> -p <port>"
  echo "  -s: VPS/EC2 public IP address (required)"
  echo "  -d: Base domain for the control plane, e.g. aws-central.lfr-demo.se (required)."
  echo "      A wildcard cert for *.<domain> is requested alongside it."
  echo "  -f: Local path to a pre-built server-config.yaml to upload (required)"
  echo "  -i: Path to SSH private key file (required)"
  echo "  -u: SSH username (required)"
  echo "  -e: Admin/contact email for Let's Encrypt registration (required)"
  echo "  -p: Local port lfr-tunneld binds to -- must match the uploaded server-config.yaml (required)"
  echo ""
  echo "PREREQUISITE: place your real Cloudflare API token in /etc/letsencrypt/cloudflare.ini"
  echo "on the target server YOURSELF before running this script (see setup_guide.md §3.2):"
  echo "  sudo mkdir -p /etc/letsencrypt"
  echo "  sudo nano /etc/letsencrypt/cloudflare.ini   # dns_cloudflare_api_token = <token>"
  echo "  sudo chmod 600 /etc/letsencrypt/cloudflare.ini"
  echo "This script only checks that file exists — it never reads, uploads, or handles the token."
  exit 1
}

while getopts "s:d:f:i:u:e:p:" opt; do
  case $opt in
    s) VPS_IP="$OPTARG" ;;
    d) DOMAIN="$OPTARG" ;;
    f) CONFIG_FILE="$OPTARG" ;;
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
    e) ADMIN_EMAIL="$OPTARG" ;;
    p) PORT="$OPTARG" ;;
    *) usage ;;
  esac
done

if [ -z "$VPS_IP" ] || [ -z "$DOMAIN" ] || [ -z "$CONFIG_FILE" ] || [ -z "$KEY_PATH" ] || \
   [ -z "$SSH_USER" ] || [ -z "$ADMIN_EMAIL" ] || [ -z "$PORT" ]; then
  echo "❌ Error: -s, -d, -f, -i, -u, -e, and -p are all required."
  usage
fi
if [ ! -f "$CONFIG_FILE" ]; then
  echo "❌ Error: config file not found locally: $CONFIG_FILE"
  exit 1
fi

echo "=== Starting Central VPS Automation for IP: $VPS_IP, domain: $DOMAIN ==="

# 0. Refuse to proceed unless the Cloudflare token is already in place — this script never
#    handles that secret itself, so this is a hard prerequisite, not something to work around.
echo "=> Checking for /etc/letsencrypt/cloudflare.ini on $VPS_IP..."
if ! ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP "sudo test -s /etc/letsencrypt/cloudflare.ini"; then
  echo "❌ Error: /etc/letsencrypt/cloudflare.ini is missing or empty on $VPS_IP."
  echo "   Place your real Cloudflare API token there yourself first (see usage below), then re-run."
  usage
fi

# 1. Build linux/amd64 lfr-tunneld binary locally
VERSION="$(grep -oE 'Version = "[^"]+"' pkg/config/version.go | cut -d'"' -f2)"
echo "=> Compiling lfr-tunneld for Linux (amd64) with Version=$VERSION..."
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o bin/lfr-tunneld-central-linux ./cmd/lfr-tunneld

# 2. Install packages on the remote VPS (including security hardening packages).
#    Note: no Postfix here — SMTP relaying is expected to be handled externally
#    (e.g. Amazon SES) via server-config.yaml's smtp_server block.
echo "=> Connecting to $VPS_IP to install dependencies (Nginx, Certbot, UFW, Fail2ban)..."
ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP << 'REMOTE_SSH'
  sudo apt-get update
  sudo apt-get install -y nginx certbot python3-certbot-dns-cloudflare curl jq ufw fail2ban unattended-upgrades
REMOTE_SSH

# 3. Request the wildcard cert via Certbot's automated Cloudflare DNS-01 plugin — fully
#    non-interactive, since /etc/letsencrypt/cloudflare.ini was already verified above.
echo "=========================================================="
echo "=> Provisioning Wildcard SSL Certificate for $DOMAIN & *.$DOMAIN"
echo "=========================================================="
ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP "sudo certbot certonly \
  --dns-cloudflare \
  --dns-cloudflare-credentials /etc/letsencrypt/cloudflare.ini \
  --agree-tos \
  --non-interactive \
  -m $ADMIN_EMAIL \
  -d '$DOMAIN' \
  -d '*.$DOMAIN'"

# 4. Upload the pre-built server-config.yaml
echo "=> Uploading server-config.yaml..."
scp $SSH_KEY_ARG "$CONFIG_FILE" $SSH_USER@$VPS_IP:/home/$SSH_USER/server-config.yaml

# 5. Generate Nginx virtual host configuration locally and upload
echo "=> Generating Nginx configuration locally..."
NGINX_TMP="/tmp/nginx-central.conf"
cat > "$NGINX_TMP" << EOF
map \$http_upgrade \$connection_upgrade {
    default upgrade;
    ''      close;
}

# HTTP -> HTTPS redirect
server {
    listen 80;
    listen [::]:80;
    server_name $DOMAIN *.$DOMAIN;
    return 301 https://\$host\$request_uri;
}

# Control plane / portal
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name $DOMAIN;

    ssl_certificate /etc/letsencrypt/live/$DOMAIN/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/$DOMAIN/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

    location / {
        proxy_pass http://127.0.0.1:$PORT;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$connection_upgrade;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Host \$host;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }

    location /tunnel {
        proxy_pass http://127.0.0.1:$PORT;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$connection_upgrade;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
}

# Wildcard data plane
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name *.$DOMAIN;

    ssl_certificate /etc/letsencrypt/live/$DOMAIN/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/$DOMAIN/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

    location / {
        proxy_pass http://127.0.0.1:$PORT;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$connection_upgrade;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Host \$host;
        proxy_set_header X-Forwarded-Proto https;

        client_max_body_size 500M;
        proxy_connect_timeout 120s;
        proxy_send_timeout 120s;
        proxy_read_timeout 120s;
    }
}
EOF

echo "=> Uploading Nginx configuration..."
scp $SSH_KEY_ARG "$NGINX_TMP" $SSH_USER@$VPS_IP:/home/$SSH_USER/lfr-tunneld-nginx.conf
rm -f "$NGINX_TMP"

# 6. Upload compiled binary and assets
echo "=> Uploading binary to VPS..."
scp $SSH_KEY_ARG bin/lfr-tunneld-central-linux $SSH_USER@$VPS_IP:/home/$SSH_USER/lfr-tunneld

echo "=> Uploading error pages..."
scp $SSH_KEY_ARG -r resources/server/error_pages $SSH_USER@$VPS_IP:/home/$SSH_USER/

echo "=> Uploading static assets..."
scp $SSH_KEY_ARG -r pkg/server/static $SSH_USER@$VPS_IP:/home/$SSH_USER/

# 7. Upload self-healing watchdog and Nginx auto-restart override. gateway-watchdog.sh
#    itself has no default port -- it requires LFT_BACKEND_PORT to be set, so it's
#    uploaded unmodified; the systemd unit's placeholder env var is what actually
#    supplies this deployment's -p value.
echo "=> Uploading watchdog and systemd overrides..."
scp $SSH_KEY_ARG scripts/common/nginx-override.conf $SSH_USER@$VPS_IP:/home/$SSH_USER/nginx-override.conf
scp $SSH_KEY_ARG scripts/common/gateway-watchdog.sh $SSH_USER@$VPS_IP:/home/$SSH_USER/gateway-watchdog.sh

sed "s/__BACKEND_PORT__/$PORT/" scripts/common/gateway-watchdog.service > /tmp/gateway-watchdog-central.service
scp $SSH_KEY_ARG /tmp/gateway-watchdog-central.service $SSH_USER@$VPS_IP:/home/$SSH_USER/gateway-watchdog.service
rm -f /tmp/gateway-watchdog-central.service

scp $SSH_KEY_ARG scripts/common/gateway-watchdog.timer $SSH_USER@$VPS_IP:/home/$SSH_USER/

# 8. Remotely execute setup and service configuration
echo "=> Registering services and securing files on VPS..."
ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP << REMOTE_SSH
  # Create system user lfr-tunnel
  if ! id "lfr-tunnel" &>/dev/null; then
    echo "Creating system user lfr-tunnel..."
    sudo useradd -r -s /bin/false lfr-tunnel
  fi

  # Binary installation
  sudo mv /home/$SSH_USER/lfr-tunneld /usr/local/bin/lfr-tunneld
  sudo chmod 755 /usr/local/bin/lfr-tunneld
  sudo chown root:root /usr/local/bin/lfr-tunneld

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
  sudo mv /home/$SSH_USER/lfr-tunneld-nginx.conf /etc/nginx/sites-available/lfr-tunnel
  sudo ln -sf /etc/nginx/sites-available/lfr-tunnel /etc/nginx/sites-enabled/lfr-tunnel
  sudo rm -f /etc/nginx/sites-enabled/default

  # systemd configuration
  echo "Creating systemd configuration for lfr-tunneld..."
  sudo tee /etc/systemd/system/lfr-tunneld.service > /dev/null << EOF
[Unit]
Description=Liferay Tunnel Gateway Daemon
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
# /etc/nginx/conf.d and the vanity webroot are here (in addition to
# /etc/lfr-tunneld) purely so the Vanity Domain Hook (see
# docs/server/setup_guide.md §4.7) has somewhere to write, without loosening
# ProtectSystem=strict/NoNewPrivileges for anything else. lfr-tunneld itself
# never writes to either path directly -- only the hook subprocess it execs
# does, on the operator's explicit opt-in via Admin Settings.
ReadWritePaths=/etc/lfr-tunneld /etc/nginx/conf.d /var/www/lfr-tunnel-vanity

[Install]
WantedBy=multi-user.target
EOF

  # Vanity Domain Hook support directories (see docs/server/setup_guide.md
  # §4.7). /etc/nginx/conf.d is already glob-included by the distro's default
  # nginx.conf (`include /etc/nginx/conf.d/*.conf;`), so handing it to
  # lfr-tunnel needs no nginx.conf edit. The webroot is a dedicated directory
  # so it's never confused with any other static assets served from
  # /var/www/lfr-tunnel.
  sudo chown lfr-tunnel:lfr-tunnel /etc/nginx/conf.d
  sudo mkdir -p /var/www/lfr-tunnel-vanity
  sudo chown -R lfr-tunnel:lfr-tunnel /var/www/lfr-tunnel-vanity

  # lfr-tunneld is deliberately unprivileged and cannot reload nginx itself --
  # this root-owned path unit watches for the hook's config changes and
  # reloads nginx on its behalf.
  sudo tee /etc/systemd/system/nginx-vanity-reload.path > /dev/null << EOF
[Unit]
Description=Watch for Vanity Domain Hook nginx config changes

[Path]
PathChanged=/etc/nginx/conf.d

[Install]
WantedBy=multi-user.target
EOF

  sudo tee /etc/systemd/system/nginx-vanity-reload.service > /dev/null << EOF
[Unit]
Description=Reload nginx after a Vanity Domain Hook config change

[Service]
Type=oneshot
ExecStart=/usr/sbin/nginx -s reload
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

  # Reload services
  sudo systemctl daemon-reload

  # Start services
  sudo systemctl enable lfr-tunneld
  sudo systemctl restart lfr-tunneld

  sudo nginx -t
  sudo systemctl restart nginx

  sudo systemctl enable --now nginx-vanity-reload.path

  sudo systemctl enable --now gateway-watchdog.timer
  sudo systemctl start gateway-watchdog.service

  # Local security hardening (UFW, Fail2ban, Auto Upgrades)
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

  echo "=== Central VPS Remote Setup Complete! ==="
  echo "=> Checking status of lfr-tunneld:"
  sudo systemctl status lfr-tunneld --no-pager
  echo "=> Checking status of Nginx:"
  sudo systemctl status nginx --no-pager
REMOTE_SSH

echo "=========================================================="
echo "🎉 Central Control Plane Setup Complete!"
echo "Reachable at: https://$DOMAIN"
echo "Watchdog, self-healing, and UFW/Fail2ban security guards are active."
echo "=========================================================="
