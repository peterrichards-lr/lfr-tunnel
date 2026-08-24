#!/bin/bash
set -euo pipefail

# Reference DNS hook for AWS Route53 (#1247).
#
# lfr-tunneld calls a DNS hook when a tunnel starts and stops, so a visitor reaches the
# gateway actually serving that tunnel rather than whichever one the wildcard record points
# at. Since #1285 a tunnel's public name carries no region -- peters.lfr-demo.se is the same
# name wherever it is served from -- so this record is what makes an edge-held tunnel
# reachable, and what keeps the name working when the client moves between gateways.
#
# The Go code knows nothing about any DNS provider; it only knows this contract. Supporting a
# different provider means writing a sibling of this script and pointing dns_hook at it, not
# patching lfr-tunnel (#1015/#1016).
#
# CONTRACT
#
#   $0 upsert <fqdn> <target>   Publish or replace the record for <fqdn> so it resolves to
#                               <target>. Exit 0 on success. Must be idempotent: it is
#                               called again for a name that already points there.
#
#   $0 delete <fqdn>            Withdraw the record. Exit 0 on success, and also when there
#                               is nothing to remove -- the caller may retry, and a name
#                               that is already absent is the desired end state either way.
#
# <target> is a hostname that already resolves to the serving gateway, so a hook is free to
# write a CNAME, an ALIAS, or to resolve it and write an A record -- whichever its provider
# prefers. This one writes a CNAME.
#
# Any non-zero exit is a failure, with an explanation on stderr. lfr-tunneld logs it and
# leaves the record alone; it does not retry, so a persistently failing hook shows up as a
# tunnel that is up but unreachable at its public name.
#
# CONFIGURATION
#
# This is a generic, reusable script -- it carries no default values of its own. Every value
# must be supplied explicitly through the environment, which is the only place that knows the
# right values for a given deployment.
#
#   LFT_DNS_ZONE_ID   Required. The Route53 hosted zone the records live in.
#   LFT_DNS_TTL       Optional, default 60. Deliberately short: this record follows a client
#                     between gateways, and a long TTL would leave visitors pinned to a node
#                     that no longer holds the tunnel for as long as it lasts.
#
# Requires the AWS CLI on PATH, with credentials already available. The credentials need
# route53:ChangeResourceRecordSets and route53:ListResourceRecordSets on this zone and
# nothing else -- do not reuse a general-purpose deployment role here.
#
# NOTE: a specific record beats the `*.<domain>` wildcard, so this coexists with it. When a
# record is withdrawn the wildcard takes over again and the name falls back to the control
# plane's offline page rather than to NXDOMAIN, which is the intended behaviour.

LFT_DNS_ZONE_ID="${LFT_DNS_ZONE_ID:-}"
LFT_DNS_TTL="${LFT_DNS_TTL:-60}"

ACTION="${1:-}"
FQDN="${2:-}"
TARGET="${3:-}"

usage() {
    echo "Usage: $0 upsert <fqdn> <target>" >&2
    echo "       $0 delete <fqdn>" >&2
    exit 64
}

require_zone() {
    if [ -z "$LFT_DNS_ZONE_ID" ]; then
        echo "❌ LFT_DNS_ZONE_ID is not set. This script carries no default -- the hosted zone" >&2
        echo "   is a per-deployment value and must come from the environment." >&2
        exit 78
    fi
}

submit_change() {
    local batch="$1"
    aws route53 change-resource-record-sets \
        --hosted-zone-id "$LFT_DNS_ZONE_ID" \
        --change-batch "$batch" \
        --output text --query 'ChangeInfo.Status'
}

# current_cname prints the existing CNAME value for FQDN, or nothing if there is no CNAME
# record. Used by delete, which has to send back the exact value Route53 holds -- a DELETE
# with the wrong value is rejected, and there is no delete-by-name call.
current_cname() {
    aws route53 list-resource-record-sets \
        --hosted-zone-id "$LFT_DNS_ZONE_ID" \
        --start-record-name "$FQDN" \
        --start-record-type CNAME \
        --max-items 1 \
        --output text \
        --query "ResourceRecordSets[?Name=='${FQDN}.' && Type=='CNAME'].ResourceRecords[0].Value | [0]" \
        2>/dev/null | sed 's/^None$//'
}

case "$ACTION" in
    upsert)
        [ -n "$FQDN" ] && [ -n "$TARGET" ] || usage
        require_zone
        submit_change "{
            \"Comment\": \"lfr-tunnel: tunnel started\",
            \"Changes\": [{
                \"Action\": \"UPSERT\",
                \"ResourceRecordSet\": {
                    \"Name\": \"${FQDN}\",
                    \"Type\": \"CNAME\",
                    \"TTL\": ${LFT_DNS_TTL},
                    \"ResourceRecords\": [{\"Value\": \"${TARGET}\"}]
                }
            }]
        }" > /dev/null
        echo "${FQDN} -> ${TARGET} (CNAME, TTL ${LFT_DNS_TTL})"
        ;;

    delete)
        [ -n "$FQDN" ] || usage
        require_zone
        EXISTING="$(current_cname)"
        if [ -z "$EXISTING" ]; then
            # Already gone. Exit 0: the caller may retry, and absent is the end state it asked
            # for -- failing here would turn a successful outcome into a logged error.
            echo "${FQDN} already absent"
            exit 0
        fi
        submit_change "{
            \"Comment\": \"lfr-tunnel: tunnel stopped\",
            \"Changes\": [{
                \"Action\": \"DELETE\",
                \"ResourceRecordSet\": {
                    \"Name\": \"${FQDN}\",
                    \"Type\": \"CNAME\",
                    \"TTL\": ${LFT_DNS_TTL},
                    \"ResourceRecords\": [{\"Value\": \"${EXISTING}\"}]
                }
            }]
        }" > /dev/null
        echo "${FQDN} withdrawn (was ${EXISTING})"
        ;;

    *)
        usage
        ;;
esac
