#!/bin/bash
set -e

# Fallback to "docker compose" if "docker-compose" is not installed
if ! command -v docker-compose >/dev/null 2>&1; then
    docker-compose() {
        docker compose "$@"
    }
fi

# Change directory to script location
CDPATH= cd -- "$(dirname -- "$0")"

echo "=== Starting E2E Docker Integration Test ==="

# Clean previous containers
docker-compose down -v --remove-orphans || true

# Start mock target, mailpit, server, proxy and client
echo "=== Spinning up Docker environment ==="
docker-compose up --build -d mock-target mailpit lfr-tunneld nginx-proxy lfr-tunnel

# Wait for Mailpit to be fully online
echo "=== Waiting for Mailpit to be ready ==="
for i in {1..15}; do
    if curl -s http://localhost:8025/api/v1/messages > /dev/null; then
        echo "Mailpit is ready!"
        break
    fi
    echo "Waiting for Mailpit..."
    sleep 1
done

# Wait for Nginx proxy to be fully online
echo "=== Waiting for Nginx proxy to be ready ==="
for i in {1..15}; do
    if curl -s http://localhost:8000/api/domains > /dev/null; then
        echo "Nginx proxy is ready!"
        break
    fi
    echo "Waiting for Nginx proxy..."
    sleep 1
done

# 1. Submit registration request
echo "=== Submitting registration request ==="
REG_REQ_RESP=$(curl -s -X POST -H "Content-Type: application/json" \
     -d '{"email": "developer@lfr-demo.se", "requested_subdomain": "peter-dev"}' \
     "http://localhost:8000/api/register-request")

echo "Registration request response: $REG_REQ_RESP"
sleep 2

# 2. Extract approval token from Mailpit
echo "=== Extracting admin approval token ==="
APPROVAL_TOKEN=$(python3 -c '
import urllib.request, json, re
try:
    data = json.loads(urllib.request.urlopen("http://localhost:8025/api/v1/messages").read())
    if not data["messages"]:
        print("")
        exit(0)
    msg_id = data["messages"][0]["ID"]
    msg = json.loads(urllib.request.urlopen(f"http://localhost:8025/api/v1/message/{msg_id}").read())
    body = msg["Text"]
    # Look for token in URL like /api/admin/approve?email=...&token=...
    match = re.search(r"token=([a-f0-9]+)", body)
    if match:
        print(match.group(1))
    else:
        print("")
except Exception as e:
    import sys
    print(f"Error: {e}", file=sys.stderr)
    print("")
')

if [ -z "$APPROVAL_TOKEN" ]; then
    echo "❌ Failed to extract approval token from Mailpit!"
    echo "=== lfr-tunneld logs ==="
    docker-compose logs lfr-tunneld
    echo "=== Mailpit messages ==="
    curl -s http://localhost:8025/api/v1/messages
    docker-compose down -v
    exit 1
fi
echo "Extracted Approval Token: $APPROVAL_TOKEN"

# 3. Call Admin Approval endpoint
echo "=== Approving developer request ==="
APPROVE_RESP=$(curl -s "http://localhost:8000/api/admin/approve?email=developer@lfr-demo.se&token=${APPROVAL_TOKEN}")
echo "Approval response: $APPROVE_RESP"
sleep 2

# 4. Extract claim token from Mailpit
echo "=== Extracting token claim token ==="
CLAIM_TOKEN=$(python3 -c '
import urllib.request, json, re
try:
    data = json.loads(urllib.request.urlopen("http://localhost:8025/api/v1/messages").read())
    # The newest message should be the claim token email
    msg_id = data["messages"][0]["ID"]
    msg = json.loads(urllib.request.urlopen(f"http://localhost:8025/api/v1/message/{msg_id}").read())
    body = msg["Text"]
    # Look for token in URL like /api/claim?token=...
    match = re.search(r"token=([a-f0-9]+)", body)
    if match:
        print(match.group(1))
    else:
        print("")
except Exception as e:
    import sys
    print(f"Error: {e}", file=sys.stderr)
    print("")
')

if [ -z "$CLAIM_TOKEN" ]; then
    echo "❌ Failed to extract claim token from Mailpit!"
    echo "=== lfr-tunneld logs ==="
    docker-compose logs lfr-tunneld
    echo "=== Mailpit messages ==="
    curl -s http://localhost:8025/api/v1/messages
    docker-compose down -v
    exit 1
fi
echo "Extracted Claim Token: $CLAIM_TOKEN"

# 5. Claim Personal Access Token (PAT)
echo "=== Claiming Personal Access Token (PAT) ==="
CLAIM_RESP=$(curl -s "http://localhost:8000/api/claim?token=${CLAIM_TOKEN}")
echo "Claim response: $CLAIM_RESP"

DEVELOPER_PAT=$(echo "$CLAIM_RESP" | python3 -c '
import sys, json
try:
    data = json.load(sys.stdin)
    print(data.get("personal_access_token", ""))
except:
    print("")
')

if [ -z "$DEVELOPER_PAT" ]; then
    echo "❌ Failed to parse Personal Access Token from claim response!"
    docker-compose down -v
    exit 1
fi
echo "Developer PAT claimed successfully: $DEVELOPER_PAT"

# 6. Start the Client Tunnel inside the container with the PAT using docker-compose run
echo "=== Starting client tunnel container ==="
CLIENT_CONTAINER_ID=$(docker-compose run -d \
  --entrypoint "./lfr-tunnel" \
  -e LFT_CLIENT_TOKEN="$DEVELOPER_PAT" \
  lfr-tunnel \
  -server http://tunnel.lfr-demo.se \
  -subdomain peter-dev \
  -ports 80)

echo "Client container ID: $CLIENT_CONTAINER_ID"

# Wait for client to connect and establish the tunnel
echo "=== Waiting for tunnel connection ==="
TUNNEL_READY=false
for i in {1..20}; do
    RESPONSE_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: peter-dev.lfr-demo.se" http://localhost:8000/ || true)
    if [ "$RESPONSE_CODE" = "200" ]; then
        echo "Tunnel is ready!"
        TUNNEL_READY=true
        break
    fi
    echo "Waiting for tunnel (HTTP status $RESPONSE_CODE)..."
    sleep 1
done

if [ "$TUNNEL_READY" = false ]; then
    echo "❌ Timeout waiting for tunnel connection!"
    echo "=== Client tunnel container stdout ==="
    docker logs "$CLIENT_CONTAINER_ID"
    echo "=== lfr-tunneld logs ==="
    docker-compose logs lfr-tunneld
    docker-compose down -v
    exit 1
fi

# Print client container logs to verify Chisel handshake
echo "=== Client tunnel container stdout ==="
docker logs "$CLIENT_CONTAINER_ID"

# 7. Query mock target subdomain through nginx-proxy
echo "=== Verifying routing through tunnel ==="
RESPONSE=$(curl -s -H "Host: peter-dev.lfr-demo.se" http://localhost:8000/)

echo "=== Response received ==="
echo "$RESPONSE"

# Verify content
if echo "$RESPONSE" | grep -q "Mock Liferay Instance"; then
    echo "✅ E2E Integration Test PASSED! Registration, Approval, and Tunnel routing work flawlessly!"
    docker-compose down -v
    exit 0
else
    echo "❌ E2E Integration Test FAILED! Staged response did not match expected output."
    docker-compose down -v
    exit 1
fi
