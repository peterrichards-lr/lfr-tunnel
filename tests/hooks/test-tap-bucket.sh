#!/usr/bin/env bash
# test-tap-bucket.sh -- the tap/bucket updater must not report success when it did nothing (#1969).
#
# The defect this guards: TAP_BUCKET_PAT expired after v1.48.31 and the release step failed on
# three consecutive releases before anyone noticed, because it runs AFTER the tag, the release
# and the assets exist -- so every outward sign of success was already true.
#
# Worse, an ABSENT credential exited 0 with "skipping cleanly", so an unset secret would publish
# release after release that silently never reached Homebrew or Scoop. That is the same shape as
# #1923, #1938 and #1956: a broken state indistinguishable from a deliberate one.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TAP="${REPO_ROOT}/scripts/update-tap-bucket.sh"
WORKFLOW="${REPO_ROOT}/.github/workflows/tap-bucket.yml"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

echo "Tap/bucket updater cases:"
echo ""

if [ ! -f "$TAP" ]; then
    fail "scripts/update-tap-bucket.sh is missing -- if it moved, move this guard with it"
    echo ""; echo "passed: $PASS  failed: $FAIL"; exit 1
fi

# PREMISE. Without this every case below could pass because the script refuses everything.
if bash -n "$TAP" 2>/dev/null; then
    pass "PREMISE  the script parses"
else
    fail "PREMISE  the script does not even parse"
    echo ""; echo "passed: $PASS  failed: $FAIL"; exit 1
fi

# FIRING. The defect: an absent credential must NOT look like a successful run.
out=$(bash "$TAP" v9.9.9 "" /dev/null --dry-run 2>&1)
rc=$?
if [ "$rc" -ne 0 ]; then
    pass "an absent credential fails (exit $rc) instead of skipping cleanly"
else
    fail "an absent credential exited 0 -- a release would report success having updated nothing"
fi
if printf '%s' "$out" | grep -q "stay on the previous"; then
    pass "the failure says what it costs users, not just that a variable is unset"
else
    fail "the failure message does not explain the consequence"
fi

# The credential check (#1975).
#
# --dry-run used to end with "The credential is valid" having tested nothing. Both target
# repositories are PUBLIC, so `git clone` with a bogus token succeeds and exits 0 -- the
# clone the script inferred validity from could not fail. These cases pin the replacement,
# which asks the API what the token may actually DO.
#
# curl is stubbed so the cases are deterministic and offline: it writes ${STUB_BODY} to the
# path after -o and prints ${STUB_CODE} as the status, which is the shape the script reads.
# git is stubbed to fail, so a token that passes the gate stops at the clone rather than
# reaching the network -- these cases are about the gate, not about cloning.
STUB_DIR="$(mktemp -d)"
trap 'rm -rf "${STUB_DIR}"' EXIT

cat > "${STUB_DIR}/curl" <<'STUB'
#!/usr/bin/env bash
out=""; prev=""
for a in "$@"; do
    [ "$prev" = "-o" ] && out="$a"
    prev="$a"
done
[ -n "$out" ] && printf '%s' "${STUB_BODY:-}" > "$out"
printf '%s' "${STUB_CODE:-200}"
exit 0
STUB
chmod +x "${STUB_DIR}/curl"

cat > "${STUB_DIR}/git" <<'STUB'
#!/usr/bin/env bash
echo "stub git: refusing to reach the network" >&2
exit 1
STUB
chmod +x "${STUB_DIR}/git"

# FIRING. A token that authenticates but cannot write must be refused BEFORE anything is
# rendered -- otherwise the push fails with the release already published.
out=$(PATH="${STUB_DIR}:$PATH" STUB_CODE=200 STUB_BODY='{"permissions":{"push":false,"pull":true}}' \
    bash "$TAP" v9.9.9 fake-pat /dev/null --dry-run 2>&1)
rc=$?
if [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "cannot push to"; then
    pass "a token without write access is refused (exit $rc), not reported as valid"
else
    fail "a read-only token was accepted -- the push would fail after the release is published"
fi

# FIRING. An expired token 401s. This is the exact incident after v1.48.31.
out=$(PATH="${STUB_DIR}:$PATH" STUB_CODE=401 STUB_BODY='{"message":"Bad credentials"}' \
    bash "$TAP" v9.9.9 expired-pat /dev/null --dry-run 2>&1)
rc=$?
if [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "not a valid credential"; then
    pass "an expired credential is detected (exit $rc) instead of cloning anyway"
else
    fail "an expired credential was not detected -- the v1.48.31 incident would repeat silently"
fi

# FIRING. A body with no permissions block means the token was ignored rather than accepted.
# Absent must never read as granted.
out=$(PATH="${STUB_DIR}:$PATH" STUB_CODE=200 STUB_BODY='{"name":"homebrew-tap"}' \
    bash "$TAP" v9.9.9 ignored-pat /dev/null --dry-run 2>&1)
rc=$?
if [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "push permission: absent"; then
    pass "a response with no permissions block is refused, not read as permission granted"
else
    fail "an absent permissions block was treated as permission to push"
fi

# CONTROL. The three cases above must be failing for the RIGHT reason. With push:true the
# script has to get PAST the gate -- if it refused this too, they would pass on a script
# that simply rejects everything.
out=$(PATH="${STUB_DIR}:$PATH" STUB_CODE=200 STUB_BODY='{"permissions":{"push":true,"pull":true}}' \
    bash "$TAP" v9.9.9 good-pat /dev/null --dry-run 2>&1 || true)
if printf '%s' "$out" | grep -q "push permission confirmed on homebrew-tap" \
   && printf '%s' "$out" | grep -q "push permission confirmed on scoop-bucket"; then
    pass "CONTROL  a writable token clears the gate on both repos, so the refusals are specific"
else
    fail "CONTROL  a writable token is also refused -- the cases above prove nothing"
fi

# CONTROL. The gate must run on the REAL path too, not only under --dry-run: a release that
# cannot land its result should fail before it renders one.
out=$(PATH="${STUB_DIR}:$PATH" STUB_CODE=401 STUB_BODY='{"message":"Bad credentials"}' \
    bash "$TAP" v9.9.9 expired-pat /dev/null 2>&1)
rc=$?
if [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "not a valid credential"; then
    pass "CONTROL  the check also guards the real path, not just --dry-run"
else
    fail "CONTROL  a real run skips the credential check"
fi

# BOUNDING. A build with genuinely no tap must still be able to opt out deliberately.
out=$(ALLOW_MISSING_TAP_PAT=true bash "$TAP" v9.9.9 "" /dev/null --dry-run 2>&1)
rc=$?
if [ "$rc" -eq 0 ] && printf '%s' "$out" | grep -q "Skipping deliberately"; then
    pass "BOUNDING  ALLOW_MISSING_TAP_PAT=true still skips, and says it was deliberate"
else
    fail "BOUNDING  the deliberate opt-out no longer works (exit $rc)"
fi

# FIRING. --dry-run must be understood. If the flag were dropped the script would PUSH during
# what a caller believes is a test -- the worst possible regression for a credential check.
if grep -q -- '--dry-run' "$TAP" && grep -q 'DRY_RUN' "$TAP"; then
    pass "the script accepts --dry-run and acts on it"
else
    fail "--dry-run is not handled: a credential test would push for real"
fi
# shellcheck disable=SC2016  # matching the literal text "${DRY_RUN}" in the script, not expanding it
if [ "$(grep -c 'if \[ "${DRY_RUN}" = "true" \]; then' "$TAP")" -ge 2 ]; then
    pass "both repositories honour the dry run, not just the first"
else
    fail "only one repository checks DRY_RUN -- a dry run would still push to the other"
fi

# The recovery path must exist and be dispatchable, or a bad credential again costs a release.
if [ ! -f "$WORKFLOW" ]; then
    fail "the dispatchable tap workflow is missing -- recovery would need a new release again"
else
    pass "a dispatchable tap/bucket workflow exists"
    if grep -q "workflow_dispatch" "$WORKFLOW"; then
        pass "it can be dispatched by hand"
    else
        fail "it cannot be dispatched, which is the whole point"
    fi
    # CONTROL for the read-back: the post-push verification is what stops a silent no-op
    # passing for success a second time.
    if grep -q "still does not advertise" "$WORKFLOW"; then
        pass "CONTROL  it reads the tap back and fails if the version did not land"
    else
        fail "CONTROL  nothing verifies the push had any effect"
    fi
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
