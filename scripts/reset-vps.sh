#!/usr/bin/env bash
set -e

if [ "$1" == "-h" ] || [ "$1" == "--help" ]; then
    echo "Usage: ./scripts/reset-vps.sh [VPS_USER] [VPS_IP] [SSH_KEY_PATH]"
    echo ""
    echo "WARNING: This completely wipes the VPS database!"
    echo "This script stops the Go server, deletes the database and caches, and restarts it."
    exit 0
fi

# Use environment variables if set, otherwise default to positional parameters or hardcoded defaults
VPS_USER=${VPS_USER:-${1:-peterrichards}}
VPS_IP=${VPS_IP:-${2:-82.39.133.178}}
SSH_KEY=${SSH_KEY:-${3:-~/.ssh/id_rsa}}

echo "=> Connecting to VPS to reset the server state..."
echo "!! WARNING: Deleting production database on $VPS_IP !!"

ssh -t -i "$SSH_KEY" $VPS_USER@$VPS_IP << 'EOF'
  echo "=> Stopping lfr-tunneld service..."
  sudo systemctl stop lfr-tunneld

  echo "=> Deleting SQLite database and caches..."
  sudo rm -f /etc/lfr-tunneld/lfr-tunnel.db
  sudo rm -f /etc/lfr-tunneld/lfr-tunnel.db-wal
  sudo rm -f /etc/lfr-tunneld/lfr-tunnel.db-shm

  echo "=> Restarting lfr-tunneld service..."
  sudo systemctl start lfr-tunneld
  sudo systemctl status lfr-tunneld --no-pager
  
  echo "=> VPS server reset complete!"
  exit
EOF
