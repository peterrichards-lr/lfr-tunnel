#!/usr/bin/env bash
set -e

if [ "$1" == "-h" ] || [ "$1" == "--help" ]; then
    echo "Usage: ./scripts/deploy.sh [VPS_USER] [VPS_IP] [SSH_KEY_PATH]"
    echo ""
    echo "Deploys the lfr-tunneld server binary to the VPS."
    echo "This script cross-compiles the binary for Linux AMD64, uploads it,"
    echo "and safely restarts the systemd service."
    exit 0
fi

# Use environment variables if set, otherwise default to positional parameters or hardcoded defaults
VPS_USER=${VPS_USER:-${1:-peterrichards}}
VPS_IP=${VPS_IP:-${2:-82.39.133.178}}
SSH_KEY=${SSH_KEY:-${3:-~/.ssh/id_rsa}}
DEPLOY_GOOS=${DEPLOY_GOOS:-linux}
DEPLOY_GOARCH=${DEPLOY_GOARCH:-amd64}

echo "=> Building lfr-tunneld for ${DEPLOY_GOOS} ${DEPLOY_GOARCH}..."
GOOS=${DEPLOY_GOOS} GOARCH=${DEPLOY_GOARCH} go build -o bin/lfr-tunneld-${DEPLOY_GOOS}-${DEPLOY_GOARCH} ./cmd/lfr-tunneld

echo "=> Uploading binary to VPS ($VPS_USER@$VPS_IP)..."
scp -i "$SSH_KEY" bin/lfr-tunneld-${DEPLOY_GOOS}-${DEPLOY_GOARCH} $VPS_USER@$VPS_IP:/tmp/lfr-tunneld

echo "=> Stopping service, moving binary, and restarting..."
# Note: This assumes $VPS_USER has sudo privileges without a password prompt for systemctl,
# which is common for service management, or requires entering it during execution.
ssh -t -i "$SSH_KEY" $VPS_USER@$VPS_IP << 'EOF'
  sudo systemctl stop lfr-tunneld
  sudo mv /tmp/lfr-tunneld /usr/local/bin/lfr-tunneld
  sudo chown lfr-tunnel:lfr-tunnel /usr/local/bin/lfr-tunneld
  sudo chmod +x /usr/local/bin/lfr-tunneld
  sudo systemctl start lfr-tunneld
  sudo systemctl status lfr-tunneld --no-pager
  exit
EOF

echo "=> Deployment successful!"
