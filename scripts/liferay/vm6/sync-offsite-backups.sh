#!/usr/bin/env bash
# scripts/liferay/vm6/sync-offsite-backups.sh
# Syncs local backups from the central VPS to the Edge server for offsite redundancy.

set -e

# Default variables
LOCAL_BACKUPS_DIR="/etc/lfr-tunneld/"
LOCAL_DB_BACKUPS_DIR="/etc/lfr-tunneld/backups/"
EDGE_USER="ubuntu"
EDGE_HOST="us.lfr-demo.se"
EDGE_REMOTE_DIR="/home/$EDGE_USER/central-backups"
# $HOME, not "~": the tilde does not expand inside quotes, and `ssh -i` does not expand it
# either -- so this resolved to a literal ./~/.ssh/id_rsa and every rsync below failed to
# find the key (#1366).
SSH_KEY="$HOME/.ssh/id_rsa"

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
