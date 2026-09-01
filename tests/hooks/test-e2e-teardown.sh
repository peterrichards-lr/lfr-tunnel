#!/usr/bin/env bash
# test-e2e-teardown.sh — the e2e runners must reap their containers (#1628)
#
# A run used to leave the whole stack up, holding ports 8000, 8025 and 4040. The lfr-tunnel
# container's entrypoint is `sleep infinity`, so nothing reaps it on its own.
#
# The interesting property is not that teardown happens on success -- that always worked. It is
# that it happens on FAILURE and on INTERRUPT, and that the trap is armed BEFORE the stack is
# started rather than just above the test run, which left the build and health-wait window
# uncovered.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
UI_RUNNER="${REPO_ROOT}/scripts/run-e2e-ui.sh"
LOCAL_RUNNER="${REPO_ROOT}/tests/e2e/run-ui.sh"

for f in "$UI_RUNNER" "$LOCAL_RUNNER"; do
  [ -f "$f" ] || { echo "FATAL: $f missing"; exit 1; }
done

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

echo "Testing e2e teardown..."

# 1. Both runners must trap the interrupt signals, not only EXIT.
for f in "$UI_RUNNER" "$LOCAL_RUNNER"; do
  name="$(basename "$f")"
  if grep -qE '^trap cleanup EXIT INT TERM' "$f"; then
    pass "$name traps EXIT, INT and TERM"
  else
    fail "$name does not trap INT/TERM -- Ctrl-C leaks the stack"
  fi
done

# 2. The ordering property, which is the actual regression. A trap registered after the stack
#    starts leaves the build and the health-wait uncovered -- exactly the window a Ctrl-C lands
#    in while someone waits for a slow build.
trap_line=$(grep -nE '^trap cleanup' "$UI_RUNNER" | head -1 | cut -d: -f1)
up_line=$(grep -nE '^docker-?compose (up|build)' "$UI_RUNNER" | head -1 | cut -d: -f1)
if [ -n "$trap_line" ] && [ -n "$up_line" ] && [ "$trap_line" -lt "$up_line" ]; then
  pass "run-e2e-ui.sh arms its trap (line $trap_line) before starting the stack (line $up_line)"
else
  fail "run-e2e-ui.sh arms its trap at ${trap_line:-?} but starts the stack at ${up_line:-?}"
fi

# 3. The exit-code capture. `cmd || true` makes the list succeed and $? read 0, so a failing
#    run would look green -- a plausible-looking "fix" for the set -e problem that silently
#    breaks the failure path instead.
if grep -qE 'TEST_EXIT_CODE=\$\?$' "$LOCAL_RUNNER" && grep -qE '\|\| TEST_EXIT_CODE=\$\?' "$LOCAL_RUNNER"; then
  pass "run-ui.sh captures the playwright exit code without swallowing it"
else
  fail "run-ui.sh no longer captures the test exit code -- a failing run may report success"
fi

if grep -qE 'playwright test" \|\| true' "$LOCAL_RUNNER"; then
  fail "run-ui.sh uses '|| true' after the test run -- \$? will always be 0"
else
  pass "run-ui.sh does not swallow the test status with '|| true'"
fi

# 4. The failure path must not exit without teardown. Previously it printed logs and exited 1
#    directly, leaking the stack on precisely the runs worth investigating.
if awk '/Tests failed. Printing Server Logs/,/^fi/' "$LOCAL_RUNNER" | grep -qE 'docker.*compose down'; then
  fail "run-ui.sh tears down inline on failure -- that races the EXIT trap"
else
  pass "run-ui.sh leaves failure teardown to the EXIT trap"
fi

# 5. The debugging escape hatch has to exist in both, or people will simply stop using the
#    runners when they need to inspect a broken stack.
for f in "$UI_RUNNER" "$LOCAL_RUNNER"; do
  name="$(basename "$f")"
  if grep -q 'E2E_KEEP_STACK' "$f"; then
    pass "$name honours E2E_KEEP_STACK"
  else
    fail "$name has no way to keep the stack for inspection"
  fi
done

# 6. Both must be syntactically valid -- these are edited rarely and by hand.
for f in "$UI_RUNNER" "$LOCAL_RUNNER"; do
  name="$(basename "$f")"
  if bash -n "$f" 2>/dev/null; then
    pass "$name parses"
  else
    fail "$name has a syntax error"
  fi
done

echo
echo "  ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
