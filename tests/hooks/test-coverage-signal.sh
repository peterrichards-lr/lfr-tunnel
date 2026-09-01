#!/usr/bin/env bash
# test-coverage-signal.sh — tests scripts/check-test-coverage-signal.sh (#1660)
#
# The check is advisory: it always exits 0 and communicates through its output. So the assertions
# are on what it SAYS, not on its status -- asserting the exit code would pass for every input
# and prove nothing.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CHECK="${REPO_ROOT}/scripts/check-test-coverage-signal.sh"

[ -x "$CHECK" ] || { echo "FATAL: $CHECK missing or not executable"; exit 1; }

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

# run <pr-body> <file>... -> output
run() {
  local body="$1"; shift
  printf '%s\n' "$@" | PR_BODY="$body" "$CHECK" 2>&1
}

warns()     { printf '%s' "$1" | grep -q '::warning'; }
says_reason(){ printf '%s' "$1" | grep -q 'a stated reason'; }

echo "Testing the test-coverage signal..."

# 1. The case it exists for.
out=$(run "" "pkg/server/api.go")
if warns "$out"; then
  pass "a Go change with no test warns"
else
  fail "a Go change with no test did not warn -- this is the whole point"
fi

# 2. Satisfied by a test alongside.
out=$(run "" "pkg/server/api.go" "pkg/server/api_test.go")
if warns "$out"; then
  fail "a change with a test alongside still warned -- false positives are how this gets disabled"
else
  pass "a change with a test alongside does not warn"
fi

# 3. UI changes count as functional, and an e2e spec satisfies them.
out=$(run "" "ui/src/pages/AdminUsers.tsx")
if warns "$out"; then pass "a UI change with no test warns"; else fail "a UI change with no test did not warn"; fi
out=$(run "" "ui/src/pages/AdminUsers.tsx" "tests/e2e/ui/tests/users.spec.ts")
if warns "$out"; then fail "a UI change with an e2e spec still warned"; else pass "an e2e spec satisfies a UI change"; fi

# 4. Docs and workflows are not functional -- warning on a README edit would train people to
#    ignore this.
for f in "docs/README.md" "AGENTS.md" ".github/dependabot.yml"; do
  out=$(run "" "$f")
  if warns "$out"; then fail "$f was treated as functional"; else pass "$f is not treated as functional"; fi
done

# 5. The escape hatch, which is where most of the value is.
out=$(run "no-test-needed: pure rename, covered by TestFoo" "pkg/server/api.go")
if says_reason "$out" && ! warns "$out"; then
  pass "a stated reason suppresses the warning"
else
  fail "no-test-needed: with a reason did not suppress the warning"
fi

# 6. ...but a bare marker must NOT. Silencing the check without saying anything is exactly what
#    it exists to prevent.
out=$(run "no-test-needed:" "pkg/server/api.go")
if warns "$out"; then
  pass "a bare 'no-test-needed:' with no reason still warns"
else
  fail "a bare marker silenced the check -- the reason is the point, not the marker"
fi

# 7. A Go test file alone is not a functional change.
out=$(run "" "pkg/server/api_test.go")
if warns "$out"; then
  fail "a test-only change was treated as functional -- _test.go lives under pkg/ and must be excluded"
else
  pass "a test-only change is not functional"
fi

# 8. Advisory: it must never fail a build.
printf '%s\n' "pkg/server/api.go" | PR_BODY="" "$CHECK" >/dev/null 2>&1
rc=$?
if [ "$rc" -eq 0 ]; then
  pass "the check exits 0 even when it warns (advisory by design)"
else
  fail "the check exited $rc -- it must annotate, not block"
fi

echo
echo "  ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
