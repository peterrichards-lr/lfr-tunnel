#!/bin/sh
# test-make-help-covers-targets.sh -- every invocable make target is discoverable (#1742).
#
# `make help` had drifted to list 13 targets while the Makefile defined 26, and everything it
# omitted was a pre-push gate: check-contexts, check-css, check-contrast, check-i18n,
# test-hooks, compile-check. A gate with no convenient invocation is a gate nobody runs, which
# is the same failure this repo keeps meeting in other forms -- a check that exists and is
# never reached reads as coverage and is none.
#
# Fixing the list once does not stop it drifting again, so this asserts the property instead:
# a target is either documented, or explicitly excluded here with a reason.
set -eu

REPO_ROOT=$(cd "$(dirname "$0")/../.." && pwd)
MAKEFILE="$REPO_ROOT/Makefile"
PASS=0
FAIL=0

pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

# Targets deliberately absent from `make help`, each for a stated reason. Adding a name here is
# a decision to make something undiscoverable, so it should be a conscious edit rather than the
# default outcome of forgetting.
#
#   test-locked - internal; the body of `test`, run while holding the lock. `make test` is the
#                 entry point and running this directly bypasses the serialisation (#1714).
#   edr-guard   - internal; a prerequisite of compile-check, never invoked on its own.
#   ui-dist     - internal; a prerequisite of build, and running it alone produces a
#                 half-built tree rather than anything useful.
EXCLUDED="test-locked edr-guard ui-dist"

targets=$(grep -oE '^[a-z][a-z0-9-]*:' "$MAKEFILE" | tr -d ':' | sort -u)
documented=$(awk '/^help:/,/^$/' "$MAKEFILE" | grep -oE 'make [a-z][a-z0-9-]*' | sed 's/make //' | sort -u)

missing=""
for target in $targets; do
    case " $EXCLUDED " in
        *" $target "*) continue ;;
    esac
    if ! echo "$documented" | grep -qx "$target"; then
        missing="$missing $target"
    fi
done

if [ -z "$missing" ]; then
    pass "every invocable make target appears in 'make help'"
else
    fail "undocumented target(s):$missing -- add to help, or to EXCLUDED here with a reason"
fi

# The reverse direction. A help entry naming a target that no longer exists sends someone to a
# command that errors, which is worse than not mentioning it.
stale=""
for target in $documented; do
    if ! echo "$targets" | grep -qx "$target"; then
        stale="$stale $target"
    fi
done

if [ -z "$stale" ]; then
    pass "'make help' names no target that has been removed"
else
    fail "help documents target(s) that no longer exist:$stale"
fi

# The gates are the reason this test exists, so name them explicitly rather than relying on the
# generic sweep above -- if someone narrows the sweep later, these must still be caught.
for gate in check-contexts check-attribution check-css check-contrast check-i18n test-hooks compile-check; do
    if echo "$documented" | grep -qx "$gate"; then
        pass "gate '$gate' is discoverable"
    else
        fail "gate '$gate' is not in 'make help' -- a gate nobody can find is a gate nobody runs"
    fi
done

# help must actually run. A syntax error in the recipe would make every assertion above pass
# against a target that errors the moment anyone uses it.
if (cd "$REPO_ROOT" && make help >/dev/null 2>&1); then
    pass "'make help' exits cleanly"
else
    fail "'make help' does not run"
fi

printf '\n'
if [ "$FAIL" -eq 0 ]; then
    printf '\033[32mAll %d checks passed\033[0m\n' "$PASS"
else
    printf '\033[31m%d of %d checks failed\033[0m\n' "$FAIL" "$((PASS + FAIL))"
    exit 1
fi
