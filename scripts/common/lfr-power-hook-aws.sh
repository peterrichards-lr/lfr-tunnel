#!/bin/bash
set -euo pipefail

# Reference power hook for AWS EC2 (#1187).
#
# lfr-tunnel-ops calls a power hook when a deploy needs to start a node that is currently
# stopped -- e.g. an edge caught inside a scheduled power-off window -- and to put it back
# afterwards. The Go code knows nothing about any cloud provider; it only knows this
# contract, so supporting a different provider means writing a sibling of this script and
# pointing power_hook at it, not patching lfr-tunnel.
#
# CONTRACT
#
#   $0 status <host>   Print "<state> [id]" on stdout and exit 0.
#                      state is this provider's own vocabulary. Only "running", "stopped"
#                      and "stopping" are meaningful to the caller; anything else makes it
#                      refuse to guess and stop rather than deploy into a transitional
#                      state. id is optional and used purely to make failure messages
#                      actionable.
#
#   $0 start <host>    Start the machine and do not return until it is running. Exit 0 on
#                      success. (The caller waits for SSH separately -- reaching "running"
#                      is not the same as being able to log in.)
#
#   $0 stop <host>     Request a stop. Exit 0 once the request is accepted; the caller
#                      confirms the state actually changed by polling `status`, so this
#                      does not need to wait for the machine to finish stopping.
#
# Any non-zero exit is a failure, with an explanation on stderr.
#
# CONFIGURATION
#
# This is a generic, reusable script -- it carries no default values of its own. Every
# value must be supplied explicitly by the caller through the environment, which is the
# only place that actually knows the right values for a given deployment (#1015/#1016).
#
#   AWS_REGION        Required. The region the instance lives in, or a comma-separated list
#                     of regions to search. A list matters when one caller serves nodes in
#                     several regions -- certificate distribution sends to every edge from a
#                     single control plane, and those edges are routinely spread across the
#                     world (#1302). Each is tried in turn until the address is found, so a
#                     single region behaves exactly as it always did.
#
#                     Commas, not spaces: callers pass this through as one word of
#                     environment (see lfr-distribute-certs.sh), and a space would split it
#                     into a bogus command rather than a second region.
#   LFT_INSTANCE_TAG  Optional, written Key=Value. Narrows the lookup to resources
#                     carrying that tag. Without it the lookup matches on public address
#                     alone, so a mis-set region or a stale DNS record could in principle
#                     find an unrelated instance that happens to share an address. There
#                     is deliberately no default: a tag value names one particular
#                     deployment. Liferay's own values live in
#                     scripts/liferay/aws/liferay-tags.env, out of this OSS script.
#
# Requires the AWS CLI and jq on PATH, with credentials already available.
#
# NOTE: this looks the instance up by public address, so it relies on that address
# surviving a stop -- an Elastic IP on AWS. A regular auto-assigned public IP is released
# when the instance stops and the lookup would then find nothing.

AWS_REGION="${AWS_REGION:-}"
LFT_INSTANCE_TAG="${LFT_INSTANCE_TAG:-}"

ACTION="${1:-}"
HOST="${2:-}"

usage() {
    echo "Usage: $0 [status|start|stop] <host>" >&2
    exit 64
}

[[ -n "$ACTION" && -n "$HOST" ]] || usage

# Checked before anything is looked up, so a mistyped action fails on the spot rather than
# after a round of API calls.
case "$ACTION" in
    status|start|stop) ;;
    *) usage ;;
esac

if [[ -z "$AWS_REGION" ]]; then
    echo "Error: AWS_REGION must be set." >&2
    exit 78
fi

for tool in aws jq; do
    command -v "$tool" >/dev/null 2>&1 || {
        echo "Error: $tool is required but not on PATH." >&2
        exit 127
    }
done

# Resolve the host to an IPv4 address. AWS's ip-address filter does not match on IPv6.
resolve_ipv4() {
    local host="$1" ip
    # getent is absent on macOS; dig covers both, and a literal IP passes straight through.
    if [[ "$host" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        echo "$host"
        return 0
    fi
    ip="$(dig +short A "$host" | grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' | head -n1 || true)"
    if [[ -z "$ip" ]]; then
        echo "Error: could not resolve $host to an IPv4 address." >&2
        exit 68
    fi
    echo "$ip"
}

# Turn an operator-supplied "Key=Value" into the provider's filter syntax, or nothing at
# all when it is absent or malformed. Ignoring a malformed value is deliberate: the worst
# it can do is widen the search back to matching on address alone, which is exactly what
# happens with no tag, whereas failing a deploy over a mistyped tag would be worse.
tag_filter() {
    local key="${LFT_INSTANCE_TAG%%=*}" value="${LFT_INSTANCE_TAG#*=}"
    if [[ -z "$LFT_INSTANCE_TAG" || "$LFT_INSTANCE_TAG" != *=* || -z "$key" || -z "$value" ]]; then
        return 0
    fi
    echo "Name=tag:${key},Values=${value}"
}

describe_in() {
    local region="$1" ip="$2" filter="$3"

    # One --filters flag taking several values. Repeating the flag would drop all but the
    # last, which would silently widen the search rather than narrow it.
    if [[ -n "$filter" ]]; then
        aws ec2 describe-instances --region "$region" \
            --filters "$filter" "Name=ip-address,Values=${ip}" --output json 2>/dev/null || true
    else
        aws ec2 describe-instances --region "$region" \
            --filters "Name=ip-address,Values=${ip}" --output json 2>/dev/null || true
    fi
}

# Echoes "<region> <id> <state>". Every action needs all three -- start and stop have to name
# the region the instance was actually found in, not the first one searched -- and resolving
# them together keeps a start from describing the same instance twice.
find_instance() {
    local ip filter region json id state
    ip="$(resolve_ipv4 "$HOST")"
    filter="$(tag_filter)"

    for region in $(echo "$AWS_REGION" | tr ',' ' '); do
        json="$(describe_in "$region" "$ip" "$filter")"
        [[ -n "$json" ]] || continue
        id="$(echo "$json" | jq -r '.Reservations[0].Instances[0].InstanceId // empty')"
        state="$(echo "$json" | jq -r '.Reservations[0].Instances[0].State.Name // empty')"
        if [[ -n "$id" ]]; then
            echo "$region $id $state"
            return 0
        fi
    done

    echo "Error: no EC2 instance found for $HOST in: $AWS_REGION -- check the region list is right." >&2
    return 69
}

# Not inside a command substitution in the case below: an `exit` from find_instance would end
# only the subshell there, and the caller would carry on with an empty instance id.
INFO="$(find_instance)" || exit $?

read -r FOUND_REGION FOUND_ID FOUND_STATE <<< "$INFO"

case "$ACTION" in
    status)
        echo "$FOUND_STATE $FOUND_ID"
        ;;
    start)
        aws ec2 start-instances --region "$FOUND_REGION" --instance-ids "$FOUND_ID" >/dev/null
        # Block until it is actually running, per the contract.
        aws ec2 wait instance-running --region "$FOUND_REGION" --instance-ids "$FOUND_ID"
        ;;
    stop)
        # Return as soon as the request is accepted. The caller polls `status` to confirm,
        # so waiting for instance-stopped here would add 30-90s to every deploy teardown
        # for no extra certainty.
        aws ec2 stop-instances --region "$FOUND_REGION" --instance-ids "$FOUND_ID" >/dev/null
        ;;
esac
