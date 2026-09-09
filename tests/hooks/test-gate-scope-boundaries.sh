#!/usr/bin/env bash
# test-gate-scope-boundaries.sh — what each gate does NOT look at, asserted (#1779)
#
# #1842 gave every checker in scripts/ the two universal properties: it refuses to report
# success over nothing, and it exits non-zero on a broken input. Both are properties of a gate's
# behaviour on the corpus it reads. Neither says anything about how big that corpus is.
#
# The corpus is where this repo's gate failures actually live. Every one of them was a checker
# that worked perfectly on a body of source smaller than anyone believed:
#
#   #1744  check-css-modifiers.cjs scanned Portal V2 only. Pointed at V1 it found 25 undefined
#          classes -- including `action-menu-content`, a typo that left the vanity-domain menu
#          permanently open in production.
#   #1774  check-theme-tokens.mjs read stylesheets only. Sixteen inline styles referenced tokens
#          no theme defines.
#   #1773  `platform_sensitive` in ci.yml had no `^pkg/config/`, so Windows reported SUCCESS in
#          five seconds without running, and master went red twice.
#
# In all three the narrowness was WRITTEN DOWN, in the gate's own header comment, before the
# defect landed. Prose does not fail a build (github-workflow SKILL 5b rule 6). This file is
# where the narrowness is stated in a form that does.
#
# EVERY CASE HERE IS LABELLED, because the two kinds prove different things and reporting one as
# the other is the mistake §5c is about:
#
#   FIRING   -- fails against the tree as it was before this file existed. It is evidence that a
#               gate now reads something it did not, or refuses something it accepted.
#   BOUNDING -- passes both before and after, on purpose. It does not fix anything; it pins a
#               deliberate edge, so that WIDENING the gate past it turns this suite red and the
#               widening becomes a decision somebody made rather than a side effect. The model is
#               case 7 of tests/hooks/test-html-balance.sh, which goes red if the HTML gate ever
#               starts reading .tsx.
#
# A bounding case that has gone red is not necessarily a bug. It means someone widened a scan,
# and the right response is to confirm the wider scope is wanted and update the case -- not to
# narrow the gate back.
#
# bash 3.2 compatible (macOS /bin/bash): no associative arrays, no mapfile, no ${var^^}.
# See AGENTS.md, "Shell scripts: bash 3.2".
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

CSS_REL="scripts/check-css-modifiers.cjs"
TOK_REL="scripts/check-theme-tokens.mjs"
I18N_REL="scripts/check-i18n-keys.cjs"

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
# A broken fixture, as distinct from a finding about the subject (§5c.5, and the marker
# tests/hooks/test-pre-push-range.sh uses). Counted as a failure -- a case that could not run
# proves nothing and must not read as green -- but named so nobody goes looking for a real
# defect that is not there.
harness() {
  printf '  \033[33mHARNESS\033[0m  %s\n' "$1"
  FAIL=$((FAIL + 1))
}

for f in "$CSS_REL" "$TOK_REL" "$I18N_REL"; do
  [ -f "${REPO_ROOT}/${f}" ] || {
    echo "FATAL: ${f} missing"
    exit 1
  }
done
command -v node >/dev/null 2>&1 || {
  echo "FATAL: node not on PATH"
  exit 1
}

SANDBOX=""
OUT_FILE="$(mktemp "${TMPDIR:-/tmp}/gate-scope-out.XXXXXX")"
cleanup() {
  [ -n "$SANDBOX" ] && rm -rf "$SANDBOX"
  rm -f "$OUT_FILE"
}
trap cleanup EXIT INT TERM

says() { printf '%s' "$1" | grep -q -- "$2"; }

# ---------------------------------------------------------------------------
# The V1 sandbox: a throwaway copy of everything the two portal gates read, so a case differs
# from a passing tree by exactly one edit and no case can corrupt the working tree.
# ---------------------------------------------------------------------------
reset_v1_sandbox() {
  [ -n "$SANDBOX" ] && rm -rf "$SANDBOX"
  SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/gate-scope.XXXXXX")"
  mkdir -p "$SANDBOX/scripts" "$SANDBOX/pkg/server/static" "$SANDBOX/pkg/client" "$SANDBOX/ui"
  cp "${REPO_ROOT}/${CSS_REL}" "${REPO_ROOT}/${TOK_REL}" "$SANDBOX/scripts/"
  cp -R "${REPO_ROOT}/scripts/lib" "$SANDBOX/scripts/lib"
  cp "${REPO_ROOT}"/pkg/server/*.html "$SANDBOX/pkg/server/"
  cp -R "${REPO_ROOT}"/pkg/server/static/. "$SANDBOX/pkg/server/static/"
  cp -R "${REPO_ROOT}"/pkg/server/templates "$SANDBOX/pkg/server/templates"
  cp "${REPO_ROOT}"/pkg/client/*.html "$SANDBOX/pkg/client/"
  cp -R "${REPO_ROOT}"/ui/src "$SANDBOX/ui/src"
}

RC=0
OUT=""
# run <relative script> -> sets $OUT and $RC.
#
# Not a command substitution: that runs in a subshell, so an exit status assigned inside never
# reaches the caller and every fire-case silently asserts against rc=0.
run() {
  (cd "$SANDBOX" && node "$1") >"$OUT_FILE" 2>&1
  RC=$?
  OUT="$(cat "$OUT_FILE")"
  case "$OUT" in
  # ERR_MODULE_NOT_FOUND (ESM) ends in the same substring as MODULE_NOT_FOUND (CJS), so one
  # pattern matches both. A gate that cannot load its own module exits non-zero having read
  # nothing, which satisfies every fire-case below (§5c.5).
  *MODULE_NOT_FOUND*)
    harness "the sandbox is missing a module the gate needs; every case below would pass on the crash:
$(printf '%s' "$OUT" | head -4)"
    ;;
  esac
}

run_css() { run "$CSS_REL"; }
run_tok() { run "$TOK_REL"; }

CLIENT_HTML="pkg/client/dashboard.html"
SERVER_HTML="pkg/server/blocked.html"

echo "Testing gate scope boundaries (#1779)..."
echo ""
echo "-- check-css-modifiers.cjs / check-theme-tokens.mjs: which web roots are walked"

# ---------------------------------------------------------------------------
# 1. FIRING. pkg/client is INSIDE the corpus.
#
#    pkg/client/dashboard.html is the client inspector: a 1600-line page in the same idiom as
#    Portal V1, one inline <style> block, all of its script inline. Both gates walked pkg/server
#    and stopped there, so nothing had ever compared a class it applies against a rule, or a
#    var() against a definition. Widening the walk found `.input-field` (x3), `.btn`,
#    `.btn-secondary` and `.traffic-header` with no rule, and `var(--text-color)` twice with no
#    fallback -- #1774's defect, in the file #1774 did not read.
#
#    Asserted by planting, not by pointing at those findings: the four classes are ratcheted in
#    V1_KNOWN_INERT and the token is fixed, so a case naming them would go green the day the
#    burndown lands and stop asserting that the file is read at all.
# ---------------------------------------------------------------------------
reset_v1_sandbox
if ! python3 - "$SANDBOX/$CLIENT_HTML" <<'PY'; then
import sys
p = sys.argv[1]
s = open(p).read()
anchor = '<main id="main-view">'
assert anchor in s, "anchor not found -- the mutation would silently no-op"
probe = (
    '<div class="probe-class-in-client-root" '
    'style="color: var(--probe-token-in-client-root)"></div>'
)
open(p, "w").write(s.replace(anchor, anchor + probe, 1))
PY
  harness "could not plant the pkg/client probe"
else
  run_css
  if [ "$RC" -ne 0 ] && says "$OUT" 'probe-class-in-client-root'; then
    pass "FIRING  an undefined class in pkg/client is reported by check-css-modifiers"
  else
    fail "check-css-modifiers did not read pkg/client (rc=$RC): $OUT"
  fi
  run_tok
  if [ "$RC" -ne 0 ] && says "$OUT" 'probe-token-in-client-root'; then
    pass "FIRING  an unresolved token in pkg/client is reported by check-theme-tokens"
  else
    fail "check-theme-tokens did not read pkg/client (rc=$RC): $OUT"
  fi
fi

# ---------------------------------------------------------------------------
# 2. BOUNDING. Everything OUTSIDE pkg/server and pkg/client is not walked.
#
#    Deliberate, and the reason is that a definition source is what makes a finding meaningful:
#    these gates resolve a class against the rules the page LINKS and a token against the theme
#    files or the page's own <style>. An arbitrary .html elsewhere in the tree -- a fixture under
#    tests/, a mock page, a standalone document in resources/ -- has neither, so every class in
#    it would be reported and the gate would be switched off inside a week.
#
#    If a third web root is ever added, this case goes red and whoever adds it has to say so.
# ---------------------------------------------------------------------------
reset_v1_sandbox
mkdir -p "$SANDBOX/resources/server/error_pages"
cat >"$SANDBOX/resources/server/error_pages/probe-outside.html" <<'HTML'
<!doctype html>
<html><head><title>outside the V1 web roots</title></head><body>
<div class="probe-class-outside-v1-roots" style="color: var(--probe-token-outside-v1-roots)">
  neither gate walks this directory
</div>
</body></html>
HTML
run_css
if [ "$RC" -eq 0 ]; then
  pass "BOUNDING  a document outside pkg/server and pkg/client is not read by check-css-modifiers"
else
  fail "check-css-modifiers now reads outside its two web roots -- intended? then update V1_WEB_ROOTS' comment and this case: $OUT"
fi
run_tok
if [ "$RC" -eq 0 ]; then
  pass "BOUNDING  the same document is not read by check-theme-tokens"
else
  fail "check-theme-tokens now reads outside its two web roots -- intended? then update CLIENT_DIR's comment and this case: $OUT"
fi

echo ""
echo "-- scripts/lib/collect-v1-usage.cjs: inline <script> versus <script src>"

# ---------------------------------------------------------------------------
# 3. FIRING. The script-only shapes inside an INLINE <script> are collected.
#
#    Every script-only shape -- `.className =`, `.classList.add()`, and the selector read-back
#    that marks a class as a behaviour hook -- was reached only through a <script src>. A page
#    that keeps its behaviour inline had all of them invisible. That is not a hypothetical shape
#    here: the client inspector is one page with 1600 lines of inline script, and
#    pkg/server/static/setup.html toggles a class the same way.
# ---------------------------------------------------------------------------
reset_v1_sandbox
if ! python3 - "$SANDBOX/$SERVER_HTML" <<'PY'; then
import sys
p = sys.argv[1]
s = open(p).read()
assert "</body>" in s, "anchor not found -- the mutation would silently no-op"
probe = "<script>document.body.classList.add('probe-inline-classlist');</script>\n"
open(p, "w").write(s.replace("</body>", probe + "</body>", 1))
PY
  harness "could not plant the inline-script probe"
else
  run_css
  if [ "$RC" -ne 0 ] && says "$OUT" 'probe-inline-classlist'; then
    pass "FIRING  a class applied by an inline <script> is reported"
  else
    fail "an inline <script>'s classList.add is still invisible (rc=$RC): $OUT"
  fi
fi

# ---------------------------------------------------------------------------
# 4. The false-positive control for case 3, and the half that makes the widening safe rather
#    than merely bigger. A class both APPLIED and READ BACK through a selector is a handle for
#    script, not styling; demanding a rule for one means adding empty rules.
#
#    Collecting the uses without also collecting the hooks would have reported
#    `.access-control-panel` and `.public-urls-panel` in the client inspector, which the page's
#    own inline script queries. Both halves come from the same block, so this is a control, not
#    a separate feature.
# ---------------------------------------------------------------------------
reset_v1_sandbox
if ! python3 - "$SANDBOX/$SERVER_HTML" <<'PY'; then
import sys
p = sys.argv[1]
s = open(p).read()
assert "</body>" in s, "anchor not found -- the mutation would silently no-op"
probe = (
    "<script>document.body.classList.add('probe-inline-hook');"
    "document.querySelector('.probe-inline-hook');</script>\n"
)
open(p, "w").write(s.replace("</body>", probe + "</body>", 1))
PY
  harness "could not plant the inline-hook probe"
else
  run_css
  if [ "$RC" -eq 0 ]; then
    pass "CONTROL   a class an inline <script> reads back is a behaviour hook, not a finding"
  else
    fail "an inline behaviour hook was reported as undefined -- the widening in case 3 needs its hook half: $OUT"
  fi
fi

echo ""
echo "-- check-css-modifiers.cjs: first-party assets a page links but does not have (#1849)"

# ---------------------------------------------------------------------------
# 5. FIRING, both kinds. A page could link a stylesheet or a script that does not exist and
#    nothing failed: the shared pass recorded it as doc.missingAssets, the stylesheet half was
#    printed to stderr WITHOUT setting the failure flag, and the script half was skipped by a
#    `continue`. So `<script src="/static/dashbaord.js">` -- one transposition -- 404s at
#    runtime, every behaviour that file provides is silently absent, and the build is green.
#
#    Two separate code paths, so both directions are asserted; the script half had never fired.
# ---------------------------------------------------------------------------
for kind in stylesheet script; do
  reset_v1_sandbox
  if [ "$kind" = stylesheet ]; then
    TAG='<link rel="stylesheet" href="/static/probe-absent.css">'
  else
    TAG='<script src="/static/probe-absent.js"></script>'
  fi
  if ! python3 - "$SANDBOX/$SERVER_HTML" "$TAG" <<'PY'; then
import sys
p, tag = sys.argv[1], sys.argv[2]
s = open(p).read()
assert "</head>" in s, "anchor not found -- the mutation would silently no-op"
open(p, "w").write(s.replace("</head>", tag + "\n</head>", 1))
PY
    harness "could not plant the missing-$kind probe"
  else
    run_css
    if [ "$RC" -ne 0 ] && says "$OUT" 'probe-absent' && says "$OUT" 'does not exist'; then
      pass "FIRING  a link to a $kind that does not exist fails the build and names the href"
    else
      fail "a missing $kind did not fail (rc=$RC): $OUT"
    fi
  fi
done

# ---------------------------------------------------------------------------
# 6. BOUNDING. Third-party and inline assets are NOT first-party and must never be reported.
#
#    A CDN href cannot be checked from here -- the file is not in this repository -- and
#    reporting one would fail every page that loads chart.js. Deliberate, and the boundary is
#    resolveAsset's: a scheme-relative or absolute URL and a data: URI return null before the
#    existence check is reached.
# ---------------------------------------------------------------------------
reset_v1_sandbox
if ! python3 - "$SANDBOX/$SERVER_HTML" <<'PY'; then
import sys
p = sys.argv[1]
s = open(p).read()
assert "</head>" in s, "anchor not found -- the mutation would silently no-op"
tags = (
    '<script src="https://cdn.example.invalid/probe-third-party.js"></script>\n'
    '<script src="//cdn.example.invalid/probe-scheme-relative.js"></script>\n'
    '<link rel="stylesheet" href="data:text/css,body%7B%7D">\n'
)
open(p, "w").write(s.replace("</head>", tags + "</head>", 1))
PY
  harness "could not plant the third-party asset probe"
else
  run_css
  if [ "$RC" -eq 0 ]; then
    pass "BOUNDING  a CDN href, a scheme-relative URL and a data: URI are not checked for existence"
  else
    fail "a third-party or inline asset was reported as missing -- this would fail every page loading chart.js: $OUT"
  fi
fi

echo ""
echo "-- check-i18n-keys.cjs: which documents, and which locales"

# ---------------------------------------------------------------------------
# The i18n sandbox needs a real git repository: the gate asks `git ls-files` both for the Go
# sources it scans and for the documents it holds its own scan list to account for.
# ---------------------------------------------------------------------------
reset_i18n_sandbox() {
  [ -n "$SANDBOX" ] && rm -rf "$SANDBOX"
  SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/gate-scope-i18n.XXXXXX")"
  mkdir -p "$SANDBOX/scripts" "$SANDBOX/pkg/server/static" "$SANDBOX/pkg/client" "$SANDBOX/ui"
  cp "${REPO_ROOT}/${I18N_REL}" "$SANDBOX/scripts/"
  cp -R "${REPO_ROOT}/pkg/server/i18n" "$SANDBOX/pkg/server/i18n"
  cp "${REPO_ROOT}/pkg/server/i18n.go" "$SANDBOX/pkg/server/"
  cp "${REPO_ROOT}/pkg/server/dashboard.html" "$SANDBOX/pkg/server/"
  cp "${REPO_ROOT}/pkg/server/static/setup.html" \
    "${REPO_ROOT}/pkg/server/static/dashboard.js" \
    "${REPO_ROOT}/pkg/server/static/offline.html" \
    "${REPO_ROOT}/pkg/server/static/maintenance.html" "$SANDBOX/pkg/server/static/"
  cp "${REPO_ROOT}/pkg/client/dashboard.html" "$SANDBOX/pkg/client/"
  cp -R "${REPO_ROOT}/ui/src" "$SANDBOX/ui/src"
  (
    cd "$SANDBOX" || exit 1
    git init -q .
    git add -A
  ) >/dev/null 2>&1
}

run_i18n() { run "$I18N_REL"; }

reset_i18n_sandbox
run_i18n
if [ "$RC" -eq 0 ]; then
  pass "PREMISE   the i18n sandbox reproduces a passing run"
else
  harness "the i18n sandbox does not pass as built; every case below would fail for that reason:
$(printf '%s' "$OUT" | head -6)"
fi

# ---------------------------------------------------------------------------
# 7. FIRING. Every tracked document using data-i18n must be accounted for.
#
#    The gate checks TWO named files against Language.properties. Everything else using the
#    attribute is out of scope because it carries its own inline bundle -- correct, and stated
#    only in a header comment. Nothing made the list answer for the tree, and
#    pkg/client/dashboard.html had used the attribute for 40 keys since it was written while
#    appearing in no list and no comment. It could have been either kind and nobody was asked.
# ---------------------------------------------------------------------------
reset_i18n_sandbox
mkdir -p "$SANDBOX/pkg/server/templates/en"
cat >"$SANDBOX/pkg/server/templates/en/probe-unaccounted.html" <<'HTML'
<!doctype html>
<html><body><span data-i18n="probe_unaccounted_key">Probe</span></body></html>
HTML
(cd "$SANDBOX" && git add -A) >/dev/null 2>&1
run_i18n
if [ "$RC" -ne 0 ] && says "$OUT" 'probe-unaccounted' && says "$OUT" 'neither scan list'; then
  pass "FIRING  a new document using data-i18n must be declared scanned or self-contained"
else
  fail "an unaccounted data-i18n document did not fail (rc=$RC): $OUT"
fi

# ---------------------------------------------------------------------------
# 8. FIRING. The self-contained list is a ratchet, not an exclusion list (SKILL 5b rule 5).
#
#    An entry whose file no longer uses the attribute must FAIL, so the list can only shrink.
#    Without this half it silently exempts whatever is written at that path next -- which is the
#    difference between a ratchet and a suppression wearing a ratchet's clothes.
# ---------------------------------------------------------------------------
reset_i18n_sandbox
if ! python3 - "$SANDBOX/pkg/server/static/offline.html" <<'PY'; then
import re, sys
p = sys.argv[1]
s = open(p).read()
out = re.sub(r'data-i18n(?:-[a-z-]+)?\s*=', 'data-probe-was-i18n=', s)
assert out != s, "no data-i18n attribute to remove -- the mutation would silently no-op"
open(p, "w").write(out)
PY
  harness "could not build the stale-entry case"
else
  run_i18n
  if [ "$RC" -ne 0 ] && says "$OUT" 'stale SELF_CONTAINED' && says "$OUT" 'offline.html'; then
    pass "FIRING  a SELF_CONTAINED entry that outlives its subject fails and names itself"
  else
    fail "a stale SELF_CONTAINED entry did not fail (rc=$RC): $OUT"
  fi
fi

# ---------------------------------------------------------------------------
# 9. FIRING. The locale list is DERIVED from pkg/server/i18n.go, not copied from it.
#
#    It used to be a literal array with a comment saying it was "kept in step with that list
#    deliberately". A hand-kept copy of another file's list is in step until the day it is not,
#    and on that day the locale the server loads has a bundle nothing checks -- the gate reports
#    success over it, which is the whole failure mode #1779 is about.
#
#    Adding a locale to i18n.go with no bundle behind it must now fail. Against the literal
#    array it did not: the new locale was simply not in the gate's list.
#
#    Matched on the message rather than on the exit code. The tree already exits 0, so an
#    exit-code assertion would be satisfied by any unrelated breakage the edit caused.
# ---------------------------------------------------------------------------
reset_i18n_sandbox
if ! python3 - "$SANDBOX/pkg/server/i18n.go" <<'PY'; then
import sys
p = sys.argv[1]
s = open(p).read()
old = '"ro", "ar"}'
assert old in s, "locale array not found -- the mutation would silently no-op"
open(p, "w").write(s.replace(old, '"ro", "ar", "xx"}', 1))
PY
  harness "could not add a locale to i18n.go"
else
  run_i18n
  if [ "$RC" -ne 0 ] && says "$OUT" 'Language_xx.properties' && says "$OUT" 'is missing'; then
    pass "FIRING  a locale added to i18n.go with no bundle fails, so the list is derived"
  else
    fail "adding a locale to i18n.go did not reach the gate (rc=$RC): $OUT"
  fi
fi

# ---------------------------------------------------------------------------
# 10. BOUNDING. A self-contained page's OWN bundle is not checked for internal drift.
#
#     Deliberate for now, and measured rather than assumed: the client inspector's
#     clientTranslations object has four keys (client_replay_req, client_req, client_resp,
#     client_replaying) present only in `en`, so five locales fall back to English -- the same
#     drift this gate exists for, one bundle out of its reach. Filed as #1854.
#
#     The case exists so that closing #1854 turns it red, and whoever closes it has to come
#     here and say the scope grew. Deleting a key from a self-contained bundle must NOT fail
#     today; the day it does, this comment is out of date.
# ---------------------------------------------------------------------------
reset_i18n_sandbox
if ! python3 - "$SANDBOX/pkg/client/dashboard.html" <<'PY'; then
import re, sys
p = sys.argv[1]
s = open(p).read()
# Remove one key from the Spanish half of the inline bundle only.
m = re.search(r'\n\s*"es":\s*\{', s)
assert m, "the es block of clientTranslations was not found"
tail = s[m.end():]
line = re.search(r'\n\s*"client_[a-z_]+":\s*"[^"]*",', tail)
assert line, "no removable key in the es block"
out = s[: m.end()] + tail[: line.start()] + tail[line.end():]
assert out != s
open(p, "w").write(out)
PY
  harness "could not remove a key from the client inspector's own bundle"
else
  run_i18n
  if [ "$RC" -eq 0 ]; then
    pass "BOUNDING  a self-contained page's own bundle is not checked for drift (#1854)"
  else
    fail "the gate now reads a self-contained page's inline bundle -- if that is #1854 landing, update this case: $OUT"
  fi
fi

echo ""
echo "-- check-closing-refs.sh: which texts reach it"

# ---------------------------------------------------------------------------
# 11. BOUNDING, and a wiring assertion. The script reads whatever it is handed, so its scope is
#     entirely a question of who calls it. There are exactly two feeds and they cover different
#     text:
#
#       * .github/workflows/issue-link-check.yml -- the PR title and body, in CI.
#       * scripts/commit-msg-hook.sh             -- each commit message, locally.
#
#     Neither is redundant. CI never reads commit messages, and this repo's squash CONCATENATES
#     them, so the merge commit carries text no CI job has seen. Losing either feed halves the
#     gate without changing a line of it -- the shape of #1528, where a check wired into nothing
#     drifted for months.
# ---------------------------------------------------------------------------
if grep -q 'check-closing-refs.sh' "${REPO_ROOT}/.github/workflows/issue-link-check.yml"; then
  pass "BOUNDING  the PR title and body reach the check, from CI"
else
  fail "nothing in issue-link-check.yml calls check-closing-refs.sh -- the PR text is no longer checked"
fi
if grep -q 'check-closing-refs.sh' "${REPO_ROOT}/scripts/commit-msg-hook.sh"; then
  pass "BOUNDING  each commit message reaches the check, from the commit-msg hook"
else
  fail "nothing in commit-msg-hook.sh calls check-closing-refs.sh -- and CI never sees a commit message"
fi

# ---------------------------------------------------------------------------
# 12. BOUNDING. The pattern is adjacency, not English.
#
#     "does not, in this case, close #12" is not caught, and that is the documented call: the
#     check matches the adjacent forms GitHub itself acts on rather than trying to parse a
#     sentence. Asserted so that widening it -- which would start reporting prose that merely
#     mentions an issue near the word "close" -- is a decision.
# ---------------------------------------------------------------------------
CLOSING_REFS="${REPO_ROOT}/scripts/check-closing-refs.sh"
printf 'This PR does not, in this case, close #12 -- there is more to do.\n' \
  | "$CLOSING_REFS" >"$OUT_FILE" 2>&1
RC=$?
if [ "$RC" -eq 0 ]; then
  pass "BOUNDING  a negation separated from the keyword by words is not reported"
else
  fail "the check now parses English -- intended? then update its 'Deliberately narrow' note and this case: $(cat "$OUT_FILE")"
fi

# The narrowness above is only worth pinning if the check still catches the adjacent form. A
# gate that reports nothing at all would satisfy case 12 vacuously (§5c).
printf 'Does not close #1521.\n' | "$CLOSING_REFS" >"$OUT_FILE" 2>&1
RC=$?
if [ "$RC" -ne 0 ] && grep -q '1521' "$OUT_FILE"; then
  pass "CONTROL   the adjacent form is still caught, so case 12 is a boundary and not a silence"
else
  fail "the check no longer catches 'Does not close #N' at all (rc=$RC): $(cat "$OUT_FILE")"
fi

echo ""
echo "-- scan-staged-secrets.sh: the staged diff, and nothing else"

# ---------------------------------------------------------------------------
# 13. BOUNDING. Content in the working tree that is not STAGED is out of scope.
#
#     Deliberate: the hook's justification is that a secret reaching a commit is in history, and
#     an unstaged file is not going into this commit. It is worth pinning because the gate's own
#     "gitleaks scanned 0 bytes although files are staged" floor sits one condition away from
#     making an untracked secret block every commit in the repository.
#
#     docker is stubbed. What is under test is which corpus this script hands over, not gitleaks.
# ---------------------------------------------------------------------------
SECRETS_WORK="$(mktemp -d "${TMPDIR:-/tmp}/gate-scope-secrets.XXXXXX")"
mkdir -p "$SECRETS_WORK/bin"
cat >"$SECRETS_WORK/bin/docker" <<'STUB'
#!/usr/bin/env bash
# Stands in for `gitleaks protect --staged` finding nothing, and saying how much it read.
echo "1:29AM INF scanned ~0 bytes (0 KB) in 1ms"
echo "1:29AM INF no leaks found"
exit 0
STUB
chmod +x "$SECRETS_WORK/bin/docker"
(
  cd "$SECRETS_WORK" || exit 1
  git init -q .
  git config user.email t@e.st
  git config user.name t
  printf 'placeholder\n' >tracked.txt
  git add tracked.txt
  git commit -qm init
  # A secret in the working tree, deliberately NOT staged.
  printf 'aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\n' >leak.txt
) >/dev/null 2>&1
(
  cd "$SECRETS_WORK" \
    && PATH="$SECRETS_WORK/bin:$PATH" "${REPO_ROOT}/scripts/scan-staged-secrets.sh"
) >"$OUT_FILE" 2>&1
RC=$?
if [ "$RC" -eq 0 ] && grep -q 'No secrets detected' "$OUT_FILE"; then
  pass "BOUNDING  an unstaged secret is outside the scan, and the zero-bytes floor does not fire"
else
  fail "an unstaged file reached the scan or tripped the floor (rc=$RC): $(cat "$OUT_FILE")"
fi

# The other half: with the SAME file staged, the zero-bytes floor must fire. Without this,
# case 13 would pass just as well on a script that never checks anything (§5c.3).
(cd "$SECRETS_WORK" && git add leak.txt) >/dev/null 2>&1
(
  cd "$SECRETS_WORK" \
    && PATH="$SECRETS_WORK/bin:$PATH" "${REPO_ROOT}/scripts/scan-staged-secrets.sh"
) >"$OUT_FILE" 2>&1
RC=$?
if [ "$RC" -ne 0 ] && grep -q 'scanned 0 bytes' "$OUT_FILE"; then
  pass "CONTROL   staging the same file DOES reach the scan, so case 13 bounds rather than silences"
else
  fail "staging the file changed nothing (rc=$RC): $(cat "$OUT_FILE")"
fi
rm -rf "$SECRETS_WORK"

echo ""
echo "-- check-stale-branches.sh: local branches whose upstream is gone"

# ---------------------------------------------------------------------------
# 14. BOUNDING. A local branch with NO upstream at all is invisible to it.
#
#     `%(upstream:track)` is empty for a branch that was never pushed, not `[gone]`, so it is
#     never reported. Deliberate and important: "never pushed" is unmerged work in progress, and
#     a cleanup tool that deleted it would destroy the only copy. tests/hooks/test-stale-branches.sh
#     already pins the live-upstream case; this is the third state, which nothing covered.
#
#     Ordinary git repositories, because the whole point is to watch it decide what to delete.
# ---------------------------------------------------------------------------
BR_WORK="$(mktemp -d "${TMPDIR:-/tmp}/gate-scope-branches.XXXXXX")"
(
  cd "$BR_WORK" || exit 1
  git init -q --bare origin.git
  git init -q clone
  cd clone || exit 1
  git config user.email t@e.st
  git config user.name t
  git remote add origin ../origin.git
  printf 'x\n' >f
  git add f
  git commit -qm init
  git branch -M master
  git push -q -u origin master
  # Merged and cleaned up on GitHub: pushed, then the remote branch deleted.
  git switch -qc merged-branch
  git push -q -u origin merged-branch
  git switch -q master
  git push -q origin --delete merged-branch
  # Never pushed at all: no upstream, so no [gone] marker.
  git switch -qc never-pushed-branch
  git switch -q master
) >/dev/null 2>&1
(cd "$BR_WORK/clone" && "${REPO_ROOT}/scripts/check-stale-branches.sh") >"$OUT_FILE" 2>&1
RC=$?
if grep -q 'never-pushed-branch' "$OUT_FILE"; then
  fail "a branch that was never pushed was reported as stale -- that is unmerged work with no other copy"
elif grep -q 'merged-branch' "$OUT_FILE"; then
  pass "BOUNDING  a branch with no upstream is out of scope; one whose upstream is gone is not"
else
  harness "the fixture reported neither branch, so nothing was compared (rc=$RC): $(cat "$OUT_FILE")"
fi

# ---------------------------------------------------------------------------
# 15. BOUNDING. Branches on the REMOTE are out of scope entirely.
#
#     It reports local refs only. An abandoned branch left on origin -- which is what a crashed
#     agent session leaves behind, and what the claim protocol in github-workflow SKILL §3 uses
#     as its lock -- is invisible here and stays invisible. Pinned rather than fixed: deleting a
#     remote branch is destructive on shared state and is not a decision this script should make
#     on its own.
# ---------------------------------------------------------------------------
(
  cd "$BR_WORK/clone" || exit 1
  git push -q origin master:refs/heads/abandoned-on-origin-only
  git fetch -q --prune origin
) >/dev/null 2>&1
(cd "$BR_WORK/clone" && "${REPO_ROOT}/scripts/check-stale-branches.sh") >"$OUT_FILE" 2>&1
if grep -q 'abandoned-on-origin-only' "$OUT_FILE"; then
  fail "the script now reports remote-only branches -- deleting one is destructive on shared state; if that is wanted, say so here"
else
  pass "BOUNDING  a branch that exists only on the remote is not reported"
fi
rm -rf "$BR_WORK"

echo ""
echo "-- check-test-coverage-signal.sh: which paths count as a functional change"

COVERAGE="${REPO_ROOT}/scripts/check-test-coverage-signal.sh"

# ---------------------------------------------------------------------------
# 16. FIRING. cmd/ and the portal pages other than dashboard.html are inside the scan.
#
#     The FUNCTIONAL pattern named `pkg/server/dashboard.html` specifically and covered no other
#     document, and left cmd/ out altogether -- so a change to either binary's entry point, or
#     to passcode.html, or to the client inspector's 1600-line page, brought no question with
#     it. cmd/lfr-tunnel/main_test.go exists, so "there is nothing to test here" was never the
#     reason; nobody had listed it.
#
#     Advisory by design, so this asserts the OUTPUT and not the exit code -- the script exits 0
#     whatever it decides, which means an exit-code assertion here could never fail (§5c.1).
# ---------------------------------------------------------------------------
for probe in cmd/lfr-tunnel/main.go pkg/server/passcode.html pkg/client/dashboard.html; do
  OUT="$(printf '%s\n' "$probe" | PR_BODY="" "$COVERAGE" 2>&1)"
  if says "$OUT" 'No test accompanies this change' && says "$OUT" "$probe"; then
    pass "FIRING  a change to $probe with no test is asked about"
  else
    fail "$probe is not treated as a functional change: $OUT"
  fi
done

# ---------------------------------------------------------------------------
# 17. BOUNDING. docs/, .github/ and the agent rules are outside it, deliberately -- a
#     documentation change has no test to bring. tests/hooks/test-coverage-signal.sh already
#     pins those three; what it does not pin is the pair below, where widening the pattern by
#     one directory would start asking for a test alongside a JIRA note.
# ---------------------------------------------------------------------------
OUT="$(printf '%s\n' ".agents/skills/edr-constraints/SKILL.md" "resources/github/branch_ruleset.json" | PR_BODY="" "$COVERAGE" 2>&1)"
if says "$OUT" 'No functional change'; then
  pass "BOUNDING  a rules or resources change is not treated as functional"
else
  fail "the functional pattern now covers .agents/ or resources/ -- intended? then say so where FUNCTIONAL is defined and update this case: $OUT"
fi

# ---------------------------------------------------------------------------
# 18. FIRING. "The diff was empty" and "the diff never ran" must not print the same thing.
#
#     Both arrive as no bytes on stdin, and both used to print "No functional change in this
#     diff" and exit 0 -- so a caller whose `git diff` had failed got the same green line as a
#     documentation-only PR. That is the "a scan of nothing reads exactly like a clean scan"
#     shape, in the one gate #1842 left alone because it is advisory and always exits 0.
#
#     The answer is not to make it fail: an advisory check that blocks a PR because a diff
#     command hiccupped is worse than the ambiguity. The answer is to say which one it was.
# ---------------------------------------------------------------------------
EMPTY_OUT="$(printf '' | PR_BODY="" "$COVERAGE" 2>&1)"
EMPTY_RC=$?
NOTHING_OUT="$(printf '%s\n' "docs/architecture.md" | PR_BODY="" "$COVERAGE" 2>&1)"
if [ "$EMPTY_RC" -ne 0 ]; then
  fail "the advisory check now fails on an empty list, which would block a PR whose diff hiccupped"
elif [ "$EMPTY_OUT" = "$NOTHING_OUT" ]; then
  fail "no file list and a documentation-only diff still print the same thing: $EMPTY_OUT"
elif says "$EMPTY_OUT" 'No file list was given'; then
  pass "FIRING  no file list is distinguishable from a diff with nothing functional in it"
else
  fail "an empty file list produced an unexpected message: $EMPTY_OUT"
fi

echo ""
echo "-- check-staged-prettier.sh: the hook's file set versus the one CI checks"

# ---------------------------------------------------------------------------
# 16. BOUNDING, and derived rather than listed. CI runs `prettier --check .` over the whole tree
#     with scope controlled by .prettierignore; the hook passes an explicit list of globs. A hook
#     that covers LESS than the gate it exists to pre-empt is the shape that erodes trust in it:
#     a green hook stops meaning anything, and #1548 failed CI on a .cjs file after passing here.
#
#     So the question is asked of Prettier itself, file by file, instead of comparing two lists
#     of extensions by eye. Anything Prettier would check that the hook's globs do not select is
#     a file that can only be caught after a push.
# ---------------------------------------------------------------------------
prettier_version_line="$(grep -m1 'PRETTIER_VERSION="' "${REPO_ROOT}/scripts/check-staged-prettier.sh")"
PRETTIER_PKG="$(printf '%s' "$prettier_version_line" | sed -n 's/.*:-\(prettier@[0-9.]*\)}.*/\1/p')"
if [ -z "$PRETTIER_PKG" ]; then
  harness "could not read the pinned Prettier version out of check-staged-prettier.sh"
elif ! command -v npx >/dev/null 2>&1; then
  harness "npx is not on PATH, so the hook's scope could not be compared with CI's"
else
  # The hook's globs, read out of the script rather than restated, so the two cannot drift.
  HOOK_GLOBS="$(sed -n "/^staged_prettier_files()/,/^}/p" "${REPO_ROOT}/scripts/check-staged-prettier.sh" \
    | grep -o "'\*\.[a-z0-9]*'" | tr -d "'" | sed 's/^\*//' | tr '\n' ' ')"
  if [ -z "$HOOK_GLOBS" ]; then
    harness "could not read the hook's glob list; the comparison below would be vacuous"
  else
    # CI's scope, in three parts, none of them a list kept by hand here:
    #   * every tracked file                    -- `prettier --check .` walks the tree
    #   * minus what .prettierignore holds back  -- git applies the same gitignore syntax
    #   * intersected with what Prettier can parse, asked of Prettier's own --support-info
    (cd "$REPO_ROOT" && git ls-files) >"${OUT_FILE}.all" 2>/dev/null
    (cd "$REPO_ROOT" && git ls-files -c -i -X .prettierignore) >"${OUT_FILE}.ign" 2>/dev/null
    (cd "$REPO_ROOT" && npx --yes "$PRETTIER_PKG" --support-info) >"${OUT_FILE}.info" 2>&1
    if ! grep -q '"languages"' "${OUT_FILE}.info"; then
      harness "Prettier could not be asked what it parses (npx or the registry is unavailable):
$(head -3 "${OUT_FILE}.info")"
    elif [ ! -s "${OUT_FILE}.all" ]; then
      harness "git ls-files listed nothing, so the comparison below would be vacuous"
    else
      HOOK_EXTS="$HOOK_GLOBS" node -e '
        const fs = require("fs");
        const [infoPath, allPath, ignPath] = process.argv.slice(1);
        const info = JSON.parse(fs.readFileSync(infoPath, "utf8"));
        const parsable = new Set();
        for (const lang of info.languages)
          for (const e of lang.extensions || []) parsable.add(e);
        const lines = (p) => fs.readFileSync(p, "utf8").split("\n").filter(Boolean);
        const ignored = new Set(lines(ignPath));
        const hookExts = process.env.HOOK_EXTS.trim().split(/\s+/);
        const missed = lines(allPath).filter((f) => {
          if (ignored.has(f)) return false;
          if (![...parsable].some((e) => f.endsWith(e))) return false;
          return !hookExts.some((e) => f.endsWith(e));
        });
        // Both halves printed. "0 missed" is also what an empty corpus prints, and the
        // parsable count is what distinguishes them (#1779).
        console.log("PARSABLE_EXTS=" + parsable.size);
        console.log("MISSED=" + missed.length);
        for (const f of missed.slice(0, 12)) console.log("  " + f);
      ' "${OUT_FILE}.info" "${OUT_FILE}.all" "${OUT_FILE}.ign" >"$OUT_FILE" 2>&1
      MISSED="$(sed -n 's/^MISSED=//p' "$OUT_FILE")"
      PARSABLE="$(sed -n 's/^PARSABLE_EXTS=//p' "$OUT_FILE")"
      if [ -z "$MISSED" ] || [ -z "$PARSABLE" ]; then
        harness "the comparison did not run: $(head -3 "$OUT_FILE")"
      elif [ "$PARSABLE" -lt 10 ]; then
        harness "Prettier reported only ${PARSABLE} parsable extensions, so 'nothing missed' would mean nothing"
      elif [ "$MISSED" -eq 0 ]; then
        pass "BOUNDING  every file CI's prettier run would check is selected by the hook's globs"
      else
        fail "$MISSED tracked file(s) CI checks but the pre-commit hook does not -- add the extension to staged_prettier_files(), or hold the file back in .prettierignore:
$(grep '^  ' "$OUT_FILE" | head -12)"
      fi
    fi
  fi
fi
rm -f "${OUT_FILE}.all" "${OUT_FILE}.ign" "${OUT_FILE}.info"

echo ""
echo "  ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
