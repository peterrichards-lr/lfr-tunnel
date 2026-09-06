#!/usr/bin/env bash
# test-css-modifiers.sh — tests scripts/check-css-modifiers.cjs (#1383, #1457, #1744)
#
# The check compares classes USED against rules DEFINED, in both portals. #1744 added the
# Portal V1 pass, after `.alert-warning` styled nothing in V1 for months while the gate that
# exists to catch exactly that looked only at V2.
#
# So most of what is asserted here is that the V1 pass FIRES, not that it passes. A scan
# that reports success because it examined nothing reads as coverage and is none -- the
# lesson #1402 wrote down for the EDR guard, which shipped an `--include` matching no
# pattern and reported a clean run over zero files. Every fire-case below therefore checks
# the exit status AND that the offending class is named, from a tree that differs from the
# real one by exactly one line.
#
# Runs against a throwaway copy of the tree, never the working tree: a test that mutates
# pkg/server to prove a point and then restores it loses on any interrupted run.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CHECK_REL="scripts/check-css-modifiers.cjs"

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
OUT_FILE="$(mktemp "${TMPDIR:-/tmp}/css-modifiers-out.XXXXXX")"
cleanup() {
  [ -n "$SANDBOX" ] && rm -rf "$SANDBOX"
  rm -f "$OUT_FILE"
}
trap cleanup EXIT

# Rebuilds a pristine copy of everything the check reads, so each case starts from a tree
# known to pass and differs from it by one edit.
reset_sandbox() {
  [ -n "$SANDBOX" ] && rm -rf "$SANDBOX"
  SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/css-modifiers-test.XXXXXX")"
  mkdir -p "$SANDBOX/scripts" "$SANDBOX/pkg/server/static" "$SANDBOX/ui"
  cp "${REPO_ROOT}/${CHECK_REL}" "$SANDBOX/scripts/"
  cp "${REPO_ROOT}"/pkg/server/*.html "$SANDBOX/pkg/server/"
  cp -R "${REPO_ROOT}"/pkg/server/static/. "$SANDBOX/pkg/server/static/"
  cp -R "${REPO_ROOT}"/ui/src "$SANDBOX/ui/src"
}

# run -> sets $OUT and $RC. Deliberately not called through a command substitution: that runs in a
# subshell, so the exit status assigned inside it never reaches the caller and every
# fire-case silently asserts against rc=0.
RC=0
OUT=""
run() {
  (cd "$SANDBOX" && node "$CHECK_REL") >"$OUT_FILE" 2>&1
  RC=$?
  OUT="$(cat "$OUT_FILE")"
}

says() { printf '%s' "$1" | grep -q -- "$2"; }

DASHBOARD_HTML="pkg/server/dashboard.html"
DASHBOARD_JS="pkg/server/static/dashboard.js"
DASHBOARD_CSS="pkg/server/static/dashboard.css"

echo "Testing check-css-modifiers..."

# ---------------------------------------------------------------------------
# 1. The tree as committed passes -- and both passes report, so a green run is
#    evidence the V1 pass ran rather than evidence it was skipped.
# ---------------------------------------------------------------------------
reset_sandbox
run
if [ "$RC" -eq 0 ]; then
  pass "the committed tree passes"
else
  fail "the committed tree fails: $OUT"
fi
if says "$OUT" 'check-css-modifiers \[V2\]: OK'; then
  pass "the V2 pass reports"
else
  fail "no V2 result in the output -- $OUT"
fi
if says "$OUT" 'check-css-modifiers \[V1\]: OK'; then
  pass "the V1 pass reports"
else
  fail "no V1 result in the output -- the V1 pass did not run"
fi

# The count in the OK line is the anti-vacuity assertion: a pass that examined a handful of
# classes has not looked at Portal V1, whatever its exit status says.
examined="$(printf '%s' "$OUT" | sed -n 's/.*\[V1\]: OK.*(\([0-9]*\) examined.*/\1/p')"
if [ -n "$examined" ] && [ "$examined" -ge 50 ]; then
  pass "the V1 pass examined ${examined} classes"
else
  fail "the V1 pass examined '${examined}' classes -- too few to have read the portal"
fi

# ---------------------------------------------------------------------------
# 2. It fires on an undefined class in V1 markup.
# ---------------------------------------------------------------------------
reset_sandbox
sed -i.bak 's/<div id="dashboard-screen">/<div id="dashboard-screen" class="probe-undefined-in-html">/' \
  "$SANDBOX/$DASHBOARD_HTML" && rm -f "$SANDBOX/$DASHBOARD_HTML.bak"
run
if [ "$RC" -ne 0 ] && says "$OUT" 'probe-undefined-in-html'; then
  pass "an undefined class in dashboard.html fails and is named"
else
  fail "an undefined class in dashboard.html did not fail (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 3. It fires on an undefined class applied from JS. This is the arm #1744's own bug lived
#    in -- dashboard.js:4071 set `alert alert-warning` with no rule behind it -- so an HTML-
#    only scan would have reported a clean pass over the defect it was filed for.
# ---------------------------------------------------------------------------
reset_sandbox
cat >>"$SANDBOX/$DASHBOARD_JS" <<'PROBE'
function cssModifierProbe(el) {
  el.classList.add('probe-undefined-in-js');
}
PROBE
run
if [ "$RC" -ne 0 ] && says "$OUT" 'probe-undefined-in-js'; then
  pass "an undefined class applied via classList.add fails and is named"
else
  fail "an undefined class in dashboard.js did not fail (rc=$RC): $OUT"
fi

reset_sandbox
cat >>"$SANDBOX/$DASHBOARD_JS" <<'PROBE'
function cssModifierProbeAssign(el) {
  el.className = 'probe-undefined-assigned';
}
PROBE
run
if [ "$RC" -ne 0 ] && says "$OUT" 'probe-undefined-assigned'; then
  pass "an undefined class assigned to .className fails and is named"
else
  fail "an undefined className assignment did not fail (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 4. The regression this issue is about: delete the .alert-warning rule and the check must
#    name it again. Without this, the rule can be dropped and nothing notices.
# ---------------------------------------------------------------------------
reset_sandbox
node -e '
  const fs = require("fs");
  const p = process.argv[1];
  const s = fs.readFileSync(p, "utf8");
  const out = s.replace(/\.alert-warning \{[^}]*\}\n/, "");
  if (out === s) { console.error("could not find the .alert-warning rule"); process.exit(2); }
  fs.writeFileSync(p, out);
' "$SANDBOX/$DASHBOARD_CSS"
if [ $? -ne 0 ]; then
  fail "could not remove the .alert-warning rule for the mutation case"
else
  run
  if [ "$RC" -ne 0 ] && says "$OUT" 'alert-warning'; then
    pass "removing the .alert-warning rule fails and names it"
  else
    fail "removing .alert-warning did not fail (rc=$RC): $OUT"
  fi
  # And the hint points at the siblings it should be written next to.
  if says "$OUT" 'near:.*alert-success'; then
    pass "the failure suggests the sibling rules"
  else
    fail "the failure gave no usable hint: $OUT"
  fi
fi

# ---------------------------------------------------------------------------
# 5. False-positive control. A gate that cries wolf gets switched off, so the two things V1
#    legitimately does with an unstyled class must stay quiet.
# ---------------------------------------------------------------------------

# 5a. A name composed by interpolation is a prefix, not a class. It passes when a defined
#     class starts with that prefix, and fails when none does.
reset_sandbox
cat >>"$SANDBOX/$DASHBOARD_JS" <<'PROBE'
function cssModifierProbeDynamicOk(kind) {
  return `<span class="toast-${kind}"></span>`;
}
PROBE
run
if [ "$RC" -eq 0 ]; then
  pass "a composed name whose prefix matches a defined rule is not reported"
else
  fail "a composed name with a matching prefix was reported: $OUT"
fi

reset_sandbox
cat >>"$SANDBOX/$DASHBOARD_JS" <<'PROBE'
function cssModifierProbeDynamicDead(kind) {
  return `<span class="nothing-defined-here--${kind}"></span>`;
}
PROBE
run
if [ "$RC" -ne 0 ] && says "$OUT" 'nothing-defined-here--'; then
  pass "a composed name matching no defined prefix is reported"
else
  fail "a dead composed-name family was not reported (rc=$RC): $OUT"
fi

# 5b. A class applied and then read back through querySelector is a handle for script, not
#     styling. Demanding a rule for those would mean adding empty ones.
reset_sandbox
cat >>"$SANDBOX/$DASHBOARD_JS" <<'PROBE'
function cssModifierProbeHook(el) {
  el.classList.add('probe-behaviour-hook');
  return document.querySelectorAll('.probe-behaviour-hook');
}
PROBE
run
if [ "$RC" -eq 0 ]; then
  pass "a class applied and queried back is treated as a behaviour hook"
else
  fail "a behaviour hook was reported as an undefined class: $OUT"
fi

# ---------------------------------------------------------------------------
# 6. The exemption list is a ratchet. An entry that no longer matches anything must FAIL,
#    so the list can only shrink -- otherwise a stale entry silently exempts a future
#    regression that happens to reuse the name.
# ---------------------------------------------------------------------------
reset_sandbox
node -e '
  const fs = require("fs");
  const p = process.argv[1];
  const s = fs.readFileSync(p, "utf8");
  const marker = "  const V1_KNOWN_INERT = new Map(\n    Object.entries({\n";
  const at = s.indexOf("V1_KNOWN_INERT = new Map(");
  if (at < 0) { console.error("no V1_KNOWN_INERT"); process.exit(2); }
  const open = s.indexOf("Object.entries({", at) + "Object.entries({".length;
  fs.writeFileSync(p, s.slice(0, open) + "\n    \x27probe-stale-entry\x27: \x27deliberately stale\x27," + s.slice(open));
' "$SANDBOX/scripts/check-css-modifiers.cjs"
run
if [ "$RC" -ne 0 ] && says "$OUT" 'probe-stale-entry'; then
  pass "a stale exemption fails and is named"
else
  fail "a stale exemption did not fail (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 7. The anti-vacuity guard itself. With no V1 documents to read the check must FAIL, not
#    report a clean pass over nothing. This is the failure mode the whole file guards
#    against, so it is asserted directly rather than inferred.
# ---------------------------------------------------------------------------
reset_sandbox
rm -f "$SANDBOX"/pkg/server/*.html "$SANDBOX"/pkg/server/static/*.html
run
if [ "$RC" -ne 0 ] && says "$OUT" 'pass over nothing'; then
  pass "a V1 pass with nothing to read fails instead of reporting success"
else
  fail "an empty V1 tree did not fail (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 8. The V2 pass still works. #1744 restructured the tail of this script to run two passes;
#    a refactor that quietly stops V2 firing would trade one blind spot for another.
# ---------------------------------------------------------------------------
reset_sandbox
target="$(find "$SANDBOX/ui/src" -name '*.tsx' | head -1)"
if [ -z "$target" ]; then
  fail "no .tsx found in ui/src to mutate"
else
  printf '\nexport const CssModifierProbe = () => <div className="probe-undefined-in-v2" />;\n' \
    >>"$target"
  run
  if [ "$RC" -ne 0 ] && says "$OUT" 'probe-undefined-in-v2' && says "$OUT" '\[V2\]'; then
    pass "an undefined class in ui/src still fails, labelled V2"
  else
    fail "the V2 pass stopped firing (rc=$RC): $OUT"
  fi
fi

echo
echo "  ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
