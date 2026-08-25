#!/usr/bin/env bash
set -uo pipefail

# Distributes renewed certificates from the control plane to every edge (#1302).
#
# Installed on the control plane as a Certbot deploy hook:
#
#   /etc/letsencrypt/renewal-hooks/deploy/50-lfr-distribute-certs
#
# A deploy hook runs only when a certificate actually renewed, which is the correct trigger --
# not every timer tick. It can also be run by hand after a manual renewal.
#
# Why this exists at all: the control plane holds the DNS-write credential and renews the
# wildcards (#1297); the edges renew nothing. Without distribution an edge keeps serving the
# previous certificate until it expires, which is a cliff rather than a degradation.
#
# CONFIGURATION -- no defaults, because which nodes exist is a property of the deployment.
#
#   LFT_EDGE_TARGETS   Required. Space or comma separated "user@host" entries.
#   LFT_CERT_KEY       Required. Private key for the distribution account. root:root 0600.
#   LFT_CERT_DOMAINS   Optional. Which certificates to send; defaults to every directory in
#                      /etc/letsencrypt/live.
#   LFT_POWER_HOOK     Optional. Script matching the power hook contract (pkg/ops/power.go).
#                      Set it and a node that is powered off is started, delivered to, and put
#                      back into the state it was found in. Unset, a sleeping node is skipped
#                      with a warning -- and skipping is not silent, because a node that misses
#                      a renewal is one that will serve an expired certificate later.
#   LFT_POWER_HOOK_ENV Optional. Extra environment for the power hook, e.g. "AWS_REGION=eu-west-1".

LIVE_DIR="${LFT_LIVE_DIR:-/etc/letsencrypt/live}"
TARGETS="${LFT_EDGE_TARGETS:-}"
KEY="${LFT_CERT_KEY:-}"
DOMAINS="${LFT_CERT_DOMAINS:-}"
POWER_HOOK="${LFT_POWER_HOOK:-}"

log()  { echo "[distribute-certs] $*"; }
warn() { echo "[distribute-certs] WARNING: $*" >&2; }
fail() { echo "[distribute-certs] ERROR: $*" >&2; exit 1; }

[ -n "$TARGETS" ] || fail "LFT_EDGE_TARGETS is not set; this script carries no list of its own"
[ -n "$KEY" ] || fail "LFT_CERT_KEY is not set"
[ -r "$KEY" ] || fail "cannot read the distribution key at $KEY"

if [ -z "$DOMAINS" ]; then
    DOMAINS="$(find "$LIVE_DIR" -mindepth 1 -maxdepth 1 -type d -exec basename {} \; 2>/dev/null | tr '\n' ' ')"
fi
[ -n "$DOMAINS" ] || fail "no certificates found under $LIVE_DIR"

BUNDLE="$(mktemp -t lfr-certs-XXXXXX.tgz)"
STAGE="$(mktemp -d -t lfr-certs-XXXXXX)"
cleanup() { rm -rf "$STAGE" "$BUNDLE"; }
trap cleanup EXIT

# --dereference because the live directory is symlinks into archive/, and the far side needs
# the files rather than dangling links.
for domain in $DOMAINS; do
    [ -d "$LIVE_DIR/$domain" ] || { warn "no such certificate: $domain"; continue; }
    mkdir -p "$STAGE/$domain"
    for f in fullchain.pem privkey.pem chain.pem; do
        [ -e "$LIVE_DIR/$domain/$f" ] && cp -L "$LIVE_DIR/$domain/$f" "$STAGE/$domain/$f"
    done
done

tar -czf "$BUNDLE" -C "$STAGE" . || fail "could not build the bundle"
log "bundled: $(echo "$DOMAINS" | tr ' ' '\n' | grep -c . ) certificate(s)"

# Power state is restored to whatever it was found in, whether delivery succeeds or fails --
# the same contract ensureInstanceRunning uses for deploys (pkg/ops/power.go). A renewal that
# leaves an edge running outside its schedule is a bill and a surprise.
power() {
    [ -n "$POWER_HOOK" ] || return 1
    # shellcheck disable=SC2086
    env ${LFT_POWER_HOOK_ENV:-} "$POWER_HOOK" "$@"
}

FAILURES=0
DELIVERED=0

for target in $(echo "$TARGETS" | tr ',' ' '); do
    host="${target#*@}"
    started_it=0

    if ! ssh -o BatchMode=yes -o ConnectTimeout=10 -i "$KEY" "$target" true 2>/dev/null; then
        if [ -n "$POWER_HOOK" ]; then
            state="$(power status "$host" 2>/dev/null | awk '{print $1}')"
            if [ "$state" = "stopped" ]; then
                log "$host is stopped; starting it to deliver"
                if power start "$host" >/dev/null 2>&1; then
                    started_it=1
                else
                    warn "$host: could not be started; it will serve its current certificate until it is next reachable"
                    FAILURES=$((FAILURES + 1))
                    continue
                fi
            fi
        else
            warn "$host: unreachable and no power hook configured; it will keep serving its current certificate"
            FAILURES=$((FAILURES + 1))
            continue
        fi
    fi

    if ssh -o BatchMode=yes -o ConnectTimeout=20 -i "$KEY" "$target" < "$BUNDLE"; then
        log "$host: delivered"
        DELIVERED=$((DELIVERED + 1))
    else
        warn "$host: delivery failed"
        FAILURES=$((FAILURES + 1))
    fi

    if [ "$started_it" -eq 1 ]; then
        log "$host: returning it to the state it was found in"
        power stop "$host" >/dev/null 2>&1 || warn "$host: started for delivery but could not be stopped again"
    fi
done

log "delivered to $DELIVERED node(s), $FAILURES failure(s)"

# Non-zero on any failure. A renewal hook that reports success while a node missed the new
# certificate is the same class of problem as a DDNS updater exiting 0 while doing nothing
# (#1300): the mechanism looks healthy right up until the certificate expires.
[ "$FAILURES" -eq 0 ]
