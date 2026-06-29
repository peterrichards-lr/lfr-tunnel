#!/usr/bin/env bash
set -e

VPS_USER="peterrichards"
VPS_IP="lfr-demo.se"

# Parse optional parameters
SSH_KEY=""
WARN_SECS=""
EDGE_NODES_FILE=""
while getopts "i:w:f:" opt; do
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
    w)
      WARN_SECS="$OPTARG"
      ;;
    f)
      EDGE_NODES_FILE="$OPTARG"
      ;;
    *) echo "Usage: $0 [-i <identity_file>] [-w <warning_seconds>] [-f <edge_nodes_file>]" && exit 1 ;;
  esac
done
shift $((OPTIND - 1))

if [ -n "$EDGE_NODES_FILE" ]; then
  if [[ "$EDGE_NODES_FILE" == "~/"* ]]; then
    EDGE_NODES_FILE="${HOME}/${EDGE_NODES_FILE#~/}"
  elif [[ "$EDGE_NODES_FILE" == "~" ]]; then
    EDGE_NODES_FILE="${HOME}"
  fi

  if [ ! -f "$EDGE_NODES_FILE" ]; then
    echo "Error: Edge nodes file '$EDGE_NODES_FILE' not found"
    exit 1
  fi

  echo "Downloading current server-config.yaml from VPS..."
  if ssh $SSH_KEY $VPS_USER@$VPS_IP "sudo [ -f /etc/lfr-tunneld/server-config.yaml ]"; then
    echo "Remote configuration found. Copying to temporary path..."
    ssh $SSH_KEY $VPS_USER@$VPS_IP "sudo cp /etc/lfr-tunneld/server-config.yaml /tmp/tmp-server-config.yaml && sudo chown $VPS_USER:$VPS_USER /tmp/tmp-server-config.yaml"
    
    if ! scp $SSH_KEY $VPS_USER@$VPS_IP:/tmp/tmp-server-config.yaml /tmp/vps-server-config.yaml 2>/dev/null; then
      echo "❌ Error: Failed to download server-config.yaml from VPS."
      ssh $SSH_KEY $VPS_USER@$VPS_IP "rm -f /tmp/tmp-server-config.yaml"
      exit 1
    fi
    ssh $SSH_KEY $VPS_USER@$VPS_IP "rm -f /tmp/tmp-server-config.yaml"
  else
    echo "Warning: /etc/lfr-tunneld/server-config.yaml not found on VPS. Creating a new basic config."
    echo "domains: []" > /tmp/vps-server-config.yaml
  fi

  echo "Updating server-config.yaml with edge nodes..."
  python3 -c '
import sys, yaml, hashlib

config_path = "/tmp/vps-server-config.yaml"
nodes_path = "'"$EDGE_NODES_FILE"'"

try:
    with open(config_path, "r") as f:
        cfg = yaml.safe_load(f) or {}
except Exception as e:
    print(f"Error reading config: {e}", file=sys.stderr)
    cfg = {}

edge_nodes = []
with open(nodes_path, "r") as f:
    for line in f:
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        parts = [p.strip() for p in line.split(",")]
        if len(parts) < 2:
            parts = [p.strip() for p in line.split(":")]
        if len(parts) < 2:
            print(f"Skipping invalid line: {line}", file=sys.stderr)
            continue
        node_id = parts[0]
        token = parts[1]
        url = parts[2] if len(parts) > 2 else ""
        token_hash = hashlib.sha256(token.encode()).hexdigest()
        edge_nodes.append({"id": node_id, "token_hash": token_hash, "url": url})

cfg["edge_nodes"] = edge_nodes

with open(config_path, "w") as f:
    yaml.safe_dump(cfg, f, default_flow_style=False)
'

  echo "Uploading updated server-config.yaml to VPS staging..."
  scp $SSH_KEY /tmp/vps-server-config.yaml $VPS_USER@$VPS_IP:/home/$VPS_USER/server-config.yaml
  rm -f /tmp/vps-server-config.yaml
fi

VERSION="${VERSION:-$(grep -oE 'Version = "[^"]+"' pkg/config/version.go | cut -d'"' -f2)}"

echo "Building Linux binary (version: $VERSION) with path trimming..."
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o bin/lfr-tunneld-linux ./cmd/lfr-tunneld

if [ -n "$WARN_SECS" ]; then
  if ! [[ "$WARN_SECS" =~ ^[0-9]+$ ]]; then
    echo "Error: warning time must be a positive integer"
    exit 1
  fi

  echo "Broadcasting maintenance warning to users via VPS localhost API..."
  ssh $SSH_KEY $VPS_USER@$VPS_IP "curl -s -X POST -H 'Content-Type: application/json' -d '{\"message\":\"Gateway is restarting for updates. Active sessions will be temporarily suspended.\", \"countdown_seconds\": $WARN_SECS, \"duration_minutes\": 5}' http://127.0.0.1:8080/api/local/broadcast"

  echo "Waiting $WARN_SECS seconds before starting deploy..."
  for ((i=WARN_SECS; i>0; i--)); do
    printf "\rTime remaining: %d seconds... " "$i"
    sleep 1
  done
  echo ""
fi

echo "Uploading binary to VPS..."
scp $SSH_KEY bin/lfr-tunneld-linux $VPS_USER@$VPS_IP:/home/$VPS_USER/lfr-tunneld

echo "Uploading error pages to VPS..."
scp $SSH_KEY -r resources/server/error_pages $VPS_USER@$VPS_IP:/home/$VPS_USER/

echo "Uploading static assets to VPS..."
scp $SSH_KEY -r pkg/server/static $VPS_USER@$VPS_IP:/home/$VPS_USER/

echo "Uploading properties translations and email templates to VPS..."
scp $SSH_KEY -r pkg/server/i18n $VPS_USER@$VPS_IP:/home/$VPS_USER/
scp $SSH_KEY -r pkg/server/templates $VPS_USER@$VPS_IP:/home/$VPS_USER/

echo "Uploading maintenance and backup scripts to VPS..."
scp $SSH_KEY scripts/enable-maintenance.sh scripts/disable-maintenance.sh scripts/restore-with-maintenance.sh scripts/restore-backup.sh scripts/sync-offsite-backups.sh scripts/sync-offsite-backups.service scripts/sync-offsite-backups.timer $VPS_USER@$VPS_IP:/home/$VPS_USER/

echo "Executing remote deployment commands..."
ssh $SSH_KEY $VPS_USER@$VPS_IP << REMOTE_SSH
    sudo mv /home/$VPS_USER/lfr-tunneld /usr/local/bin/lfr-tunneld
    sudo chmod +x /usr/local/bin/lfr-tunneld
    
    # Install maintenance and backup scripts to system path
    sudo mv /home/$VPS_USER/enable-maintenance.sh /usr/local/bin/enable-maintenance.sh
    sudo chmod +x /usr/local/bin/enable-maintenance.sh
    sudo mv /home/$VPS_USER/disable-maintenance.sh /usr/local/bin/disable-maintenance.sh
    sudo chmod +x /usr/local/bin/disable-maintenance.sh
    sudo mv /home/$VPS_USER/restore-with-maintenance.sh /usr/local/bin/restore-with-maintenance.sh
    sudo chmod +x /usr/local/bin/restore-with-maintenance.sh
    sudo mv /home/$VPS_USER/restore-backup.sh /usr/local/bin/restore-backup.sh
    sudo chmod +x /usr/local/bin/restore-backup.sh
    sudo mv /home/$VPS_USER/sync-offsite-backups.sh /usr/local/bin/sync-offsite-backups.sh
    sudo chmod +x /usr/local/bin/sync-offsite-backups.sh
    
    # Install offsite sync systemd files
    sudo mv /home/$VPS_USER/sync-offsite-backups.service /etc/systemd/system/
    sudo mv /home/$VPS_USER/sync-offsite-backups.timer /etc/systemd/system/
    sudo systemctl daemon-reload
    sudo systemctl enable --now sync-offsite-backups.timer
    
    sudo mkdir -p /var/www/lfr-tunnel/error_pages
    sudo cp -r /home/$VPS_USER/error_pages/* /var/www/lfr-tunnel/error_pages/
    sudo mkdir -p /var/www/lfr-tunnel/static
    sudo cp -r /home/$VPS_USER/static/* /var/www/lfr-tunnel/static/
    
    # Copy Properties and Email Templates to the secure /etc/lfr-tunneld/ path
    sudo mkdir -p /etc/lfr-tunneld/i18n
    sudo cp -r /home/$VPS_USER/i18n/*.properties /etc/lfr-tunneld/i18n/
    sudo mkdir -p /etc/lfr-tunneld/templates
    sudo cp -r /home/$VPS_USER/templates/* /etc/lfr-tunneld/templates/
    
    # Clean up temporary home files
    rm -rf /home/$VPS_USER/error_pages /home/$VPS_USER/static /home/$VPS_USER/i18n /home/$VPS_USER/templates
    
    if [ -f /home/$VPS_USER/server-config.yaml ]; then
        sudo mkdir -p /etc/lfr-tunneld
        if [ -f /etc/lfr-tunneld/server-config.yaml ]; then
            sudo cp /etc/lfr-tunneld/server-config.yaml /etc/lfr-tunneld/server-config.yaml.backup-\$(date +%Y-%m-%d_%H-%M-%S)
        fi
        sudo mv /home/$VPS_USER/server-config.yaml /etc/lfr-tunneld/server-config.yaml
        sudo chmod 600 /etc/lfr-tunneld/server-config.yaml
        sudo chown lfr-tunnel:lfr-tunnel /etc/lfr-tunneld/server-config.yaml
    fi
    
    sudo systemctl restart lfr-tunneld
REMOTE_SSH

echo "Deployment complete!"
