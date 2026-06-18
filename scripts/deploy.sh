#!/usr/bin/env bash
set -e

VPS_USER="peterrichards"
VPS_IP="lfr-demo.se"

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

echo "Building Linux binary with path trimming..."
GOOS=linux GOARCH=amd64 go build -trimpath -o bin/lfr-tunneld-linux ./cmd/lfr-tunneld

echo "Uploading binary to VPS..."
scp $SSH_KEY bin/lfr-tunneld-linux $VPS_USER@$VPS_IP:/home/$VPS_USER/lfr-tunneld

echo "Uploading error pages to VPS..."
scp $SSH_KEY -r resources/server/error_pages $VPS_USER@$VPS_IP:/home/$VPS_USER/

echo "Uploading static assets to VPS..."
scp $SSH_KEY -r pkg/server/static $VPS_USER@$VPS_IP:/home/$VPS_USER/

echo "Uploading properties translations and email templates to VPS..."
scp $SSH_KEY -r pkg/server/i18n $VPS_USER@$VPS_IP:/home/$VPS_USER/
scp $SSH_KEY -r pkg/server/templates $VPS_USER@$VPS_IP:/home/$VPS_USER/

echo "Executing remote deployment commands..."
ssh $SSH_KEY $VPS_USER@$VPS_IP << REMOTE_SSH
    sudo mv /home/$VPS_USER/lfr-tunneld /usr/local/bin/lfr-tunneld
    sudo chmod +x /usr/local/bin/lfr-tunneld
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
