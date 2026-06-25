#!/usr/bin/env bash
# scripts/sync-offsite-backups.sh
# Syncs local backups from the central VPS to the Edge server for offsite redundancy.

set -e

# Default variables
LOCAL_BACKUPS_DIR="/etc/lfr-tunneld/"
LOCAL_DB_BACKUPS_DIR="/etc/lfr-tunneld/backups/"
EDGE_USER="ubuntu"
EDGE_HOST="us.lfr-demo.se"
EDGE_REMOTE_DIR="/home/$EDGE_USER/central-backups"
SSH_KEY="~/.ssh/id_rsa"

# Create remote directory if it doesn't exist
ssh -i "$SSH_KEY" "$EDGE_USER@$EDGE_HOST" "mkdir -p $EDGE_REMOTE_DIR/config_backups $EDGE_REMOTE_DIR/db_backups"

echo "Syncing configuration backups to $EDGE_HOST..."
rsync -avz -e "ssh -i $SSH_KEY" \
    --include="server-config.yaml.backup-*" \
    --exclude="*" \
    "$LOCAL_BACKUPS_DIR" "$EDGE_USER@$EDGE_HOST:$EDGE_REMOTE_DIR/config_backups/"

echo "Syncing database backups to $EDGE_HOST..."
rsync -avz -e "ssh -i $SSH_KEY" \
    "$LOCAL_DB_BACKUPS_DIR" "$EDGE_USER@$EDGE_HOST:$EDGE_REMOTE_DIR/db_backups/"

echo "Offsite sync complete."
