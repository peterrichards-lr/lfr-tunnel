#!/bin/bash
set -e

# Change directory to script location
CDPATH= cd -- "$(dirname -- "$0")"

echo "=== Tearing down old environment ==="
docker-compose down -v --remove-orphans || true

echo "=== Rebuilding and starting environment ==="
docker-compose up --build -d mock-target mailpit lfr-tunneld nginx-proxy

echo "=== Waiting for Server to be ready ==="
for i in {1..30}; do
    if curl -s -f http://localhost:8000/api/domains > /dev/null; then
        echo "✅ Environment is ready at http://localhost:8000"
        break
    fi
    sleep 1
done

echo "Mailpit is available at http://localhost:8025"
