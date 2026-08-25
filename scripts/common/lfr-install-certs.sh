#!/usr/bin/env bash
set -euo pipefail

# Installs a certificate bundle that has been staged on this edge, and reloads nginx (#1302).
#
# This is the privileged half of certificate distribution, and the only thing the receiving
# account may run as root. It takes NO ARGUMENTS on purpose: there is nothing for a caller to
# manipulate. It reads a fixed staging directory, validates what it finds, and either installs
# it or refuses.
#
#   /etc/sudoers.d/lfr-certsync:
#     certsync ALL=(root) NOPASSWD: /usr/local/bin/lfr-install-certs
#
# The control plane holds the DNS-write credential and renews the wildcards; an edge renews
# nothing (#1297). What arrives here is therefore trusted only as far as these checks go --
# a compromised control plane must not be able to install a certificate for a name this node
# has no business serving, nor roll one back to a revoked predecessor.

STAGING="${LFT_CERT_STAGING:-/var/lib/lfr-certs/staging}"
INSTALL_ROOT="${LFT_CERT_INSTALL_ROOT:-/etc/lfr-tunneld/certs}"
# The domains this node is allowed to receive certificates for. Read from the node's own
# config rather than passed in, so the answer comes from the machine being changed.
CONFIG_FILE="${LFT_SERVER_CONFIG:-/etc/lfr-tunneld/server-config.yaml}"

log()  { echo "[install-certs] $*"; }
fail() { echo "[install-certs] ERROR: $*" >&2; exit 1; }

# Root is required to install, not to validate. Checked at the point privilege is actually
# used, so a non-root run still reports why a bundle would be refused -- which is what makes
# the validation testable, and what lets an operator check a bundle by hand.
[ -d "$STAGING" ] || fail "nothing staged at $STAGING"

# The domains this node serves, from its own config. A certificate for anything else is
# refused: the control plane distributing certs must not be able to install one for a name
# this node was never configured to answer on.
# A while-read loop rather than mapfile: that is a bash 4 builtin, and a script this generic
# should not require it. macOS ships bash 3.2, which is also where it gets tested by hand.
ALLOWED=""
while IFS= read -r d; do
    [ -n "$d" ] && ALLOWED="$ALLOWED $d"
done < <(awk '
    /^domains:/        { in_domains = 1; next }
    /^tunnel_domains:/ { in_domains = 1; next }
    /^[^[:space:]-]/   { in_domains = 0 }
    in_domains && /^[[:space:]]*-[[:space:]]*/ {
        gsub(/^[[:space:]]*-[[:space:]]*"?/, ""); gsub(/"?[[:space:]]*$/, "");
        if ($0 != "") print
    }
' "$CONFIG_FILE" 2>/dev/null | sort -u)

[ -n "$ALLOWED" ] || fail "could not read any domains from $CONFIG_FILE; refusing to install blind"

installed=0

for staged in "$STAGING"/*/; do
    [ -d "$staged" ] || continue
    name="$(basename "$staged")"

    fullchain="$staged/fullchain.pem"
    privkey="$staged/privkey.pem"
    [ -s "$fullchain" ] && [ -s "$privkey" ] || fail "$name: staged bundle is missing fullchain.pem or privkey.pem"

    # 1. The key must match the certificate. A mismatched pair installs cleanly and then fails
    #    every TLS handshake, which is a far worse outcome than refusing here.
    cert_pub="$(openssl x509 -in "$fullchain" -noout -pubkey 2>/dev/null | openssl md5)"
    key_pub="$(openssl pkey -in "$privkey" -pubout 2>/dev/null | openssl md5)"
    [ -n "$cert_pub" ] && [ "$cert_pub" = "$key_pub" ] || fail "$name: private key does not match the certificate"

    # 2. Every name on the certificate must be one this node serves. Checked as a suffix match
    #    so a wildcard counts for the domain it covers.
    sans="$(openssl x509 -in "$fullchain" -noout -ext subjectAltName 2>/dev/null \
            | tr ',' '\n' | sed -n 's/.*DNS://p' | tr -d ' ')"
    [ -n "$sans" ] || fail "$name: certificate has no subjectAltName"

    for san in $sans; do
        bare="${san#\*.}"
        ok=0
        for allowed in $ALLOWED; do
            if [ "$bare" = "$allowed" ] || [ "${bare%.$allowed}" != "$bare" ]; then
                ok=1
                break
            fi
        done
        [ "$ok" -eq 1 ] || fail "$name: certificate covers $san, which this node does not serve"
    done

    # 3. Never go backwards. Installing an older certificate over a newer one is how a
    #    revoked or superseded one comes back, and a distribution mechanism that can roll back
    #    is a distribution mechanism that can be used to.
    live="$INSTALL_ROOT/$name/fullchain.pem"
    if [ -s "$live" ]; then
        new_end="$(openssl x509 -in "$fullchain" -noout -enddate | cut -d= -f2)"
        old_end="$(openssl x509 -in "$live" -noout -enddate | cut -d= -f2)"
        new_epoch="$(date -d "$new_end" +%s 2>/dev/null || date -j -f '%b %d %T %Y %Z' "$new_end" +%s 2>/dev/null || echo 0)"
        old_epoch="$(date -d "$old_end" +%s 2>/dev/null || date -j -f '%b %d %T %Y %Z' "$old_end" +%s 2>/dev/null || echo 0)"
        if [ "$new_epoch" -le "$old_epoch" ]; then
            log "$name: staged certificate is not newer than the installed one ($new_end <= $old_end); skipping"
            continue
        fi
    fi

    [ "$(id -u)" -eq 0 ] || fail "$name: validated, but installing needs root (run via the sudoers entry)"

    # Fixed filenames only, never paths out of the archive.
    install -d -m 700 -o root -g root "$INSTALL_ROOT/$name"
    install -m 644 -o root -g root "$fullchain" "$INSTALL_ROOT/$name/fullchain.pem"
    install -m 600 -o root -g root "$privkey"   "$INSTALL_ROOT/$name/privkey.pem"
    if [ -s "$staged/chain.pem" ]; then
        install -m 644 -o root -g root "$staged/chain.pem" "$INSTALL_ROOT/$name/chain.pem"
    fi
    log "$name: installed, valid to $(openssl x509 -in "$INSTALL_ROOT/$name/fullchain.pem" -noout -enddate | cut -d= -f2)"
    installed=$((installed + 1))
done

rm -rf "${STAGING:?}"/*

if [ "$installed" -eq 0 ]; then
    log "nothing newer to install"
    exit 0
fi

# Test before reloading. A bad config plus a reload takes the node's public traffic down, and
# this runs unattended from a renewal hook where nobody is watching.
nginx -t 2>&1 | sed 's/^/[install-certs] nginx: /'
systemctl reload nginx
log "installed $installed certificate(s) and reloaded nginx"
