#!/bin/sh
# Fake DNS hook for the multi-region edge E2E (#1247).
#
# Records every call instead of touching a provider, so the harness can assert what the
# control plane asked for -- that a tunnel held by the edge published a record pointing at the
# edge, and that a planned move republished it at the control plane rather than leaving
# visitors aimed at a node that no longer holds it.
#
# Same contract as scripts/common/lfr-dns-hook-route53.sh:
#   $0 upsert <fqdn> <target>
#   $0 delete <fqdn>
#
# Reading it back: docker-compose exec -T lfr-tunneld-control cat /tmp/dns-hook.log
set -eu

LOG="${LFT_DNS_FAKE_LOG:-/tmp/dns-hook.log}"
echo "$*" >> "$LOG"
echo "recorded: $*"
