#!/usr/bin/env bash
set -euo pipefail

# Configuration
TOKEN_FILE="/etc/letsencrypt/cloudflare.ini"
DOMAINS=("lfr-demo.se" "lfr-demo.online")
RECORD_NAMES=("@" "*" "tunnel" "portal")

# Extract API Token from cloudflare.ini
API_TOKEN=$(grep -E "^dns_cloudflare_api_token[[:space:]]*=" "${TOKEN_FILE}" | cut -d'=' -f2 | tr -d ' "[:space:]')

if [ -z "${API_TOKEN}" ]; then
    echo "[Error] Could not extract Cloudflare API token from ${TOKEN_FILE}" >&2
    exit 1
fi

# Detect Public IPv4 and IPv6 locally from network interface ens3 (with fallback to external queries)
if ip addr show ens3 >/dev/null 2>&1; then
    IPV4=$(ip -4 addr show ens3 | awk '/inet / {print $2}' | cut -d/ -f1 | grep '178' || ip -4 addr show ens3 | awk '/inet / {print $2}' | cut -d/ -f1 | head -n1 || echo "")
    IPV6=$(ip -6 addr show ens3 scope global | awk '/inet6 / {print $2}' | cut -d/ -f1 | head -n1 || echo "")
else
    # Fallback to external queries if ens3 is not found
    IPV4=$(curl -s4 --connect-timeout 5 https://api.ipify.org || echo "")
    IPV6=$(curl -s6 --connect-timeout 5 https://api.ipify.org || echo "")
fi

if [ -z "${IPV4}" ] && [ -z "${IPV6}" ]; then
    echo "[Error] Could not retrieve public IPv4 or IPv6 address." >&2
    exit 1
fi

echo "[DDNS] Detected Public IPs - IPv4: ${IPV4:-N/A}, IPv6: ${IPV6:-N/A}"

# API Helper
cf_api() {
    local method=$1
    local endpoint=$2
    local data=${3:-""}
    
    if [ -n "${data}" ]; then
        curl -s -X "${method}" "https://api.cloudflare.com/client/v4/${endpoint}" \
            -H "Authorization: Bearer ${API_TOKEN}" \
            -H "Content-Type: application/json" \
            -d "${data}"
    else
        curl -s -X "${method}" "https://api.cloudflare.com/client/v4/${endpoint}" \
            -H "Authorization: Bearer ${API_TOKEN}" \
            -H "Content-Type: application/json"
    fi
}

for domain in "${DOMAINS[@]}"; do
    echo "[DDNS] Processing domain: ${domain}"
    
    # Fetch Zone ID
    zone_resp=$(cf_api "GET" "zones?name=${domain}")
    zone_id=$(echo "${zone_resp}" | jq -r '.result[0].id // empty')
    
    if [ -z "${zone_id}" ]; then
        echo "[Error] Could not find Zone ID for ${domain}" >&2
        continue
    fi

    for rname in "${RECORD_NAMES[@]}"; do
        full_rname="${domain}"
        if [ "${rname}" != "@" ]; then
            full_rname="${rname}.${domain}"
        fi

        # 1. Update IPv4 (A Record)
        if [ -n "${IPV4}" ]; then
            rec_resp=$(cf_api "GET" "zones/${zone_id}/dns_records?name=${full_rname}&type=A")
            rec_id=$(echo "${rec_resp}" | jq -r '.result[0].id // empty')
            current_ip=$(echo "${rec_resp}" | jq -r '.result[0].content // empty')

            if [ -z "${rec_id}" ]; then
                echo "[DDNS] Creating A record: ${full_rname} -> ${IPV4}"
                cf_api "POST" "zones/${zone_id}/dns_records" "{\"type\":\"A\",\"name\":\"${full_rname}\",\"content\":\"${IPV4}\",\"ttl\":120,\"proxied\":false}" > /dev/null
            elif [ "${current_ip}" != "${IPV4}" ]; then
                echo "[DDNS] Updating A record for ${full_rname}: ${current_ip} -> ${IPV4}"
                cf_api "PUT" "zones/${zone_id}/dns_records/${rec_id}" "{\"type\":\"A\",\"name\":\"${full_rname}\",\"content\":\"${IPV4}\",\"ttl\":120,\"proxied\":false}" > /dev/null
            else
                echo "[DDNS] A record for ${full_rname} is up to date (${IPV4})."
            fi
        fi

        # 2. Update IPv6 (AAAA Record)
        if [ -n "${IPV6}" ]; then
            rec_resp=$(cf_api "GET" "zones/${zone_id}/dns_records?name=${full_rname}&type=AAAA")
            rec_id=$(echo "${rec_resp}" | jq -r '.result[0].id // empty')
            current_ip=$(echo "${rec_resp}" | jq -r '.result[0].content // empty')

            if [ -z "${rec_id}" ]; then
                echo "[DDNS] Creating AAAA record: ${full_rname} -> ${IPV6}"
                cf_api "POST" "zones/${zone_id}/dns_records" "{\"type\":\"AAAA\",\"name\":\"${full_rname}\",\"content\":\"${IPV6}\",\"ttl\":120,\"proxied\":false}" > /dev/null
            elif [ "${current_ip}" != "${IPV6}" ]; then
                echo "[DDNS] Updating AAAA record for ${full_rname}: ${current_ip} -> ${IPV6}"
                cf_api "PUT" "zones/${zone_id}/dns_records/${rec_id}" "{\"type\":\"AAAA\",\"name\":\"${full_rname}\",\"content\":\"${IPV6}\",\"ttl\":120,\"proxied\":false}" > /dev/null
            else
                echo "[DDNS] AAAA record for ${full_rname} is up to date (${IPV6})."
            fi
        fi
    done
done
