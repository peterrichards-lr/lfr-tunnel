#!/usr/bin/env bash
# test-power-hook-credentials.sh — missing AWS credentials must not read as a wrong region (#1644)
#
# describe_in() runs `aws ec2 describe-instances ... 2>/dev/null || true`, so an auth failure
# returns empty output that is indistinguishable from an empty match. find_instance() then said:
#
#   Error: no EC2 instance found for tunnel.lfr-demo.se in: eu-west-1 -- check the region list is right.
#
# The region list was right. The credentials were missing. That message sends the operator to
# audit AWS_REGION, lfr-tunnel-ops.yaml and the DNS spec, all of which are correct -- and on this
# machine the real fix is one variable, AWS_PROFILE.
#
# Both branches are exercised with a stubbed `aws` on PATH, because the distinction only exists
# in what the two failures say.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
HOOK="${REPO_ROOT}/scripts/common/lfr-power-hook-aws.sh"

[ -f "$HOOK" ] || { echo "FATAL: $HOOK missing"; exit 1; }

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

echo "Testing the AWS power hook's credential reporting..."

STUB_DIR="$(mktemp -d)"
trap 'rm -rf "$STUB_DIR"' EXIT

# `dig`/`host` may be absent, and the hook resolves the host before looking anything up. Stub
# them so the test exercises the credential path rather than DNS availability.
for r in dig host getent; do
    cat > "${STUB_DIR}/${r}" <<'STUB'
#!/bin/sh
echo "203.0.113.10"
STUB
    chmod +x "${STUB_DIR}/${r}"
done

make_aws_stub() {
    cat > "${STUB_DIR}/aws" <<STUB
#!/bin/sh
case "\$1 \$2" in
  "sts get-caller-identity") exit ${1} ;;
esac
# describe-instances: valid JSON, no matches
echo '{"Reservations":[]}'
exit 0
STUB
    chmod +x "${STUB_DIR}/aws"
}

run_hook() {
    PATH="${STUB_DIR}:${PATH}" AWS_REGION="eu-west-1" \
        bash "$HOOK" status tunnel.example.com 2>&1
}

# 1. No credentials: sts fails. The message must name credentials, and must NOT blame regions.
make_aws_stub 255
out="$(run_hook)"; rc=$?

if echo "$out" | grep -qiE 'credential'; then
    pass "a credential failure says so"
else
    fail "a credential failure does not mention credentials. Got: $out"
fi

if echo "$out" | grep -qi 'check the region list'; then
    fail "a credential failure still blames the region list -- this is the bug. Got: $out"
else
    pass "a credential failure does not blame the region list"
fi

if echo "$out" | grep -q 'AWS_PROFILE'; then
    pass "the message names AWS_PROFILE, which is the actual fix on this machine"
else
    fail "the message does not mention AWS_PROFILE. Got: $out"
fi

if [ "$rc" -ne 0 ]; then
    pass "a credential failure is still a failure (exit $rc)"
else
    fail "a credential failure exited 0 -- a deploy would carry on"
fi

# 2. Credentials fine, genuinely no instance: the ORIGINAL message is correct here and must
#    survive. A fix that replaced it everywhere would make the real not-found case misleading
#    in the opposite direction.
make_aws_stub 0
out2="$(run_hook)"; rc2=$?

if echo "$out2" | grep -qi 'no EC2 instance found'; then
    pass "a genuine not-found still reports not-found"
else
    fail "the not-found message was lost. Got: $out2"
fi

if echo "$out2" | grep -qi 'credential'; then
    fail "a genuine not-found now blames credentials -- misleading the other way. Got: $out2"
else
    pass "a genuine not-found does not blame credentials"
fi

if [ "$rc2" -ne 0 ]; then
    pass "a genuine not-found is still a failure (exit $rc2)"
else
    fail "a genuine not-found exited 0"
fi

echo
echo "  ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
