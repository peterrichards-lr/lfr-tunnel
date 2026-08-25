#!/usr/bin/env bash
# scripts/common/route53-ddns-edge.sh
# Keeps a stateless regional edge gateway's own DNS records pointed at its current public IP.
set -euo pipefail

# Route53 equivalent of cloudflare-ddns-edge.sh, and its replacement (#1300). The zones moved
# off Cloudflare to Route53 on 2026-08-11 (#858); the vm6 updater was ported at the time
# (scripts/liferay/vm6/route53-ddns.sh) but this one was not, so every edge carried on asking
# Cloudflare for a zone it no longer serves -- every five minutes, for two weeks, exiting 0
# each time.
#
# Same contract as the script it replaces: this file is shared verbatim by every edge, so it
# must never hardcode one edge's domain. The names it manages come from ddns-domains.txt,
# written by setup-edge-vps.sh from that node's own -d argument.
#
# Credentials come from the instance's IAM role -- nothing is read from disk. The role needs
# route53:ChangeResourceRecordSets and route53:ListResourceRecordSets on the zone, ideally
# narrowed with the route53:ChangeResourceRecordSetsNormalizedRecordNames condition key to
# this edge's own names, plus route53:ListHostedZonesByName on *.
#
# Three defects in the Cloudflare version are fixed here rather than carried across:
#
#  1. It never checked that what it detected was an IP. api.ipify.org sits behind Cloudflare,
#     and when that errors it returns an HTML error body with HTTP 200 -- so `curl` succeeded
#     and the script took "error code: 1200" as this node's address. It would have published
#     that string had the zone lookup not failed first. Detection is now validated, and IMDS
#     is preferred over any third party: it is authoritative for what this instance's address
#     actually is, needs no egress, and cannot rate-limit us.
#  2. A failed domain was skipped with `continue` and the script still exited 0, so systemd
#     recorded success. Failures are now counted and the exit status reflects them.
#  3. It re-published unconditionally where the record already matched on some paths. Route53
#     UPSERT is idempotent, but a no-op change still costs an API call and a change record, so
#     the current value is read first.

DOMAINS_FILE="${LFT_DDNS_DOMAINS_FILE:-/etc/lfr-tunneld/ddns-domains.txt}"
TTL="${LFT_DDNS_TTL:-120}"

if [ ! -f "${DOMAINS_FILE}" ]; then
    echo "[Error] ${DOMAINS_FILE} not found — this edge's own domain(s) were never configured." >&2
    exit 1
fi

FULL_DOMAINS=()
while IFS= read -r line || [ -n "$line" ]; do
    [ -z "$line" ] && continue
    case "$line" in \#*) continue ;; esac
    FULL_DOMAINS+=("$line")
done < "${DOMAINS_FILE}"

if [ ${#FULL_DOMAINS[@]} -eq 0 ]; then
    echo "[Error] ${DOMAINS_FILE} exists but contains no domains." >&2
    exit 1
fi

if ! command -v aws > /dev/null 2>&1; then
    echo "[Error] The AWS CLI is not installed, so this node cannot update Route53." >&2
    exit 1
fi

is_ipv4() { [[ "$1" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; }
is_ipv6() { [[ "$1" =~ ^[0-9a-fA-F:]+$ && "$1" == *:* ]]; }

# detect_ip asks the instance metadata service first -- authoritative, no egress, no rate
# limit -- and falls back to an external echo service only if IMDS is unavailable (this script
# is meant to work on a non-EC2 edge too). Every answer is validated before use.
detect_ip() {
    local family="$1" imds_path="$2" curl_flag="$3" value=""
    local token
    token=$(curl -s --max-time 3 -X PUT \
        -H "X-aws-ec2-metadata-token-ttl-seconds: 60" \
        http://169.254.169.254/latest/api/token 2>/dev/null || true)
    if [ -n "$token" ]; then
        value=$(curl -s --max-time 3 -H "X-aws-ec2-metadata-token: $token" \
            "http://169.254.169.254/latest/meta-data/${imds_path}" 2>/dev/null || true)
    fi
    if ! "is_${family}" "${value:-}"; then
        value=$(curl -s "${curl_flag}" --connect-timeout 5 https://api.ipify.org 2>/dev/null || true)
    fi
    if "is_${family}" "${value:-}"; then
        echo "$value"
    fi
}

IPV4=$(detect_ip ipv4 "public-ipv4" -4)
IPV6=$(detect_ip ipv6 "ipv6" -6)

if [ -z "${IPV4}" ] && [ -z "${IPV6}" ]; then
    echo "[Error] Could not determine a valid public IPv4 or IPv6 address for this node." >&2
    exit 1
fi

echo "[Edge DDNS] Detected Public IPs - IPv4: ${IPV4:-N/A}, IPv6: ${IPV6:-N/A}"

FAILURES=0

# zone_id_for resolves the public hosted zone for a domain. Route53 returns names as FQDNs
# with a trailing dot, and private zones can share a name with a public one, so both are
# matched explicitly rather than taking the first result.
zone_id_for() {
    local zone="$1"
    aws route53 list-hosted-zones-by-name \
        --dns-name "${zone}" \
        --max-items 1 \
        --output text \
        --query "HostedZones[?Name=='${zone}.' && Config.PrivateZone==\`false\`].Id | [0]" \
        2>/dev/null | sed -e 's|^/hostedzone/||' -e 's/^None$//'
}

# current_value reads what a record holds today, so an unchanged address costs no write.
current_value() {
    local zone_id="$1" name="$2" rtype="$3"
    aws route53 list-resource-record-sets \
        --hosted-zone-id "${zone_id}" \
        --start-record-name "${name}" \
        --start-record-type "${rtype}" \
        --max-items 1 \
        --output text \
        --query "ResourceRecordSets[?Name=='${name}.' && Type=='${rtype}'].ResourceRecords[0].Value | [0]" \
        2>/dev/null | sed 's/^None$//'
}

upsert() {
    local zone_id="$1" name="$2" rtype="$3" value="$4"
    local existing
    existing=$(current_value "${zone_id}" "${name}" "${rtype}")

    if [ "${existing}" = "${value}" ]; then
        echo "[Edge DDNS] ${rtype} record for ${name} is up to date (${value})."
        return 0
    fi

    if aws route53 change-resource-record-sets \
        --hosted-zone-id "${zone_id}" \
        --change-batch "{
            \"Comment\": \"lfr-tunnel edge DDNS\",
            \"Changes\": [{
                \"Action\": \"UPSERT\",
                \"ResourceRecordSet\": {
                    \"Name\": \"${name}\",
                    \"Type\": \"${rtype}\",
                    \"TTL\": ${TTL},
                    \"ResourceRecords\": [{\"Value\": \"${value}\"}]
                }
            }]
        }" --output text --query 'ChangeInfo.Status' > /dev/null 2>&1; then
        if [ -n "${existing}" ]; then
            echo "[Edge DDNS] Updated ${rtype} record for ${name}: ${existing} -> ${value}"
        else
            echo "[Edge DDNS] Created ${rtype} record: ${name} -> ${value}"
        fi
        return 0
    fi

    echo "[Error] Failed to upsert ${rtype} record for ${name} -> ${value}" >&2
    return 1
}

for full_domain in "${FULL_DOMAINS[@]}"; do
    # Split "aws-edge-us.lfr-demo.se" into record prefix "aws-edge-us" and zone "lfr-demo.se"
    # -- this edge's own domain, never a shared or hardcoded one.
    record_prefix="${full_domain%%.*}"
    zone="${full_domain#*.}"
    echo "[Edge DDNS] Processing domain: ${zone} (record: ${record_prefix})"

    zone_id=$(zone_id_for "${zone}")
    if [ -z "${zone_id}" ]; then
        echo "[Error] No public Route53 hosted zone found for ${zone}" >&2
        FAILURES=$((FAILURES + 1))
        continue
    fi

    for rname in "${record_prefix}" "*.${record_prefix}"; do
        full_rname="${rname}.${zone}"
        if [ -n "${IPV4}" ]; then
            upsert "${zone_id}" "${full_rname}" "A" "${IPV4}" || FAILURES=$((FAILURES + 1))
        fi
        if [ -n "${IPV6}" ]; then
            upsert "${zone_id}" "${full_rname}" "AAAA" "${IPV6}" || FAILURES=$((FAILURES + 1))
        fi
    done
done

if [ "${FAILURES}" -gt 0 ]; then
    # Non-zero so systemd records a failed unit. The script this replaces exited 0 whatever
    # happened, which is why it went unnoticed for two weeks (#1300).
    echo "[Error] ${FAILURES} DNS update(s) failed." >&2
    exit 1
fi
