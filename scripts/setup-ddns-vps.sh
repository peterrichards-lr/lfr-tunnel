#!/usr/bin/env bash
set -e

VPS_USER=${VPS_USER:-"peterrichards"}
VPS_IP=${VPS_IP:-"lfr-demo.se"}

echo "Uploading files to VPS..."
scp scripts/cloudflare-ddns.sh $VPS_USER@$VPS_IP:/home/$VPS_USER/cloudflare-ddns.sh
scp scripts/cloudflare-ddns.service $VPS_USER@$VPS_IP:/home/$VPS_USER/cloudflare-ddns.service
scp scripts/cloudflare-ddns.timer $VPS_USER@$VPS_IP:/home/$VPS_USER/cloudflare-ddns.timer

echo "Orchestrating systemd registration and permissions on VPS..."
ssh $VPS_USER@$VPS_IP << REMOTE_SSH
  echo "=> Installing and securing script under /usr/local/bin..."
  sudo mv /home/$VPS_USER/cloudflare-ddns.sh /usr/local/bin/cloudflare-ddns.sh
  sudo chmod 700 /usr/local/bin/cloudflare-ddns.sh
  sudo chown root:root /usr/local/bin/cloudflare-ddns.sh

  echo "=> Installing systemd service and timer files..."
  sudo mv /home/$VPS_USER/cloudflare-ddns.service /etc/systemd/system/cloudflare-ddns.service
  sudo mv /home/$VPS_USER/cloudflare-ddns.timer /etc/systemd/system/cloudflare-ddns.timer
  sudo chown root:root /etc/systemd/system/cloudflare-ddns.service /etc/systemd/system/cloudflare-ddns.timer

  echo "=> Reloading systemd..."
  sudo systemctl daemon-reload

  echo "=> Enabling and starting systemd timer..."
  sudo systemctl enable --now cloudflare-ddns.timer

  echo "=> Starting DDNS service manually for a trial run..."
  sudo systemctl start cloudflare-ddns.service

  echo "=> DDNS Service logs:"
  sudo journalctl -u cloudflare-ddns.service --no-pager -n 25

  echo "=> Timer Status:"
  sudo systemctl status cloudflare-ddns.timer --no-pager
REMOTE_SSH

echo "Cloudflare DDNS installation complete!"
