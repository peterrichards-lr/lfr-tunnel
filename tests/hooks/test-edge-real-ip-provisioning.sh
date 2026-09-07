#!/usr/bin/env bash
# test-edge-real-ip-provisioning.sh — a freshly provisioned edge must render a real_ip block (#1781)
#
# renderRealIPBlock (pkg/ops/nginx.go) returns "" for -role edge when -trusted-proxy is empty, and
# buildRenderConfigFromFlags only resolves the peer-edge set when TrustedProxy != "". So an edge
# provisioned WITHOUT that flag serves with no real_ip block at all: every request the control
# plane or a peer edge forwards to it is attributed to the forwarding gateway rather than to the
# visitor, and that address feeds the per-tunnel IP whitelist, the rate limiter's auto-ban and
# every audit entry (#1450, #1750, #1757, #1767).
#
# THE FAILURE MODE IS SILENT, which is the whole reason this file exists. A config rendered
# without -trusted-proxy is well-formed, passes `nginx -t`, starts, and serves traffic; only the
# attribution is wrong. Nothing in the provisioning path fails, so nothing but an assertion here
# will notice the argument going missing again.
#
# The class, stated once: a provisioning call site that omits an argument whose absence renders a
# valid-looking but wrong config. The two members are the two scripts that call
# render-nginx-config, and they are BOTH checked below -- central's correctness has a different
# cause (its trusted set is derived from -dns-spec unconditionally, #1767, so it must NOT be
# passed -trusted-proxy, which buildRenderConfigFromFlags refuses for that role). Asserting only
# the edge would leave the reason central is fine undocumented and unenforced.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
EDGE_SCRIPT="${REPO_ROOT}/scripts/common/setup-edge-vps.sh"
CENTRAL_SCRIPT="${REPO_ROOT}/scripts/common/setup-central-vps.sh"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

echo "Checking edge provisioning renders a real_ip block..."

# ---------------------------------------------------------------------------
# 1. The argument is actually passed, from a variable rather than hardcoded.
# ---------------------------------------------------------------------------
if grep -q 'RENDER_ARGS=(.*-trusted-proxy "\$TRUSTED_PROXY"' "$EDGE_SCRIPT"; then
  pass "setup-edge-vps.sh passes -trusted-proxy to render-nginx-config"
else
  fail "setup-edge-vps.sh does not pass -trusted-proxy: a provisioned edge would render NO real_ip block, and every forwarded request would be attributed to the forwarding gateway (#1450, #1781)"
fi

if grep -qE '^\s*C\) TRUSTED_PROXY="\$OPTARG" ;;' "$EDGE_SCRIPT" &&
   grep -q 'while getopts "[^"]*C:' "$EDGE_SCRIPT"; then
  pass "setup-edge-vps.sh accepts the control plane's address as -C"
else
  fail "setup-edge-vps.sh no longer parses -C into TRUSTED_PROXY"
fi

# ---------------------------------------------------------------------------
# 2. The peer-edge set is left to its default. Passing -dns-spec explicitly turns a missing
#    fleet spec from a warning into an error, which would break every deployment that is not
#    Liferay's own -- see resolveTrustedPeers in pkg/ops/nginx.go.
# ---------------------------------------------------------------------------
# Comments are stripped first: this file's own rationale for NOT passing the flag mentions it by
# name, and matching that would make the check permanently red.
uncommented() { grep -v '^[[:space:]]*#' "$1"; }

if ! uncommented "$EDGE_SCRIPT" | grep -q -- '-dns-spec'; then
  pass "setup-edge-vps.sh leaves -dns-spec at its default, so a deployment with no fleet spec still provisions"
else
  fail "setup-edge-vps.sh passes -dns-spec: an explicit path makes a missing spec fatal rather than a warning"
fi

# ---------------------------------------------------------------------------
# 3. Central is the other member of the class, and is correct for a DIFFERENT reason: its
#    trusted set comes from -dns-spec unconditionally (#1767). buildRenderConfigFromFlags
#    REFUSES -trusted-proxy for -role central, so passing it there would fail the provision.
# ---------------------------------------------------------------------------
if ! uncommented "$CENTRAL_SCRIPT" | grep -q -- '-trusted-proxy'; then
  pass "setup-central-vps.sh passes no -trusted-proxy, which render-nginx-config refuses for central"
else
  fail "setup-central-vps.sh passes -trusted-proxy: render-nginx-config refuses that flag for -role central and the provision would fail"
fi

if ! uncommented "$CENTRAL_SCRIPT" | grep -q -- '-dns-spec'; then
  pass "setup-central-vps.sh leaves -dns-spec at its default, which is what gives central its edge set"
else
  fail "setup-central-vps.sh passes -dns-spec explicitly, making a missing spec fatal"
fi

# ---------------------------------------------------------------------------
# 4. Exercised, not only grepped. The check has to REFUSE, not warn: a warning during a
#    provision that then succeeds is exactly as silent as no check at all.
#
#    Run from a scratch directory with stubbed ssh/scp/go on PATH. Argument validation happens
#    before the script touches anything, so these invocations do nothing -- and if that ever
#    stops being true, the stubs and the empty working directory mean a regression fails the
#    test rather than reaching a real host.
# ---------------------------------------------------------------------------
SANDBOX="$(mktemp -d)"
trap 'rm -rf "$SANDBOX"' EXIT
mkdir -p "${SANDBOX}/stub"
for stub in ssh scp go certbot; do
  printf '#!/bin/sh\necho "REFUSED: the test stub for %s was invoked" >&2\nexit 97\n' "$stub" > "${SANDBOX}/stub/${stub}"
  chmod +x "${SANDBOX}/stub/${stub}"
done

# Every required argument except the one under test, so a failure can only be about that one.
run_edge() {
  (
    cd "${SANDBOX}" || exit 98
    PATH="${SANDBOX}/stub:${PATH}" bash "$EDGE_SCRIPT" \
      -s 203.0.113.9 -t token -r example.com -i /dev/null -u ubuntu \
      -d edge.example.com -c https://tunnel.example.com -p 8090 -n dns-route53 "$@" 2>&1
  )
}

OUT="$(run_edge)"
RC=$?
if [ "$RC" -ne 0 ]; then
  pass "provisioning without -C exits non-zero (${RC})"
else
  fail "provisioning without -C exited 0: an edge would be provisioned with no real_ip block (#1781)"
fi

# A non-zero exit alone is not enough, and a run without this second assertion reported a false
# PASS when the refusal was deleted: the script simply ran on and died at the first stubbed
# command instead. The refusal has to come BEFORE anything is executed, or a provision gets part
# way -- certificates issued, packages installed -- before failing.
if ! printf '%s' "$OUT" | grep -q 'REFUSED: the test stub'; then
  pass "the refusal happens before any command is run"
else
  fail "the script executed something before refusing: the missing -C was not caught by validation at all:
$OUT"
fi
if printf '%s' "$OUT" | grep -q -- '-C' && printf '%s' "$OUT" | grep -qi 'real_ip'; then
  pass "the refusal names -C and says what it is for"
else
  fail "the refusal does not name -C and real_ip, so an operator cannot tell what is missing:
$OUT"
fi

# A URL in -C is the mistake -c invites. It must be refused rather than quietly rendered: nginx
# matches set_real_ip_from against the peer's address, so a name never matches and the block is
# inert while looking present.
OUT="$(run_edge -C https://tunnel.example.com)"
RC=$?
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -qi 'not a URL'; then
  pass "a URL passed to -C is refused"
else
  fail "a URL passed to -C was accepted (rc=${RC}), which renders an inert real_ip block:
$OUT"
fi

OUT="$(run_edge -C tunnel.example.com)"
RC=$?
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -qi 'IPv4 or IPv6'; then
  pass "a hostname passed to -C is refused"
else
  fail "a hostname passed to -C was accepted (rc=${RC}), which renders an inert real_ip block:
$OUT"
fi

# The positive case, as far as it can be exercised without provisioning anything: a valid
# address must get PAST validation. It cannot go further -- the next step reads
# pkg/config/version.go, which does not exist in the sandbox -- so this asserts only that the
# script stopped complaining about -C, which is the property under test.
for good in 203.0.113.10 2001:db8::1 203.0.113.0/24; do
  OUT="$(run_edge -C "$good")"
  if printf '%s' "$OUT" | grep -q 'Error: -C'; then
    fail "-C rejected a valid address ${good}:
$OUT"
  else
    pass "-C accepts ${good}"
  fi
done

echo
if [ "$FAIL" -gt 0 ]; then
  printf '\033[31m%d failed\033[0m, %d passed\n' "$FAIL" "$PASS"
  exit 1
fi
printf '\033[32mAll %d checks passed\033[0m\n' "$PASS"
