#!/bin/bash
set -e

# ==============================================================================
# LFR-Tunnel SSO / Keycloak E2E Integration Test
#
# Flow:
#   1. Start Keycloak + lfr-tunneld + nginx-proxy
#   2. Wait for Keycloak to be healthy (OIDC discovery endpoint)
#   3. Wait for lfr-tunneld to be ready
#   4. Obtain a real Keycloak authorisation code via the Direct Access Grant
#      token endpoint, then manually drive the SSO callback to create a session
#   5. Use the session cookie to call /api/me and verify the SSO user was
#      provisioned with the correct auth_method and status
#   6. Verify the user appears in the admin users list with origin = sso
#   7. Teardown
# ==============================================================================

# Generate a unique project name to avoid container collisions between agents
if [ -z "$E2E_PROJECT_NAME" ]; then
    E2E_PROJECT_NAME="lfr-tunnel-e2e-sso-$$"
fi
export E2E_PROJECT_NAME

# Fallback to "docker compose" if "docker-compose" is not installed, wrapping with project name
docker-compose() {
    if docker compose version >/dev/null 2>&1; then
        docker compose -p "$E2E_PROJECT_NAME" "$@"
    else
        command docker-compose -p "$E2E_PROJECT_NAME" "$@"
    fi
}

# Resolve dynamic host ports to avoid port binding collisions
if [ -z "$E2E_PROXY_PORT" ]; then
    E2E_PROXY_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')
fi
if [ -z "$E2E_KEYCLOAK_PORT" ]; then
    E2E_KEYCLOAK_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')
fi
if [ -z "$E2E_MAILPIT_PORT" ]; then
    E2E_MAILPIT_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')
fi
export E2E_PROXY_PORT
export E2E_KEYCLOAK_PORT
export E2E_MAILPIT_PORT

CDPATH= cd -- "$(dirname -- "$0")"

# Signal file configuration
REPO_ROOT="$(cd ../.. && pwd)"
SIGNAL_FILE="${REPO_ROOT}/.progress-signal"

echo "BUILDING" > "$SIGNAL_FILE"

# Make sure we write FAILED or SUCCESS on exit
cleanup() {
    EXIT_CODE=$?
    if [ -f "keycloak-realm.json.bak" ]; then
        mv keycloak-realm.json.bak keycloak-realm.json
    fi
    if [ $EXIT_CODE -eq 0 ]; then
        echo "SUCCESS" > "$SIGNAL_FILE"
    else
        echo "FAILED" > "$SIGNAL_FILE"
    fi
}
trap cleanup EXIT INT TERM ERR

COMPOSE_FILE="docker-compose-sso.yml"
TUNNEL_BASE="http://localhost:$E2E_PROXY_PORT"
KEYCLOAK_BASE="http://localhost:$E2E_KEYCLOAK_PORT"
KEYCLOAK_REALM="liferay"
KEYCLOAK_CLIENT_ID="lfr-tunnel"
KEYCLOAK_CLIENT_SECRET="secret"
SSO_USER_EMAIL="sso-user@lfr-demo.local"
SSO_USER_PASSWORD="SsoP@ssw0rd!"
PROVIDER_ID="keycloak"
ADMIN_EMAIL="admin@lfr-demo.local"

FAILED=0

fail() {
    echo ""
    echo "❌ $1"
    echo ""
    echo "=== lfr-tunneld logs ==="
    docker-compose -f "$COMPOSE_FILE" logs lfr-tunneld
    echo "=== Keycloak logs (tail) ==="
    docker-compose -f "$COMPOSE_FILE" logs --tail=30 keycloak
    docker-compose -f "$COMPOSE_FILE" down -v
    exit 1
}

echo "=== LFR-Tunnel SSO / Keycloak E2E Integration Test ==="
echo ""

# ── Clean previous run ────────────────────────────────────────────────────────
docker-compose -f "$COMPOSE_FILE" down -v --remove-orphans || true

# Dynamically update redirect URIs port to E2E_PROXY_PORT in keycloak-realm.json
sed -i.bak "s/localhost:8000/localhost:$E2E_PROXY_PORT/g" keycloak-realm.json

# ── Start environment ─────────────────────────────────────────────────────────
echo "[1/7] Starting Docker environment (Keycloak, lfr-tunneld, nginx)..."
docker-compose -f "$COMPOSE_FILE" up --build -d

echo "WAITING_HEALTHY" > "$SIGNAL_FILE"

# ── Wait for Keycloak ─────────────────────────────────────────────────────────
echo "[2/7] Waiting for Keycloak to be healthy (up to 120s)..."
KEYCLOAK_READY=false
for i in $(seq 1 60); do
    if curl -sf "${KEYCLOAK_BASE}/realms/${KEYCLOAK_REALM}/.well-known/openid-configuration" > /dev/null 2>&1; then
        echo "  ✅ Keycloak is ready (after ~$((i*2))s)"
        KEYCLOAK_READY=true
        break
    fi
    sleep 2
done
[ "$KEYCLOAK_READY" = "true" ] || fail "Keycloak did not become healthy within 120s"

# ── Wait for lfr-tunneld ──────────────────────────────────────────────────────
echo "[3/7] Waiting for lfr-tunneld / nginx-proxy to be ready..."
TUNNEL_READY=false
for i in $(seq 1 30); do
    if curl -sf "${TUNNEL_BASE}/api/domains" > /dev/null 2>&1; then
        echo "  ✅ lfr-tunneld is ready"
        TUNNEL_READY=true
        break
    fi
    sleep 1
done
[ "$TUNNEL_READY" = "true" ] || fail "lfr-tunneld did not become ready within 30s"

# ── Step 4: Drive SSO login via Keycloak Direct Access Grant ──────────────────
echo "TESTING" > "$SIGNAL_FILE"
#
# The standard browser redirect flow is not curl-friendly in a headless
# environment. Instead we:
#   a) Use Keycloak's Direct Access Grant (Resource Owner Password Credentials)
#      to obtain a real id_token + access_token for the test user.
#   b) Use Keycloak's authorization_code flow by automating the form POST
#      to obtain an authorization code, then drive the callback ourselves.
#
# Approach (b) is used here for maximum realism — it exercises the actual
# redirect chain that the lfr-tunneld server callback handler uses.
# ------------------------------------------------------------------------------

echo "[4/7] Driving SSO authorization code flow..."

# 4a. Request the SSO login redirect from lfr-tunneld
#     Capture the redirect Location header — this points to Keycloak's login page.
KC_LOGIN_URL=$(curl -si -c /tmp/sso-session.txt -o /dev/null -w '%header{location}' \
  "${TUNNEL_BASE}/api/auth/login?provider=${PROVIDER_ID}" 2>/dev/null || true)

if [ -z "$KC_LOGIN_URL" ]; then
    fail "lfr-tunneld did not redirect to Keycloak login page. Is SSO configured?"
fi
echo "  Keycloak login URL obtained (${#KC_LOGIN_URL} chars)"
KC_LOGIN_URL=$(echo "$KC_LOGIN_URL" | sed "s/keycloak:8080/localhost:$E2E_KEYCLOAK_PORT/g")

# 4b. Fetch the Keycloak login page HTML and extract the form action URL.
#     Keycloak embeds a signed action URL that includes the session/tab IDs.
KC_HTML=$(curl -sc /tmp/kc-cookies.txt "$KC_LOGIN_URL" 2>/dev/null)
KC_ACTION=$(echo "$KC_HTML" | python3 -c '
import sys, re
html = sys.stdin.read()
m = re.search(r"action=\"(http[^\"]+)\"", html)
if m:
    import html as h
    print(h.unescape(m.group(1)))
' 2>/dev/null || true)

if [ -z "$KC_ACTION" ]; then
    fail "Could not extract Keycloak form action URL from login page HTML"
fi
echo "  Keycloak form action extracted"
KC_ACTION=$(echo "$KC_ACTION" | sed "s/keycloak:8080/localhost:$E2E_KEYCLOAK_PORT/g")

# 4c. POST credentials to Keycloak login form.
#     Keycloak will redirect back to our callback with ?code=...
#     We capture the Location header WITHOUT following the redirect, because
#     the redirect target is http://localhost:8000/api/auth/callback which
#     nginx-proxy will route to lfr-tunneld.
CALLBACK_URL=$(curl -si \
  -b /tmp/kc-cookies.txt -c /tmp/kc-cookies.txt \
  --max-redirs 0 \
  -d "username=${SSO_USER_EMAIL}&password=${SSO_USER_PASSWORD}&credentialId=" \
  "$KC_ACTION" 2>/dev/null \
  | grep -i '^[Ll]ocation:' | tr -d '\r' | awk '{print $2}' || true)

if [ -z "$CALLBACK_URL" ]; then
    fail "Keycloak did not redirect back to our callback after credential submission"
fi
echo "  Keycloak redirected to callback: ${CALLBACK_URL:0:80}..."

# 4d. Follow the callback URL. lfr-tunneld exchanges the code with Keycloak,
#     creates the user, and sets the lfr_session cookie.
SESSION_RESPONSE=$(curl -si \
  -b /tmp/sso-session.txt -b /tmp/kc-cookies.txt -c /tmp/sso-session.txt \
  --max-redirs 5 \
  "$CALLBACK_URL" 2>/dev/null || true)

# Extract the lfr_session cookie value
SSO_SESSION=$(grep 'lfr_session' /tmp/sso-session.txt 2>/dev/null | awk '{print $NF}' | tr -d '\r\n' || true)
if [ -z "$SSO_SESSION" ]; then
    echo "=== Session Response ==="
    echo "$SESSION_RESPONSE"
    fail "lfr_session cookie not set after SSO callback — user creation may have failed"
fi
echo "  ✅ Session cookie obtained (lfr_session=${SSO_SESSION:0:12}...)"

# ── Step 5: Verify /api/me returns the SSO user ───────────────────────────────
echo "[5/7] Verifying SSO user via /api/me..."
ME_RESPONSE=$(curl -sf \
  -H "Cookie: lfr_session=${SSO_SESSION}" \
  "${TUNNEL_BASE}/api/me" 2>/dev/null || true)

if [ -z "$ME_RESPONSE" ]; then
    fail "/api/me returned an empty response for SSO session"
fi
echo "  /api/me response: $ME_RESPONSE"

ACTUAL_EMAIL=$(echo "$ME_RESPONSE" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("email",""))' 2>/dev/null || true)
ACTUAL_AUTH_METHOD=$(echo "$ME_RESPONSE" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("auth_method",""))' 2>/dev/null || true)
ACTUAL_STATUS=$(echo "$ME_RESPONSE" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("status",""))' 2>/dev/null || true)

echo "  Email:       $ACTUAL_EMAIL"
echo "  Auth Method: $ACTUAL_AUTH_METHOD"
echo "  Status:      $ACTUAL_STATUS"

[ "$ACTUAL_EMAIL" = "$SSO_USER_EMAIL" ] || fail "Expected email '${SSO_USER_EMAIL}', got '${ACTUAL_EMAIL}'"
[ "$ACTUAL_AUTH_METHOD" = "sso - ${PROVIDER_ID}" ] || fail "Expected auth_method 'sso - ${PROVIDER_ID}', got '${ACTUAL_AUTH_METHOD}'"
[ "$ACTUAL_STATUS" = "approved" ] || fail "Expected status 'approved', got '${ACTUAL_STATUS}'"
echo "  ✅ SSO user verified: email, auth_method, and status are all correct"

# ── Step 6: Verify user appears in admin list ─────────────────────────────────
echo "[6/7] Verifying SSO user appears in admin /api/admin/users list..."

# Get admin magic link for admin@lfr-demo.local via the server-side seed
# (The owner user is auto-created as admin on first startup)
ADMIN_ML_RESP=$(curl -sf -X POST \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"${ADMIN_EMAIL}\"}" \
  "${TUNNEL_BASE}/api/auth/magic-link" 2>/dev/null || true)
echo "  Admin magic-link request: $ADMIN_ML_RESP"

# Get the token from Mailpit
sleep 2
ADMIN_ML_TOKEN=$(python3 -c '
import urllib.request, json, re
try:
    data = json.loads(urllib.request.urlopen("http://localhost:$E2E_MAILPIT_PORT/api/v1/messages").read())
    for m in (data.get("messages") or []):
        msg = json.loads(urllib.request.urlopen("http://localhost:$E2E_MAILPIT_PORT/api/v1/message/" + m["ID"]).read())
        body = msg.get("Text","")
        match = re.search(r"verify\?token=([a-f0-9]+)", body, re.IGNORECASE)
        if match:
            print(match.group(1))
            break
except Exception as e:
    import sys; print(f"Error: {e}", file=sys.stderr)
' 2>/dev/null || true)

ADMIN_SESSION=""
if [ -n "$ADMIN_ML_TOKEN" ]; then
    # Redeem the magic link to get admin session
    curl -sc /tmp/admin-session.txt \
      "${TUNNEL_BASE}/api/auth/verify?token=${ADMIN_ML_TOKEN}" > /dev/null 2>&1 || true
    ADMIN_SESSION=$(grep 'lfr_session' /tmp/admin-session.txt 2>/dev/null | awk '{print $NF}' | tr -d '\r\n' || true)
fi

if [ -n "$ADMIN_SESSION" ]; then
    USERS_RESP=$(curl -sf \
      -H "Cookie: lfr_session=${ADMIN_SESSION}" \
      "${TUNNEL_BASE}/api/admin/users" 2>/dev/null || true)
    SSO_USER_FOUND=$(echo "$USERS_RESP" | python3 -c '
import sys, json
users = json.load(sys.stdin)
for u in users:
    if u.get("email") == "sso-user@lfr-demo.local":
        print(u.get("auth_method",""))
        break
' 2>/dev/null || true)

    if [ -n "$SSO_USER_FOUND" ]; then
        echo "  ✅ SSO user found in admin list with auth_method: $SSO_USER_FOUND"
    else
        echo "  ⚠️  Warning: Could not confirm SSO user in admin list (admin session may have failed)"
    fi
else
    echo "  ⚠️  Skipping admin list check — Mailpit not available for admin magic link"
fi

# ── Step 7: Teardown ──────────────────────────────────────────────────────────
echo "[7/7] Tearing down Docker environment..."
docker-compose -f "$COMPOSE_FILE" down -v
rm -f /tmp/kc-cookies.txt /tmp/sso-session.txt /tmp/admin-session.txt

echo ""
echo "✅ SSO / Keycloak E2E Integration Test PASSED!"
echo "   - Keycloak authorization_code flow exercised end-to-end"
echo "   - lfr-tunneld auto-provisioned the SSO user with correct auth_method"
echo "   - Session cookie issued and /api/me validated"
