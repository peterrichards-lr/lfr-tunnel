#!/usr/bin/env bash
set -e

VPS_USER=${VPS_USER:-"peterrichards"}
VPS_IP=${VPS_IP:-"lfr-demo.se"}

echo "Uploading self-healing files to VPS..."
scp scripts/nginx-override.conf $VPS_USER@$VPS_IP:/home/$VPS_USER/nginx-override.conf
scp scripts/gateway-watchdog.sh $VPS_USER@$VPS_IP:/home/$VPS_USER/gateway-watchdog.sh
scp scripts/gateway-watchdog.service $VPS_USER@$VPS_IP:/home/$VPS_USER/gateway-watchdog.service
scp scripts/gateway-watchdog.timer $VPS_USER@$VPS_IP:/home/$VPS_USER/gateway-watchdog.timer

echo "Registering self-healing layers on VPS..."
ssh $VPS_USER@$VPS_IP << REMOTE_SSH
  echo "=> Deploying systemd override for Nginx..."
  sudo mkdir -p /etc/systemd/system/nginx.service.d/
  sudo mv /home/$VPS_USER/nginx-override.conf /etc/systemd/system/nginx.service.d/override.conf
  sudo chown root:root /etc/systemd/system/nginx.service.d/override.conf

  echo "=> Installing and securing active watchdog script..."
  sudo mv /home/$VPS_USER/gateway-watchdog.sh /usr/local/bin/gateway-watchdog.sh
  sudo chmod 700 /usr/local/bin/gateway-watchdog.sh
  sudo chown root:root /usr/local/bin/gateway-watchdog.sh

  echo "=> Installing active watchdog systemd service and timer..."
  sudo mv /home/$VPS_USER/gateway-watchdog.service /etc/systemd/system/gateway-watchdog.service
  sudo mv /home/$VPS_USER/gateway-watchdog.timer /etc/systemd/system/gateway-watchdog.timer
  sudo chown root:root /etc/systemd/system/gateway-watchdog.service /etc/systemd/system/gateway-watchdog.timer

  echo "=> Reloading systemd..."
  sudo systemctl daemon-reload

  echo "=> Restarting Nginx to apply systemd override..."
  sudo systemctl restart nginx

  echo "=> Enabling and starting gateway-watchdog.timer..."
  sudo systemctl enable --now gateway-watchdog.timer

  echo "=> Executing an immediate active watchdog manual trial run..."
  sudo systemctl start gateway-watchdog.service

  echo "=> Watchdog Trial Run logs:"
  sudo journalctl -u gateway-watchdog.service --no-pager -n 25

  echo "=> Watchdog Timer Status:"
  sudo systemctl status gateway-watchdog.timer --no-pager
REMOTE_SSH

echo "Self-healing deployment complete!"
