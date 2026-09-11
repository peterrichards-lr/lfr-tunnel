#!/usr/bin/env bash
set -euo pipefail

LOG_TAG="GatewayWatchdog"
# The local port lfr-tunneld binds to. This has no default -- it must be set
# via the LFT_BACKEND_PORT environment variable (declared in the systemd
# unit's Environment= line, filled in by whichever setup script installs
# this watchdog with the actual deployment's port).
if [ -z "${LFT_BACKEND_PORT:-}" ]; then
    echo "[$(date -u +'%Y-%m-%dT%H:%M:%SZ')] [${LOG_TAG}] [ERROR] LFT_BACKEND_PORT is not set -- check the gateway-watchdog.service Environment= line." >&2
    exit 1
fi
BACKEND_PORT="${LFT_BACKEND_PORT}"

log_info() {
    echo "[$(date -u +'%Y-%m-%dT%H:%M:%SZ')] [${LOG_TAG}] [INFO] $1"
}

log_error() {
    echo "[$(date -u +'%Y-%m-%dT%H:%M:%SZ')] [${LOG_TAG}] [ERROR] $1" >&2
}

# The spool: what this script did, somewhere a person can eventually be told about it.
#
# Everything below heals the box and says so to journald, on a machine nobody is watching. A
# gateway could go down, restart, and come back with no trace, while whoever was using the portal
# saw an outage they could not explain (#1875).
#
# Delivery cannot go through the gateway's own API -- the gateway being down is the event. So this
# records, and the daemon forwards through the notification funnel that already exists, with the
# credentials that already exist (#1732).
#
# It records rather than consumes: this runs as root and lfr-tunneld does not, so having the
# daemon delete what it has read would need write permission across that boundary and would race
# with this script appending. The daemon keeps a high-water mark instead and only ever reads.
# Trimming is therefore this script's job, below.
SPOOL="${LFT_WATCHDOG_SPOOL:-/var/lib/lfr-tunnel/watchdog-events.jsonl}"

# Keep the spool bounded. 200 lines is far more than the daemon needs (it forwards on a ticker)
# and still enough to see a night of restart-looping after the fact.
SPOOL_MAX_LINES=200

# restarts_last_hour is the signal the issue asked for: three restarts in an hour is a different
# event from one, and nothing counted them. Counted from the spool itself so it survives this
# script exiting between runs -- it is a oneshot timer unit, so there is no memory to keep it in.
restarts_in_last_hour() {
    [ -f "${SPOOL}" ] || { echo 0; return; }
    local cutoff
    cutoff=$(date -u -d '1 hour ago' +'%Y-%m-%dT%H:%M:%SZ' 2>/dev/null \
        || date -u -v-1H +'%Y-%m-%dT%H:%M:%SZ' 2>/dev/null \
        || echo '')
    if [ -z "${cutoff}" ]; then
        # No usable date arithmetic. Report every recorded restart rather than zero: an
        # overcount reads as "look at this box", an undercount reads as "nothing happened".
        grep -c '"event":"restart"' "${SPOOL}" 2>/dev/null || echo 0
        return
    fi
    # Timestamps are ISO-8601 UTC with a fixed width, so a string compare is a time compare.
    awk -v cutoff="${cutoff}" '
        /"event":"restart"/ {
            if (match($0, /"ts":"[^"]+"/)) {
                ts = substr($0, RSTART + 6, RLENGTH - 7)
                if (ts >= cutoff) n++
            }
        }
        END { print n + 0 }
    ' "${SPOOL}" 2>/dev/null || echo 0
}

# record_event <service> <outcome>
#
# outcome is recovered | failed. A failed restart is the one that must not be lost: the box did
# not heal itself and nothing else is going to notice.
record_event() {
    local service="$1" outcome="$2" count
    count=$(restarts_in_last_hour)
    count=$((count + 1))

    mkdir -p "$(dirname "${SPOOL}")" 2>/dev/null || true
    printf '{"ts":"%s","event":"restart","service":"%s","outcome":"%s","restarts_last_hour":%d}\n' \
        "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" "${service}" "${outcome}" "${count}" >> "${SPOOL}" || {
        log_error "Could not write the watchdog spool at ${SPOOL} -- this restart will not be reported to anyone."
        return 0
    }

    # World-readable on purpose: lfr-tunneld runs as a different user and has to read this. It
    # holds timestamps and service names, nothing sensitive.
    chmod 644 "${SPOOL}" 2>/dev/null || true

    local lines
    lines=$(wc -l < "${SPOOL}" 2>/dev/null || echo 0)
    if [ "${lines}" -gt "${SPOOL_MAX_LINES}" ]; then
        tail -n "${SPOOL_MAX_LINES}" "${SPOOL}" > "${SPOOL}.tmp" 2>/dev/null \
            && mv "${SPOOL}.tmp" "${SPOOL}" \
            && chmod 644 "${SPOOL}" 2>/dev/null || true
    fi
}

# 1. Guarantee Nginx's Let's Encrypt config dependency exists
# Overridable only so this script can be exercised off a gateway. Nothing in production sets
# these -- the defaults are the real paths, and tests/hooks/test-watchdog-spool.sh asserts that a
# run with no overrides still looks at /etc/letsencrypt, so the indirection cannot quietly become
# a way for the guard to test a path the gateway never takes.
OPTIONS_FILE="${LFT_LE_OPTIONS_FILE:-/etc/letsencrypt/options-ssl-nginx.conf}"
DHPARAMS_FILE="${LFT_LE_DHPARAMS_FILE:-/etc/letsencrypt/ssl-dhparams.pem}"
HEALED=0

if [ ! -f "${OPTIONS_FILE}" ]; then
    log_error "Missing Nginx SSL options file ${OPTIONS_FILE}. Healing..."
    sudo mkdir -p /etc/letsencrypt
    sudo tee "${OPTIONS_FILE}" > /dev/null << 'EOF'
ssl_session_cache shared:le_nginx_SSL:10m;
ssl_session_timeout 1440m;
ssl_session_tickets off;

ssl_protocols TLSv1.2 TLSv1.3;
ssl_prefer_server_ciphers off;

ssl_ciphers "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:DHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES256-GCM-SHA384";
EOF
    HEALED=1
fi

if [ ! -f "${DHPARAMS_FILE}" ]; then
    log_error "Missing Nginx DH parameters file ${DHPARAMS_FILE}. Healing..."
    sudo mkdir -p /etc/letsencrypt
    sudo openssl dhparam -out "${DHPARAMS_FILE}" 2048
    HEALED=1
fi

# If files were healed, ensure Nginx configuration is validated and restarted
if [ "${HEALED}" -eq 1 ]; then
    log_info "Let's Encrypt dependencies restored. Testing Nginx config..."
    if sudo nginx -t; then
        log_info "Nginx config verified. Restarting Nginx..."
        sudo systemctl restart nginx
        record_event "nginx-ssl-deps" "recovered"
    else
        log_error "Nginx config test failed even after healing dependencies!"
        record_event "nginx-ssl-deps" "failed"
    fi
fi

# 2. Check HTTP status of local lfr-tunneld daemon
TUNNEL_HEALTH_URL="http://127.0.0.1:${BACKEND_PORT}/api/version"
if ! curl -sf --connect-timeout 5 "${TUNNEL_HEALTH_URL}" > /dev/null; then
    log_error "Liferay Tunnel Daemon on port ${BACKEND_PORT} is not responding! Attempting restart..."
    sudo systemctl restart lfr-tunneld
    sleep 2
    if curl -sf "${TUNNEL_HEALTH_URL}" > /dev/null; then
        log_info "Liferay Tunnel Daemon successfully recovered!"
        record_event "lfr-tunneld" "recovered"
    else
        log_error "Liferay Tunnel Daemon failed to recover after restart!"
        record_event "lfr-tunneld" "failed"
    fi
fi

# 3. Check HTTP status of Nginx front-end
NGINX_HEALTH_URL="https://127.0.0.1"
# Note: Use -k to allow curl connection over loopback without cert hostname validation
if ! curl -sfk --connect-timeout 5 "${NGINX_HEALTH_URL}" > /dev/null; then
    # Double check if Nginx is dead or just slow. If dead, restart it.
    if ! systemctl is-active --quiet nginx; then
        log_error "Nginx is not running! Force restarting..."
        sudo systemctl restart nginx
        sleep 2
        if systemctl is-active --quiet nginx; then
            log_info "Nginx successfully restarted and running!"
            record_event "nginx" "recovered"
        else
            log_error "Nginx failed to start after force restart!"
            record_event "nginx" "failed"
        fi
    fi
fi
