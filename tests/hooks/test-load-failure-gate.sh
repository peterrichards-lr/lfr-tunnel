#!/usr/bin/env bash
# test-load-failure-gate.sh -- assert check-load-failure-surfaced.cjs actually fires (#1868).
#
# The gate's value is entirely in what it REFUSES, so every case plants a fixture page and
# requires a verdict. The opt-out case exists because the first version of that regex used \s*\S,
# and \s matches newlines -- so a marker with no reason matched the first character of the NEXT
# line and an unreasoned opt-out passed. This file is what stops that coming back.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GATE="${REPO_ROOT}/scripts/check-load-failure-surfaced.cjs"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/load-gate.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT INT TERM

if [ ! -f "$GATE" ]; then
    fail "scripts/check-load-failure-surfaced.cjs is missing -- if it moved, move this guard too"
    echo ""
    echo "passed: $PASS  failed: $FAIL"
    exit 1
fi

# run_case <label> <expected-exit> <catch-body>
run_case() {
    local label="$1" want="$2" body="$3" dir got
    dir="$WORK/$(printf '%s' "$label" | tr -c 'a-zA-Z0-9' '_')"
    mkdir -p "$dir/ui/src/pages" "$dir/scripts"
    cp "$GATE" "$dir/scripts/"

    cat > "$dir/ui/src/pages/Probe.tsx" <<EOF
export default function Probe() {
  const load = async () => {
    try {
      const res = await axios.get('/api/thing');
      setThing(res.data);
    } catch (e) {
$body
    } finally {
      setLoading(false);
    }
  };
  return <div />;
}
EOF

    ( cd "$dir" && node scripts/check-load-failure-surfaced.cjs >/dev/null 2>&1 )
    got=$?
    if [ "$got" -eq "$want" ]; then
        pass "$label (exit $got)"
    else
        fail "$label (expected exit $want, got $got)"
        ( cd "$dir" && node scripts/check-load-failure-surfaced.cjs 2>&1 | sed 's/^/        /' )
    fi
}

echo "Load-failure gate cases:"
echo ""

# PREMISE. Without this, every refusal below could be the gate failing everything.
run_case "a handler that sets state passes" 0 '      console.error(e);
      setLoadError("could not load");'

# FIRING. The defect: log and carry on, so the page renders as if the load worked.
run_case "console.error alone is refused" 1 '      console.error(e);'

# FIRING. Argument shape must not matter -- the original grep missed four sites because it
# looked for console.error(e) exactly, while real code wrote console.error('msg', err).
run_case "console.error with a message is refused" 1 "      console.error('Failed to load thing', e);"

# FIRING. A comment is not handling.
run_case "a comment plus console.error is refused" 1 '      // the server may be down
      console.error(e);'

# BOUNDING. A toast is surfacing it; this gate does not judge how.
run_case "a toast passes" 0 '      showToast("Failed to load", "error");'

# BOUNDING. Rethrowing hands it to a boundary that will surface it.
run_case "a rethrow passes" 0 '      throw e;'

# BOUNDING. The reasoned opt-out, for a catch that is not a page load at all.
run_case "an opt-out WITH a reason passes" 0 '      // load-failure-gate: one malformed frame, not a page load
      console.error(e);'

# FIRING. The opt-out must justify itself. \s*\S matched the next line and let this through.
run_case "an opt-out with NO reason is refused" 1 '      // load-failure-gate:
      console.error(e);'

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
