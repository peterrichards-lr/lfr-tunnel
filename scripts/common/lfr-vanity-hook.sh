#!/bin/bash
set -euo pipefail

# This is a generic, reusable script -- it carries no default values of its
# own. Every config value must be supplied explicitly via environment
# variables by the caller (the operator-configured vanity_domain_hook
# invocation, or a Liferay-specific wrapper), which is the only place that
# actually knows the right values for a given deployment.
#
# IMPORTANT: this script runs as a child of lfr-tunneld, which is
# deliberately unprivileged (systemd's ProtectSystem=strict, NoNewPrivileges,
# empty CapabilityBoundingSet -- see docs/server/setup_guide.md's "Vanity
# Domain Hook" section). It cannot write to /etc/nginx/sites-enabled or the
# default /etc/letsencrypt, and it cannot reload nginx itself. NGINX_CONF_DIR
# and WEBROOT_PATH must therefore point at directories the service account
# actually owns (typically a dedicated /etc/nginx/vanity-domains.d and a
# dedicated webroot), and CERTBOT_DIR must point certbot's own
# config/work/logs dirs somewhere writable too (typically under
# /etc/lfr-tunneld, which is already in the service's ReadWritePaths).
# Reloading nginx after a config change is handled out-of-band by a
# root-owned systemd path unit watching NGINX_CONF_DIR -- this script only
# needs to wait for that reload to land before asking Certbot to validate.
NGINX_CONF_DIR="${NGINX_CONF_DIR:-}"
WEBROOT_PATH="${WEBROOT_PATH:-}"
UPSTREAM_URL="${UPSTREAM_URL:-}"
CERTBOT_DIR="${CERTBOT_DIR:-}"
ACME_EMAIL="${ACME_EMAIL:-}"

ACTION="$1"
DOMAIN="$2"

if [[ -z "$ACTION" || -z "$DOMAIN" ]]; then
    echo "Usage: $0 [add|remove] [domain]"
    exit 1
fi

if [[ -z "$NGINX_CONF_DIR" ]]; then
    echo "Error: NGINX_CONF_DIR must be set (e.g. /etc/nginx/vanity-domains.d)."
    exit 1
fi

# Validate domain format (basic sanity check)
if [[ ! "$DOMAIN" =~ ^[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$ ]]; then
    echo "Error: Invalid domain format: $DOMAIN"
    exit 1
fi

# wait_for_nginx_pickup polls a sentinel file through nginx's own HTTP
# listener (bypassing DNS via --resolve, since the vanity domain's public
# DNS may not be the thing under test) until the just-written config is
# actually being served, or times out. Without this, Certbot's HTTP-01
# challenge could race the reload-watcher's systemd path unit and fail
# transiently on an otherwise-correct setup.
wait_for_nginx_pickup() {
    local sentinel_name="reload-check-$$"
    local sentinel_path="$WEBROOT_PATH/.well-known/acme-challenge/$sentinel_name"
    echo "ok" > "$sentinel_path"

    local code="000"
    for _ in $(seq 1 20); do
        code=$(curl --resolve "$DOMAIN:80:127.0.0.1" -s -o /dev/null -w '%{http_code}' \
            "http://$DOMAIN/.well-known/acme-challenge/$sentinel_name" || echo "000")
        if [[ "$code" == "200" ]]; then
            break
        fi
        sleep 0.5
    done
    rm -f "$sentinel_path"

    if [[ "$code" != "200" ]]; then
        echo "Error: nginx did not pick up the config for $DOMAIN within 10s (last status: $code)."
        echo "Check that NGINX_CONF_DIR is included from nginx.conf and that the reload-watcher path unit is running."
        return 1
    fi
    return 0
}

case "$ACTION" in
    add)
        if [[ -z "$WEBROOT_PATH" ]]; then
            echo "Error: WEBROOT_PATH must be set (e.g. /var/www/lfr-tunnel-vanity)."
            exit 1
        fi
        if [[ -z "$UPSTREAM_URL" ]]; then
            echo "Error: UPSTREAM_URL must be set (e.g. http://127.0.0.1:8080)."
            exit 1
        fi
        if [[ -z "$CERTBOT_DIR" ]]; then
            echo "Error: CERTBOT_DIR must be set (e.g. /etc/lfr-tunneld/letsencrypt) -- this script's own Certbot state dir, since it cannot use the system default /etc/letsencrypt."
            exit 1
        fi
        if [[ -z "$ACME_EMAIL" ]]; then
            echo "Error: ACME_EMAIL must be set to a real contact address for Let's Encrypt registration."
            exit 1
        fi

        echo "Adding vanity domain: $DOMAIN"
        # 1. Create webroot directory if it doesn't exist
        mkdir -p "$WEBROOT_PATH/.well-known/acme-challenge"

        # 2. Write bootstrap HTTP-only config for validation
        cat <<EOF > "$NGINX_CONF_DIR/$DOMAIN.conf"
server {
    listen 80;
    server_name $DOMAIN *.$DOMAIN;

    location /.well-known/acme-challenge/ {
        root $WEBROOT_PATH;
        try_files \$uri =404;
    }

    location / {
        return 301 https://\$host\$request_uri;
    }
}
EOF

        # The reload-watcher path unit picks up the config change and reloads
        # nginx as root; wait for evidence it actually has before continuing.
        wait_for_nginx_pickup

        # 3. Request Certbot certificate
        echo "Requesting Let's Encrypt certificate for $DOMAIN..."
        if certbot certonly --webroot -w "$WEBROOT_PATH" -d "$DOMAIN" \
            --config-dir "$CERTBOT_DIR" --work-dir "$CERTBOT_DIR/work" --logs-dir "$CERTBOT_DIR/logs" \
            --non-interactive --agree-tos --email "$ACME_EMAIL" --keep-until-expiring; then
            echo "Certificate obtained successfully."
        else
            echo "Certbot failed, trying with fallback..."
            certbot certonly --webroot -w "$WEBROOT_PATH" -d "$DOMAIN" \
                --config-dir "$CERTBOT_DIR" --work-dir "$CERTBOT_DIR/work" --logs-dir "$CERTBOT_DIR/logs" \
                --non-interactive --agree-tos --register-unsafely-without-email --keep-until-expiring
        fi

        # 4. Write full SSL configuration
        cat <<EOF > "$NGINX_CONF_DIR/$DOMAIN.conf"
server {
    listen 80;
    server_name $DOMAIN *.$DOMAIN;

    location /.well-known/acme-challenge/ {
        root $WEBROOT_PATH;
    }

    location / {
        return 301 https://\$host\$request_uri;
    }
}

server {
    listen 443 ssl;
    server_name $DOMAIN *.$DOMAIN;

    ssl_certificate $CERTBOT_DIR/live/$DOMAIN/fullchain.pem;
    ssl_certificate_key $CERTBOT_DIR/live/$DOMAIN/privkey.pem;

    # Safe SSL config defaults
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;
    ssl_prefer_server_ciphers on;

    location / {
        proxy_pass $UPSTREAM_URL;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        # WebSocket support
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
EOF

        # Reload-watcher picks this up too -- nothing more to do here.
        echo "Vanity domain setup completed: $DOMAIN"
        ;;

    remove)
        echo "Removing vanity domain: $DOMAIN"
        # Remove nginx configuration -- the reload-watcher picks up the removal.
        if [ -f "$NGINX_CONF_DIR/$DOMAIN.conf" ]; then
            rm "$NGINX_CONF_DIR/$DOMAIN.conf"
            echo "Removed configuration file: $NGINX_CONF_DIR/$DOMAIN.conf"
        fi

        # Clean up certbot certificate (if CERTBOT_DIR is configured -- if the
        # domain was never fully added, e.g. it failed before certbot ran,
        # there may be nothing to clean up here).
        if [[ -n "$CERTBOT_DIR" ]] && certbot certificates --config-dir "$CERTBOT_DIR" --cert-name "$DOMAIN" >/dev/null 2>&1; then
            echo "Deleting Let's Encrypt certificate for $DOMAIN..."
            certbot delete --config-dir "$CERTBOT_DIR" --work-dir "$CERTBOT_DIR/work" --logs-dir "$CERTBOT_DIR/logs" \
                --cert-name "$DOMAIN" --non-interactive || true
        fi
        echo "Vanity domain removal completed: $DOMAIN"
        ;;

    *)
        echo "Unknown action: $ACTION"
        exit 1
        ;;
esac
