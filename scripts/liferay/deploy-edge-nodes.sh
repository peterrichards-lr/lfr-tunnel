#!/usr/bin/env bash
set -e

# Default variables
SSH_USER="peterrichards"
EDGE_NODES_FILE="edge_nodes.txt"
SSH_KEY=""

while getopts "i:u:f:" opt; do
  case $opt in
    i) 
      KEY_PATH="$OPTARG"
      if [[ "$KEY_PATH" == "~/"* ]]; then
        KEY_PATH="${HOME}/${KEY_PATH#~/}"
      elif [[ "$KEY_PATH" == "~" ]]; then
        KEY_PATH="${HOME}"
      fi
      SSH_KEY="-i $KEY_PATH"
      ;;
    u) SSH_USER="$OPTARG" ;;
    f) EDGE_NODES_FILE="$OPTARG" ;;
    *) echo "Usage: $0 [-i <identity_file>] [-u <ssh_user>] [-f <edge_nodes_file>]" && exit 1 ;;
  esac
done

if [ ! -f "$EDGE_NODES_FILE" ]; then
  echo "Error: Edge nodes file '$EDGE_NODES_FILE' not found"
  exit 1
fi

VERSION="${VERSION:-$(grep -oE 'Version = "[^"]+"' pkg/config/version.go | cut -d'"' -f2)}"
echo "Building Linux binary (version: $VERSION) with path trimming..."
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=$VERSION" -trimpath -o bin/lfr-tunneld-linux ./cmd/lfr-tunneld

while IFS= read -r line; do
  # Skip empty lines and comments
  [[ -z "$line" || "$line" == \#* ]] && continue
  
  # Parse format: id,token,url
  url=$(echo "$line" | awk -F',' '{print $3}')
  if [ -n "$url" ]; then
    host=$(echo "$url" | sed 's|^https://||' | sed 's|:.*||' | sed 's|/.*||')
    if [ -n "$host" ]; then
      echo "Deploying to Edge Node: $host..."
      scp -o StrictHostKeyChecking=no $SSH_KEY bin/lfr-tunneld-linux $SSH_USER@$host:/home/$SSH_USER/lfr-tunneld
      ssh -n -o StrictHostKeyChecking=no $SSH_KEY $SSH_USER@$host "sudo mv /home/$SSH_USER/lfr-tunneld /usr/local/bin/lfr-tunneld && sudo chmod +x /usr/local/bin/lfr-tunneld && sudo systemctl restart lfr-tunneld"
      echo "✅ Successfully deployed to $host."
    fi
  fi
done < "$EDGE_NODES_FILE"

echo "Edge Node deployment complete!"
