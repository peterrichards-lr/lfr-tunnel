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

docker-compose up --build -d mock-target mock-liferay mailpit lfr-tunneld nginx-proxy lfr-tunnel

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

# 7b. preserve_host follows Liferay's own header, through a real tunnel (#2571).
#
# The unit tests prove the probe and the watch against httptest stand-ins; nothing showed the HOST a
# request actually reaches the target with once the client has decided. mock-liferay answers with
# the Host it received: on :80 it sends Liferay-Portal, on :81 it does not (the control). A client
# that never looked at the header fails the first half; one that turned preserve_host on for every
# named port fails the second. Both are named with -ports, which before #2571 never got
# preserve_host at all.
liferay_fail() {
    echo "❌ $1"
    echo "=== client log ($2) ==="
    docker logs "$3" 2>&1 | tail -40 || true
    echo "=== lfr-tunneld logs (tail) ==="
    docker-compose logs --tail=40 lfr-tunneld || true
    exit 1
}

# Starts a foreground tunnel to mock-liferay:<port> under <subdomain>; prints the container ID.
start_liferay_tunnel() {
    docker-compose run -d --no-deps \
        --entrypoint "./lfr-tunnel" \
        -e LFT_CLIENT_TOKEN="$DEVELOPER_PAT" \
        -e LFT_TARGET_HOST=mock-liferay \
        lfr-tunnel \
        -server http://tunnel.lfr-demo.local \
        -subdomain "$1" \
        -ports "$2"
}

# The Host mock-liferay saw for a request to <subdomain> through the proxy ("" if no answer).
host_seen() {
    curl -s -H "Host: $1.lfr-demo.local" "http://localhost:$E2E_PROXY_PORT/" | sed -n 's/^host=//p' | tr -d '\r'
}

# ONE subdomain for every tunnel below, each stopped before the next: every distinct subdomain a
# user registers becomes a reservation, the default quota is 3, and peter-dev and the MCP step's
# mcp-dev hold two. A fourth name is refused with 403 "quota limit reached" (measured).
LR_SUB="lr-test"

# Stops a foreground tunnel container gracefully (SIGTERM: the client deregisters, freeing the
# lease for the next step), then removes it.
stop_tunnel_container() {
    docker stop -t 15 "$1" >/dev/null 2>&1 || true
    docker rm "$1" >/dev/null 2>&1 || true
}

echo "=== preserve_host: a port that answers with Liferay-Portal ==="
LR_ID=$(start_liferay_tunnel "$LR_SUB" 80)
LR_SEEN=""
for _ in {1..30}; do
    LR_SEEN=$(host_seen "$LR_SUB")
    [ "$LR_SEEN" = "$LR_SUB.lfr-demo.local" ] && break
    sleep 1
done
if [ "$LR_SEEN" != "$LR_SUB.lfr-demo.local" ]; then
    liferay_fail "Liferay (Liferay-Portal on :80) received Host '$LR_SEEN', not the public $LR_SUB.lfr-demo.local: preserve_host was not turned on." "$LR_SUB" "$LR_ID"
fi
if ! docker logs "$LR_ID" 2>&1 | grep -q "Liferay identified on port 80"; then
    liferay_fail "The public Host arrived, but the client never logged identifying Liferay on port 80 -- something other than the header turned preserve_host on." "$LR_SUB" "$LR_ID"
fi
echo "✅ Liferay-Portal on :80 -> the target received the public Host ($LR_SEEN)."
stop_tunnel_container "$LR_ID"

echo "=== preserve_host: the same server WITHOUT the header (control) ==="
NOLR_ID=$(start_liferay_tunnel "$LR_SUB" 81)
NOLR_SEEN=""
for _ in {1..30}; do
    NOLR_SEEN=$(host_seen "$LR_SUB")
    [ -n "$NOLR_SEEN" ] && break
    sleep 1
done
if [ -z "$NOLR_SEEN" ]; then
    liferay_fail "The control tunnel ($LR_SUB -> :81) never answered." "$LR_SUB" "$NOLR_ID"
fi
# Past two watch intervals (5s each), so a late decision would have been made by now.
sleep 12
NOLR_SEEN=$(host_seen "$LR_SUB")
if [ "$NOLR_SEEN" = "$LR_SUB.lfr-demo.local" ]; then
    liferay_fail "A server WITHOUT Liferay-Portal received the public Host: preserve_host was turned on for a port that is not Liferay." "$LR_SUB" "$NOLR_ID"
fi
echo "✅ No header on :81 -> the target received the local Host ($NOLR_SEEN), not the public one."
stop_tunnel_container "$NOLR_ID"

# 7c. -background reports the gateway's answer (#2573).
#
# Before #2573 -background exited 0 the moment the tunnel process started, so a refused token
# reached only the log and a wrapper such as LDM saw a timeout. Both outcomes, against the real
# gateway, in the long-running client container. `timeout` bounds each call: the wait is 20s.
echo "=== -background with a token the gateway refuses ==="
# `|| BG_CODE=$?`, not set +e: this script traps ERR into cleanup(), which runs `docker-compose
# down` -- a command expected to fail must be the left side of a ||, where ERR does not fire.
BG_CODE=0
BG_OUT=$(docker-compose exec -T -e LFT_CLIENT_TOKEN="lft_not_a_real_token" lfr-tunnel \
    timeout 60 ./lfr-tunnel -background -server http://tunnel.lfr-demo.local -subdomain bg-refused -ports 80 2>&1) || BG_CODE=$?
echo "$BG_OUT"
if [ "$BG_CODE" != "1" ]; then
    echo "❌ -background with a refused token exited $BG_CODE, want 1."
    exit 1
fi
case "$BG_OUT" in
    *"did not start"*"did not accept this access token"*) ;;
    *) echo "❌ -background exited 1 but did not say why (want the gateway's token refusal)."; exit 1 ;;
esac
echo "✅ A refused token: -background exits 1 and prints the gateway's reason."

echo "=== -background with a valid token ==="
BG_CODE=0
BG_OUT=$(docker-compose exec -T -e LFT_CLIENT_TOKEN="$DEVELOPER_PAT" lfr-tunnel \
    timeout 60 ./lfr-tunnel -background -server http://tunnel.lfr-demo.local -subdomain "$LR_SUB" -ports 80 2>&1) || BG_CODE=$?
echo "$BG_OUT"
if [ "$BG_CODE" != "0" ]; then
    echo "❌ -background with a valid token exited $BG_CODE, want 0."
    exit 1
fi
case "$BG_OUT" in
    *"Registered: "*"$LR_SUB"*) ;;
    *) echo "❌ -background exited 0 without printing the registered URL for $LR_SUB."; exit 1 ;;
esac
BG_ROUTED=$(curl -s -H "Host: $LR_SUB.lfr-demo.local" "http://localhost:$E2E_PROXY_PORT/")
if ! echo "$BG_ROUTED" | grep -q "Mock Liferay Instance"; then
    echo "❌ -background reported $LR_SUB registered, but it does not serve the mock target: $BG_ROUTED"
    exit 1
fi
docker-compose exec -T lfr-tunnel ./lfr-tunnel -stop -subdomain "$LR_SUB" >/dev/null 2>&1 || true
echo "✅ A valid token: -background exits 0 once registered, prints the URL, and the URL serves."

# 8. Start a real tunnel through the MCP server (#2341).
#
# start_tunnel could never report success for ~36 releases (#2336): it compared the PID of the
# `-background` intermediate with the PID the tunnel's grandchild wrote to its state file, and the
# two never match. #2339 fixed it with unit tests, but nothing ran the client, so nothing could
# show the tool works -- the same hole the original bug lived in. This runs the real binary inside
# the client container (never on the host: .agents/skills/edr-constraints) and checks, in this
# order on purpose:
#
#   1. the MCP server says `success` and returns public URLs and a PID   (what it REPORTED)
#   2. the URL names the subdomain asked for, and that PID is a live
#      process running with the arguments the call passed              (it is OUR tunnel)
#   3. the public URL it returned serves the mock target through nginx   (what it reported is TRUE)
#   4. stop_tunnel stops THAT process                                    (cleanup, and that tool)
#
# Step 3 is the one that earns the test. A server that reported a plausible URL for a tunnel that
# never came up would pass 1 and 2.
echo "=== Starting a tunnel through the MCP server ==="
MCP_SUBDOMAIN="mcp-dev"

# Every failure in this step dumps what is needed to diagnose it from a CI log alone.
mcp_fail() {
    echo "❌ $1"
    echo "=== MCP tunnel client log ==="
    docker-compose exec -T lfr-tunnel sh -c 'tail -60 ~/.lfr-tunnel/logs/*.log 2>/dev/null' || true
    echo "=== lfr-tunneld logs (tail) ==="
    docker-compose logs --tail=60 lfr-tunneld || true
    exit 1
}

# One JSON-RPC exchange with `lfr-tunnel mcp` in the long-running client container. The server
# reads newline-delimited requests from stdin and exits at EOF, after answering; the tunnel it
# starts is a detached background process and outlives it.
#
# Bounded, because nothing else is: the server blocks on the `-background` intermediate, and the
# e2e job sets no timeout-minutes, so a regression that leaked the pipe to the tunnel would hang
# the job for GitHub's six-hour default instead of failing it. `timeout` is busybox's, inside the
# container, so it behaves the same on a macOS host.
mcp_call() {
    printf '%s\n' \
        '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
        "$1" |
        docker-compose exec -T \
            -e LFT_CLIENT_TOKEN="$DEVELOPER_PAT" \
            -e LFT_CLIENT_SERVER=http://tunnel.lfr-demo.local \
            lfr-tunnel timeout 60 ./lfr-tunnel mcp
}

# A process's state letter from /proc inside the client container, or "gone".
#
# Not `kill -0`, which cannot tell a live process from a zombie. The client container's PID 1 is
# `sleep infinity`, which never reaps: the tunnel is reparented to it when `-background`'s
# intermediate exits, so once it stops it stays a zombie for the life of the container. On a real
# host init/launchd reaps it. Measured on the first run of this test: `kill -0` reported the
# stopped tunnel as still running -- and would equally have reported a tunnel that died on start
# as alive. The state is read after the LAST ')' so a command name containing spaces cannot shift
# the field.
mcp_pid_state() {
    docker-compose exec -T lfr-tunnel sh -c \
        '[ -r "/proc/$1/stat" ] && sed "s/.*) //" "/proc/$1/stat" | cut -d" " -f1 || echo gone' _ "$1" | tr -d '\r'
}

# Live means running, sleeping or in uninterruptible wait. Asserted as membership rather than as
# "not gone and not a zombie", so an empty answer -- the exec itself failing -- is not read as
# alive (e2e-testing §3).
mcp_pid_live() {
    case "$1" in
        R | S | D) return 0 ;;
        *) return 1 ;;
    esac
}

# Pulls one field out of the tool result for the request with id 2. The tool's payload is JSON
# inside result.content[0].text. A tool error (isError), a JSON-RPC error, or no answer at all is
# printed on STDOUT and exits non-zero, so the caller's failure message carries it.
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
    if "error" in msg:
        print("JSON-RPC error: " + json.dumps(msg["error"]))
        sys.exit(1)
    result = msg.get("result") or {}
    text = result["content"][0]["text"]
    if result.get("isError"):
        print("tool error: " + text)
        sys.exit(1)
    value = json.loads(text).get(field)
    print(json.dumps(value) if isinstance(value, (list, dict)) else ("" if value is None else value))
    sys.exit(0)
print("no response to request 2")
sys.exit(1)
' "$1"
}

MCP_T0=$(python3 -c 'import time; print(time.time())')
START_OUT=$(mcp_call '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"start_tunnel","arguments":{"subdomain":"'"$MCP_SUBDOMAIN"'","ports":"80","target_host":"mock-target"}}}') ||
    mcp_fail "the MCP server did not answer start_tunnel within 60s, or exited non-zero."
MCP_ELAPSED=$(python3 -c "import time; print(f'{time.time() - $MCP_T0:.2f}')")
echo "MCP start_tunnel response (${MCP_ELAPSED}s, against start_tunnel's 2s registration window): $START_OUT"

MCP_STATUS=$(echo "$START_OUT" | mcp_field status) || mcp_fail "start_tunnel failed: $MCP_STATUS"
MCP_PID=$(echo "$START_OUT" | mcp_field pid) || mcp_fail "start_tunnel gave no pid: $MCP_PID"
MCP_URLS=$(echo "$START_OUT" | mcp_field public_urls) || mcp_fail "start_tunnel gave no public_urls: $MCP_URLS"

# Positive assertions first (e2e-testing §3): "no error" is satisfied by a server that never
# started anything.
if [ "$MCP_STATUS" != "success" ]; then
    mcp_fail "start_tunnel reported status '$MCP_STATUS', not 'success' -- the #2336 failure mode."
fi
MCP_HOST=$(echo "$MCP_URLS" | python3 -c '
import json, sys
from urllib.parse import urlparse
raw = sys.stdin.read().strip()
urls = json.loads(raw) if raw else []
print(urlparse(urls[0]).hostname if urls else "")
')
if [ -z "$MCP_HOST" ]; then
    mcp_fail "start_tunnel reported success with no public URL: '$MCP_URLS'"
fi
# The tunnel must be the one asked for. Without -subdomain the client falls back to the container
# hostname and would still register and route, so step 3 alone would not notice it being dropped.
if [ "${MCP_HOST%%.*}" != "$MCP_SUBDOMAIN" ]; then
    mcp_fail "start_tunnel reported $MCP_HOST, not a host for the requested subdomain '$MCP_SUBDOMAIN'."
fi
case "$MCP_PID" in
    '' | *[!0-9]*) mcp_fail "start_tunnel reported a PID that is not a number: '$MCP_PID'" ;;
esac
MCP_PID_STATE=$(mcp_pid_state "$MCP_PID")
if ! mcp_pid_live "$MCP_PID_STATE"; then
    mcp_fail "start_tunnel reported PID $MCP_PID, which is not a live process in the client container (state '$MCP_PID_STATE')."
fi
# And it was started with what the call passed. target_host in particular is invisible otherwise:
# this container already sets LFT_TARGET_HOST=mock-target, so routing would work without it.
MCP_CMDLINE=$(docker-compose exec -T lfr-tunnel sh -c 'tr "\0" " " < "/proc/$1/cmdline"' _ "$MCP_PID" | tr -d '\r')
for want in "-subdomain $MCP_SUBDOMAIN" "-ports 80" "-target-host mock-target"; do
    case "$MCP_CMDLINE" in
        *"$want"*) ;;
        *) mcp_fail "PID $MCP_PID was not started with '$want': $MCP_CMDLINE" ;;
    esac
done
echo "MCP reported success: PID $MCP_PID ($MCP_PID_STATE), public host $MCP_HOST"

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
    mcp_fail "The URL start_tunnel reported ($MCP_HOST) does not serve the mock target. Last response: $MCP_BODY"
fi
echo "✅ The tunnel start_tunnel reported is real: $MCP_HOST serves the mock target."

# Cleanup, and the other tool this exercises. stop_tunnel reports success whenever the client's
# -stop exits 0 -- which it also does when it finds nothing to stop -- so success alone proves
# nothing. The tunnel must still be live going in, the stop must name THIS PID, and the process
# must be gone (or a zombie, see mcp_pid_state) afterwards.
MCP_PID_STATE=$(mcp_pid_state "$MCP_PID")
if ! mcp_pid_live "$MCP_PID_STATE"; then
    mcp_fail "PID $MCP_PID was no longer live before stop_tunnel was called (state '$MCP_PID_STATE')."
fi
STOP_OUT=$(mcp_call '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"stop_tunnel","arguments":{"subdomain":"'"$MCP_SUBDOMAIN"'"}}}') ||
    mcp_fail "the MCP server did not answer stop_tunnel within 60s, or exited non-zero."
echo "MCP stop_tunnel response: $STOP_OUT"
STOP_STATUS=$(echo "$STOP_OUT" | mcp_field status) || mcp_fail "stop_tunnel failed: $STOP_STATUS"
STOP_MESSAGE=$(echo "$STOP_OUT" | mcp_field message) || mcp_fail "stop_tunnel gave no message: $STOP_MESSAGE"
if [ "$STOP_STATUS" != "success" ]; then
    mcp_fail "stop_tunnel reported status '$STOP_STATUS'."
fi
case "$STOP_MESSAGE" in
    *"Stopping background tunnel for subdomain '$MCP_SUBDOMAIN' (PID: $MCP_PID)"*) ;;
    *) mcp_fail "stop_tunnel did not stop PID $MCP_PID: $STOP_MESSAGE" ;;
esac
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
    mcp_fail "stop_tunnel reported success but PID $MCP_PID is still running (state '$MCP_PID_STATE')."
fi
echo "PID $MCP_PID after stop_tunnel: $MCP_PID_STATE"
echo "✅ stop_tunnel stopped it."

echo "✅ E2E Integration Test PASSED! Registration, approval, tunnel routing and the MCP server's start_tunnel work."
docker-compose down -v
exit 0
