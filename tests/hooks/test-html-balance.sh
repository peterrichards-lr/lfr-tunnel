#!/usr/bin/env bash
# test-html-balance.sh — tests scripts/check-html-balance.mjs (#1791)
#
# The gate exists because an unbalanced HTML document is silent everywhere: it is not a parse
# error, *.html is in .prettierignore, and the browser's recovery rules hand you a DIFFERENT
# DOM rather than a complaint. #1785 and #1791 both shipped that way, in files that read
# correctly on their own.
#
# So what is asserted here is mostly that the gate FIRES, and fires FOR THE RIGHT REASON. A
# non-zero exit is shared by every way a Node script can die -- a missing file, a syntax
# error, the wrong cwd -- so every fire-case below checks the exit status AND that the output
# names the offending document and element. That is the #1716 lesson: "exited non-zero" is
# satisfied by failures that never reached the subject.
#
# The false-positive controls matter as much. A gate that reports `<li>` without `</li>`, or a
# `'</div>'` inside a <script> string literal, gets switched off within a week -- and
# dashboard.html contains both.
#
# Runs against a throwaway copy of the tree, never the working tree.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CHECK_REL="scripts/check-html-balance.mjs"

[ -f "${REPO_ROOT}/${CHECK_REL}" ] || {
  echo "FATAL: ${CHECK_REL} missing"
  exit 1
}
command -v node >/dev/null 2>&1 || {
  echo "FATAL: node not on PATH"
  exit 1
}

PASS=0
FAIL=0
pass() {
  printf '  \033[32mPASS\033[0m  %s\n' "$1"
  PASS=$((PASS + 1))
}
fail() {
  printf '  \033[31mFAIL\033[0m  %s\n' "$1"
  FAIL=$((FAIL + 1))
}

SANDBOX=""
OUT_FILE="$(mktemp "${TMPDIR:-/tmp}/html-balance-out.XXXXXX")"
cleanup() {
  [ -n "$SANDBOX" ] && rm -rf "$SANDBOX"
  rm -f "$OUT_FILE"
}
trap cleanup EXIT

# A pristine copy of everything the gate reads, so each case differs from a passing tree by
# exactly one edit. The gate walks the cwd, so the sandbox has to carry the real documents --
# a handful of synthetic ones would not exercise the count floor.
reset_sandbox() {
  [ -n "$SANDBOX" ] && rm -rf "$SANDBOX"
  SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/html-balance-test.XXXXXX")"
  mkdir -p "$SANDBOX/scripts"
  cp "${REPO_ROOT}/${CHECK_REL}" "$SANDBOX/scripts/"
  (cd "$REPO_ROOT" && git ls-files '*.html' '*.htm') | while IFS= read -r rel; do
    mkdir -p "$SANDBOX/$(dirname "$rel")"
    cp "${REPO_ROOT}/$rel" "$SANDBOX/$rel"
  done
}

# run -> sets $OUT and $RC. Deliberately not a command substitution: that runs in a subshell,
# so the exit status assigned inside never reaches the caller and every fire-case would
# silently assert against rc=0.
RC=0
OUT=""
run() {
  (cd "$SANDBOX" && node "$CHECK_REL") >"$OUT_FILE" 2>&1
  RC=$?
  OUT="$(cat "$OUT_FILE")"
}

says() { printf '%s' "$1" | grep -q -- "$2"; }

SERVER_HTML="pkg/server/dashboard.html"
CLIENT_HTML="pkg/client/dashboard.html"

echo "Testing check-html-balance..."

# ---------------------------------------------------------------------------
# 1. The tree as committed passes -- and says how much it read, so a green run is evidence
#    the scan happened rather than evidence it was skipped (#1779).
# ---------------------------------------------------------------------------
reset_sandbox
run
if [ "$RC" -eq 0 ]; then
  pass "the committed tree passes"
else
  fail "the committed tree fails: $OUT"
fi

docs="$(printf '%s' "$OUT" | sed -n 's/.*OK (\([0-9]*\) documents.*/\1/p')"
if [ -n "$docs" ] && [ "$docs" -ge 40 ]; then
  pass "the scan read ${docs} documents"
else
  fail "the scan read '${docs}' documents -- too few to have covered the repo"
fi

elems="$(printf '%s' "$OUT" | sed -n 's/.*documents, \([0-9]*\) elements.*/\1/p')"
if [ -n "$elems" ] && [ "$elems" -ge 500 ]; then
  pass "the scan tokenised ${elems} elements"
else
  fail "the scan tokenised '${elems}' elements -- the documents were opened but not parsed"
fi

# ---------------------------------------------------------------------------
# 2. The #1791 regression itself: remove #dashboard-shell's closing tag and the gate must
#    name the element, not merely exit non-zero.
# ---------------------------------------------------------------------------
reset_sandbox
if ! python3 - "$SANDBOX/$SERVER_HTML" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
old = "    </div><!-- /#dashboard-shell -->\n"
assert old in s, "anchor not found -- the mutation would silently no-op"
out = s.replace(old, "", 1)
assert out != s
open(p, "w").write(out)
PY
then
  fail "could not apply the dropped-closing-tag mutation"
else
  run
  if [ "$RC" -ne 0 ] && says "$OUT" 'dashboard-shell' && says "$OUT" 'unclosed'; then
    pass "a dropped </div> fails and names #dashboard-shell as unclosed"
  else
    fail "a dropped </div> did not fail (rc=$RC): $OUT"
  fi
  if says "$OUT" "$SERVER_HTML"; then
    pass "the failure names the document"
  else
    fail "the failure did not name $SERVER_HTML: $OUT"
  fi
fi

# ---------------------------------------------------------------------------
# 3. The other half of the class, and the arm #1791's own enumeration found: a SURPLUS
#    closing tag. pkg/client/dashboard.html had two, which closed .header-panel early. A
#    checker that only counts unclosed elements reports a clean pass on that file.
# ---------------------------------------------------------------------------
reset_sandbox
if ! python3 - "$SANDBOX/$CLIENT_HTML" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
old = '        </div><!-- /.header-panel -->\n'
assert old in s, "anchor not found -- the mutation would silently no-op"
out = s.replace(old, old + '        </div>\n', 1)
assert out != s
open(p, "w").write(out)
PY
then
  fail "could not apply the surplus-closing-tag mutation"
else
  run
  if [ "$RC" -ne 0 ] && says "$OUT" 'stray-close' && says "$OUT" "$CLIENT_HTML"; then
    pass "a surplus </div> fails and is reported as a stray close"
  else
    fail "a surplus </div> did not fail (rc=$RC): $OUT"
  fi
fi

# ---------------------------------------------------------------------------
# 4. False-positive controls. Both of these are legal HTML that the repo actually contains,
#    and reporting either would make the gate unusable.
# ---------------------------------------------------------------------------

# 4a. Optional end tags. `<li>`, `<p>` and `<tr>` may all be left unclosed.
reset_sandbox
cat >"$SANDBOX/pkg/server/probe-optional.html" <<'HTML'
<!doctype html>
<html><head><title>t</title></head><body>
<ul><li>one<li>two</ul>
<p>a paragraph
<table><tr><td>cell<td>cell</table>
</body></html>
HTML
run
if [ "$RC" -eq 0 ]; then
  pass "unclosed <li>/<p>/<tr> are not reported"
else
  fail "an optional end tag was reported as an imbalance: $OUT"
fi

# 4b. Raw text. dashboard.html's inline <script> writes markup into strings, so a scan that
#     does not skip script content sees closing tags that are not tags at all.
reset_sandbox
cat >"$SANDBOX/pkg/server/probe-rawtext.html" <<'HTML'
<!doctype html>
<html><head><title>t</title></head><body>
<div id="probe-rawtext">
  <script>
    var s = '</div></div><div>';
    if (1 < 2) { document.write(s); }
  </script>
</div>
</body></html>
HTML
run
if [ "$RC" -eq 0 ]; then
  pass "markup inside a <script> string literal is not counted"
else
  fail "script content was parsed as markup: $OUT"
fi

# ---------------------------------------------------------------------------
# 5. The ratchet. Both halves, because only the second one makes it a ratchet rather than a
#    suppression list: an entry must tolerate a real problem, AND fail once it goes stale.
# ---------------------------------------------------------------------------

# 5a. A live entry tolerates the problem it names. Without this the ratchet is inert and
#     5b would pass for the wrong reason (nothing is ever tolerated, so nothing goes stale).
reset_sandbox
if ! python3 - "$SANDBOX/$SERVER_HTML" "$SANDBOX/scripts/check-html-balance.mjs" <<'PY'
import sys
html, gate = sys.argv[1], sys.argv[2]
s = open(html).read()
old = "    </div><!-- /#dashboard-shell -->\n"
assert old in s, "anchor not found"
open(html, "w").write(s.replace(old, "", 1))

g = open(gate).read()
anchor = "const KNOWN_UNBALANCED = Object.freeze({});"
assert anchor in g, "KNOWN_UNBALANCED anchor not found"
entry = (
    "const KNOWN_UNBALANCED = Object.freeze({\n"
    "  'pkg/server/dashboard.html': ['unclosed:div(#dashboard-shell)'],\n"
    "});"
)
open(gate, "w").write(g.replace(anchor, entry, 1))
PY
then
  fail "could not build the live-ratchet case"
else
  run
  if [ "$RC" -eq 0 ] && says "$OUT" '1 ratcheted'; then
    pass "a ratcheted problem is tolerated and counted"
  else
    fail "a ratcheted problem was not tolerated (rc=$RC): $OUT"
  fi
fi

# 5b. The half that matters. Fixing a file without removing its entry must go RED, so the
#     list can only shrink. An exclusion list that tolerates its own staleness silently
#     exempts the next regression that lands in the same file.
reset_sandbox
if ! python3 - "$SANDBOX/scripts/check-html-balance.mjs" <<'PY'
import sys
gate = sys.argv[1]
g = open(gate).read()
anchor = "const KNOWN_UNBALANCED = Object.freeze({});"
assert anchor in g, "KNOWN_UNBALANCED anchor not found"
entry = (
    "const KNOWN_UNBALANCED = Object.freeze({\n"
    "  'pkg/server/probe-stale-ratchet.html': ['unclosed:div(#gone)'],\n"
    "});"
)
open(gate, "w").write(g.replace(anchor, entry, 1))
PY
then
  fail "could not build the stale-ratchet case"
else
  run
  if [ "$RC" -ne 0 ] && says "$OUT" 'stale ratchet entry' && says "$OUT" 'probe-stale-ratchet'; then
    pass "a stale ratchet entry fails and names itself"
  else
    fail "a stale ratchet entry did not fail (rc=$RC): $OUT"
  fi
fi

# ---------------------------------------------------------------------------
# 6. Anti-vacuity (#1779). A scan of nothing must not report success -- the failure mode
#    that made three other gates in this repo narrower than anyone believed.
# ---------------------------------------------------------------------------
reset_sandbox
find "$SANDBOX" -name '*.html' -delete
run
if [ "$RC" -ne 0 ] && says "$OUT" 'pass over nothing'; then
  pass "an empty tree fails instead of reporting success"
else
  fail "an empty tree did not fail (rc=$RC): $OUT"
fi

# The floor has to be reachable, or the assertion above is untestable in the real tree.
reset_sandbox
(cd "$SANDBOX" && LFT_HTML_MIN_FILES=999999 node "$CHECK_REL") >"$OUT_FILE" 2>&1
RC=$?
OUT="$(cat "$OUT_FILE")"
if [ "$RC" -ne 0 ] && says "$OUT" 'pass over nothing'; then
  pass "the document floor is honoured via LFT_HTML_MIN_FILES"
else
  fail "LFT_HTML_MIN_FILES is ignored, so the floor cannot be exercised (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 7. The declared blind spot, asserted rather than only commented (SKILL 5b rule 6). JSX is
#    deliberately out of scope: `pnpm run build` fails on unbalanced JSX already. If someone
#    widens this gate to .tsx, this case goes red and the widening becomes a decision rather
#    than an accident -- and whoever does it has to confirm the duplication is wanted.
# ---------------------------------------------------------------------------
reset_sandbox
mkdir -p "$SANDBOX/ui/src"
cat >"$SANDBOX/ui/src/ProbeUnbalanced.tsx" <<'TSX'
export const ProbeUnbalanced = () => (
  <div className="probe-unbalanced-jsx">
    <span>never closed
  </div>
);
TSX
run
if [ "$RC" -eq 0 ]; then
  pass "JSX is out of scope, as documented (tsc owns it)"
else
  fail "the gate now reads .tsx -- intended? then update the scope comment and this case: $OUT"
fi

echo
echo "  ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
