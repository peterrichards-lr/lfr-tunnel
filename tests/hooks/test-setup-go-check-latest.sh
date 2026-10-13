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

# offenders <dir> — print "<file>:<line>" for each setup-go step that does not set
# check-latest: true UNDER ITS OWN `with:`. Python, not awk: the first awk version credited a
# check-latest anywhere in the step (under env:, say) and could not see a quoted `uses:` at all
# (#2366 review). A step is the list item (`- `) that contains the `uses:` line, up to the next
# non-blank line indented at or left of its dash.
offenders() {
    python3 - "$1" <<'PY'
import glob, os, re, sys
USES = re.compile(r"""^(\s*)(-\s+)?uses:\s*["']?actions/setup-go@""")
ITEM = re.compile(r"^(\s*)-\s")
CL = re.compile(r"""^\s*check-latest:\s*["']?true["']?\s*(#.*)?$""")
FLOW = re.compile(r"""check-latest:\s*["']?true["']?\s*[,}]""")
ind = lambda l: len(l) - len(l.lstrip(" "))
d = sys.argv[1]
for f in sorted(glob.glob(os.path.join(d, "*.yml")) + glob.glob(os.path.join(d, "*.yaml"))):
    lines = open(f, encoding="utf-8").read().splitlines()
    for i, line in enumerate(lines):
        if not USES.match(line):
            continue
        s = i  # walk back to the list item that opens this step
        while s >= 0 and not (ITEM.match(lines[s]) and ind(lines[s]) <= ind(line)):
            s -= 1
        if s < 0:
            print(f"{f}:{i + 1}")
            continue
        dash = ind(lines[s])
        e = s + 1
        while e < len(lines) and (not lines[e].strip() or ind(lines[e]) > dash):
            e += 1
        step = [lines[s][:dash] + "  " + lines[s][dash + 1:].lstrip()] + lines[s + 1:e]
        ok = False
        for j, l in enumerate(step):
            m = re.match(r"^(\s*)with:\s*(.*)$", l)
            if not m or ind(l) != dash + 2:
                continue
            if FLOW.search(m.group(2)):
                ok = True
            for w in step[j + 1:]:
                if w.strip() and ind(w) <= ind(l):
                    break
                if CL.match(w) and ind(w) > ind(l):
                    ok = True
        if not ok:
            print(f"{f}:{i + 1}")
PY
}

count_steps() {
    python3 -c 'import glob,re,sys; print(sum(len(re.findall(r"(?m)^\s*(?:-\s+)?uses:\s*[\"\x27]?actions/setup-go@", open(f).read())) for f in glob.glob(sys.argv[1]+"/*.yml")+glob.glob(sys.argv[1]+"/*.yaml")))' "$1"
}

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

# Controls added after the #2366 review: forms the first parser got wrong.
cat > "$WORK/a.yml" <<'YML'
jobs:
  build:
    steps:
      - name: Set up Go
        uses: "actions/setup-go@v7"
        with:
          go-version: '1.26'
YML
bad=$(offenders "$WORK")
if [ -n "$bad" ]; then pass "CONTROL: a quoted uses: is still seen"
else fail "CONTROL: a quoted uses: made the step invisible"; fi

cat > "$WORK/a.yml" <<'YML'
jobs:
  build:
    steps:
      - name: Set up Go
        uses: actions/setup-go@v7
        env:
          check-latest: true
        with:
          go-version: '1.26'
YML
bad=$(offenders "$WORK")
if [ -n "$bad" ]; then pass "CONTROL: check-latest outside with: is not credited"
else fail "CONTROL: check-latest under env: was credited"; fi

cat > "$WORK/a.yml" <<'YML'
jobs:
  build:
    steps:
      - with:
          go-version: '1.26'
          check-latest: 'true'  # pinned to newest patch
        uses: actions/setup-go@v7
      - uses: actions/setup-go@v7
        with: { go-version: '1.26', check-latest: true }
YML
bad=$(offenders "$WORK")
if [ -z "$bad" ]; then pass "quoted 'true' with a comment, with: before uses:, and a flow mapping all pass"
else fail "valid forms were refused: $bad"; fi

echo ""
echo "  passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ]
