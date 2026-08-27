#!/usr/bin/env bash
# drain-and-wait.sh — announce a pending gateway restart and wait for tunnels to move
#
# Usage (on the gateway box):
#   sudo ./drain-and-wait.sh announce [window_seconds] [wait_seconds] [reason]
#   sudo ./drain-and-wait.sh clear
#
# Maintenance mode stops NEW connections arriving. It does nothing about the ones already
# attached, which a stop kills outright -- measured against real infrastructure, a client
# dropped that way was down 24m36s, while one that moved on a warning was not down at all
# (#1246). This announces the restart so clients move first, via the same make-before-break
# path a scheduled edge stop uses. Clients pick the announcement up on the tunnel-status
# heartbeat they already send (#1238).
#
# Extracted from pkg/ops/deploy.go, which was the only caller when the drain concept landed
# in #1305. Everything written BEFORE that -- restore-with-maintenance.sh in particular --
# silently had no drain, because there was nothing to call. One copy, installed once, callable
# by anything that wraps work in a maintenance window (#1455).
#
# Best-effort by design, and never fatal: an older gateway with no /api/local/drain endpoint,
# or a config whose bind address cannot be read, must not stop a deploy or a restore. It
# simply behaves as it did before this existed.
set -uo pipefail

CONFIG="${LFT_CONFIG:-/etc/lfr-tunneld/server-config.yaml}"

# Defaults match what deploy used inline before this was extracted. The window is what a client
# is told it has; the wait bounds how long we sit here watching it actually leave.
DEFAULT_WINDOW=45
DEFAULT_WAIT=90
POLL_INTERVAL=5

usage() {
    echo "Usage: $0 announce [window_seconds] [wait_seconds] [reason]"
    echo "       $0 clear"
    exit 2
}

# drain_url resolves the local drain endpoint from the gateway's own config.
#
# The bind address may be a wildcard, which is not dialable -- 0.0.0.0 means "every
# interface", so the request has to be aimed at loopback with the port kept.
drain_url() {
    local bind
    bind=$(grep -E '^http_bind_addr:' "$CONFIG" 2>/dev/null | sed -e 's/.*"\(.*\)".*/\1/')
    [ -n "$bind" ] || return 1
    case "$bind" in
        0.0.0.0:*|"[::]:"*) echo "http://127.0.0.1:${bind##*:}/api/local/drain" ;;
        *) echo "http://${bind}/api/local/drain" ;;
    esac
}

# local_leases reports how many tunnels this gateway is serving ITSELF.
#
# Tunnels held by edge nodes are deliberately excluded by the endpoint: an edge proxies
# independently, so restarting the control plane does not interrupt them, and waiting for them
# would mean waiting forever.
local_leases() {
    curl -sf -m 5 "$1" 2>/dev/null | sed -n 's/.*"local_leases":\([0-9]*\).*/\1/p'
}

announce() {
    local window="${1:-$DEFAULT_WINDOW}"
    local wait_for="${2:-$DEFAULT_WAIT}"
    local reason="${3:-Gateway is restarting for maintenance}"

    local url
    if ! url=$(drain_url); then
        echo "[drain] Could not read $CONFIG for a bind address; skipping the drain announcement."
        return 0
    fi

    if ! curl -sf -m 5 -X POST "$url" \
        -H 'Content-Type: application/json' \
        -d "{\"seconds\": $window, \"reason\": \"$reason\"}" > /dev/null 2>&1; then
        echo "[drain] No drain endpoint answered at $url; continuing without a drain."
        return 0
    fi

    echo "[drain] Announced a ${window}s window; waiting up to ${wait_for}s for tunnels to move..."
    local waited=0
    local leases=""
    while [ "$waited" -lt "$wait_for" ]; do
        leases=$(local_leases "$url")
        # An empty reading means the endpoint stopped answering. Break rather than spin: the
        # caller is about to restart anyway, and a silent endpoint tells us nothing useful.
        [ -n "$leases" ] || break
        if [ "$leases" -eq 0 ]; then
            echo "[drain] Drained; no tunnels left attached."
            return 0
        fi
        echo "[drain]   $leases tunnel(s) still attached..."
        sleep "$POLL_INTERVAL"
        waited=$((waited + POLL_INTERVAL))
    done

    # Deliberately not fatal on timeout. Reporting what is still attached and carrying on is
    # the same outcome as before this existed, whereas refusing to proceed because one client
    # will not move would be a new way for a deploy or a restore to fail.
    if [ -n "$leases" ] && [ "$leases" -ne 0 ]; then
        echo "[drain] WARNING: proceeding with $leases tunnel(s) still attached; they will be dropped."
    fi
    return 0
}

# clear withdraws the announcement. Without this, clients keep migrating away from a node that
# is staying up.
clear_drain() {
    local url
    if ! url=$(drain_url); then
        return 0
    fi
    curl -sf -m 5 -X POST "$url" \
        -H 'Content-Type: application/json' \
        -d '{"seconds": 0}' > /dev/null 2>&1 || true
    echo "[drain] Announcement cleared."
}

case "${1:-}" in
    announce) shift; announce "$@" ;;
    clear) clear_drain ;;
    *) usage ;;
esac
