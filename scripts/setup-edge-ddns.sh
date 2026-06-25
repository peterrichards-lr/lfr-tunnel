#!/usr/bin/env bash
# scripts/setup-edge-ddns.sh
# Automates deploying the Cloudflare DDNS service on the stateless Edge VPS.
set -e

# Default variables
SSH_USER="ubuntu"
SSH_KEY_ARG=""
VPS_IP=""

usage() {
  echo "Usage: $0 -s <vps_ip> [-i <identity_file>] [-u <ssh_user>]"
  echo "  -s: VPS Public IP address (required)"
  echo "  -i: Path to SSH private key file (optional)"
  echo "  -u: SSH username (default: ubuntu)"
  exit 1
}

# Parse parameters
while getopts "s:i:u:" opt; do
  case $opt in
    s) VPS_IP="$OPTARG" ;;
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
    *) usage ;;
  esac
done

if [ -z "$VPS_IP" ]; then
  echo "❌ Error: VPS IP (-s) is a required parameter."
  usage
fi

echo "=== Deploying Cloudflare DDNS on Edge VPS: $VPS_IP ==="

# 1. Upload the edge-specific DDNS script
echo "=> Uploading cloudflare-ddns-edge.sh to VPS..."
scp $SSH_KEY_ARG scripts/cloudflare-ddns-edge.sh $SSH_USER@$VPS_IP:/home/$SSH_USER/cloudflare-ddns-edge.sh

# 2. Configure systemd service and timer remotely
echo "=> Registering DDNS systemd service and timer on VPS..."
ssh $SSH_KEY_ARG $SSH_USER@$VPS_IP << 'REMOTE_SSH'
  # Make the script executable and move to /usr/local/bin
  sudo mv /home/$SSH_USER/cloudflare-ddns-edge.sh /usr/local/bin/cloudflare-ddns-edge.sh
  sudo chmod 700 /usr/local/bin/cloudflare-ddns-edge.sh
  sudo chown root:root /usr/local/bin/cloudflare-ddns-edge.sh

  # Create systemd service
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

  # Create systemd timer
  sudo tee /etc/systemd/system/cloudflare-ddns-edge.timer > /dev/null << EOF
[Unit]
Description=Trigger Cloudflare Dynamic DNS (Edge Subdomains) update every 5 minutes

[Timer]
OnBootSec=1min
OnUnitActiveSec=5min

[Install]
WantedBy=timers.target
EOF

  # Secure systemd unit files
  sudo chown root:root /etc/systemd/system/cloudflare-ddns-edge.service /etc/systemd/system/cloudflare-ddns-edge.timer

  # Reload systemd and enable/start timer
  sudo systemctl daemon-reload
  sudo systemctl enable --now cloudflare-ddns-edge.timer

  # Trigger an immediate run
  echo "=> Running DDNS updater trial run..."
  sudo systemctl start cloudflare-ddns-edge.service
  
  echo "=> Verification logs:"
  sudo journalctl -u cloudflare-ddns-edge.service --no-pager -n 20
  
  echo "=> Timer status:"
  sudo systemctl status cloudflare-ddns-edge.timer --no-pager
REMOTE_SSH

echo "=========================================================="
echo "🎉 Edge Cloudflare DDNS Setup Complete!"
echo "=========================================================="
