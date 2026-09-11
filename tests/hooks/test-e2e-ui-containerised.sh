#!/usr/bin/env bash
# test-e2e-ui-containerised.sh -- Playwright and chromium must run in a container, never on the
# host (#1858), and the runner image tag must come from the lockfile that governs the install
# (#1863).
#
# Asserted by reading the runner rather than by running it, the same way
# test-build-keeps-tracked-files.sh reads the Makefile: a full `make e2e-ui` takes ~15 minutes and
# needs Docker, so a test that ran one would be skipped in practice and prove nothing. The failure
# this guards against is written at edit time, and reading the recipe catches it there.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
RUNNER="${REPO_ROOT}/scripts/run-e2e-ui.sh"
LOCKFILE="${REPO_ROOT}/tests/e2e/ui/pnpm-lock.yaml"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/e2e-ui-guard.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT INT TERM

echo "Testing that the UI E2E suite runs Playwright in a container"
echo ""

for required in "$RUNNER" "$LOCKFILE"; do
    if [ ! -f "$required" ]; then
        fail "$required is missing -- if it moved, move this guard with it"
        echo ""
        echo "passed: $PASS  failed: $FAIL"
        exit 1
    fi
done

# -- PREMISE. Everything below reads this file; if the read finds nothing the assertions are
#    vacuous rather than satisfied.
if [ -s "$RUNNER" ] && grep -q "playwright" "$RUNNER"; then
    pass "PREMISE   the runner mentions Playwright at all"
else
    fail "PREMISE   nothing about Playwright found in $RUNNER -- the cases below prove nothing"
fi

# -- FIRING. The defect itself: `pnpm exec playwright test` and `playwright install --with-deps`
#    ran on the workstation, downloading chromium into ~/Library/Caches/ms-playwright and
#    executing it there, against the rule that a local E2E belongs in Docker or an LDM box.
if grep -qE '^[[:space:]]*(pnpm|npx|npm)[[:space:]].*playwright' "$RUNNER"; then
    fail "FIRING    Playwright is invoked directly on the host:"
    grep -nE '^[[:space:]]*(pnpm|npx|npm)[[:space:]].*playwright' "$RUNNER" | sed 's/^/            /'
else
    pass "FIRING    no host-side pnpm/npx/npm playwright invocation"
fi

# -- FIRING. Positive half: absence above is satisfied by a runner that does not run Playwright at
#    all, so require that it runs inside `docker run`.
if grep -q "docker run" "$RUNNER" && grep -qE 'docker run|playwright test' "$RUNNER"; then
    pass "FIRING    Playwright is run through 'docker run'"
else
    fail "FIRING    no containerised Playwright invocation found"
fi

# -- FIRING. The browser cache must not be the host's. A bind mount of ~/Library/Caches or
#    PLAYWRIGHT_BROWSERS_PATH pointing at the host would put the binaries back where they were.
# Comment lines are stripped first. The runner quotes Playwright's own error text, which names
# /ms-playwright/..., and matching that would fail the guard on its own documentation -- the
# distinction check-edr-safety.sh draws with is_documentation_prose, for the same reason.
if grep -vE '^[[:space:]]*#' "$RUNNER" | grep -qE "ms-playwright|PLAYWRIGHT_BROWSERS_PATH"; then
    fail "FIRING    the runner references a host browser cache; browsers must come from the image"
else
    pass "FIRING    no host browser cache is mounted or pointed at"
fi

# -- The image tag must be DERIVED from the lockfile, because the image ships browser builds for
#    exactly one library version. A literal is a second source of truth: pinning v1.60.0 from
#    package.json's ^1.60.0 while pnpm installed 1.61.1 made Playwright refuse to launch.
DERIVE_LINE="$(grep -n 'PLAYWRIGHT_VERSION=' "$RUNNER" | head -1 | cut -d: -f2-)"
if [ -n "$DERIVE_LINE" ]; then
    pass "BOUNDING  the image tag is derived, not written as a literal version"
else
    fail "BOUNDING  no PLAYWRIGHT_VERSION derivation found -- a hardcoded tag will drift (#1863)"
fi

# Run the real expression from the runner against the real lockfile, rather than restating it
# here. A copy of the sed would be the same second-source-of-truth mistake one level down.
derive() { # derive <lockfile>
    PLAYWRIGHT_LOCKFILE="$1" bash -c "
        PROJECT_ROOT='$REPO_ROOT'
        $(grep 'PLAYWRIGHT_VERSION=' "$RUNNER" | head -1)
        printf '%s' \"\$PLAYWRIGHT_VERSION\"
    " 2>/dev/null
}

WANT="$(sed -n "s/^  '@playwright\/test@\([0-9][0-9.]*\)':.*/\1/p" "$LOCKFILE" | head -1)"
GOT="$(derive "$LOCKFILE")"
if [ -n "$WANT" ] && [ "$GOT" = "$WANT" ]; then
    pass "FIRING    the derivation reads $WANT out of the real pnpm-lock.yaml"
else
    fail "FIRING    derivation returned '$GOT', lockfile says '$WANT' -- pnpm's format may have changed"
fi

# -- CONTROL. A lockfile the expression cannot parse must yield nothing, so the runner's
#    fail-closed branch fires. Without this case, a derivation that always returned empty would
#    pass every assertion above that only checks the happy path.
printf 'lockfileVersion: 9.0\nsettings:\n  autoInstallPeers: true\n' > "$WORK/garbage-lock.yaml"
if [ -z "$(derive "$WORK/garbage-lock.yaml")" ]; then
    pass "CONTROL   an unparseable lockfile derives nothing, so the runner refuses rather than guesses"
else
    fail "CONTROL   derivation invented a version from a lockfile with no Playwright entry"
fi

# -- FIRING. And the runner must actually refuse on that empty value rather than building
#    `:v` and failing somewhere less legible.
if grep -qE 'if \[ -z "\$PLAYWRIGHT_VERSION" \]' "$RUNNER"; then
    pass "FIRING    the runner fails closed when no version can be derived"
else
    fail "FIRING    no empty-version guard; a bad lockfile would build an image tagged ':v'"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
