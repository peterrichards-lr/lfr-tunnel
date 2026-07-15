#!/usr/bin/env bash
set -euo pipefail

# Configuration
TOKEN_FILE="/etc/letsencrypt/cloudflare.ini"
CONFIG_FILE="/etc/lfr-tunneld/server-config.yaml"
RECORD_NAMES=("@" "*" "tunnel" "portal")

# Extract dynamic domains list from server-config.yaml if it exists
DOMAINS=()
if [ -f "${CONFIG_FILE}" ]; then
    DOMAINS_STR=$(python3 -c "
import sys, re
domains = []
in_domains = False
try:
    for line in open('${CONFIG_FILE}'):
        line_strip = line.strip()
        if line_strip.startswith('domains:'):
            in_domains = True
            continue
        if in_domains:
            # Stop if we hit another top-level key
            if line.strip() and not line.startswith(' ') and not line.startswith('-'):
                break
            match = re.search(r'-\s+([a-zA-Z0-9.-]+)', line_strip)
            if match:
                domains.append(match.group(1))
    print(' '.join(domains))
except Exception:
    pass
" 2>/dev/null || true)
    if [ -n "${DOMAINS_STR}" ]; then
        DOMAINS=(${DOMAINS_STR})
        echo "[DDNS] Dynamically loaded domains from ${CONFIG_FILE}: ${DOMAINS[*]}"
    fi
fi

# Fallback default domains if config doesn't exist or parsing returned empty
if [ ${#DOMAINS[@]} -eq 0 ]; then
    DOMAINS=("lfr-demo.se" "lfr-demo.online")
fi

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

        # 3. Update SPF (TXT Record for root @)
        if [ "${rname}" = "@" ]; then
            spf_content="v=spf1"
            if [ -n "${IPV4}" ]; then
                spf_content="${spf_content} ip4:${IPV4}"
            fi
            if [ -n "${IPV6}" ]; then
                spf_content="${spf_content} ip6:${IPV6}"
            fi
            spf_content="${spf_content} -all"

            rec_resp=$(cf_api "GET" "zones/${zone_id}/dns_records?name=${domain}&type=TXT")
            
            # Extract IDs of all TXT records containing 'v=spf1' (handling quotes robustly)
            rec_ids=($(echo "${rec_resp}" | jq -r '.result[] | select(.content | contains("v=spf1")) | .id // empty'))

            if [ ${#rec_ids[@]} -gt 0 ]; then
                primary_id="${rec_ids[0]}"
                current_spf=$(echo "${rec_resp}" | jq -r ".result[] | select(.id == \"${primary_id}\") | .content")
                
                # Strip leading/trailing double quotes from Cloudflare's returned content
                current_spf=$(echo "${current_spf}" | sed -e 's/^"//' -e 's/"$//')

                if [ "${current_spf}" != "${spf_content}" ]; then
                    echo "[DDNS] Updating SPF TXT record for ${domain}: ${current_spf} -> ${spf_content}"
                    cf_api "PUT" "zones/${zone_id}/dns_records/${primary_id}" "{\"type\":\"TXT\",\"name\":\"${domain}\",\"content\":\"\\\"${spf_content}\\\"\",\"ttl\":120}" > /dev/null
                else
                    echo "[DDNS] SPF record for ${domain} is up to date (${spf_content})."
                fi

                # Delete duplicate SPF records to keep domain configuration clean
                if [ ${#rec_ids[@]} -gt 1 ]; then
                    for ((i=1; i<${#rec_ids[@]}; i++)); do
                        dup_id="${rec_ids[i]}"
                        echo "[DDNS] Deleting duplicate SPF record (ID: ${dup_id}) on ${domain}"
                        cf_api "DELETE" "zones/${zone_id}/dns_records/${dup_id}" > /dev/null
                    done
                fi
            else
                echo "[DDNS] Creating SPF TXT record for ${domain}: ${spf_content}"
                cf_api "POST" "zones/${zone_id}/dns_records" "{\"type\":\"TXT\",\"name\":\"${domain}\",\"content\":\"\\\"${spf_content}\\\"\",\"ttl\":120}" > /dev/null
            fi
        fi
    done
done
