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
    # errexit off around the whole nvm block, not just "|| true" on each call. Sourcing nvm.sh
    # runs its auto-use, which returns 3 in a non-interactive shell with no .nvmrc -- and nvm
    # restores whatever errexit setting it found on the way in, so with set -e active it
    # re-arms errexit and then returns non-zero, killing this script before it ever reaches
    # the explicit-PATH fallback written for exactly this case. It exited 3 having printed
    # nothing at all, which is why nobody could see what had gone wrong.
    set +e
    # shellcheck disable=SC1091
    . "$HOME/.nvm/nvm.sh"
    nvm use 22.23.1
    set -e
fi
# Fallback to explicit path if nvm didn't load properly in non-interactive shell
export PATH="$HOME/.nvm/versions/node/v22.23.1/bin:$PATH"

# Generate a unique project name to avoid container collisions between agents
if [ -z "$E2E_PROJECT_NAME" ]; then
    E2E_PROJECT_NAME="lfr-tunnel-e2e-ui-$$"
fi
export E2E_PROJECT_NAME

# Shared `docker-compose` wrapper -- selects v2 vs v1 by capability, not by existence (#1355).
# shellcheck source=../tests/e2e/lib/compose.sh
. "$(dirname -- "${BASH_SOURCE[0]}")/../tests/e2e/lib/compose.sh"

export E2E_MAILPIT_PORT=8025
export E2E_PROXY_PORT=8000

echo "=== Building UI ==="
cd ui && pnpm install && pnpm run build
cd ..

echo "=== Syncing UI bundle to Go embedded filesystem ==="
rm -rf pkg/server/ui-dist
cp -r ui/dist pkg/server/ui-dist
# .gitkeep is tracked -- //go:embed ui-dist/* fails to compile on an empty directory (#1196).
# The rm above takes it with the bundle, so every run left it deleted in git status, ready to
# be committed by accident.
touch pkg/server/ui-dist/.gitkeep

echo "=== Starting Docker Compose for E2E UI Tests ==="
cd tests/e2e || exit 1
echo "=== Building Docker Images ==="
# Pre-pull the base images, retrying a transient registry error (#1530). Docker Hub 504d on
# node:20-alpine and failed a required check on an unrelated PR; the build itself has no retry
# around its FROM resolution. Never fatal -- if a pull really fails, the build below says so.
"$(dirname -- "${BASH_SOURCE[0]}")/common/pull-images-with-retry.sh" \
    "$(dirname -- "${BASH_SOURCE[0]}")/../tests/e2e/docker-compose.yml" || true

docker-compose build --no-cache lfr-tunnel lfr-tunneld
echo "=== Starting E2E Environment ==="
docker-compose up -d mailpit mock-target lfr-tunneld nginx-proxy lfr-tunnel

# Wait for services to be healthy
echo "=== Waiting for services to become healthy ==="
HEALTHY=false
for _ in {1..30}; do
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
