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
  echo "PREREQUISITE: the target instance must already be able to write the DNS-01 challenge"
  echo "record into its hosted zone. On AWS that means an instance profile granting, on the"
  echo "zones this deployment serves and nothing else:"
  echo "  route53:ChangeResourceRecordSets, route53:ListResourceRecordSets, route53:GetHostedZone"
  echo "plus route53:ListHostedZonesByName and route53:GetChange on *."
  echo "This script never reads, uploads, or handles a credential of any kind."
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

# 0. Refuse to proceed unless the instance can actually write a DNS-01 challenge record. This
#    script never handles a credential itself, so this is a hard prerequisite rather than
#    something to work around -- and checking it here turns a silent renewal failure 30 days
#    before expiry into a refusal at provisioning time (#1297).
echo "=> Checking $VPS_IP can write DNS-01 challenge records into its hosted zone..."
if ! ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP "command -v aws >/dev/null 2>&1"; then
  echo "❌ Error: the AWS CLI is not installed on $VPS_IP, so the Route53 DNS-01 plugin has"
  echo "   nothing to authenticate with. Install it and attach an instance profile first."
  usage
fi
if ! ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP "aws route53 list-hosted-zones-by-name --dns-name '$DOMAIN' --max-items 1 >/dev/null 2>&1"; then
  echo "❌ Error: $VPS_IP cannot read Route53 hosted zones for $DOMAIN."
  echo "   Attach an instance profile granting the permissions listed in the usage below,"
  echo "   then re-run. Nothing here needs a token on disk."
  usage
fi

# 1. Build linux/amd64 lfr-tunneld binary locally, and the lfr-tunnel-ops CLI (needed below
#    to render the nginx config from the same template reconcile-nginx uses -- never `go run`
#    it, see the edr-constraints skill).
VERSION="$(grep -oE 'Version = "[^"]+"' pkg/config/version.go | cut -d'"' -f2)"
echo "=> Compiling lfr-tunneld for Linux (amd64) with Version=$VERSION..."
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o bin/lfr-tunneld-central-linux ./cmd/lfr-tunneld
go build -o bin/lfr-tunnel-ops ./cmd/lfr-tunnel-ops

# 2. Install packages on the remote VPS (including security hardening packages).
#    Note: no Postfix here — SMTP relaying is expected to be handled externally
#    (e.g. Amazon SES) via server-config.yaml's smtp_server block.
echo "=> Connecting to $VPS_IP to install dependencies (Nginx, Certbot, UFW, Fail2ban)..."
ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP << 'REMOTE_SSH'
  sudo apt-get update
  sudo apt-get install -y nginx certbot python3-certbot-dns-route53 curl jq ufw fail2ban unattended-upgrades
REMOTE_SSH

# 3. Request the wildcard cert via Certbot's Route53 DNS-01 plugin. Fully non-interactive and
#    credential-free from this script's point of view: the plugin uses the instance's own IAM
#    role, so no token is ever written down, uploaded, or rotated by hand.
#
#    This was --dns-cloudflare until #1297. Both zones moved to Route53 on 2026-08-11, so the
#    Cloudflare plugin was writing the challenge record into a zone that is no longer
#    authoritative -- the CA queries the AWS nameservers and would never have seen it. Nothing
#    failed at the time because the certificates had just been issued; the first renewal after
#    the migration would have been the first failure, silently, 30 days before expiry.
echo "=========================================================="
echo "=> Provisioning Wildcard SSL Certificate for $DOMAIN & *.$DOMAIN"
echo "=========================================================="
ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP "sudo certbot certonly \
  --dns-route53 \
  --agree-tos \
  --non-interactive \
  -m $ADMIN_EMAIL \
  -d '$DOMAIN' \
  -d '*.$DOMAIN'"

# 3b. Prove renewal actually works, rather than assuming it because issuance did. A dry run
#     performs a real challenge against the live zone, which is the only thing that
#     distinguishes a working authenticator from one pointed at the wrong provider -- exactly
#     the failure #1297 was.
echo "=> Verifying unattended renewal (certbot renew --dry-run)..."
ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP "sudo certbot renew --dry-run" || {
  echo "❌ Renewal dry run failed. The certificate is issued but will not renew unattended."
  echo "   Check the instance's Route53 permissions before going further."
  exit 1
}

# 4. Upload the pre-built server-config.yaml
echo "=> Uploading server-config.yaml..."
scp $SSH_KEY_ARG "$CONFIG_FILE" $SSH_USER@$VPS_IP:/home/$SSH_USER/server-config.yaml

# 5. Generate Nginx virtual host configuration locally and upload -- rendered by
#    lfr-tunnel-ops from the exact same template `reconcile-nginx` uses to re-sync an
#    already-provisioned box, so the two can never drift apart from each other again (#997,
#    #1026).
echo "=> Generating Nginx configuration locally..."
NGINX_TMP="/tmp/nginx-central.conf"
./bin/lfr-tunnel-ops render-nginx-config -domains "$DOMAIN" -port "$PORT" > "$NGINX_TMP"

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
