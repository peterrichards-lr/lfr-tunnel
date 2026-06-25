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

# Signal file configuration
REPO_ROOT="$(cd ../.. && pwd)"
SIGNAL_FILE="${REPO_ROOT}/.progress-signal"

echo "BUILDING" > "$SIGNAL_FILE"

# Make sure we write FAILED or SUCCESS on exit
cleanup() {
    EXIT_CODE=$?
    echo "=== Cleaning up E2E Edge resources ==="
    if [ -n "$CLIENT_CONTAINER_ID" ]; then
        echo "Stopping and removing explicit client container: $CLIENT_CONTAINER_ID"
        docker stop "$CLIENT_CONTAINER_ID" >/dev/null 2>&1 || true
        docker rm "$CLIENT_CONTAINER_ID" >/dev/null 2>&1 || true
    fi
    if [ -n "$AUTO_CLIENT_ID" ]; then
        echo "Stopping and removing auto client container: $AUTO_CLIENT_ID"
        docker stop "$AUTO_CLIENT_ID" >/dev/null 2>&1 || true
        docker rm "$AUTO_CLIENT_ID" >/dev/null 2>&1 || true
    fi
    docker-compose -f docker-compose-edge.yml down -v --remove-orphans >/dev/null 2>&1 || true

    if [ $EXIT_CODE -eq 0 ]; then
        echo "SUCCESS" > "$SIGNAL_FILE"
    else
        echo "FAILED" > "$SIGNAL_FILE"
    fi
}
trap cleanup EXIT INT TERM ERR

echo "=== Starting E2E Edge Docker Integration Test ==="

# Clean previous containers
docker-compose -f docker-compose-edge.yml down -v --remove-orphans || true

# Start services
echo "=== Spinning up Docker Edge environment ==="
docker-compose -f docker-compose-edge.yml up --build -d mock-target mailpit lfr-tunneld-control lfr-tunneld-edge nginx-proxy lfr-tunnel

echo "WAITING_HEALTHY" > "$SIGNAL_FILE"

# Wait for Mailpit to be fully online
echo "=== Waiting for Mailpit ==="
for i in {1..15}; do
    if curl -s http://localhost:8025/api/v1/messages > /dev/null; then
        echo "Mailpit is ready!"
        break
    fi
    sleep 1
done

# Wait for Nginx proxy to be fully online
echo "=== Waiting for Nginx proxy ==="
for i in {1..30}; do
    if curl -s -f http://localhost:8000/api/domains > /dev/null; then
        echo "Nginx proxy is ready!"
        break
    fi
    sleep 1
done

echo "TESTING" > "$SIGNAL_FILE"

# 1. Submit registration request
echo "=== Submitting registration request ==="
REG_REQ_RESP=$(curl -s -X POST -H "Content-Type: application/json" \
     -d '{"email": "developer@lfr-demo.local", "requested_subdomain": "peter-dev"}' \
     "http://localhost:8000/api/register-request")

echo "Registration request response: $REG_REQ_RESP"
sleep 2

# 2. Extract verification token
echo "=== Extracting verification token ==="
VERIFICATION_TOKEN=$(python3 -c '
import urllib.request, json, re
try:
    data = json.loads(urllib.request.urlopen("http://localhost:8025/api/v1/messages").read())
    for m in data["messages"]:
        msg_id = m.get("ID")
        msg = json.loads(urllib.request.urlopen("http://localhost:8025/api/v1/message/" + msg_id).read())
        body = (msg.get("HTML") or "") + "\n" + (msg.get("Text") or "")
        match = re.search(r"setup\?token=([a-f0-9A-Z]+)", body, re.IGNORECASE)
        if match:
            print(match.group(1))
            exit(0)
except Exception as e:
    import sys; print(f"Error: {e}", file=sys.stderr)
')

if [ -z "$VERIFICATION_TOKEN" ]; then
    echo "❌ Failed to extract verification token!"
    exit 1
fi
echo "Extracted Verification Token: $VERIFICATION_TOKEN"

# 2.5. Call Verification
VERIFY_RESP=$(curl -s "http://localhost:8000/api/verify-email?token=${VERIFICATION_TOKEN}")
echo "Verify response: $VERIFY_RESP"
sleep 2

# 3. Extract admin approval token
APPROVAL_TOKEN=$(python3 -c '
import urllib.request, json, re
try:
    data = json.loads(urllib.request.urlopen("http://localhost:8025/api/v1/messages").read())
    for m in data["messages"]:
        msg_id = m.get("ID")
        msg = json.loads(urllib.request.urlopen("http://localhost:8025/api/v1/message/" + msg_id).read())
        body = (msg.get("HTML") or "") + "\n" + (msg.get("Text") or "")
        match = re.search(r"approve\?email=[^&]+&token=([a-f0-9]+)", body)
        if match:
            print(match.group(1))
            exit(0)
except Exception as e:
    import sys; print(f"Error: {e}", file=sys.stderr)
')

if [ -z "$APPROVAL_TOKEN" ]; then
    echo "❌ Failed to extract approval token!"
    exit 1
fi
echo "Extracted Approval Token: $APPROVAL_TOKEN"

# 3.5. Approve developer
APPROVE_RESP=$(curl -s "http://localhost:8000/api/admin/approve?email=developer@lfr-demo.local&token=${APPROVAL_TOKEN}")
echo "Approval response: $APPROVE_RESP"
sleep 2

# 4. Extract claim token
CLAIM_TOKEN=$(python3 -c '
import urllib.request, json, re
try:
    data = json.loads(urllib.request.urlopen("http://localhost:8025/api/v1/messages").read())
    for m in data["messages"]:
        msg_id = m.get("ID")
        msg = json.loads(urllib.request.urlopen("http://localhost:8025/api/v1/message/" + msg_id).read())
        body = (msg.get("HTML") or "") + "\n" + (msg.get("Text") or "")
        match = re.search(r"claim\?token=([a-f0-9]+)", body)
        if match:
            print(match.group(1))
            exit(0)
except Exception as e:
    import sys; print(f"Error: {e}", file=sys.stderr)
')

if [ -z "$CLAIM_TOKEN" ]; then
    echo "❌ Failed to extract claim token!"
    exit 1
fi
echo "Extracted Claim Token: $CLAIM_TOKEN"

# 5. Claim PAT
CLAIM_RESP=$(curl -s "http://localhost:8000/api/claim?token=${CLAIM_TOKEN}")
DEVELOPER_PAT=$(echo "$CLAIM_RESP" | python3 -c '
import sys, json
try:
    data = json.load(sys.stdin)
    print(data.get("personal_access_token", ""))
except:
    print("")
')

if [ -z "$DEVELOPER_PAT" ]; then
    echo "❌ Failed to claim PAT!"
    exit 1
fi
echo "Developer PAT claimed: $DEVELOPER_PAT"

# 5.5. Reserve subdomains
echo "=== Requesting magic link ==="
curl -s -X POST -H "Content-Type: application/json" \
     -d '{"email": "developer@lfr-demo.local"}' \
     "http://localhost:8000/api/auth/magic-link"
sleep 2

DEV_ML_TOKEN=$(python3 -c '
import urllib.request, json, re
try:
    data = json.loads(urllib.request.urlopen("http://localhost:8025/api/v1/messages").read())
    for m in data.get("messages", []):
        msg = json.loads(urllib.request.urlopen("http://localhost:8025/api/v1/message/" + m["ID"]).read())
        body = msg.get("Text","") or msg.get("HTML","")
        match = re.search(r"token=([a-f0-9]+)", body, re.IGNORECASE)
        if match:
            print(match.group(1))
            exit(0)
except Exception as e:
    import sys; print(f"Error: {e}", file=sys.stderr)
')

if [ -z "$DEV_ML_TOKEN" ]; then
    echo "❌ Failed to extract magic link token!"
    exit 1
fi

curl -s -c /tmp/dev-session.txt -X POST -H "Content-Type: application/json" \
     -d "{\"token\": \"$DEV_ML_TOKEN\"}" \
     "http://localhost:8000/api/auth/verify"

echo "=== Reserving subdomain peter-dev ==="
curl -s -b /tmp/dev-session.txt -X POST -H "Content-Type: application/json" \
     -d '{"subdomain": "peter-dev", "domain": "us.lfr-demo.local"}' \
     "http://localhost:8000/api/portal/reservations"

echo "=== Reserving subdomain peter-auto ==="
curl -s -b /tmp/dev-session.txt -X POST -H "Content-Type: application/json" \
     -d '{"subdomain": "peter-auto", "domain": "lfr-demo.local"}' \
     "http://localhost:8000/api/portal/reservations"
curl -s -b /tmp/dev-session.txt -X POST -H "Content-Type: application/json" \
     -d '{"subdomain": "peter-auto", "domain": "us.lfr-demo.local"}' \
     "http://localhost:8000/api/portal/reservations"
sleep 2

# 6. Test Explicit Edge Gateway Connection (-region us)
echo "=== Starting explicit regional edge client tunnel ==="
CLIENT_CONTAINER_ID=$(docker-compose -f docker-compose-edge.yml run -d --no-deps \
  --entrypoint "./lfr-tunnel" \
  -e LFT_CLIENT_TOKEN="$DEVELOPER_PAT" \
  lfr-tunnel \
  -config client-config-edge.yaml \
  -region us \
  -subdomain peter-dev \
  -ports 80)

echo "Explicit Client Container: $CLIENT_CONTAINER_ID"

# Wait for tunnel connection
echo "=== Waiting for explicit regional tunnel ==="
TUNNEL_READY=false
for i in {1..20}; do
    RESPONSE_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: peter-dev.us.lfr-demo.local" http://localhost:8000/ || true)
    if [ "$RESPONSE_CODE" = "200" ]; then
        echo "Regional edge tunnel is ready!"
        TUNNEL_READY=true
        break
    fi
    echo "Waiting for regional tunnel (HTTP $RESPONSE_CODE)..."
    sleep 1
done

if [ "$TUNNEL_READY" = false ]; then
    echo "❌ Timeout waiting for regional tunnel!"
    echo "=== Client logs ==="
    docker logs "$CLIENT_CONTAINER_ID"
    echo "=== Control Plane logs ==="
    docker-compose -f docker-compose-edge.yml logs lfr-tunneld-control
    echo "=== Edge logs ==="
    docker-compose -f docker-compose-edge.yml logs lfr-tunneld-edge
    exit 1
fi

echo "=== Verifying routing through explicit regional tunnel ==="
RESPONSE=$(curl -s -H "Host: peter-dev.us.lfr-demo.local" http://localhost:8000/)
if ! echo "$RESPONSE" | grep -q "Mock Liferay Instance"; then
    echo "❌ Edge routing content mismatch!"
    exit 1
fi
echo "✅ Explicit regional edge tunnel routing verified successfully!"

# 7. Test Auto-Probing Latency Selection
echo "=== Starting auto-probing client tunnel ==="
AUTO_CLIENT_ID=$(docker-compose -f docker-compose-edge.yml run -d --no-deps \
  --entrypoint "./lfr-tunnel" \
  -e LFT_CLIENT_TOKEN="$DEVELOPER_PAT" \
  lfr-tunnel \
  -config client-config-edge.yaml \
  -subdomain peter-auto \
  -ports 80)

echo "Auto-probing Client Container: $AUTO_CLIENT_ID"

# Wait for auto-probed tunnel connection
echo "=== Waiting for auto-probed tunnel ==="
AUTO_TUNNEL_READY=false
for i in {1..20}; do
    CODE_EU=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: peter-auto.lfr-demo.local" http://localhost:8000/ || true)
    CODE_US=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: peter-auto.us.lfr-demo.local" http://localhost:8000/ || true)
    if [ "$CODE_EU" = "200" ] || [ "$CODE_US" = "200" ]; then
        echo "Auto-probed tunnel connected successfully (EU: $CODE_EU, US: $CODE_US)!"
        AUTO_TUNNEL_READY=true
        break
    fi
    echo "Waiting for auto-probed tunnel..."
    sleep 1
done

if [ "$AUTO_TUNNEL_READY" = false ]; then
    echo "❌ Timeout waiting for auto-probed tunnel!"
    echo "=== Auto Client logs ==="
    docker logs "$AUTO_CLIENT_ID"
    exit 1
fi

# Print logs to inspect auto-probing output
echo "=== Auto Client stdout ==="
docker logs "$AUTO_CLIENT_ID"

echo "✅ All Multi-Region Edge E2E Integration Tests PASSED!"
exit 0
