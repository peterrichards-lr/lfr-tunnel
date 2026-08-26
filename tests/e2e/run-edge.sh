#!/bin/bash
set -e

# Shared `docker-compose` wrapper -- selects v2 vs v1 by capability, not by existence (#1355).
# shellcheck source=lib/compose.sh
. "$(dirname -- "${BASH_SOURCE[0]}")/lib/compose.sh"

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
    if [ -n "$DRAIN_CLIENT_ID" ]; then
        echo "Stopping and removing drain-test client container: $DRAIN_CLIENT_ID"
        docker stop "$DRAIN_CLIENT_ID" >/dev/null 2>&1 || true
        docker rm "$DRAIN_CLIENT_ID" >/dev/null 2>&1 || true
    fi
    if [ -n "$FAIL_CLIENT_ID" ]; then
        echo "Stopping and removing failover-test client container: $FAIL_CLIENT_ID"
        docker stop "$FAIL_CLIENT_ID" >/dev/null 2>&1 || true
        docker rm "$FAIL_CLIENT_ID" >/dev/null 2>&1 || true
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

# One reservation each, on the shared domain. Both gateways issue tunnels there now (#1285),
# so a reservation follows the client between them instead of being spent twice -- which is
# what previously made a move fail with a quota 403 rather than a routing error.
echo "=== Reserving subdomain peter-dev ==="
curl -s -b /tmp/dev-session.txt -X POST -H "Content-Type: application/json" \
     -d '{"subdomain": "peter-dev", "domain": "lfr-demo.local"}' \
     "http://localhost:8000/api/portal/reservations"

echo "=== Reserving subdomain peter-auto ==="
curl -s -b /tmp/dev-session.txt -X POST -H "Content-Type: application/json" \
     -d '{"subdomain": "peter-auto", "domain": "lfr-demo.local"}' \
     "http://localhost:8000/api/portal/reservations"

# A third reservation for the drain test, which needs a client it can guarantee is on the
# control plane. Reusing the auto-probing client did not work: which gateway that lands on is
# decided by a latency probe, and CI put it on the edge, so the drain was announced on a
# gateway it was not attached to and the test failed on its own premise.
echo "=== Reserving subdomain peter-drain ==="
curl -s -b /tmp/dev-session.txt -X POST -H "Content-Type: application/json" \
     -d '{"subdomain": "peter-drain", "domain": "lfr-demo.local"}' \
     "http://localhost:8000/api/portal/reservations"
sleep 2

echo "=== Reserving subdomain peter-fail ==="
# Checked, unlike the three above. A refused reservation is silent here and only surfaces much
# later as "Failed to register: quota limit reached" against a client that looks broken -- which
# is exactly how the quota ceiling was found when this phase was added.
FAIL_RES=$(curl -s -b /tmp/dev-session.txt -X POST -H "Content-Type: application/json" \
     -d '{"subdomain": "peter-fail", "domain": "lfr-demo.local"}' \
     "http://localhost:8000/api/portal/reservations")
if ! echo "$FAIL_RES" | grep -q '"subdomain":"peter-fail"'; then
    echo "❌ Could not reserve peter-fail, so the failover test has no tunnel to work with."
    echo "    Response: $FAIL_RES"
    exit 1
fi
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

# Wait for tunnel connection.
#
# Addressed at the edge directly (port 8090) rather than through nginx, because the host is
# now apex-level -- peter-dev.lfr-demo.local, with no "us" in it (#1285) -- and this nginx
# sends every *.lfr-demo.local to central. Getting apex traffic to the node actually holding
# the tunnel is #1247; until that exists, talking to the node directly stands in for it.
echo "=== Waiting for explicit regional tunnel ==="
TUNNEL_READY=false
for i in {1..20}; do
    RESPONSE_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: peter-dev.lfr-demo.local" http://localhost:8090/ || true)
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
RESPONSE=$(curl -s -H "Host: peter-dev.lfr-demo.local" http://localhost:8090/)
if ! echo "$RESPONSE" | grep -q "Mock Liferay Instance"; then
    echo "❌ Edge routing content mismatch!"
    exit 1
fi

# The URL the client was given must not name the node serving it. A regional host here is the
# defect #1285 fixes: it changes the moment the client moves, breaking every existing link.
if docker logs "$CLIENT_CONTAINER_ID" 2>&1 | grep -q "peter-dev.us.lfr-demo.local"; then
    echo "❌ The edge issued a region-scoped URL; a tunnel's public URL must not name its gateway."
    docker logs "$CLIENT_CONTAINER_ID" 2>&1 | grep "peter-dev"
    exit 1
fi
if ! docker logs "$CLIENT_CONTAINER_ID" 2>&1 | grep -q "peter-dev.lfr-demo.local"; then
    echo "❌ The edge did not issue the shared-domain host the client should have been given."
    docker logs "$CLIENT_CONTAINER_ID"
    exit 1
fi
echo "✅ Explicit regional edge tunnel routing verified successfully, on a region-agnostic host!"

# The record has to point at the node actually holding the tunnel. Without it an apex-issued
# host resolves to the control plane via the wildcard, and the control plane holds no lease for
# it -- the tunnel is up and every visitor gets an offline page (#1247).
echo "=== Verifying DNS was published for the edge that holds the tunnel ==="
DNS_PUBLISHED=false
for i in {1..30}; do
    DNS_LOG=$(docker-compose -f docker-compose-edge.yml exec -T lfr-tunneld-control cat /tmp/dns-hook.log 2>/dev/null || true)
    if echo "$DNS_LOG" | grep -q "^upsert peter-dev.lfr-demo.local us.lfr-demo.local$"; then
        DNS_PUBLISHED=true
        break
    fi
    sleep 1
done

if [ "$DNS_PUBLISHED" = false ]; then
    echo "❌ The control plane never pointed peter-dev.lfr-demo.local at the edge serving it."
    echo "    Expected: upsert peter-dev.lfr-demo.local us.lfr-demo.local"
    echo "=== DNS hook calls so far ==="
    docker-compose -f docker-compose-edge.yml exec -T lfr-tunneld-control cat /tmp/dns-hook.log 2>&1 || echo "(no hook calls at all)"
    exit 1
fi
echo "✅ DNS published: peter-dev.lfr-demo.local -> us.lfr-demo.local"

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

# Wait for auto-probed tunnel connection.
#
# One host either way now: whichever region the probe picks, the tunnel is issued on the shared
# domain (#1285). Central is reachable through nginx, the edge through its own port, since
# apex traffic does not yet follow the tunnel to the node holding it (#1247).
echo "=== Waiting for auto-probed tunnel ==="
AUTO_TUNNEL_READY=false
for i in {1..20}; do
    CODE_EU=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: peter-auto.lfr-demo.local" http://localhost:8000/ || true)
    CODE_US=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: peter-auto.lfr-demo.local" http://localhost:8090/ || true)
    if [ "$CODE_EU" = "200" ] || [ "$CODE_US" = "200" ]; then
        echo "Auto-probed tunnel connected successfully (central: $CODE_EU, edge: $CODE_US)!"
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

# And it has to come back up on the SAME host it started on. That is the point of the move:
# a visitor holding peter-dev.lfr-demo.local keeps working, and never learns which node is
# serving it (#1285). The client has moved to the control plane, so nginx's *.lfr-demo.local
# vhost now reaches it.
echo "=== Verifying the tunnel survived the move on the same host ==="
SURVIVED=false
for i in {1..60}; do
    CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: peter-dev.lfr-demo.local" http://localhost:8000/ || true)
    if [ "$CODE" = "200" ]; then
        SURVIVED=true
        break
    fi
    sleep 2
done

if [ "$SURVIVED" = false ]; then
    echo "❌ The client moved but peter-dev.lfr-demo.local never served again."
    echo "    A planned move must not change the URL a visitor is holding."
    echo "=== Client logs ==="
    docker logs "$CLIENT_CONTAINER_ID"
    exit 1
fi

RESPONSE=$(curl -s -H "Host: peter-dev.lfr-demo.local" http://localhost:8000/)
if ! echo "$RESPONSE" | grep -q "Mock Liferay Instance"; then
    echo "❌ The tunnel answered after the move but served the wrong content."
    exit 1
fi

if docker logs "$CLIENT_CONTAINER_ID" 2>&1 | grep -q "peter-dev.us.lfr-demo.local"; then
    echo "❌ The client advertised a region-scoped URL at some point during the move."
    docker logs "$CLIENT_CONTAINER_ID" 2>&1 | grep "peter-dev"
    exit 1
fi

# And the record has to have moved with it. The name is unchanged, so the only thing that can
# get a visitor to the new gateway is the record being repointed (#1247).
echo "=== Verifying DNS followed the tunnel to the control plane ==="
DNS_MOVED=false
for i in {1..30}; do
    DNS_LOG=$(docker-compose -f docker-compose-edge.yml exec -T lfr-tunneld-control cat /tmp/dns-hook.log 2>/dev/null || true)
    if echo "$DNS_LOG" | grep -q "^upsert peter-dev.lfr-demo.local tunnel.lfr-demo.local$"; then
        DNS_MOVED=true
        break
    fi
    sleep 1
done

if [ "$DNS_MOVED" = false ]; then
    echo "❌ The tunnel moved to the control plane but its DNS record still points at the edge."
    echo "    Expected: upsert peter-dev.lfr-demo.local tunnel.lfr-demo.local"
    echo "=== DNS hook calls ==="
    docker-compose -f docker-compose-edge.yml exec -T lfr-tunneld-control cat /tmp/dns-hook.log 2>&1 || true
    exit 1
fi

# Now let the scheduled stop actually happen. Up to here the edge has only *announced* that it
# is stopping; the container stayed up, so the client kept failing back to it and being warned
# off again, and the record oscillated. That is a property of the harness, not of the product:
# a real edge stops. Stopping it here settles the end state so the assertion below means
# something.
echo "=== Stopping the edge, as its schedule said it would ==="
docker-compose -f docker-compose-edge.yml stop lfr-tunneld-edge > /dev/null 2>&1 || true

# Past the withdrawal grace (5s in this harness's config), the record has to be left pointing
# at the gateway that actually holds the tunnel. The failure this replaced: the edge's
# deregistration arrives AFTER central has published, so a withdrawal that only asks "has
# anything happened since?" deletes the record that just replaced it, leaving the tunnel live
# and unreachable.
sleep 12
FINAL_DNS=$(docker-compose -f docker-compose-edge.yml exec -T lfr-tunneld-control cat /tmp/dns-hook.log 2>/dev/null || true)
FINAL_TARGET=$(echo "$FINAL_DNS" | awk '
    $2 == "peter-dev.lfr-demo.local" && $1 == "upsert" { target = $3 }
    $2 == "peter-dev.lfr-demo.local" && $1 == "delete" { target = "<withdrawn>" }
    END { print target }
')

if [ "$FINAL_TARGET" != "tunnel.lfr-demo.local" ]; then
    echo "❌ peter-dev.lfr-demo.local ends up pointing at '${FINAL_TARGET}', not the gateway holding it."
    echo "    A tunnel that is up but whose name resolves elsewhere is unreachable to every visitor."
    echo "=== DNS hook calls ==="
    echo "$FINAL_DNS"
    exit 1
fi
echo "✅ DNS followed the tunnel: peter-dev.lfr-demo.local -> tunnel.lfr-demo.local"
echo "✅ Client moved off the stopping edge and kept serving!"

# 9. Drain before restart (#1303).
#
# The payoff for the whole make-before-break chain: restarting a gateway on purpose, for a
# deploy, without dropping the tunnels it is serving. Maintenance mode alone only stops NEW
# connections; the ones already attached were killed by the restart.
#
# Uses the auto-probed client, not the explicit one. peter-dev moved off the edge under a
# planned shutdown earlier, so its region is in the hour-long cooldown that stops a client
# bouncing straight back (#1246) -- it has nowhere to go and could not migrate. peter-auto has
# never been warned.
echo "=== Bringing the edge back up so there is somewhere to drain to ==="
docker-compose -f docker-compose-edge.yml start lfr-tunneld-edge > /dev/null 2>&1
EDGE_BACK=false
for i in {1..40}; do
    if docker-compose -f docker-compose-edge.yml logs --tail=40 lfr-tunneld-edge 2>/dev/null | grep -q "Successfully connected and authenticated"; then
        EDGE_BACK=true
        break
    fi
    sleep 2
done
if [ "$EDGE_BACK" = false ]; then
    echo "❌ The edge never re-authenticated, so the drain test has nowhere to migrate to."
    docker-compose -f docker-compose-edge.yml logs --tail=20 lfr-tunneld-edge
    exit 1
fi

# A client pinned to the control plane with -region eu, rather than whichever gateway the
# auto-probe happened to choose. The drain has to be announced on the gateway the client is
# actually attached to, and "actually" must not depend on measured latency inside a CI runner.
echo "=== Starting a client on the control plane for the drain test ==="
DRAIN_CLIENT_ID=$(docker-compose -f docker-compose-edge.yml run -d --no-deps \
  --entrypoint "./lfr-tunnel" \
  -e LFT_CLIENT_TOKEN="$DEVELOPER_PAT" \
  lfr-tunnel \
  -config client-config-edge.yaml \
  -region eu \
  -subdomain peter-drain \
  -ports 80)

DRAIN_READY=false
for i in {1..30}; do
    CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: peter-drain.lfr-demo.local" http://localhost:8000/ || true)
    if [ "$CODE" = "200" ]; then
        DRAIN_READY=true
        break
    fi
    sleep 2
done

if [ "$DRAIN_READY" = false ]; then
    echo "❌ The drain-test client never came up on the control plane."
    docker logs "$DRAIN_CLIENT_ID" 2>&1 | tail -25
    exit 1
fi
echo "Drain-test client is serving on the control plane"

DRAIN_URL="http://127.0.0.1:8080/api/local/drain"
DRAIN_REASON="Deploy drain E2E"

echo "=== Announcing a drain on the control plane ==="
DRAIN_RESP=$(docker-compose -f docker-compose-edge.yml exec -T lfr-tunneld-control \
    wget -q -O - --post-data="{\"seconds\": 45, \"reason\": \"${DRAIN_REASON}\"}" \
    --header='Content-Type: application/json' "$DRAIN_URL" 2>/dev/null || true)

if ! echo "$DRAIN_RESP" | grep -q '"draining":true'; then
    echo "❌ The control plane did not report itself draining."
    echo "    Response: ${DRAIN_RESP:-<none>}"
    exit 1
fi
echo "Control plane reports: $DRAIN_RESP"

# The announcement has to reach a real client over the real heartbeat. Asserted on the reason
# text, which is unique to this drain -- the scheduled-stop warning earlier in this run also
# logs "shutting down", so matching on that alone would pass without the drain doing anything.
echo "=== Waiting for the client to receive the drain announcement ==="
DRAIN_SEEN=false
for i in {1..40}; do
    if docker logs "$DRAIN_CLIENT_ID" 2>&1 | grep -q "$DRAIN_REASON"; then
        DRAIN_SEEN=true
        break
    fi
    sleep 2
done

if [ "$DRAIN_SEEN" = false ]; then
    echo "❌ The client never received the drain announcement."
    echo "    An operator-initiated drain that no client hears is the same as no drain at all."
    docker logs "$DRAIN_CLIENT_ID" 2>&1 | tail -20
    exit 1
fi
echo "✅ Client received the drain announcement"

echo "=== Waiting for the client to move off the control plane ==="
DRAIN_MOVED=false
for i in {1..60}; do
    CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: peter-drain.lfr-demo.local" http://localhost:8090/ || true)
    if [ "$CODE" = "200" ]; then
        DRAIN_MOVED=true
        break
    fi
    sleep 2
done

if [ "$DRAIN_MOVED" = false ]; then
    echo "❌ The client heard the drain but never came up on the edge."
    docker logs "$DRAIN_CLIENT_ID" 2>&1 | tail -25
    exit 1
fi
echo "✅ Client migrated to the edge ahead of the restart"

# The actual assertion: restart the control plane and require the tunnel to serve throughout.
# Sampled continuously rather than checked once afterwards, because a single check after the
# fact cannot tell "never dropped" from "dropped and recovered before we looked".
echo "=== Restarting the control plane while sampling the tunnel ==="
# Sampled at BOTH gateways, requiring one of them to answer at each instant. A tunnel follows
# its client: it moved to the edge on the drain announcement, and once the control plane is
# back the failback prober legitimately returns it there. Sampling a single gateway records a
# 502 for a tunnel that is serving perfectly well on the other one -- which is exactly what an
# earlier version of this test did, and it read as an outage that never happened.

# Asserted after the run, not just used to size the loop: a sampler that died early writes a
# short log, and a short log is indistinguishable from "the tunnel never dropped" unless the
# count itself is checked (#1358). Driven from one variable so the two cannot drift.
EXPECTED_SAMPLES=60
SAMPLE_LOG=$(mktemp)
(
    for i in $(seq 1 "$EXPECTED_SAMPLES"); do
        EDGE_CODE=$(curl -s -o /dev/null -w "%{http_code}" --max-time 2 \
            -H "Host: peter-drain.lfr-demo.local" http://localhost:8090/ 2>/dev/null || echo 000)
        CENTRAL_CODE=$(curl -s -o /dev/null -w "%{http_code}" --max-time 2 \
            -H "Host: peter-drain.lfr-demo.local" http://localhost:8000/ 2>/dev/null || echo 000)
        if [ "$EDGE_CODE" = "200" ] || [ "$CENTRAL_CODE" = "200" ]; then
            echo "200" >> "$SAMPLE_LOG"
        else
            echo "edge=$EDGE_CODE central=$CENTRAL_CODE" >> "$SAMPLE_LOG"
        fi
        sleep 0.5
    done
) &
SAMPLER_PID=$!

sleep 3
docker-compose -f docker-compose-edge.yml restart lfr-tunneld-control > /dev/null 2>&1
echo "Control plane restarted; letting the sampler finish..."
wait $SAMPLER_PID 2>/dev/null || true

# grep -c and grep -vc print the count AND exit 1 when that count is zero, so `|| echo 0` fired
# in addition to grep's own "0" and produced the two-line string "0\n0" (#1358). That is not a
# valid integer, so the comparison below errored, the failure branch was skipped, and the suite
# reported success. Assign on the failure branch instead of appending a second value to stdout.
TOTAL=$(grep -c . "$SAMPLE_LOG" 2>/dev/null) || TOTAL=0
FAILED=$(grep -vc '^200$' "$SAMPLE_LOG" 2>/dev/null) || FAILED=0
echo "Sampled ${TOTAL} requests through the tunnel during the restart; ${FAILED} did not return 200."

# The sampler having run is part of the assertion, not a precondition to assume. Without this,
# an empty log reports zero failures and the suite certifies "zero interruption" on no evidence.
if [ "$TOTAL" -ne "$EXPECTED_SAMPLES" ]; then
    echo "❌ Sampler collected ${TOTAL} of ${EXPECTED_SAMPLES} expected samples."
    echo "    The tunnel was never actually observed, so this run proves nothing about the drain."
    echo "    Sample log contents:"
    cat "$SAMPLE_LOG"
    rm -f "$SAMPLE_LOG"
    exit 1
fi

if [ "$FAILED" -ne 0 ]; then
    echo "❌ The tunnel was interrupted by a control plane restart it had been warned about."
    echo "    Non-200 responses:"
    grep -v '^200$' "$SAMPLE_LOG" | sort | uniq -c
    echo "    Sample sequence (first ${EXPECTED_SAMPLES}, in order):"
    tr '\n' ' ' < "$SAMPLE_LOG"; echo
    echo "=== Drain client log (tail) ==="
    docker logs "$DRAIN_CLIENT_ID" 2>&1 | tail -40
    echo "=== Edge log (tail) ==="
    docker-compose -f docker-compose-edge.yml logs --tail=40 lfr-tunneld-edge 2>&1
    rm -f "$SAMPLE_LOG"
    exit 1
fi
rm -f "$SAMPLE_LOG"
echo "✅ Control plane restarted with zero interruption to the drained tunnel!"

# Leaving the announcement set would have every client migrate away from a gateway that is
# staying up, so the deploy clears it -- and so must this.
docker-compose -f docker-compose-edge.yml exec -T lfr-tunneld-control \
    wget -q -O - --post-data='{"seconds": 0}' --header='Content-Type: application/json' "$DRAIN_URL" > /dev/null 2>&1 || true

# 10. Unplanned failover (#1374).
#
# Everything above this point is an ANNOUNCED move: a scheduled shutdown warning, or a drain
# posted before a restart. Both depend on the gateway being well behaved enough to say goodbye.
# This is the case that has no announcement -- a crash, an OOM kill, a partition, an instance
# terminated outside its window -- and it was the one path the suite never covered.
#
# kill, not stop: stop sends SIGTERM, the gateway shuts down gracefully and may announce, which
# turns this back into the planned path already tested above.
echo "=== Starting a client pinned to the edge for the failover test ==="
# The failover cooldown is compressed for THIS client only, not in the compose service.
# Phase 9 depends on peter-dev still being inside the hour-long PLANNED cooldown, and a
# service-wide override would quietly change that phase's premise while appearing to pass.
# In production this is 90s; the client ignores an unparseable value and keeps the default,
# so a typo here cannot weaken a real deployment.
FAIL_CLIENT_ID=$(docker-compose -f docker-compose-edge.yml run -d --no-deps \
  --entrypoint "./lfr-tunnel" \
  -e LFT_CLIENT_TOKEN="$DEVELOPER_PAT" \
  -e LFT_REGION_FAILOVER_COOLDOWN=5s \
  lfr-tunnel \
  -config client-config-edge.yaml \
  -region us \
  -subdomain peter-fail \
  -ports 80)

FAIL_READY=false
for i in {1..30}; do
    if curl -s -o /dev/null -w "%{http_code}" --max-time 2 \
        -H "Host: peter-fail.lfr-demo.local" http://localhost:8090/ 2>/dev/null | grep -q 200; then
        FAIL_READY=true
        break
    fi
    sleep 2
done
if [ "$FAIL_READY" = false ]; then
    echo "❌ The failover-test client never came up on the edge."
    docker logs "$FAIL_CLIENT_ID" 2>&1 | tail -30
    exit 1
fi
echo "Failover-test client is serving on the edge"

echo "=== Killing the edge outright, with no warning ==="
docker-compose -f docker-compose-edge.yml kill lfr-tunneld-edge > /dev/null 2>&1

# Unlike the drain, this IS an interruption: the gateway carrying the tunnel vanished. What is
# being asserted is recovery, and how long it takes -- not that nothing was dropped.
RECOVERED=false
RECOVERY_SECONDS=0
for i in $(seq 1 60); do
    if curl -s -o /dev/null -w "%{http_code}" --max-time 2 \
        -H "Host: peter-fail.lfr-demo.local" http://localhost:8000/ 2>/dev/null | grep -q 200; then
        RECOVERED=true
        RECOVERY_SECONDS=$i
        break
    fi
    sleep 1
done

if [ "$RECOVERED" = false ]; then
    echo "❌ The client never recovered after its gateway was killed without warning."
    echo "    This is the failure mode with no announcement to react to, so nothing else covers it."
    echo "=== Failover client log (tail) ==="
    docker logs "$FAIL_CLIENT_ID" 2>&1 | tail -40
    echo "=== Control plane log (tail) ==="
    docker-compose -f docker-compose-edge.yml logs --tail=30 lfr-tunneld-control 2>&1
    exit 1
fi
echo "✅ Client recovered onto the control plane ${RECOVERY_SECONDS}s after its edge was killed!"

# Recovering by luck is not recovering. The client must have noticed the gateway was gone,
# rather than the tunnel having been re-established by some unrelated reconnect.
# The exact line this path emits, not a loose alternation: "reconnect" and friends appear in
# ordinary operation, so matching those would pass whether or not the failover machinery ran.
#
# Note there are TWO failover paths, and an abrupt kill takes the second:
#   - "Triggering dynamic region failover"  -- the gateway ANSWERED with 503 / lease evicted
#   - "Successfully failed over to region"  -- the tunnel connection itself dropped
# A killed gateway answers nothing, so the connection-loss path is the one under test here.
# Asserting the first was wrong, and the loose version hid that by matching "reconnect".
if ! docker logs "$FAIL_CLIENT_ID" 2>&1 | grep -q "Successfully failed over to region"; then
    echo "❌ The client is serving again but never ran the region failover path."
    echo "    Recovery has to be attributable to the mechanism, or this passes for reasons the"
    echo "    code does not own -- an ordinary reconnect would look identical from outside."
    docker logs "$FAIL_CLIENT_ID" 2>&1 | tail -40
    exit 1
fi
echo "✅ Client detected the gateway loss and ran the failover path"

# 11. Failback (#1374).
#
# The other half of the journey, and the half #1310 was found in: once the edge is healthy the
# client should return to its preferred region -- but NOT immediately after a planned move, or
# it bounces straight back to a gateway that is about to be switched off.
echo "=== Bringing the killed edge back for the failback test ==="
docker-compose -f docker-compose-edge.yml start lfr-tunneld-edge > /dev/null 2>&1
EDGE_BACK=false
for i in {1..40}; do
    if docker-compose -f docker-compose-edge.yml logs --tail=40 lfr-tunneld-edge 2>/dev/null | grep -q "Successfully connected and authenticated"; then
        EDGE_BACK=true
        break
    fi
    sleep 2
done
if [ "$EDGE_BACK" = false ]; then
    echo "❌ The edge never came back, so failback cannot be tested."
    docker-compose -f docker-compose-edge.yml logs --tail=20 lfr-tunneld-edge
    exit 1
fi

# LFT_REGION_FAILOVER_COOLDOWN is compressed on this client only, so this waits seconds rather
# than the production 90. The failback prober ticks every 15s on top of that.
# Waits on the MECHANISM, not on the edge answering. An edge cross-proxies to whichever gateway
# holds the lease (#1249), so it returns 200 for this tunnel the moment it is back up -- while
# the client is still on the control plane. Polling the HTTP code therefore succeeded on the
# first iteration and asserted the log before the prober had ticked once, which is what made an
# earlier version of this phase fail for a reason that had nothing to do with failback.
FAILED_BACK=false
for i in $(seq 1 60); do
    if docker logs "$FAIL_CLIENT_ID" 2>&1 | grep -q "Successfully failed back to primary region"; then
        FAILED_BACK=true
        break
    fi
    sleep 2
done

if [ "$FAILED_BACK" = false ]; then
    echo "❌ The client never returned to its preferred region after the edge recovered."
    echo "    Without failback a single transient fault pins every client on the control plane"
    echo "    permanently, which defeats the point of having regional edges."
    echo "=== Failback-related lines from the whole client log ==="
    docker logs "$FAIL_CLIENT_ID" 2>&1 | grep -iE 'failback|failed back|primary region|holding off|cooling' || echo "    (none -- the prober never reported on the primary at all)"
    echo "=== Failover client log (tail) ==="
    docker logs "$FAIL_CLIENT_ID" 2>&1 | tail -40
    exit 1
fi
# Having returned, it must also still be serving.
if ! curl -s -o /dev/null -w "%{http_code}" --max-time 3 \
    -H "Host: peter-fail.lfr-demo.local" http://localhost:8090/ 2>/dev/null | grep -q 200; then
    echo "❌ The client failed back but the tunnel is not serving on the edge."
    docker logs "$FAIL_CLIENT_ID" 2>&1 | tail -30
    exit 1
fi
echo "✅ Client failed back to the edge once it was healthy again!"

echo "✅ All Multi-Region Edge E2E Integration Tests PASSED!"
exit 0
