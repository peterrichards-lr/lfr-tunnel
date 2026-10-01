#!/usr/bin/env bash
#
# test-bundle-decoration.sh -- scripts/check-bundle-decoration.py sees both faces of #2327.
#
# The gate's value is two detections, and a passing run distinguishes neither of them from a scan
# that read nothing:
#
#   FIRING   a bundle value containing markup is reported
#   FIRING   a bundle value whose leading icon the view already draws is reported
#   BOUNDING V1 is deliberately NOT scanned for the icon rule, because the defect cannot occur
#            there -- applyTranslations does `el.innerText = bundle[key]`, so an icon inline
#            beside a data-i18n attribute is OVERWRITTEN rather than doubled. Pinned so that
#            widening the scan later is a decision rather than an accident (SKILL 5b rule 6).
#
# Fixtures live in a temp tree: the gate globs the bundles and ui/src from its working directory,
# so anything planted in the real tree would be read by the real run.
#
# bash 3.2 compatible (macOS /bin/bash). See AGENTS.md.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "$REPO_ROOT" || exit 1

GATE="scripts/check-bundle-decoration.py"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

if [ ! -f "$GATE" ]; then
    fail "PREMISE: $GATE is missing -- if it moved, move this test with it"
    echo ""; echo "passed: $PASS  failed: $FAIL"; exit 1
fi

echo "-- PREMISE: the gate passes against the real tree"
if python3 "$GATE" >/dev/null 2>&1; then
    pass "PREMISE: the real tree is clean, so a failure below is about the fixture"
else
    fail "PREMISE: the real tree already fails -- fix that first"
fi

FIXTURE="$(mktemp -d "${TMPDIR:-/tmp}/lft-decoration.XXXXXX")"
cleanup() { rm -rf "$FIXTURE"; }
trap cleanup EXIT

# A tree the gate will accept: it needs 5+ bundles, 20+ V2 files and a few hundred English values.
new_tree() {
    local dir="$FIXTURE/$1"
    mkdir -p "$dir/scripts" "$dir/pkg/server/i18n" "$dir/ui/src/pages"
    cp "$GATE" "$dir/scripts/"
    local l
    for l in "" _de _es _fr _ja _ar; do
        cp pkg/server/i18n/Language.properties "$dir/pkg/server/i18n/Language${l}.properties"
    done
    local i=1
    while [ "$i" -le 24 ]; do
        printf "export default function P%d() { return <div>{t('noop', 'x')}</div>; }\n" "$i" \
            >"$dir/ui/src/pages/P$i.tsx"
        i=$((i + 1))
    done
    printf '%s' "$dir"
}

echo ""
echo "-- FIRING: a bundle value containing markup"

markup="$(new_tree markup)"
printf 'planted_markup_key=Status: <span style="color: red;">ACTIVE</span>\n' \
    >>"$markup/pkg/server/i18n/Language.properties"
out="$( cd "$markup" && python3 scripts/check-bundle-decoration.py 2>&1 )"
rc=$?
if [ "$rc" -eq 0 ]; then
    fail "FIRING: a value containing a <span> was accepted -- V2 would show those tags to an admin"
elif grep -q 'planted_markup_key' <<<"$out"; then
    pass "FIRING: markup in a bundle value is reported, and the key is named"
else
    fail "FIRING: the gate failed without naming the key, so nobody can act on it: $out"
fi

echo ""
echo "-- FIRING: a leading icon the view already draws"

icons="$(new_tree icons)"
printf 'planted_icon_key=\xf0\x9f\x94\x92 Some Heading\n' >>"$icons/pkg/server/i18n/Language.properties"
# A V2 component that draws its own icon beside the same key -- the doubling shape.
printf 'export default function X() {\n  return <h3>\xf0\x9f\x94\x92 {t('"'"'planted_icon_key'"'"', '"'"'Some Heading'"'"')}</h3>;\n}\n' \
    >"$icons/ui/src/pages/Doubled.tsx"
out="$( cd "$icons" && python3 scripts/check-bundle-decoration.py 2>&1 )"
rc=$?
if [ "$rc" -eq 0 ]; then
    fail "FIRING: a key whose view already draws an icon kept one of its own -- the heading shows two"
elif grep -q 'planted_icon_key' <<<"$out"; then
    pass "FIRING: a doubled icon is reported, and the key is named"
else
    fail "FIRING: the gate failed without naming the key: $out"
fi

echo ""
echo "-- BOUNDING: an icon in a bundle value NOBODY draws beside is left alone"

# This is the whole reason the icon rule is narrow. 14 of 84 heading keys carry an icon, which is
# a design question (#2327) and not this gate's business. Only the DOUBLED case is a defect.
lone="$(new_tree lone)"
printf 'planted_lone_icon=\xf0\x9f\x94\x92 Nobody Draws This\n' >>"$lone/pkg/server/i18n/Language.properties"
out="$( cd "$lone" && python3 scripts/check-bundle-decoration.py 2>&1 )"
if [ $? -eq 0 ]; then
    pass "BOUNDING  an icon with no view drawing one beside it is not a finding"
else
    fail "BOUNDING  a lone icon was reported -- the gate has become an opinion about which headings get icons: $out"
fi

echo ""
echo "-- BOUNDING: V1 is not scanned for the icon rule"

# applyTranslations sets el.innerText, so V1's inline icon is REPLACED by the bundle value, never
# appended. The defect cannot occur in V1, and scanning dashboard.html would report every one of
# the eight elements that correctly keep an icon beside a translated span.
v1="$(new_tree v1)"
printf 'planted_v1_icon=\xf0\x9f\x94\x92 V1 Heading\n' >>"$v1/pkg/server/i18n/Language.properties"
mkdir -p "$v1/pkg/server"
printf '<h4>\xf0\x9f\x94\x92 <span data-i18n="planted_v1_icon">V1 Heading</span></h4>\n' \
    >"$v1/pkg/server/dashboard.html"
out="$( cd "$v1" && python3 scripts/check-bundle-decoration.py 2>&1 )"
if [ $? -eq 0 ]; then
    pass "BOUNDING  V1 markup is out of scope; innerText overwrites rather than doubles"
else
    fail "BOUNDING  V1 is being scanned -- every correct V1 heading would now be reported: $out"
fi

echo ""
echo "-- BOUNDING: a scan of nothing is refused"

mkdir -p "$FIXTURE/empty/scripts"
cp "$GATE" "$FIXTURE/empty/scripts/"
if ( cd "$FIXTURE/empty" && python3 scripts/check-bundle-decoration.py >/dev/null 2>&1 ); then
    fail "BOUNDING  the gate reported success with no bundles and no V2 source (#1779)"
else
    pass "BOUNDING  a tree with no bundles is refused rather than reported clean"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
