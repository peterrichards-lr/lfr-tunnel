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
# TWO populations, not one (#1454). A tunnel hears about a drain on that heartbeat; a browser
# never sends one, so somebody working in the portal got no warning at all and simply watched
# the page fail. They are genuinely different sets -- on 2026-08-27 central had zero tunnels
# attached and one active portal session at the moment of a restart -- so a restart decision
# made on tunnels alone is blind to the people it inconveniences. This warns the portal too,
# through the banner that already exists, and only when somebody is actually logged in.
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

# local_api_url resolves one of the gateway's localhost-only endpoints from its own config.
#
# The bind address may be a wildcard, which is not dialable -- 0.0.0.0 means "every
# interface", so the request has to be aimed at loopback with the port kept.
local_api_url() {
    local path="$1" bind
    bind=$(grep -E '^http_bind_addr:' "$CONFIG" 2>/dev/null | sed -e 's/.*"\(.*\)".*/\1/')
    [ -n "$bind" ] || return 1
    case "$bind" in
        0.0.0.0:*|"[::]:"*) echo "http://127.0.0.1:${bind##*:}${path}" ;;
        *) echo "http://${bind}${path}" ;;
    esac
}

drain_url() { local_api_url /api/local/drain; }
broadcast_url() { local_api_url /api/local/broadcast; }

# json_escape makes a caller-supplied string safe to embed in the payloads below.
#
# A reason containing a double quote used to produce invalid JSON, which the endpoint rejects
# -- so the drain was silently skipped and the restart dropped every attached tunnel. Failing
# open is right for a missing endpoint; failing open because of an apostrophe policy is not.
json_escape() {
    printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
}

# local_leases reports how many tunnels this gateway is serving ITSELF.
#
# Tunnels held by edge nodes are deliberately excluded by the endpoint: an edge proxies
# independently, so restarting the control plane does not interrupt them, and waiting for them
# would mean waiting forever.
local_leases() {
    curl -sf -m 5 "$1" 2>/dev/null | sed -n 's/.*"local_leases":\([0-9]*\).*/\1/p'
}

# portal_sessions reports how many people are logged into the portal right now.
#
# Absent on a gateway with no database -- an edge -- which reads as zero and means no banner is
# raised there. That is correct rather than a fallback: an edge does not serve the portal at
# all (#1478), so there is nobody on it to warn.
portal_sessions() {
    curl -sf -m 5 "$1" 2>/dev/null | sed -n 's/.*"portal_sessions":\([0-9]*\).*/\1/p'
}

# warn_portal raises the portal banner, and only when somebody is there to read it.
#
# The banner is set rather than the maintenance state machine armed. Arming it would flip the
# gateway into maintenance mode and kick tunnels on a timer of its own -- a second thing
# sequencing the restart, racing the caller that is already doing it. The script owns the
# sequence; this only tells people what is about to happen.
#
# Nobody is signed out by the restart: portal sessions are persisted (#1304), so the honest
# message is "briefly unavailable", not "you will be logged out".
warn_portal() {
    local window="$1" sessions="$2" url message

    if ! url=$(broadcast_url); then
        return 0
    fi

    message="Scheduled maintenance: the portal will be briefly unavailable in ${window}s while the gateway restarts. You will stay signed in."
    if ! curl -sf -m 5 -X POST "$url" \
        -H 'Content-Type: application/json' \
        -d "{\"message\": \"$(json_escape "$message")\"}" > /dev/null 2>&1; then
        echo "[drain] No broadcast endpoint answered; portal users were not warned."
        return 0
    fi
    echo "[drain] Warned $sessions portal session(s): the portal is briefly unavailable in ${window}s."
}

# clear_portal_warning takes the banner down again.
#
# Separate from clearing the drain because they can fail independently, and a banner left up
# after a successful restore tells everyone the system is in maintenance when it is not --
# which is how a notice stops being believed.
clear_portal_warning() {
    local url
    if ! url=$(broadcast_url); then
        return 0
    fi
    curl -sf -m 5 -X POST "$url" \
        -H 'Content-Type: application/json' \
        -d '{"message": ""}' > /dev/null 2>&1 || true
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
        -d "{\"seconds\": $window, \"reason\": \"$(json_escape "$reason")\"}" > /dev/null 2>&1; then
        echo "[drain] No drain endpoint answered at $url; continuing without a drain."
        return 0
    fi

    # Warn the portal too, if anyone is on it. Read after announcing rather than before, so a
    # gateway with no drain endpoint short-circuits above and this never runs against one.
    local sessions
    sessions=$(portal_sessions "$url")
    if [ -n "$sessions" ] && [ "$sessions" -gt 0 ]; then
        warn_portal "$window" "$sessions"
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
    clear_portal_warning
    echo "[drain] Announcement cleared."
}

case "${1:-}" in
    announce) shift; announce "$@" ;;
    clear) clear_drain ;;
    *) usage ;;
esac
