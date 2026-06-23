#!/usr/bin/env bash
set -e

VPS_USER="peterrichards"
VPS_IP="lfr-demo.se"

# Parse optional parameters
SSH_KEY=""
WARN_SECS=""
while getopts "i:w:" opt; do
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
    *) echo "Usage: $0 [-i <identity_file>] [-w <warning_seconds>]" && exit 1 ;;
  esac
done
shift $((OPTIND - 1))

VERSION="${VERSION:-$(git describe --tags --abbrev=0 --dirty 2>/dev/null || git describe --always --dirty 2>/dev/null || echo "dev")}"

echo "Building Linux binary (version: $VERSION) with path trimming..."
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o bin/lfr-tunneld-linux ./cmd/lfr-tunneld

if [ -n "$WARN_SECS" ]; then
  if ! [[ "$WARN_SECS" =~ ^[0-9]+$ ]]; then
    echo "Error: warning time must be a positive integer"
    exit 1
  fi

  echo "Broadcasting maintenance warning to users via VPS localhost API..."
  ssh $SSH_KEY $VPS_USER@$VPS_IP "curl -s -X POST -H 'Content-Type: application/json' -d '{\"message\":\"Gateway is restarting for updates in $WARN_SECS seconds. Active sessions will be temporarily invalidated.\"}' http://127.0.0.1:8080/api/local/broadcast"

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
scp $SSH_KEY scripts/enable-maintenance.sh scripts/disable-maintenance.sh scripts/restore-with-maintenance.sh scripts/restore-backup.sh $VPS_USER@$VPS_IP:/home/$VPS_USER/

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
    
    sudo systemctl restart lfr-tunneld
REMOTE_SSH

echo "Deployment complete!"
