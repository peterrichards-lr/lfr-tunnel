#!/bin/bash
set -euo pipefail

# Configuration parameters with sane defaults
NGINX_CONF_DIR="${NGINX_CONF_DIR:-/etc/nginx/sites-enabled}"
WEBROOT_PATH="${WEBROOT_PATH:-/var/www/letsencrypt}"
UPSTREAM_URL="${UPSTREAM_URL:-http://127.0.0.1:8080}"
ACME_EMAIL="${ACME_EMAIL:-admin@lfr-demo.se}"

ACTION="$1"
DOMAIN="$2"

if [[ -z "$ACTION" || -z "$DOMAIN" ]]; then
    echo "Usage: $0 [add|remove] [domain]"
    exit 1
fi

# Validate domain format (basic sanity check)
if [[ ! "$DOMAIN" =~ ^[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$ ]]; then
    echo "Error: Invalid domain format: $DOMAIN"
    exit 1
fi

case "$ACTION" in
    add)
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

        # Reload nginx for HTTP-01 challenge
        nginx -s reload || systemctl reload nginx || true

        # 3. Request Certbot certificate
        echo "Requesting Let's Encrypt certificate for $DOMAIN..."
        if certbot certonly --webroot -w "$WEBROOT_PATH" -d "$DOMAIN" --non-interactive --agree-tos --email "$ACME_EMAIL" --keep-until-expiring; then
            echo "Certificate obtained successfully."
        else
            echo "Certbot failed, trying with fallback..."
            certbot certonly --webroot -w "$WEBROOT_PATH" -d "$DOMAIN" --non-interactive --agree-tos --register-unsafely-without-email --keep-until-expiring
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

    ssl_certificate /etc/letsencrypt/live/$DOMAIN/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/$DOMAIN/privkey.pem;

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

        # Reload nginx to activate SSL config
        nginx -s reload || systemctl reload nginx
        echo "Vanity domain setup completed: $DOMAIN"
        ;;

    remove)
        echo "Removing vanity domain: $DOMAIN"
        # Remove nginx configuration
        if [ -f "$NGINX_CONF_DIR/$DOMAIN.conf" ]; then
            rm "$NGINX_CONF_DIR/$DOMAIN.conf"
            echo "Removed configuration file: $NGINX_CONF_DIR/$DOMAIN.conf"
        fi

        # Reload Nginx
        nginx -s reload || systemctl reload nginx || true

        # Clean up certbot certificate
        if certbot certificates --cert-name "$DOMAIN" >/dev/null 2>&1; then
            echo "Deleting Let's Encrypt certificate for $DOMAIN..."
            certbot delete --cert-name "$DOMAIN" --non-interactive || true
        fi
        echo "Vanity domain removal completed: $DOMAIN"
        ;;

    *)
        echo "Unknown action: $ACTION"
        exit 1
        ;;
esac
