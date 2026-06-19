#!/bin/bash

# Exit on error
set -e

# Load local environment configuration if present
if [ -f .env ]; then
  # Load env variables (filtering out comments)
  export $(grep -v '^#' .env | xargs)
fi

# Check if LFT_TOKEN is set
if [ -z "$LFT_TOKEN" ]; then
  echo "Error: LFT_TOKEN is not set."
  echo "Please copy '.env.example' to '.env' and configure your token."
  exit 1
fi

# Set default values if not configured
LFT_SERVER="${LFT_SERVER:-https://lfr-demo.se}"
LFT_PORTS="${LFT_PORTS:-8080}"

# Check if Docker image is built
if ! docker image inspect lfr-tunnel-client:latest >/dev/null 2>&1; then
  echo "Docker image 'lfr-tunnel-client:latest' not found. Building it now..."
  docker build --load -t lfr-tunnel-client:latest .
fi

# Determine if we should run docker interactively (if stdin is a terminal)
if [ -t 0 ]; then
  DOCKER_FLAGS="-it"
else
  DOCKER_FLAGS="-i"
fi

echo "[Docker Client] Launching tunnel..."
# Set default target host if not configured
LFT_TARGET_HOST="${LFT_TARGET_HOST:-host.docker.internal}"

# Execute the docker containerized client
docker run --rm $DOCKER_FLAGS \
  -e LFT_TARGET_HOST="$LFT_TARGET_HOST" \
  lfr-tunnel-client:latest \
  -server "$LFT_SERVER" \
  -token "$LFT_TOKEN" \
  ${LFT_SUBDOMAIN:+-subdomain "$LFT_SUBDOMAIN"} \
  -ports "$LFT_PORTS" \
  "$@"
