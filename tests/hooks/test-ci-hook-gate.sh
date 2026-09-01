#!/usr/bin/env bash
# test-ci-hook-gate.sh — Hook Tests must run when the Makefile changes (#1643)
#
# `make test-hooks` includes three tests whose subject IS the Makefile:
# test-build-keeps-tracked-files.sh, test-compile-check.sh and test-e2e-teardown.sh. The CI job
# was gated on the `shell` filter, which matches only *.sh and workflow files -- so a
# Makefile-only change skipped exactly the guards that watch the Makefile.
#
# That is how #1632 silenced test-build-keeps-tracked-files.sh: it moved the `rm -rf` and the
# `touch .gitkeep` out of `build:` into a new `ui-dist:` target, the guard found nothing to check
# and went quiet while still exiting 0, and CI said nothing because only the Makefile and Go had
# changed. Found days later, by accident.
#
# This asserts the wiring, because the failure mode is a job that never runs -- there is nothing
# to observe at runtime.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CI="${REPO_ROOT}/.github/workflows/ci.yml"

[ -f "$CI" ] || { echo "FATAL: $CI missing"; exit 1; }

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

echo "Testing the Hook Tests CI gate..."

# 1. The gate itself. `shell` is the wrong filter and is the specific regression.
if grep -qE "if: needs\.changes\.outputs\.hooks == 'true'" "$CI"; then
  pass "hook-tests is gated on the hooks filter"
else
  fail "hook-tests is not gated on 'hooks' -- if it is back on 'shell', a Makefile-only change skips it"
fi

# 2. The filter has to actually match a Makefile change, or the gate above is cosmetic.
HOOKS_PATTERN=$(grep -A 2 'HOOKS=true' "$CI" | grep -oE "grep -qE '[^']+'" | head -1 | sed "s/grep -qE '//;s/'$//")
if [ -z "$HOOKS_PATTERN" ]; then
  HOOKS_PATTERN=$(grep -B 2 'HOOKS=true' "$CI" | grep -oE "grep -qE '[^']+'" | head -1 | sed "s/grep -qE '//;s/'$//")
fi

if [ -z "$HOOKS_PATTERN" ]; then
  fail "could not find the hooks filter pattern in $CI"
else
  for path in "Makefile" "tests/hooks/test-compile-check.sh" "scripts/run-e2e-ui.sh"; do
    if echo "$path" | grep -qE "$HOOKS_PATTERN"; then
      pass "hooks filter matches '$path'"
    else
      fail "hooks filter does NOT match '$path' -- a change there would skip the hook tests"
    fi
  done

  # A path it must NOT match, or the filter is just "always true" and proves nothing.
  if echo "docs/README.md" | grep -qE "$HOOKS_PATTERN"; then
    fail "hooks filter matches docs/README.md -- it is too broad to mean anything"
  else
    pass "hooks filter ignores unrelated paths"
  fi
fi

# 3. The output has to be declared and emitted, or the gate reads an empty string and the job
#    never runs at all -- silently, and in the safe-looking direction.
if grep -qE '^\s+hooks: \$\{\{ steps\.filter\.outputs\.hooks \}\}' "$CI"; then
  pass "the changes job declares a hooks output"
else
  fail "the changes job does not declare 'hooks' -- the gate would always be false"
fi

if grep -qE 'echo "hooks=\$HOOKS"' "$CI"; then
  pass "the hooks output is written to GITHUB_OUTPUT"
else
  fail "hooks is never written to GITHUB_OUTPUT -- the gate would always be false"
fi

# 4. run_everything must set it too, or a force-push or unresolvable base silently skips the
#    hook tests on exactly the runs where least is known about the diff.
# sed with an explicit indent, not awk with \s: POSIX awk has no \s escape, so the range end
# never matched and this read the rest of the file -- passing for the wrong reason.
if sed -n '/run_everything() {/,/^          }/p' "$CI" | grep -q 'hooks=true'; then
  pass "run_everything sets hooks=true"
else
  fail "run_everything does not set hooks=true -- an unresolvable diff would skip the hook tests"
fi

# 5. The premise: these tests really do read the Makefile. If that stops being true this guard
#    is guarding nothing, and should be reconsidered rather than left in place looking useful.
MAKEFILE_READERS=0
for t in test-build-keeps-tracked-files.sh test-compile-check.sh test-e2e-teardown.sh; do
  [ -f "${SCRIPT_DIR}/${t}" ] || continue
  if grep -q 'Makefile' "${SCRIPT_DIR}/${t}"; then
    MAKEFILE_READERS=$((MAKEFILE_READERS + 1))
  fi
done
if [ "$MAKEFILE_READERS" -ge 2 ]; then
  pass "$MAKEFILE_READERS hook tests read the Makefile, so gating on it is warranted"
else
  fail "only $MAKEFILE_READERS hook tests read the Makefile -- re-check whether this gate still earns its place"
fi

echo
echo "  ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
