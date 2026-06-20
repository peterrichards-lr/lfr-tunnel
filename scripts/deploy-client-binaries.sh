#!/usr/bin/env bash
# scripts/deploy-client-binaries.sh
# Deploys signed client binaries and checksums to the VPS downloads directory.

set -e

VPS_USER="peterrichards"
VPS_IP="lfr-demo.se"
DOWNLOADS_DIR="/var/www/lfr-tunnel/static/downloads"
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="$PROJECT_ROOT/dist"

# Parse optional identity file
SSH_KEY=""
while getopts "i:" opt; do
  case $opt in
    i) 
      KEY_PATH="$OPTARG"
      # Manually resolve tilde (~) to $HOME if it starts with ~/ or is exactly ~
      if [[ "$KEY_PATH" == "~/"* ]]; then
        KEY_PATH="${HOME}/${KEY_PATH#~/}"
      elif [[ "$KEY_PATH" == "~" ]]; then
        KEY_PATH="${HOME}"
      fi
      SSH_KEY="-i $KEY_PATH"
      ;;
    *) echo "Usage: $0 [-i <identity_file>]" && exit 1 ;;
  esac
done
shift $((OPTIND - 1))

echo "=== Deploying Client Binaries and Checksums to VPS ==="

# Check that the binaries exist
if [ ! -d "$BIN_DIR" ] || [ ! -f "$BIN_DIR/checksums.txt" ]; then
    echo "ERROR: Client binaries or checksums.txt not found in $BIN_DIR. Build and sign them first." >&2
    exit 1
fi

echo "Uploading files from $BIN_DIR to $VPS_USER@$VPS_IP..."
# Copy all binaries, signatures (.asc), and checksums.txt to the home folder on the VPS first
scp $SSH_KEY "$BIN_DIR"/lfr-tunnel-* "$BIN_DIR"/checksums.txt "$VPS_USER@$VPS_IP:/home/$VPS_USER/"

echo "Moving files to secure web server downloads directory on VPS..."
ssh $SSH_KEY "$VPS_USER@$VPS_IP" << REMOTE_SSH
    # Ensure downloads directory exists
    sudo mkdir -p "$DOWNLOADS_DIR"
    
    # Move files and set correct permissions
    sudo cp /home/$VPS_USER/lfr-tunnel-* /home/$VPS_USER/checksums.txt "$DOWNLOADS_DIR/"
    sudo chmod -R +r "$DOWNLOADS_DIR"
    
    # Clean up temporary home folder copies
    rm -f /home/$VPS_USER/lfr-tunnel-* /home/$VPS_USER/checksums.txt
REMOTE_SSH

echo "=== Client Binaries Deployment Complete! ==="
