#!/bin/bash
set -e

# Change directory to project root
cd "$(dirname "$0")/.." || exit 1
PROJECT_ROOT="$(pwd)"
export PROJECT_ROOT

if [ -z "$HOME" ]; then
    export HOME="/Users/peterrichards"
fi

if [ -s "$HOME/.nvm/nvm.sh" ]; then
    source "$HOME/.nvm/nvm.sh"
    nvm use 22.23.1 || true
fi
# Fallback to explicit path if nvm didn't load properly in non-interactive shell
export PATH="$HOME/.nvm/versions/node/v22.23.1/bin:$PATH"

# Generate a unique project name to avoid container collisions between agents
if [ -z "$E2E_PROJECT_NAME" ]; then
    E2E_PROJECT_NAME="lfr-tunnel-e2e-ui-$$"
fi
export E2E_PROJECT_NAME

# Fallback to "docker compose" if "docker-compose" is not installed, wrapping with project name
docker-compose() {
    if docker compose version >/dev/null 2>&1; then
        docker compose -p "$E2E_PROJECT_NAME" "$@"
    else
        command docker-compose -p "$E2E_PROJECT_NAME" "$@"
    fi
}

export E2E_MAILPIT_PORT=8025
export E2E_PROXY_PORT=8000

echo "=== Building UI ==="
cd ui && pnpm install && pnpm run build
cd ..

echo "=== Syncing UI bundle to Go embedded filesystem ==="
rm -rf pkg/server/ui-dist
cp -r ui/dist pkg/server/ui-dist

echo "=== Starting Docker Compose for E2E UI Tests ==="
cd tests/e2e || exit 1
echo "=== Building Docker Images ==="
docker-compose build --no-cache lfr-tunnel lfr-tunneld
echo "=== Starting E2E Environment ==="
docker-compose up -d mailpit mock-target lfr-tunneld nginx-proxy lfr-tunnel

# Wait for services to be healthy
echo "=== Waiting for services to become healthy ==="
HEALTHY=false
for i in {1..30}; do
    RESPONSE_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:${E2E_PROXY_PORT}/api/version || true)
    if [ "$RESPONSE_CODE" = "200" ]; then
        HEALTHY=true
        break
    fi
    echo "Waiting for lfr-tunneld (HTTP $RESPONSE_CODE)..."
    sleep 2
done

if [ "$HEALTHY" = false ]; then
    echo "❌ Timeout waiting for services to become healthy!"
    docker-compose logs
    docker-compose down -v
    exit 1
fi

cleanup() {
    echo "=== Tearing down Docker Compose ==="
    cd "$PROJECT_ROOT/tests/e2e" || true
    docker-compose down -v || true
}
trap cleanup EXIT

echo "=== Running Playwright UI Tests ==="
cd "$PROJECT_ROOT/tests/e2e/ui" || exit 1
pnpm install
pnpm exec playwright install --with-deps chromium

export INSPECTOR_URL="http://localhost:${E2E_PROXY_PORT}"

# Run tests
pnpm exec playwright test
