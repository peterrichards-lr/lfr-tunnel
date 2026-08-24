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

# Build the images before choosing the stop window. A cold build takes minutes, and computing
# the window first meant the stop had already passed by the time the stack was running -- the
# warning window closed before anything could observe it.
echo "=== Building images (before the schedule clock starts) ==="
docker-compose -f docker-compose-edge.yml build

# Generate the control config with a stop window a couple of minutes out, so the shutdown
# migration test below has a real scheduled stop to react to (#1254).
#
# The times have to be computed at run time -- a committed "00:00" would mean waiting until
# midnight. E2E_STOP_MINUTES is how far ahead the stop is; the warning fires one minute before
# it (edge_shutdown_warning_minutes in the config), and central only evaluates schedules once
# per 60s health cycle, so anything under about three minutes risks the window closing before
# it is noticed.
E2E_STOP_MINUTES="${E2E_STOP_MINUTES:-5}"
if date -u -v+1M >/dev/null 2>&1; then
    # BSD date (macOS)
    E2E_STOP_TIME=$(date -u -v+${E2E_STOP_MINUTES}M +%H:%M)
    E2E_START_TIME=$(date -u -v+$((E2E_STOP_MINUTES + 20))M +%H:%M)
else
    # GNU date (Linux, CI)
    E2E_STOP_TIME=$(date -u -d "+${E2E_STOP_MINUTES} minutes" +%H:%M)
    E2E_START_TIME=$(date -u -d "+$((E2E_STOP_MINUTES + 20)) minutes" +%H:%M)
fi
echo "=== Edge stop window for this run: stops ${E2E_STOP_TIME} UTC, starts ${E2E_START_TIME} UTC (now $(date -u +%H:%M:%S)) ==="
sed -e "s|__E2E_STOP_TIME__|${E2E_STOP_TIME}|" \
    -e "s|__E2E_START_TIME__|${E2E_START_TIME}|" \
    server-config-control.yaml > server-config-control.generated.yaml

if grep -q "__E2E_" server-config-control.generated.yaml; then
    echo "❌ Schedule placeholders were not substituted; the migration test would never fire."
    exit 1
fi

# Start services
echo "=== Spinning up Docker Edge environment ==="
docker-compose -f docker-compose-edge.yml up -d mock-target mailpit lfr-tunneld-control lfr-tunneld-edge nginx-proxy lfr-tunnel

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

# Fail here, loudly, if the edge never joins the control plane.
#
# Without this the symptom surfaced much later as "Timeout waiting for regional tunnel",
# with the actual cause -- "Authentication failed: unknown node_id" -- buried in container
# logs. The whole suite ran for a long time against an edge that had never authenticated,
# which is exactly the false confidence this test exists to remove (#1254).
echo "=== Waiting for the edge to join the control plane ==="
EDGE_JOINED=false
for i in {1..30}; do
    if docker-compose -f docker-compose-edge.yml logs lfr-tunneld-control 2>&1 | grep -q "successfully authenticated"; then
        echo "Edge control channel is up."
        EDGE_JOINED=true
        break
    fi
    sleep 2
done

if [ "$EDGE_JOINED" = false ]; then
    echo "❌ The edge never authenticated with the control plane."
    echo "    Everything downstream -- schedules, shutdown warnings, lease kicks, regional"
    echo "    routing -- depends on this channel, so the rest of the suite would be testing"
    echo "    an edge the control plane does not know about."
    echo "    Check that the node id central expects matches the one the edge derives from"
    echo "    its token (edgeNodeIDFromToken: the token minus its last '-' segment)."
    echo "=== Edge logs ==="
    docker-compose -f docker-compose-edge.yml logs lfr-tunneld-edge | grep -iE "Edge Control" | tail -10
    echo "=== Control Plane logs ==="
    docker-compose -f docker-compose-edge.yml logs lfr-tunneld-control | grep -iE "Edge WS" | tail -10
    exit 1
fi

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
# POST, not GET: approving on GET meant any fetch of the emailed link approved the
# user, and that link is delivered to a chat channel where previews follow URLs (#1143).
APPROVE_RESP=$(curl -s -X POST "http://localhost:8000/api/admin/approve" \
  --data-urlencode "email=developer@lfr-demo.local" \
  --data-urlencode "token=${APPROVAL_TOKEN}")
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
# Reserved on the central domain as well, because a reservation is currently per-domain and the
# planned move below re-registers on whichever gateway it elects. Without this the move fails
# with a 403 quota error, which is the region-scoped-hostname defect (#1285) showing up as a
# reservation problem. Drop this second call once hostnames stop carrying the region.
curl -s -b /tmp/dev-session.txt -X POST -H "Content-Type: application/json" \
     -d '{"subdomain": "peter-dev", "domain": "lfr-demo.local"}' \
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

# 8. Test the planned move ahead of a scheduled edge shutdown (#1246).
#
# The explicit -region us client from step 6 is still attached to the edge, and the edge is
# scheduled to stop at E2E_STOP_TIME with a one-minute warning. It should leave before that
# happens rather than being dropped by it -- which is the whole point: measured against real
# infrastructure, a client that waited to be dropped was down 24m36s.
#
# Asserted from the client's own log rather than by timing, so a slow container cannot make
# this pass or fail spuriously.
echo "=== Waiting for the scheduled stop to trigger a planned move (stop at ${E2E_STOP_TIME} UTC) ==="
MIGRATED=false
for i in {1..150}; do
    CLIENT_LOG=$(docker logs "$CLIENT_CONTAINER_ID" 2>&1 || true)
    if echo "$CLIENT_LOG" | grep -q "moving to another gateway now"; then
        echo "Client announced a planned move at $(date -u +%H:%M:%S) UTC"
        MIGRATED=true
        break
    fi
    sleep 2
done

if [ "$MIGRATED" = false ]; then
    echo "❌ The client never moved off an edge that announced it was stopping."
    echo "    Expected the shutdown warning to reach it and the migrator to end the session."
    echo "=== Client logs ==="
    docker logs "$CLIENT_CONTAINER_ID"
    echo "=== Control Plane logs (did it send the warning?) ==="
    docker-compose -f docker-compose-edge.yml logs lfr-tunneld-control | grep -iE "shutdown warning|schedule" | tail -20
    echo "=== Edge logs (did it receive and relay it?) ==="
    docker-compose -f docker-compose-edge.yml logs lfr-tunneld-edge | grep -iE "shutdown warning|Control plane says" | tail -20
    exit 1
fi

# The warning has to have actually arrived, not just the migrator having fired on stale state.
if ! docker logs "$CLIENT_CONTAINER_ID" 2>&1 | grep -q "Gateway reports it is shutting down"; then
    echo "❌ The client moved without ever logging the shutdown warning that should have caused it."
    docker logs "$CLIENT_CONTAINER_ID"
    exit 1
fi

# And it has to land somewhere that serves traffic. Which hostname it lands on is deliberately
# NOT asserted yet: today the gateway that registers a tunnel builds the hostname from its own
# domains, so moving between gateways changes the visitor's URL. That is the defect #1285
# fixes -- hostnames are supposed to be region-agnostic. Until it lands, accept either form and
# assert only what #1246 actually promises: the warning arrives and the tunnel comes back up
# somewhere. Tighten this to the region-agnostic host alone once #1285 has merged.
echo "=== Verifying the tunnel survived the move ==="
SURVIVED=false
SURVIVING_HOST=""
for i in {1..60}; do
    for HOST in peter-dev.lfr-demo.local peter-dev.us.lfr-demo.local; do
        CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: $HOST" http://localhost:8000/ || true)
        if [ "$CODE" = "200" ]; then
            SURVIVED=true
            SURVIVING_HOST="$HOST"
            break 2
        fi
    done
    sleep 2
done

if [ "$SURVIVED" = false ]; then
    echo "❌ The client moved but its tunnel never came back up on any gateway."
    echo "=== Client logs ==="
    docker logs "$CLIENT_CONTAINER_ID"
    exit 1
fi
echo "Tunnel answered again on ${SURVIVING_HOST}"

RESPONSE=$(curl -s -H "Host: $SURVIVING_HOST" http://localhost:8000/)
if ! echo "$RESPONSE" | grep -q "Mock Liferay Instance"; then
    echo "❌ The tunnel answered after the move but served the wrong content."
    exit 1
fi
echo "✅ Client moved off the stopping edge and kept serving!"

echo "✅ All Multi-Region Edge E2E Integration Tests PASSED!"
exit 0
