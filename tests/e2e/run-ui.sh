#!/bin/bash
set -e

# Make sure we're in the right directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Registered before the stack is started, so an interrupt or a failure anywhere below still
# reaps it (#1628). This script previously had no trap at all: `set -e` meant a failing
# Playwright run exited before TEST_EXIT_CODE was even read, and the explicit failure path
# below exited without tearing down -- so every failed run leaked the whole stack, holding
# ports 8000, 8025 and 4040. The lfr-tunnel container's entrypoint is `sleep infinity`, so it
# never exits on its own either.
cleanup() {
    if [ -n "${E2E_KEEP_STACK:-}" ]; then
        echo "=== E2E_KEEP_STACK set -- leaving the stack up for inspection ==="
        echo "    Tear it down with: (cd tests/e2e && docker compose down -v --remove-orphans)"
        return
    fi
    echo "=== Tearing down environment ==="
    docker compose down -v --remove-orphans 2>/dev/null \
        || docker-compose down -v --remove-orphans 2>/dev/null \
        || true
}
trap cleanup EXIT INT TERM

echo "=== Tearing down old environment ==="
docker compose down -v --remove-orphans || docker-compose down -v --remove-orphans

echo "=== Rebuilding and starting environment ==="
docker compose up -d --build || docker-compose up -d --build

echo "=== Waiting for Server to be ready ==="
sleep 5 # Wait for DB and Go server to initialize
until curl --output /dev/null --silent --fail http://localhost:8000/api/version; do
    printf '.'
    sleep 2
done

echo -e "\n✅ Environment is ready at http://localhost:8000"
echo "Mailpit is available at http://localhost:8025"

echo "=== Starting Client Tunnel ==="
# Generate PAT via magic link
CLI_PAT=$(python3 get-pat.py)
if [ -z "$CLI_PAT" ]; then
    echo "❌ Failed to generate CLI PAT!"
    docker compose down -v
    exit 1
fi

docker compose exec -d lfr-tunnel /bin/sh -c "./lfr-tunnel -server http://tunnel.lfr-demo.local -token $CLI_PAT -subdomain client-ui-test -ports 80" || docker compose exec -d lfr-tunnel /bin/sh -c "./lfr-tunnel -server http://tunnel.lfr-demo.local -token $CLI_PAT -subdomain client-ui-test -ports 80"

echo "=== Waiting for Client Inspector to be ready ==="
until curl --output /dev/null --silent --fail http://localhost:4040/api/config; do
    printf '.'
    sleep 2
done

echo -e "\n✅ Client Inspector is ready at http://localhost:4040"

echo "=== Running Playwright Tests ==="
# `|| TEST_EXIT_CODE=$?` rather than a bare command followed by `TEST_EXIT_CODE=$?`: under
# `set -e` the failing command exited the script before that assignment was ever reached, which
# is the one case it exists for. Note `|| true` does NOT work here -- it makes the list succeed
# and `$?` read 0, so every run would look green.
TEST_EXIT_CODE=0
docker run --rm --network host -v /var/run/docker.sock:/var/run/docker.sock -v "$(pwd)/ui":/e2e -w /e2e mcr.microsoft.com/playwright:v1.60.0-jammy /bin/sh -c "apt-get update && apt-get install -y docker.io && npm install && npx playwright test" || TEST_EXIT_CODE=$?

if [ $TEST_EXIT_CODE -ne 0 ]; then
    echo -e "\n❌ Tests failed. Printing Server Logs:\n"
    docker compose logs
    # Teardown is the EXIT trap's job, so failing here reaps the stack like any other exit.
    exit 1
fi

echo "=== UI Tests Complete! ==="
