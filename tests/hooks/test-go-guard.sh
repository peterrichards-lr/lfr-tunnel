#!/usr/bin/env bash
# test-go-guard.sh -- assert what scripts/edr-go-wrapper.sh refuses, allows, and pins (#1860).
#
# The wrapper exists because GOTMPDIR, not -o, decides where an unsigned binary first lands
# (#1337), and nothing outside make inherits the Makefile's pin. A guard with no test is the
# thing this repo keeps being bitten by, so every branch is exercised here -- including the
# CONTROL at the end, which mutates the wrapper and requires the refusal case to fail. Without
# it, a wrapper that refused nothing would pass this file in silence.
#
# The real toolchain is replaced with a stub via LFT_GO_REAL, so nothing is compiled and the
# assertions can read exactly what the wrapper would have handed to `go`.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
WRAPPER="${REPO_ROOT}/scripts/edr-go-wrapper.sh"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/go-guard.XXXXXX")"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT INT TERM

if [ ! -x "$WRAPPER" ]; then
    fail "scripts/edr-go-wrapper.sh is missing or not executable -- if it moved, move this test"
    echo ""
    echo "passed: $PASS  failed: $FAIL"
    exit 1
fi

# The stub records what it was handed, so "allowed" can mean "actually reached the toolchain"
# rather than merely "exited 0".
STUB="$WORK/go-stub"
cat >"$STUB" <<'STUBEOF'
#!/bin/sh
echo "ARGS:$*"
echo "GOTMPDIR:${GOTMPDIR:-unset}"
STUBEOF
chmod +x "$STUB"

# A whitelist that exists on every platform. The wrapper's default is /private/tmp on macOS and
# /tmp elsewhere; hardcoding the macOS path made the "quiet" case below fail on Linux, where the
# wrapper correctly REFUSED because /private/tmp cannot be created.
WL="$WORK/whitelist"
mkdir -p "$WL"

# head and grep, and deliberately no `go`. The no-toolchain case below cannot just use
# /usr/bin:/bin -- a CI runner may well have a real go there, and then the search finds one and
# proceeds, which is how that case passed locally and failed on ubuntu.
TOOLS="$WORK/tools"
mkdir -p "$TOOLS"
ln -s "$(command -v head)" "$TOOLS/head" 2>/dev/null || true
ln -s "$(command -v grep)" "$TOOLS/grep" 2>/dev/null || true

echo "Testing the go EDR guard wrapper"
echo ""

run_wrapper() { # run_wrapper <outfile> <args...>
    local out=$1
    shift
    LFT_GO_REAL="$STUB" "$WRAPPER" "$@" >"$out" 2>"$out.err"
    echo $?
}

# -- PREMISE. Without this, every "refused" assertion below could be passing because the wrapper
#    is broken for everything, not because it refuses the right things.
rc=$(run_wrapper "$WORK/premise" build ./...)
if [ "$rc" = 0 ] && grep -q "^ARGS:build ./\.\.\.$" "$WORK/premise"; then
    pass "PREMISE   an allowed command reaches the toolchain with its arguments intact"
else
    fail "PREMISE   'go build' did not reach the stub (rc=$rc) -- the cases below prove nothing"
fi

# -- FIRING. The two forms that execute an unsigned binary from a temp path.
rc=$(run_wrapper "$WORK/baretest" test ./pkg/config/)
if [ "$rc" = 1 ] && ! grep -q "^ARGS:" "$WORK/baretest"; then
    pass "FIRING    bare 'test' is refused and never reaches the toolchain"
else
    fail "FIRING    bare 'test' was not refused (rc=$rc)"
fi

rc=$(run_wrapper "$WORK/gorun" run ./cmd/lfr-tunnel)
if [ "$rc" = 1 ] && ! grep -q "^ARGS:" "$WORK/gorun"; then
    pass "FIRING    'run' is refused and never reaches the toolchain"
else
    fail "FIRING    'run' was not refused (rc=$rc)"
fi

# -- BOUNDING. Makefile:154 is `go test -c -o $(TEST_BINARY)`. Refusing this would break
#    `make test` outright, so the -c form must pass through.
rc=$(run_wrapper "$WORK/testc" test -c -o /private/tmp/x ./pkg/config/)
if [ "$rc" = 0 ] && grep -q "^ARGS:test -c -o /private/tmp/x" "$WORK/testc"; then
    pass "BOUNDING  'test -c' compiles without executing, so it is allowed through"
else
    fail "BOUNDING  'test -c' was blocked (rc=$rc) -- this breaks 'make test'"
fi

# -- FIRING. The pin itself: the whole point of the wrapper.
if grep -q "^GOTMPDIR:/private/tmp$" "$WORK/premise" || grep -q "^GOTMPDIR:/tmp$" "$WORK/premise"; then
    pass "FIRING    GOTMPDIR is pinned to the whitelist for a linking subcommand"
else
    fail "FIRING    GOTMPDIR was not pinned: $(grep '^GOTMPDIR:' "$WORK/premise" || echo missing)"
fi

# -- BOUNDING. Inside make, GOTMPDIR is already correct; the wrapper must then be silent, or
#    every build in the repo grows a line of noise.
LFT_GO_REAL="$STUB" GOTMPDIR="$WL" LFT_TEST_DIR="$WL" \
    "$WRAPPER" build ./... >"$WORK/quiet" 2>"$WORK/quiet.err"
if [ ! -s "$WORK/quiet.err" ]; then
    pass "BOUNDING  an already-correct GOTMPDIR produces no notice"
else
    fail "BOUNDING  wrapper was noisy when nothing needed changing: $(cat "$WORK/quiet.err")"
fi

# -- FIRING. Fail closed when it cannot do its job, rather than linking somewhere unwatched.
LFT_GO_REAL="$STUB" LFT_TEST_DIR=/dev/null/cannot-exist \
    "$WRAPPER" build ./... >"$WORK/nodir" 2>&1
if [ $? -ne 0 ] && grep -q "REFUSED" "$WORK/nodir"; then
    pass "FIRING    an unusable whitelist is refused, not silently bypassed"
else
    fail "FIRING    wrapper proceeded with an unusable whitelist"
fi

# -- FIRING. No toolchain behind the shim: PATH holds only a marked shim, so the search must find
#    nothing and refuse rather than picking itself.
ONLY="$WORK/onlyshim"
mkdir -p "$ONLY"
cp "$WRAPPER" "$ONLY/go"
chmod +x "$ONLY/go"
env -i PATH="$ONLY:$TOOLS" HOME="$HOME" LFT_TEST_DIR="$WL" \
    "$ONLY/go" build ./... >"$WORK/noreal" 2>&1
if [ $? -ne 0 ] && grep -q "no real Go toolchain" "$WORK/noreal"; then
    pass "FIRING    a shim with no toolchain behind it refuses instead of picking itself"
else
    fail "FIRING    wrapper did not refuse with no real toolchain on PATH"
fi

# -- FIRING. The same case on a PATH without coreutils. This hung the suite once: the marker scan
#    shelled out to `head`/`grep` through PATH, found neither, so the shim did not recognise
#    itself, chose itself as the toolchain and exec'd itself forever -- one spinning process,
#    since exec replaces rather than forks. Absolute tool paths fixed it; assert it stays fixed.
env -i PATH="$ONLY" HOME="$HOME" LFT_TEST_DIR="$WL" \
    "$ONLY/go" build ./... >"$WORK/nocoreutils" 2>&1
if [ $? -ne 0 ] && grep -q "REFUSED" "$WORK/nocoreutils"; then
    pass "FIRING    a PATH without coreutils still refuses rather than looping"
else
    fail "FIRING    wrapper did not terminate cleanly on a PATH without coreutils"
fi

# -- FIRING. The depth backstop, which needs no external command and so holds even when the
#    marker scan cannot run. `go generate` legitimately nests, hence a counter not a boolean.
LFT_GO_REAL="$STUB" LFT_GO_GUARD_DEPTH=9 "$WRAPPER" build ./... >"$WORK/depth" 2>&1
if [ $? -ne 0 ] && grep -q "recursion detected" "$WORK/depth"; then
    pass "FIRING    the recursion depth backstop refuses past its limit"
else
    fail "FIRING    depth backstop did not fire at depth 10"
fi

# -- BOUNDING. Shallow nesting must still work, or `go generate` breaks.
rc=$(LFT_GO_REAL="$STUB" LFT_GO_GUARD_DEPTH=1 bash -c '"$0" build ./... >"$1" 2>"$1.err"; echo $?' "$WRAPPER" "$WORK/nested")
if [ "$rc" = 0 ] && grep -q "^ARGS:build" "$WORK/nested"; then
    pass "BOUNDING  shallow nesting is allowed, so 'go generate' still works"
else
    fail "BOUNDING  shallow nesting was refused (rc=$rc) -- this breaks 'go generate'"
fi

# -- CONTROL. Strip the bare-test refusal and require the corresponding case to fail. This is
#    what makes the FIRING results above evidence rather than decoration.
MUTANT="$WORK/mutant-go"
python3 - "$WRAPPER" "$MUTANT" <<'PY'
import sys
src, dst = sys.argv[1], sys.argv[2]
s = open(src).read()
anchor = 'if [ "$has_c" = no ]; then'
assert anchor in s, "anchor not found -- the mutation would silently no-op"
s2 = s.replace(anchor, 'if false; then', 1)
assert s2 != s
open(dst, "w").write(s2)
PY
chmod +x "$MUTANT"
LFT_GO_REAL="$STUB" "$MUTANT" test ./pkg/config/ >"$WORK/mutant.out" 2>&1
if grep -q "^ARGS:test " "$WORK/mutant.out"; then
    pass "CONTROL   with the refusal removed, bare 'test' does reach the toolchain"
else
    fail "CONTROL   mutant behaved identically -- the refusal case proves nothing"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
