#!/usr/bin/env bash
#
# test-standalone-page-locale.sh -- a page that translates itself must say what it translated to
# (#2302).
#
# THE DEFECT. offline.html and maintenance.html localise their own body text at runtime and kept
# `<html lang="en">` for the life of the page. So an Arabic visitor got Arabic prose in a document
# declaring English -- and, in offline.html, laid out by `document.body.style.direction` rather
# than the `dir` attribute, which is what :lang(), hyphenation, font fallback and every screen
# reader's speech synthesiser actually read. offline.html is what a visitor sees when someone's
# tunnel is down, so it is the most-seen page in this repo by a wide margin.
#
# WHY THESE TWO ARE FIXED IN THE PAGE and setup.html is not:
#
#   offline.html      picks its locale from navigator.language, which the server cannot see. A
#                     server rewrite would declare one locale while the page rendered another.
#   maintenance.html  under iron curtain this file is written to disk by pkg/nginx/maintenance.go
#                     and served by NGINX to every visitor, with no per-request negotiation and
#                     often with the Go server unreachable. A rewrite at the in-app serve site
#                     would bake one locale for everyone on the path where it is most seen, and
#                     make the two paths disagree.
#   setup.html        served per-request AND already defers to the server via /api/i18n, so it is
#                     rewritten server-side instead -- no pre-script window at all.
#                     TestSetupPageDeclaresTheResolvedLocale covers that one.
#
# This is a static check because the behaviour is a line of JavaScript in a file nothing else
# reads. A gate that greps is weaker than one that runs the page -- it cannot tell you the value
# is correct, only that the assignment is there -- and it is what stops the attribute being
# dropped in a later edit, which is the failure this issue is about.
#
# bash 3.2 compatible (macOS /bin/bash). See AGENTS.md.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "$REPO_ROOT" || exit 1

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

echo "-- FIRING: a self-translating page declares its locale and direction"

# The two that translate themselves in the browser.
for page in offline maintenance; do
    f="pkg/server/static/${page}.html"
    if [ ! -f "$f" ]; then
        fail "PREMISE: $f is missing -- if it moved, move this check with it"
        continue
    fi
    if ! grep -q 'documentElement\.lang' "$f"; then
        fail "$f never sets documentElement.lang -- it translates itself into a document that says English"
    elif ! grep -q 'documentElement\.dir' "$f"; then
        fail "$f sets lang but not dir -- an Arabic reader gets RTL text in an LTR document"
    else
        pass "$page.html declares both lang and dir"
    fi
done

# The style hack offline.html used instead, which must not come back: a `direction` style on
# <body> moves the glyphs and tells assistive technology nothing.
#
# COMMENT LINES STRIPPED FIRST. offline.html's own comment explains what it stopped doing and
# therefore SPELLS the pattern, so a naive grep finds the documentation and reports the defect it
# describes -- the self-match of github-workflow SKILL 5c rule 6, which this repo has now hit
# five times. check-nolint-ratchet.sh and check-sigpipe-ratchet.sh strip comments for the same
# reason; `//` at the start of a line inside the page's <script>, not inside a URL.
# A here-string, not a pipe: `grep -q` exits on its first match while the producer is still
# writing a 400-line file, which under `set -o pipefail` turns a SUCCESSFUL match into a non-zero
# status -- i.e. reports "clean" for a file that still has the hack (#2290). The sigpipe ratchet
# refused this on push, which is what it is for.
offline_code="$(grep -vE '^[[:space:]]*//' pkg/server/static/offline.html)"
if grep -q 'body\.style\.direction' <<<"$offline_code"; then
    fail "offline.html still sets body.style.direction -- the dir attribute is what is read, not the style"
else
    pass "offline.html no longer lays out RTL with a style instead of an attribute"
fi

echo ""
echo "-- BOUNDING: English-only pages are NOT required to do this"

# These have no translation mechanism at all, so lang="en" is correct and nothing needs doing.
# Pinned so that adding a translation to one of them without adding the declaration turns this
# suite red, rather than quietly joining the class the check above exists for.
ENGLISH_ONLY="pkg/server/offline.html
pkg/server/passcode.html
pkg/server/unauthorized_ip.html
pkg/server/blocked.html
pkg/server/static/gone.html"

bounded=0
while IFS= read -r f; do
    [ -n "$f" ] || continue
    [ -f "$f" ] || continue
    if grep -q 'data-i18n' "$f" && ! grep -q 'documentElement\.lang' "$f"; then
        fail "$f has gained data-i18n elements but still never declares its locale -- it has joined the class above"
    else
        bounded=$((bounded + 1))
    fi
done <<EOF
$ENGLISH_ONLY
EOF
if [ "$bounded" -lt 4 ]; then
    fail "only $bounded English-only page(s) checked -- the list has gone stale, so this bound is not holding anything"
else
    pass "BOUNDING  $bounded English-only page(s) still carry no translation mechanism"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
