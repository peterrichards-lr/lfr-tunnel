#!/usr/bin/env bash
# test-setup-go-check-latest.sh — every actions/setup-go step must set check-latest: true (#2365)
#
# Without it, `go-version: '1.26'` resolves to whichever 1.26.x the runner image has cached.
# On 2026-10-13 that was 1.26.8 while 1.26.9 carried twelve stdlib security fixes: the
# Vulnerability Scan failed on every PR, and release.yml would have built the GitHub release
# with the unpatched stdlib. The rule is per STEP, so a new workflow or a new job that copies an
# old setup-go block is caught.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

# offenders <dir> — print "<file>:<line>" for each setup-go step whose `with:` block lacks
# check-latest: true. A step ends at the next list item (`- `) at or above its own indent.
offenders() {
    local f
    for f in "$1"/*.yml "$1"/*.yaml; do
        [ -f "$f" ] || continue
        awk -v file="$f" '
            function flush() { if (in_step && !ok) print file ":" start; in_step = 0 }
            /^[[:space:]]*-[[:space:]]/ {
                match($0, /^[[:space:]]*/); ind = RLENGTH
                if (in_step && ind <= step_ind) flush()
            }
            /uses:[[:space:]]*actions\/setup-go@/ {
                flush(); in_step = 1; ok = 0; start = NR
                match($0, /^[[:space:]]*/); step_ind = RLENGTH
                if ($0 !~ /^[[:space:]]*-/) step_ind -= 2
                next
            }
            in_step && /^[[:space:]]*check-latest:[[:space:]]*true[[:space:]]*$/ { ok = 1 }
            in_step && /^[[:space:]]*[a-zA-Z_-]+:/ {
                match($0, /^[[:space:]]*/); if (RLENGTH <= step_ind) flush()
            }
            END { flush() }
        ' "$f"
    done
}

count_steps() { cat "$1"/*.yml "$1"/*.yaml 2>/dev/null | grep -cE 'uses:[[:space:]]*actions/setup-go@' || true; }

echo "Testing that every actions/setup-go step sets check-latest: true"
echo ""

WF="$REPO_ROOT/.github/workflows"
n=$(count_steps "$WF")
if [ "$n" -ge 9 ]; then pass "found $n setup-go steps (floor 9), so the check examines the real corpus"
else fail "found only $n setup-go steps, below the floor of 9 -- the check would pass over nothing"; fi

bad=$(offenders "$WF")
if [ -z "$bad" ]; then pass "every setup-go step in .github/workflows sets check-latest: true"
else fail "setup-go step(s) without check-latest: true:"; printf '        %s\n' $bad; fi

# Controls: the check must go red on a step that omits it, in each place it can be omitted.
WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT
cat > "$WORK/a.yml" <<'YML'
jobs:
  build:
    steps:
      - uses: actions/setup-go@v7
        with:
          go-version: '1.26'
          cache: true
      - run: go build ./...
YML
bad=$(offenders "$WORK")
if [ -n "$bad" ]; then pass "CONTROL: a setup-go step without check-latest is caught"
else fail "CONTROL: a setup-go step without check-latest was NOT caught"; fi

cat > "$WORK/a.yml" <<'YML'
jobs:
  build:
    steps:
      - name: Set up Go
        uses: actions/setup-go@v7
        with:
          go-version: '1.26'
      - name: next step
        with:
          check-latest: true
YML
bad=$(offenders "$WORK")
if [ -n "$bad" ]; then pass "CONTROL: check-latest on the NEXT step does not satisfy a named setup-go step"
else fail "CONTROL: check-latest on the next step was credited to the setup-go step"; fi

cat > "$WORK/a.yml" <<'YML'
jobs:
  build:
    steps:
      - name: Set up Go
        uses: actions/setup-go@v7
        with:
          go-version: '1.26'
          check-latest: true
YML
bad=$(offenders "$WORK")
if [ -z "$bad" ]; then pass "a compliant named step passes"
else fail "a compliant named step was refused: $bad"; fi

echo ""
echo "  passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ]
