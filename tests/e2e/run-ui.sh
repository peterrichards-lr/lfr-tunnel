#!/bin/bash
set -e

# Make sure we're in the right directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

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
docker run --rm --network host -v /var/run/docker.sock:/var/run/docker.sock -v "$(pwd)/ui":/e2e -w /e2e mcr.microsoft.com/playwright:v1.60.0-jammy /bin/sh -c "apt-get update && apt-get install -y docker.io && npm install && npx playwright test"
TEST_EXIT_CODE=$?

echo "=== Tearing Down Test Environment ==="
if [ $TEST_EXIT_CODE -ne 0 ]; then
    echo -e "\n❌ Tests failed. Printing Server Logs:\n"
    docker compose logs
    exit 1
fi

echo "=== Tearing down environment ==="
docker compose down -v || docker-compose down -v

echo "=== UI Tests Complete! ==="
