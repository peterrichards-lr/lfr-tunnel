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

echo "=== Running Playwright Tests ==="
cd ui
npx playwright test || {
    echo -e "\n❌ Tests failed. Printing Server Logs:\n"
    docker compose logs
    exit 1
}

echo "=== Tearing down environment ==="
cd ..
docker compose down -v || docker-compose down -v

echo "=== UI Tests Complete! ==="
