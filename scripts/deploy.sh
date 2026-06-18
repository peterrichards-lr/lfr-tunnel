#!/usr/bin/env bash
set -e

VPS_USER="peterrichards"
VPS_IP="lfr-demo.se"

echo "Building Linux binary with path trimming..."
GOOS=linux GOARCH=amd64 go build -trimpath -o bin/lfr-tunneld-linux ./cmd/lfr-tunneld

echo "Uploading binary to VPS..."
scp bin/lfr-tunneld-linux $VPS_USER@$VPS_IP:/home/$VPS_USER/lfr-tunneld

echo "Uploading error pages to VPS..."
scp -r resources/server/error_pages $VPS_USER@$VPS_IP:/home/$VPS_USER/

echo "Uploading static assets to VPS..."
scp -r pkg/server/static $VPS_USER@$VPS_IP:/home/$VPS_USER/

echo "Executing remote deployment commands..."
ssh $VPS_USER@$VPS_IP << REMOTE_SSH
    sudo mv /home/$VPS_USER/lfr-tunneld /usr/local/bin/lfr-tunneld
    sudo chmod +x /usr/local/bin/lfr-tunneld
    sudo mkdir -p /var/www/lfr-tunnel/error_pages
    sudo cp -r /home/$VPS_USER/error_pages/* /var/www/lfr-tunnel/error_pages/
    sudo mkdir -p /var/www/lfr-tunnel/static
    sudo cp -r /home/$VPS_USER/static/* /var/www/lfr-tunnel/static/
    sudo systemctl restart lfr-tunneld
REMOTE_SSH

echo "Deployment complete!"
