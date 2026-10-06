#!/bin/bash
set -e

# Generate a unique project name to avoid container collisions between agents
if [ -z "$E2E_PROJECT_NAME" ]; then
    E2E_PROJECT_NAME="lfr-tunnel-e2e-$$"
fi
export E2E_PROJECT_NAME

# Shared `docker-compose` wrapper -- selects v2 vs v1 by capability, not by existence (#1355).
# shellcheck source=lib/compose.sh
. "$(dirname -- "${BASH_SOURCE[0]}")/lib/compose.sh"

# Resolve dynamic host ports to avoid port binding collisions
if [ -z "$E2E_PROXY_PORT" ]; then
    E2E_PROXY_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')
fi
if [ -z "$E2E_MAILPIT_PORT" ]; then
    E2E_MAILPIT_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')
fi
export E2E_PROXY_PORT
export E2E_MAILPIT_PORT

# Change directory to script location
# Not a stray space (#1366): `CDPATH= cd` is a prefix assignment that neutralises
# CDPATH for this one command, so an entry in the user's CDPATH cannot silently
# redirect the cd. shellcheck reads it as an empty assignment.
# shellcheck disable=SC1007
CDPATH= cd -- "$(dirname -- "$0")"

# Signal file configuration
REPO_ROOT="$(cd ../.. && pwd)"
SIGNAL_FILE="${REPO_ROOT}/.progress-signal"

echo "BUILDING" > "$SIGNAL_FILE"

# Make sure we write FAILED or SUCCESS on exit
cleanup() {
    EXIT_CODE=$?
    echo "=== Cleaning up E2E resources ==="
    if [ -n "$CLIENT_CONTAINER_ID" ]; then
        echo "Stopping and removing client container: $CLIENT_CONTAINER_ID"
        docker stop "$CLIENT_CONTAINER_ID" >/dev/null 2>&1 || true
        docker rm "$CLIENT_CONTAINER_ID" >/dev/null 2>&1 || true
    fi
    docker-compose down -v --remove-orphans >/dev/null 2>&1 || true

    if [ $EXIT_CODE -eq 0 ]; then
        echo "SUCCESS" > "$SIGNAL_FILE"
    else
        echo "FAILED" > "$SIGNAL_FILE"
    fi
}
trap cleanup EXIT INT TERM ERR

echo "=== Starting E2E Docker Integration Test ==="

# Clean previous containers
docker-compose down -v --remove-orphans || true

# Start mock target, mailpit, server, proxy and client
echo "=== Spinning up Docker environment ==="
# Pre-pull the base images, retrying a transient registry error (#1530). Docker Hub 504d on
# node:20-alpine and failed a required check on an unrelated PR; the build itself has no retry
# around its FROM resolution. Never fatal -- if a pull really fails, the build below says so.
"$(dirname -- "${BASH_SOURCE[0]}")/../../scripts/common/pull-images-with-retry.sh" \
    "$(dirname -- "${BASH_SOURCE[0]}")/docker-compose.yml" || true

docker-compose up --build -d mock-target mailpit lfr-tunneld nginx-proxy lfr-tunnel

echo "WAITING_HEALTHY" > "$SIGNAL_FILE"

# Wait for Mailpit to be fully online
echo "=== Waiting for Mailpit to be ready ==="
for _ in {1..15}; do
    if curl -s http://localhost:$E2E_MAILPIT_PORT/api/v1/messages > /dev/null; then
        echo "Mailpit is ready!"
        break
    fi
    echo "Waiting for Mailpit..."
    sleep 1
done

# Wait for Nginx proxy to be fully online
echo "=== Waiting for Nginx proxy to be ready ==="
for _ in {1..30}; do
    if curl -s -f http://localhost:$E2E_PROXY_PORT/api/domains > /dev/null; then
        echo "Nginx proxy is ready!"
        break
    fi
    echo "Waiting for Nginx proxy..."
    sleep 1
done

# 1. Submit registration request
echo "=== Submitting registration request ==="
echo "TESTING" > "$SIGNAL_FILE"
REG_REQ_RESP=$(curl -s -X POST -H "Content-Type: application/json" \
     -d '{"email": "developer@lfr-demo.local", "requested_subdomain": "peter-dev"}' \
     "http://localhost:$E2E_PROXY_PORT/api/register-request")

echo "Registration request response: $REG_REQ_RESP"
sleep 2

# 2. Extract verification token from Mailpit
echo "=== Extracting verification token ==="
VERIFICATION_TOKEN=$(python3 -c '
import urllib.request, json, re, os
port = os.environ.get("E2E_MAILPIT_PORT", "8025")
try:
    data = json.loads(urllib.request.urlopen(f"http://localhost:{port}/api/v1/messages").read())
    if not data["messages"]:
        print("")
        exit(0)
    for m in data["messages"]:
        msg_id = m.get("ID")
        msg = json.loads(urllib.request.urlopen(f"http://localhost:{port}/api/v1/message/" + msg_id).read())
        body = (msg.get("HTML") or "") + "\n" + (msg.get("Text") or "")
        match = re.search(r"setup\?token=([a-f0-9A-Z]+)", body, re.IGNORECASE)
        if match:
            print(match.group(1))
            exit(0)
    print("")
except Exception as e:
    import sys
    print(f"Error: {e}", file=sys.stderr)
    print("")
')

if [ -z "$VERIFICATION_TOKEN" ]; then
    echo "❌ Failed to extract verification token from Mailpit!"
    echo "=== lfr-tunneld logs ==="
    docker-compose logs lfr-tunneld
    echo "=== Mailpit messages ==="
    curl -s http://localhost:$E2E_MAILPIT_PORT/api/v1/messages
    docker-compose down -v
    exit 1
fi
echo "Extracted Verification Token: $VERIFICATION_TOKEN"

# 2.5. Call Verification endpoint
echo "=== Verifying developer email ==="
VERIFY_RESP=$(curl -s "http://localhost:$E2E_PROXY_PORT/api/verify-email?token=${VERIFICATION_TOKEN}")
echo "Verify response: $VERIFY_RESP"
sleep 2


# 3. Extract admin approval token from Mailpit
echo "=== Extracting admin approval token ==="
APPROVAL_TOKEN=$(python3 -c '
import urllib.request, json, re, os
port = os.environ.get("E2E_MAILPIT_PORT", "8025")
try:
    data = json.loads(urllib.request.urlopen(f"http://localhost:{port}/api/v1/messages").read())
    if not data["messages"]:
        print("")
        exit(0)
    for m in data["messages"]:
        msg_id = m.get("ID")
        msg = json.loads(urllib.request.urlopen(f"http://localhost:{port}/api/v1/message/" + msg_id).read())
        body = (msg.get("HTML") or "") + "\n" + (msg.get("Text") or "")
        match = re.search(r"approve\?email=[^&]+&token=([a-f0-9]+)", body)
        if match:
            print(match.group(1))
            exit(0)
    print("")
except Exception as e:
    import sys
    print(f"Error: {e}", file=sys.stderr)
    print("")
')

if [ -z "$APPROVAL_TOKEN" ]; then
    echo "❌ Failed to extract approval token from Mailpit!"
    docker-compose down -v
    exit 1
fi
echo "Extracted Approval Token: $APPROVAL_TOKEN"

# 3.5. Call Admin Approval endpoint
echo "=== Approving developer request ==="
# POST, not GET: approving on GET meant any fetch of the emailed link approved the
# user, and that link is delivered to a chat channel where previews follow URLs (#1143).
APPROVE_RESP=$(curl -s -X POST "http://localhost:$E2E_PROXY_PORT/api/admin/approve" \
  --data-urlencode "email=developer@lfr-demo.local" \
  --data-urlencode "token=${APPROVAL_TOKEN}")
echo "Approval response: $APPROVE_RESP"
sleep 2

# 4. Extract claim token from Mailpit
echo "=== Extracting token claim token ==="
CLAIM_TOKEN=$(python3 -c '
import urllib.request, json, re, os
port = os.environ.get("E2E_MAILPIT_PORT", "8025")
try:
    data = json.loads(urllib.request.urlopen(f"http://localhost:{port}/api/v1/messages").read())
    for m in data["messages"]:
        msg_id = m.get("ID")
        msg = json.loads(urllib.request.urlopen(f"http://localhost:{port}/api/v1/message/" + msg_id).read())
        body = (msg.get("HTML") or "") + "\n" + (msg.get("Text") or "")
        match = re.search(r"claim\?token=([a-f0-9]+)", body)
        if match:
            print(match.group(1))
            exit(0)
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
    curl -s http://localhost:$E2E_MAILPIT_PORT/api/v1/messages
    docker-compose down -v
    exit 1
fi
echo "Extracted Claim Token: $CLAIM_TOKEN"

# 5. Claim Personal Access Token (PAT)
echo "=== Claiming Personal Access Token (PAT) ==="
CLAIM_RESP=$(curl -s "http://localhost:$E2E_PROXY_PORT/api/claim?token=${CLAIM_TOKEN}")
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

# 5.5. Reserve the subdomain 'peter-dev' for the developer via the Portal API
echo "=== Requesting magic link for developer ==="
curl -s -X POST -H "Content-Type: application/json" \
     -d '{"email": "developer@lfr-demo.local"}' \
     "http://localhost:$E2E_PROXY_PORT/api/auth/magic-link"
sleep 2

echo "=== Extracting developer magic link token ==="
DEV_ML_TOKEN=$(python3 -c '
import urllib.request, json, re, os
port = os.environ.get("E2E_MAILPIT_PORT", "8025")
try:
    data = json.loads(urllib.request.urlopen(f"http://localhost:{port}/api/v1/messages").read())
    for m in (data.get("messages") or []):
        msg = json.loads(urllib.request.urlopen(f"http://localhost:{port}/api/v1/message/" + m["ID"]).read())
        body = msg.get("Text","") or msg.get("HTML","")
        match = re.search(r"token=([a-f0-9]+)", body, re.IGNORECASE)
        if match:
            print(match.group(1))
            exit(0)
except Exception as e:
    import sys; print(f"Error: {e}", file=sys.stderr)
' 2>/dev/null || true)

if [ -z "$DEV_ML_TOKEN" ]; then
    echo "❌ Failed to extract developer magic link token!"
    docker-compose down -v
    exit 1
fi
echo "Developer Magic Link Token: $DEV_ML_TOKEN"

echo "=== Logging in to Portal to establish session ==="
curl -s -c /tmp/dev-session.txt -X POST -H "Content-Type: application/json" \
     -d "{\"token\": \"$DEV_ML_TOKEN\"}" \
     "http://localhost:$E2E_PROXY_PORT/api/auth/verify"

echo "=== Reserving subdomain peter-dev ==="
RESERVE_RESP=$(curl -s -b /tmp/dev-session.txt -X POST -H "Content-Type: application/json" \
     -d '{"subdomain": "peter-dev", "domain": "lfr-demo.local"}' \
     "http://localhost:$E2E_PROXY_PORT/api/portal/reservations")
echo "Reservation response: $RESERVE_RESP"

# 6. Start the Client Tunnel inside the container with the PAT using docker-compose run
echo "=== Starting client tunnel container ==="
CLIENT_CONTAINER_ID=$(docker-compose run -d --no-deps \
  --entrypoint "./lfr-tunnel" \
  -e LFT_CLIENT_TOKEN="$DEVELOPER_PAT" \
  lfr-tunnel \
  -server http://tunnel.lfr-demo.local \
  -subdomain peter-dev \
  -ports 80)

echo "Client container ID: $CLIENT_CONTAINER_ID"

# Wait for client to connect and establish the tunnel
echo "=== Waiting for tunnel connection ==="
TUNNEL_READY=false
for _ in {1..20}; do
    RESPONSE_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: peter-dev.lfr-demo.local" http://localhost:$E2E_PROXY_PORT/ || true)
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
RESPONSE=$(curl -s -H "Host: peter-dev.lfr-demo.local" http://localhost:$E2E_PROXY_PORT/)

echo "=== Response received ==="
echo "$RESPONSE"

# Verify content
if ! echo "$RESPONSE" | grep -q "Mock Liferay Instance"; then
    echo "❌ E2E Integration Test FAILED! Staged response did not match expected output."
    docker-compose down -v
    exit 1
fi
echo "✅ Registration, approval and tunnel routing work."

# 8. Start a real tunnel through the MCP server (#2341).
#
# start_tunnel could never report success for ~36 releases (#2336): it compared the PID of the
# `-background` intermediate with the PID the tunnel's grandchild wrote to its state file, and the
# two never match. #2339 fixed it with unit tests, but nothing ran the client, so nothing could
# show the tool works -- the same hole the original bug lived in. This runs the real binary inside
# the client container (never on the host: .agents/skills/edr-constraints) and checks four things,
# in this order on purpose:
#
#   1. the MCP server says `success` and returns public URLs and a PID   (what it REPORTED)
#   2. that PID is a live process in the container                        (the PID is not stale)
#   3. the public URL it returned serves the mock target through nginx   (what it reported is TRUE)
#   4. stop_tunnel stops it                                               (cleanup, and that tool)
#
# Step 3 is the one that earns the test. A server that reported a plausible URL for a tunnel that
# never came up would pass 1 and 2.
echo "=== Starting a tunnel through the MCP server ==="
MCP_SUBDOMAIN="mcp-dev"

# One JSON-RPC exchange with `lfr-tunnel mcp` in the long-running client container. The server
# reads newline-delimited requests from stdin and exits at EOF, after answering; the tunnel it
# starts is a detached background process and outlives it.
mcp_call() {
    printf '%s\n' \
        '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
        "$1" |
        docker-compose exec -T \
            -e LFT_CLIENT_TOKEN="$DEVELOPER_PAT" \
            -e LFT_CLIENT_SERVER=http://tunnel.lfr-demo.local \
            lfr-tunnel ./lfr-tunnel mcp
}

# A process's state letter from /proc inside the client container, or "gone".
#
# Not `kill -0`, which cannot tell a live process from a zombie. The client container's PID 1 is
# `sleep infinity`, which never reaps: the tunnel is reparented to it when `-background`'s
# intermediate exits, so once it stops it stays a zombie for the life of the container. On a real
# host init/launchd reaps it. Measured on the first run of this test: `kill -0` reported the
# stopped tunnel as still running -- and would equally have reported a tunnel that died on start
# as alive.
mcp_pid_state() {
    docker-compose exec -T lfr-tunnel sh -c \
        '[ -r "/proc/$1/stat" ] && awk "{print \$3}" "/proc/$1/stat" || echo gone' _ "$1" | tr -d '\r'
}

# Pulls one field out of the tool result for the request with id 2. The tool's payload is JSON
# inside result.content[0].text; a tool error is reported as isError with "ERROR: ..." text, and
# is turned into a failure here rather than read as an empty field.
mcp_field() {
    python3 -c '
import json, sys
field = sys.argv[1]
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    msg = json.loads(line)
    if msg.get("id") != 2:
        continue
    result = msg.get("result") or {}
    text = result["content"][0]["text"]
    if result.get("isError"):
        sys.exit("tool error: " + text)
    value = json.loads(text).get(field)
    print(json.dumps(value) if isinstance(value, (list, dict)) else ("" if value is None else value))
    sys.exit(0)
sys.exit("no response to request 2")
' "$1"
}

START_OUT=$(mcp_call '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"start_tunnel","arguments":{"subdomain":"'"$MCP_SUBDOMAIN"'","ports":"80","target_host":"mock-target"}}}')
echo "MCP start_tunnel response: $START_OUT"

MCP_STATUS=$(echo "$START_OUT" | mcp_field status) || { echo "❌ start_tunnel failed: $MCP_STATUS"; exit 1; }
MCP_PID=$(echo "$START_OUT" | mcp_field pid)
MCP_URLS=$(echo "$START_OUT" | mcp_field public_urls)

# Positive assertions first (e2e-testing §3): "no error" is satisfied by a server that never
# started anything.
if [ "$MCP_STATUS" != "success" ]; then
    echo "❌ start_tunnel reported status '$MCP_STATUS', not 'success' -- the #2336 failure mode."
    docker-compose exec -T lfr-tunnel sh -c 'cat ~/.lfr-tunnel/logs/*.log 2>/dev/null | tail -40' || true
    exit 1
fi
MCP_HOST=$(echo "$MCP_URLS" | python3 -c '
import json, sys
from urllib.parse import urlparse
urls = json.load(sys.stdin) or []
print(urlparse(urls[0]).hostname if urls else "")
')
if [ -z "$MCP_HOST" ]; then
    echo "❌ start_tunnel reported success with no public URL: $MCP_URLS"
    exit 1
fi
case "$MCP_PID" in
    ''|*[!0-9]*) echo "❌ start_tunnel reported a PID that is not a number: '$MCP_PID'"; exit 1 ;;
esac
MCP_PID_STATE=$(mcp_pid_state "$MCP_PID")
if [ "$MCP_PID_STATE" = "gone" ] || [ "$MCP_PID_STATE" = "Z" ]; then
    echo "❌ start_tunnel reported PID $MCP_PID, which is not a live process in the client container (state $MCP_PID_STATE)."
    exit 1
fi
echo "MCP reported success: PID $MCP_PID, public host $MCP_HOST"

# The reported URL must actually serve the target. Its HOST is used, not a hardcoded
# "$MCP_SUBDOMAIN.lfr-demo.local", so this checks what the tool said rather than what the test
# expected it to say.
MCP_ROUTED=false
for _ in {1..20}; do
    MCP_BODY=$(curl -s -H "Host: $MCP_HOST" "http://localhost:$E2E_PROXY_PORT/" || true)
    if echo "$MCP_BODY" | grep -q "Mock Liferay Instance"; then
        MCP_ROUTED=true
        break
    fi
    sleep 1
done
if [ "$MCP_ROUTED" = false ]; then
    echo "❌ The URL start_tunnel reported ($MCP_HOST) does not serve the mock target."
    echo "Last response: $MCP_BODY"
    exit 1
fi
echo "✅ The tunnel start_tunnel reported is real: $MCP_HOST serves the mock target."

# Cleanup, and the other tool this exercises: stop_tunnel must report success AND the process
# must actually be gone.
STOP_OUT=$(mcp_call '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"stop_tunnel","arguments":{"subdomain":"'"$MCP_SUBDOMAIN"'"}}}')
echo "MCP stop_tunnel response: $STOP_OUT"
STOP_STATUS=$(echo "$STOP_OUT" | mcp_field status) || { echo "❌ stop_tunnel failed: $STOP_STATUS"; exit 1; }
if [ "$STOP_STATUS" != "success" ]; then
    echo "❌ stop_tunnel reported status '$STOP_STATUS'."
    exit 1
fi
MCP_STOPPED=false
for _ in {1..10}; do
    MCP_PID_STATE=$(mcp_pid_state "$MCP_PID")
    if [ "$MCP_PID_STATE" = "gone" ] || [ "$MCP_PID_STATE" = "Z" ]; then
        MCP_STOPPED=true
        break
    fi
    sleep 1
done
if [ "$MCP_STOPPED" = false ]; then
    echo "❌ stop_tunnel reported success but PID $MCP_PID is still running (state $MCP_PID_STATE)."
    exit 1
fi
echo "PID $MCP_PID after stop_tunnel: $MCP_PID_STATE"
echo "✅ stop_tunnel stopped it."

echo "✅ E2E Integration Test PASSED! Registration, approval, tunnel routing and the MCP server's start_tunnel work."
docker-compose down -v
exit 0
