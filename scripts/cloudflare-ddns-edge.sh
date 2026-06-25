#!/usr/bin/env bash
# scripts/cloudflare-ddns-edge.sh
# Automates updating Cloudflare DNS records for stateless regional edge gateways.
set -euo pipefail

# Configuration
TOKEN_FILE="/etc/letsencrypt/cloudflare.ini"
DOMAINS=("lfr-demo.se" "lfr-demo.online")
# Edge subdomains to update: "us" and "*.us"
RECORD_NAMES=("us" "*.us")

# Extract API Token from cloudflare.ini
if [ ! -f "${TOKEN_FILE}" ]; then
    echo "[Error] Cloudflare credentials file ${TOKEN_FILE} not found" >&2
    exit 1
fi

API_TOKEN=$(grep -E "^dns_cloudflare_api_token[[:space:]]*=" "${TOKEN_FILE}" | cut -d'=' -f2 | tr -d ' "[:space:]')

if [ -z "${API_TOKEN}" ]; then
    echo "[Error] Could not extract Cloudflare API token from ${TOKEN_FILE}" >&2
    exit 1
fi

# Detect Public IPv4 (and IPv6 if present) via external query
IPV4=$(curl -s4 --connect-timeout 5 https://api.ipify.org || echo "")
IPV6=$(curl -s6 --connect-timeout 5 https://api.ipify.org || echo "")

if [ -z "${IPV4}" ] && [ -z "${IPV6}" ]; then
    echo "[Error] Could not retrieve public IPv4 or IPv6 address." >&2
    exit 1
fi

echo "[Edge DDNS] Detected Public IPs - IPv4: ${IPV4:-N/A}, IPv6: ${IPV6:-N/A}"

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
    echo "[Edge DDNS] Processing domain: ${domain}"
    
    # Fetch Zone ID
    zone_resp=$(cf_api "GET" "zones?name=${domain}")
    zone_id=$(echo "${zone_resp}" | jq -r '.result[0].id // empty')
    
    if [ -z "${zone_id}" ]; then
        echo "[Error] Could not find Zone ID for ${domain}" >&2
        continue
    fi

    for rname in "${RECORD_NAMES[@]}"; do
        full_rname="${rname}.${domain}"

        # 1. Update IPv4 (A Record)
        if [ -n "${IPV4}" ]; then
            rec_resp=$(cf_api "GET" "zones/${zone_id}/dns_records?name=${full_rname}&type=A")
            rec_id=$(echo "${rec_resp}" | jq -r '.result[0].id // empty')
            current_ip=$(echo "${rec_resp}" | jq -r '.result[0].content // empty')

            if [ -z "${rec_id}" ]; then
                echo "[Edge DDNS] Creating A record: ${full_rname} -> ${IPV4}"
                cf_api "POST" "zones/${zone_id}/dns_records" "{\"type\":\"A\",\"name\":\"${full_rname}\",\"content\":\"${IPV4}\",\"ttl\":120,\"proxied\":false}" > /dev/null
            elif [ "${current_ip}" != "${IPV4}" ]; then
                echo "[Edge DDNS] Updating A record for ${full_rname}: ${current_ip} -> ${IPV4}"
                cf_api "PUT" "zones/${zone_id}/dns_records/${rec_id}" "{\"type\":\"A\",\"name\":\"${full_rname}\",\"content\":\"${IPV4}\",\"ttl\":120,\"proxied\":false}" > /dev/null
            else
                echo "[Edge DDNS] A record for ${full_rname} is up to date (${IPV4})."
            fi
        fi

        # 2. Update IPv6 (AAAA Record)
        if [ -n "${IPV6}" ]; then
            rec_resp=$(cf_api "GET" "zones/${zone_id}/dns_records?name=${full_rname}&type=AAAA")
            rec_id=$(echo "${rec_resp}" | jq -r '.result[0].id // empty')
            current_ip=$(echo "${rec_resp}" | jq -r '.result[0].content // empty')

            if [ -z "${rec_id}" ]; then
                echo "[Edge DDNS] Creating AAAA record: ${full_rname} -> ${IPV6}"
                cf_api "POST" "zones/${zone_id}/dns_records" "{\"type\":\"AAAA\",\"name\":\"${full_rname}\",\"content\":\"${IPV6}\",\"ttl\":120,\"proxied\":false}" > /dev/null
            elif [ "${current_ip}" != "${IPV6}" ]; then
                echo "[Edge DDNS] Updating AAAA record for ${full_rname}: ${current_ip} -> ${IPV6}"
                cf_api "PUT" "zones/${zone_id}/dns_records/${rec_id}" "{\"type\":\"AAAA\",\"name\":\"${full_rname}\",\"content\":\"${IPV6}\",\"ttl\":120,\"proxied\":false}" > /dev/null
            else
                echo "[Edge DDNS] AAAA record for ${full_rname} is up to date (${IPV6})."
            fi
        fi
    done
done
