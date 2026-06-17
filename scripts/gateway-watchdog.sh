#!/usr/bin/env bash
set -euo pipefail

LOG_TAG="GatewayWatchdog"

log_info() {
    echo "[$(date -u +'%Y-%m-%dT%H:%M:%SZ')] [${LOG_TAG}] [INFO] $1"
}

log_error() {
    echo "[$(date -u +'%Y-%m-%dT%H:%M:%SZ')] [${LOG_TAG}] [ERROR] $1" >&2
}

# 1. Guarantee Nginx's Let's Encrypt config dependency exists
OPTIONS_FILE="/etc/letsencrypt/options-ssl-nginx.conf"
DHPARAMS_FILE="/etc/letsencrypt/ssl-dhparams.pem"
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
    else
        log_error "Nginx config test failed even after healing dependencies!"
    fi
fi

# 2. Check HTTP status of local lfr-tunneld daemon
TUNNEL_HEALTH_URL="http://127.0.0.1:8080/api/version"
if ! curl -sf --connect-timeout 5 "${TUNNEL_HEALTH_URL}" > /dev/null; then
    log_error "Liferay Tunnel Daemon on port 8080 is not responding! Attempting restart..."
    sudo systemctl restart lfr-tunneld
    sleep 2
    if curl -sf "${TUNNEL_HEALTH_URL}" > /dev/null; then
        log_info "Liferay Tunnel Daemon successfully recovered!"
    else
        log_error "Liferay Tunnel Daemon failed to recover after restart!"
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
        else
            log_error "Nginx failed to start after force restart!"
        fi
    fi
fi
