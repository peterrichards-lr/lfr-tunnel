#!/usr/bin/env bash
set -euo pipefail

# Route53 equivalent of cloudflare-ddns.sh (#858 -- DNS migrated off a personal Cloudflare
# account to a corporate AWS account's Route53). Same job: keep each domain's A/AAAA/SPF
# records pointed at this box's current public IP, run on the same timer cadence. Mirrors
# that script's structure closely so the two stay easy to compare/keep in sync, but the
# actual API calls differ significantly -- Route53's `change-resource-record-sets` UPSERT
# action creates-or-updates in one call (Cloudflare's REST API needs a separate GET-then-
# POST-or-PUT), and Route53 names in list responses are always FQDNs with a trailing dot,
# with a leading "*" returned as the escaped octal form "\052" (accepted as literal "*" on
# write, but never returned that way on read -- verified live against both zones).
#
# IMPORTANT (verified live via `aws route53 list-resource-record-sets`, not carried over
# assumption from cloudflare-ddns.sh): Route53 cannot ALIAS the zone apex ("@") to an
# external domain in a different hosted zone the way Cloudflare's proxy-based CNAME
# flattening could -- both lfr-demo.se's and lfr-demo.online's apexes are their own literal
# A/AAAA records here, not CNAMEs/ALIASes to each other. Cloudflare-ddns.sh's
# LFT_DDNS_CNAME_ALIASED_NAMES skip-list mechanism (for "this name is a CNAME, don't manage
# A/AAAA for it") therefore has nothing to skip on Route53 and isn't carried over.
#
# The two domains genuinely manage a different SET of names, though: lfr-demo.online has no
# wildcard subdomain space at all (no "*.lfr-demo.online" record, and none should be
# created -- only lfr-demo.se hands out auto-generated/reserved tunnel subdomains).
# Configured per-domain via LFT_DDNS_RECORD_NAMES below rather than hardcoded, so this can
# be corrected without editing the script if the DNS design changes again.
CONFIG_FILE="/etc/lfr-tunneld/server-config.yaml"

# Semicolon-separated "domain:name1,name2" pairs -- the exact names managed per domain.
# Defaults reflect the live structure as of the Route53 migration (#858): lfr-demo.se gets
# the full set including the wildcard; lfr-demo.online does not.
LFT_DDNS_RECORD_NAMES="${LFT_DDNS_RECORD_NAMES:-lfr-demo.se:@,*,tunnel,portal;lfr-demo.online:@,tunnel,portal}"

declare -A RECORD_NAMES_BY_DOMAIN=()
IFS=';' read -ra _name_entries <<< "${LFT_DDNS_RECORD_NAMES}"
for _entry in "${_name_entries[@]}"; do
    # Skip empty entries (e.g. a stray/doubled ";" from a hand-edited env var) and entries
    # with no ":" -- assigning an empty-string key to an associative array is a fatal bash
    # error ("bad array subscript"), which would otherwise crash the whole script, halting
    # DNS management for every domain rather than just the misconfigured one.
    if [[ -z "${_entry}" || "${_entry}" != *:* ]]; then
        [[ -n "${_entry}" ]] && echo "[Error] Ignoring malformed LFT_DDNS_RECORD_NAMES entry (missing ':'): ${_entry}" >&2
        continue
    fi
    _entry_domain="${_entry%%:*}"
    _entry_names="${_entry#*:}"
    if [[ -z "${_entry_domain}" || -z "${_entry_names}" ]]; then
        echo "[Error] Ignoring malformed LFT_DDNS_RECORD_NAMES entry (empty domain or names): ${_entry}" >&2
        continue
    fi
    RECORD_NAMES_BY_DOMAIN["${_entry_domain}"]="${_entry_names}"
done
unset _name_entries _entry _entry_domain _entry_names

# SPF includes to append before "-all". Same mechanism as cloudflare-ddns.sh; defaults to
# just amazonses.com since #857 moved outbound mail off the smtp2go.com relay this same env
# var previously also had to include.
LFT_DDNS_SPF_INCLUDES="${LFT_DDNS_SPF_INCLUDES:-amazonses.com}"

# Whether to add "ip4:<box's own IPv4>"/"ip6:<box's own IPv6>" to the SPF record.
# cloudflare-ddns.sh always did this, back when the box sent mail directly via self-hosted
# Postfix. Now that #857 moved outbound mail entirely through Amazon SES, the box's own IP
# no longer sends mail and doesn't need SPF coverage -- confirmed live: the current SPF
# record is exactly "v=spf1 include:amazonses.com -all", no ip4:/ip6: at all. Defaults to
# false to match that; set to "true" only if a direct-send mail path is ever reintroduced.
LFT_DDNS_SPF_INCLUDE_BOX_IP="${LFT_DDNS_SPF_INCLUDE_BOX_IP:-false}"

# Extract dynamic domains list from server-config.yaml if it exists (identical to
# cloudflare-ddns.sh's extraction logic).
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

if [ ${#DOMAINS[@]} -eq 0 ]; then
    DOMAINS=("lfr-demo.se" "lfr-demo.online")
fi

# Detect Public IPv4 and IPv6 the same way cloudflare-ddns.sh does: prefer the local
# interface, fall back to an external echo service.
if ip addr show ens3 >/dev/null 2>&1; then
    IPV4=$(ip -4 addr show ens3 | awk '/inet / {print $2}' | cut -d/ -f1 | grep '178' || ip -4 addr show ens3 | awk '/inet / {print $2}' | cut -d/ -f1 | head -n1 || echo "")
    IPV6=$(ip -6 addr show ens3 scope global | awk '/inet6 / {print $2}' | cut -d/ -f1 | head -n1 || echo "")
else
    IPV4=$(curl -s4 --connect-timeout 5 https://api.ipify.org || echo "")
    IPV6=$(curl -s6 --connect-timeout 5 https://api.ipify.org || echo "")
fi

if [ -z "${IPV4}" ] && [ -z "${IPV6}" ]; then
    echo "[Error] Could not retrieve public IPv4 or IPv6 address." >&2
    exit 1
fi

echo "[DDNS] Detected Public IPs - IPv4: ${IPV4:-N/A}, IPv6: ${IPV6:-N/A}"

# upsert_record submits a single-change UPSERT batch -- Route53's one call creates-or-updates,
# unlike Cloudflare's separate GET-then-POST-or-PUT. rrs_json is the full ResourceRecordSet
# object as a JSON string (caller builds it, since TXT vs A/AAAA shape differs slightly).
# Always uses the literal "*" form for Name (Route53's write API accepts it, even though
# reads never return it that way -- see query_name below).
upsert_record() {
    local zone_id=$1
    local rrs_json=$2
    aws route53 change-resource-record-sets \
        --hosted-zone-id "${zone_id}" \
        --change-batch "{\"Changes\":[{\"Action\":\"UPSERT\",\"ResourceRecordSet\":${rrs_json}}]}" \
        > /dev/null
}

# query_name converts a leading "*" to Route53's escaped-octal form ("\052") for use in a
# list-resource-record-sets --query filter -- list responses always return wildcard names
# this way (confirmed live: lfr-demo.se's wildcard shows up as "\052.lfr-demo.se."), even
# though change-resource-record-sets accepts the literal "*" form fine on write. Querying
# with the literal "*" form would never match, silently treating an existing wildcard
# record as absent every single run.
query_name() {
    local full_rname=$1
    if [[ "${full_rname}" == \** ]]; then
        echo "\\052${full_rname#\*}"
    else
        echo "${full_rname}"
    fi
}

# current_record_value looks up a record set's current value, or empty if it doesn't exist.
current_record_value() {
    local zone_id=$1
    local full_rname=$2
    local rtype=$3
    local q_rname
    q_rname=$(query_name "${full_rname}")
    aws route53 list-resource-record-sets --hosted-zone-id "${zone_id}" \
        --query "ResourceRecordSets[?Name=='${q_rname}.' && Type=='${rtype}'].ResourceRecords[0].Value" \
        --output text 2>/dev/null || echo ""
}

for domain in "${DOMAINS[@]}"; do
    echo "[DDNS] Processing domain: ${domain}"

    record_names_csv="${RECORD_NAMES_BY_DOMAIN[${domain}]:-}"
    if [ -z "${record_names_csv}" ]; then
        echo "[Error] No LFT_DDNS_RECORD_NAMES entry for ${domain} -- skipping (nothing to manage)." >&2
        continue
    fi
    IFS=',' read -ra record_names <<< "${record_names_csv}"

    zone_id=$(aws route53 list-hosted-zones-by-name --dns-name "${domain}" --max-items 1 \
        --query "HostedZones[0].Id" --output text 2>/dev/null || echo "")
    zone_id="${zone_id#/hostedzone/}"

    if [ -z "${zone_id}" ] || [ "${zone_id}" = "None" ]; then
        echo "[Error] Could not find hosted zone for ${domain}" >&2
        continue
    fi

    for rname in "${record_names[@]}"; do
        full_rname="${domain}"
        if [ "${rname}" != "@" ]; then
            full_rname="${rname}.${domain}"
        fi

        # 1. Update IPv4 (A record)
        if [ -n "${IPV4}" ]; then
            current_ip=$(current_record_value "${zone_id}" "${full_rname}" "A")
            if [ "${current_ip}" = "None" ] || [ -z "${current_ip}" ]; then
                echo "[DDNS] Creating A record: ${full_rname} -> ${IPV4}"
                upsert_record "${zone_id}" "{\"Name\":\"${full_rname}\",\"Type\":\"A\",\"TTL\":120,\"ResourceRecords\":[{\"Value\":\"${IPV4}\"}]}"
            elif [ "${current_ip}" != "${IPV4}" ]; then
                echo "[DDNS] Updating A record for ${full_rname}: ${current_ip} -> ${IPV4}"
                upsert_record "${zone_id}" "{\"Name\":\"${full_rname}\",\"Type\":\"A\",\"TTL\":120,\"ResourceRecords\":[{\"Value\":\"${IPV4}\"}]}"
            else
                echo "[DDNS] A record for ${full_rname} is up to date (${IPV4})."
            fi
        fi

        # 2. Update IPv6 (AAAA record)
        if [ -n "${IPV6}" ]; then
            current_ip=$(current_record_value "${zone_id}" "${full_rname}" "AAAA")
            if [ "${current_ip}" = "None" ] || [ -z "${current_ip}" ]; then
                echo "[DDNS] Creating AAAA record: ${full_rname} -> ${IPV6}"
                upsert_record "${zone_id}" "{\"Name\":\"${full_rname}\",\"Type\":\"AAAA\",\"TTL\":120,\"ResourceRecords\":[{\"Value\":\"${IPV6}\"}]}"
            elif [ "${current_ip}" != "${IPV6}" ]; then
                echo "[DDNS] Updating AAAA record for ${full_rname}: ${current_ip} -> ${IPV6}"
                upsert_record "${zone_id}" "{\"Name\":\"${full_rname}\",\"Type\":\"AAAA\",\"TTL\":120,\"ResourceRecords\":[{\"Value\":\"${IPV6}\"}]}"
            else
                echo "[DDNS] AAAA record for ${full_rname} is up to date (${IPV6})."
            fi
        fi

        # 3. Update SPF (TXT record for root @) -- Route53 stores TXT values WITH the
        # surrounding double quotes as part of the string, same DNS wire-format requirement
        # Cloudflare's script already accounts for.
        if [ "${rname}" = "@" ]; then
            spf_content="v=spf1"
            if [ "${LFT_DDNS_SPF_INCLUDE_BOX_IP}" = "true" ]; then
                if [ -n "${IPV4}" ]; then
                    spf_content="${spf_content} ip4:${IPV4}"
                fi
                if [ -n "${IPV6}" ]; then
                    spf_content="${spf_content} ip6:${IPV6}"
                fi
            fi
            read -ra _spf_includes <<< "${LFT_DDNS_SPF_INCLUDES}"
            for _spf_include in "${_spf_includes[@]}"; do
                spf_content="${spf_content} include:${_spf_include}"
            done
            spf_content="${spf_content} -all"

            current_spf=$(current_record_value "${zone_id}" "${domain}" "TXT")
            # Strip the surrounding quotes Route53 returns as part of the value.
            current_spf="${current_spf#\"}"
            current_spf="${current_spf%\"}"

            if [ "${current_spf}" = "None" ] || [ -z "${current_spf}" ]; then
                echo "[DDNS] Creating SPF TXT record for ${domain}: ${spf_content}"
                upsert_record "${zone_id}" "{\"Name\":\"${domain}\",\"Type\":\"TXT\",\"TTL\":120,\"ResourceRecords\":[{\"Value\":\"\\\"${spf_content}\\\"\"}]}"
            elif [ "${current_spf}" != "${spf_content}" ]; then
                echo "[DDNS] Updating SPF TXT record for ${domain}: ${current_spf} -> ${spf_content}"
                upsert_record "${zone_id}" "{\"Name\":\"${domain}\",\"Type\":\"TXT\",\"TTL\":120,\"ResourceRecords\":[{\"Value\":\"\\\"${spf_content}\\\"\"}]}"
            else
                echo "[DDNS] SPF record for ${domain} is up to date (${spf_content})."
            fi
        fi
    done
done
