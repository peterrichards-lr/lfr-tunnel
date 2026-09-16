#!/usr/bin/env bash
# test-privacy-disclosures.sh -- assert check-privacy-disclosures.cjs actually fires (#1954).
#
# The gate compares PRIVACY.md §1's `### <Letter>.` subsections against the
# `data-disclosure="<Letter>"` markers in every pkg/server/templates/*/privacy.html.
#
# It exists because the served page drifted from the canonical policy TWICE without anything
# noticing: #1894 added §1.D and the served page never got it (while policy_version was bumped and
# every user re-consented), then #1955 added §1.E and the served page did not get that either.
#
# A comparison that always passes is the failure this repo keeps finding (#1779), so every case
# below plants a fixture pair and requires a specific verdict -- including the vacuity cases, where
# one side parses to nothing and a naive implementation reports that the sets agree.
#
# Cases are labelled PREMISE / FIRING / BOUNDING / CONTROL. A BOUNDING case pins a deliberate limit
# so that crossing it turns this suite red rather than silently widening the gate.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GATE="${REPO_ROOT}/scripts/check-privacy-disclosures.cjs"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/privacy-disclosures.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT INT TERM

if [ ! -f "$GATE" ]; then
    fail "scripts/check-privacy-disclosures.cjs is missing -- if it moved, move this guard with it"
    echo ""
    echo "passed: $PASS  failed: $FAIL"
    exit 1
fi

# Long enough to clear the gate's MIN_SECTION_CHARS floor. Deliberately says nothing true about the
# product: what a section CONTAINS is outside this gate's contract, and a case below pins that.
FILLER="This paragraph stands in for a real translated summary of the subsection it is marked with, and is written to be comfortably longer than the minimum."

# emit_template <path> <spec>
#
# spec is a comma-separated list of letters, or one of:
#   __none__   -- a page with no markers at all, the shape every template had before #1954
#   __stub__   -- every letter marked, but with a placeholder too short to be a disclosure
#   __nested__ -- a marked section containing another <section>, which the reader cannot span
#   __dup__    -- the same letter marked twice
emit_template() {
    local out="$1" spec="$2"
    mkdir -p "$(dirname "$out")"
    {
        echo '<!DOCTYPE html>'
        echo '<html lang="{{.Lang}}" dir="{{.Dir}}"><body><div class="container">'
        echo '<h1>{{.Title}}</h1>'
        echo '<h2>1. Information We Collect &amp; Process</h2>'
        case "$spec" in
            __none__)
                echo '<ul><li>IP addresses</li><li>Email addresses</li><li>Audit logs</li></ul>'
                ;;
            __stub__)
                local IFS=,
                for l in A B C D E; do
                    printf '<section data-disclosure="%s"><h3>%s</h3><p>TODO</p></section>\n' "$l" "$l"
                done
                ;;
            __nested__)
                printf '<section data-disclosure="A"><p>%s</p><section><p>%s</p></section></section>\n' "$FILLER" "$FILLER"
                ;;
            __dup__)
                printf '<section data-disclosure="A"><p>%s</p></section>\n' "$FILLER"
                printf '<section data-disclosure="A"><p>%s</p></section>\n' "$FILLER"
                ;;
            *)
                local IFS=,
                for l in $spec; do
                    [ -n "$l" ] || continue
                    printf '<section data-disclosure="%s"><h3>%s</h3><p>%s</p></section>\n' "$l" "$l" "$FILLER"
                done
                ;;
        esac
        echo '</div></body></html>'
    } > "$out"
}

# emit_md <path> <spec>
#
# spec is a comma-separated list of letters, or one of:
#   __no_section_one__  -- a policy with no "## 1." heading at all
#   __no_subsections__  -- a "## 1." with no `###` subsections under it
#   __outside__         -- subsections A,B under §1 and a `### Z.` under §4, which is NOT a
#                          category of processing and must not become a requirement
emit_md() {
    local out="$1" spec="$2"
    {
        echo '# Privacy Policy & Cookie Disclosure'
        echo ''
        case "$spec" in
            __no_section_one__)
                echo '## Information'
                echo ''
                echo '### A. Network Data'
                echo ''
                echo '* Something.'
                ;;
            __no_subsections__)
                echo '## 1. Information Collected & Processed'
                echo ''
                echo 'Prose with no subsection headings at all.'
                ;;
            __outside__)
                echo '## 1. Information Collected & Processed'
                echo ''
                echo '### A. Network Data'
                echo ''
                echo '* Something.'
                echo ''
                echo '### B. Account Data'
                echo ''
                echo '* Something.'
                echo ''
                echo '## 4. GDPR Right to Be Forgotten'
                echo ''
                echo '### Z. The Purge Protocol'
                echo ''
                echo '* Not a category of processing.'
                ;;
            *)
                echo '## 1. Information Collected & Processed'
                echo ''
                local IFS=,
                for l in $spec; do
                    [ -n "$l" ] || continue
                    printf '### %s. Category %s\n\n* Something.\n\n' "$l" "$l"
                done
                echo '## 2. Personal Access Tokens'
                ;;
        esac
    } > "$out"
}

# run_case <label> <expected-exit> <md spec> <template spec>
#
# The template spec is pipe-separated `lang:spec` pairs, e.g. "en:A,B|es:A". The literal string
# "__empty__" means no template directories at all.
run_case() {
    local label="$1" want="$2" md_spec="$3" tpl_spec="$4"
    local dir
    dir="$WORK/$(printf '%s' "$label" | tr -c 'a-zA-Z0-9' '_')"
    mkdir -p "$dir/scripts" "$dir/pkg/server/templates"
    cp "$GATE" "$dir/scripts/"

    emit_md "$dir/PRIVACY.md" "$md_spec"

    if [ "$tpl_spec" != "__empty__" ]; then
        local rest="$tpl_spec" pair lang spec
        while [ -n "$rest" ]; do
            case "$rest" in
                *"|"*) pair="${rest%%|*}"; rest="${rest#*|}" ;;
                *)     pair="$rest";       rest="" ;;
            esac
            lang="${pair%%:*}"
            spec="${pair#*:}"
            emit_template "$dir/pkg/server/templates/$lang/privacy.html" "$spec"
        done
    fi

    ( cd "$dir" && node scripts/check-privacy-disclosures.cjs >/dev/null 2>&1 )
    local got=$?

    if [ "$got" -eq "$want" ]; then
        pass "$label (exit $got)"
    else
        fail "$label (expected exit $want, got $got)"
        ( cd "$dir" && node scripts/check-privacy-disclosures.cjs 2>&1 | sed 's/^/        /' )
    fi
}

echo "Privacy disclosure coverage gate cases:"
echo ""

# PREMISE. Without this, every failing case below could be failing because the gate is broken for
# everything rather than because it detects the specific defect.
run_case "all five languages cover every subsection" 0 "A,B,C,D,E" \
    "en:A,B,C,D,E|es:A,B,C,D,E|fr:A,B,C,D,E|ro:A,B,C,D,E|ar:A,B,C,D,E"

# FIRING. The #1894 and #1955 defects exactly: §1 gained a category and the served page did not.
run_case "§1 gains a category no template has" 1 "A,B,C,D,E,F" \
    "en:A,B,C,D,E|es:A,B,C,D,E|fr:A,B,C,D,E|ro:A,B,C,D,E|ar:A,B,C,D,E"

# FIRING. One language behind is the likelier real shape -- someone updates English and stops. If
# the gate only compared against en, this passes and four languages ship an incomplete policy.
run_case "one language behind the other four" 1 "A,B,C,D,E" \
    "en:A,B,C,D,E|es:A,B,C,D,E|fr:A,B,C,D|ro:A,B,C,D,E|ar:A,B,C,D,E"

# FIRING. The state the served page was actually in before this issue: no markers at all. It must
# be reported as missing everything, not skipped as "nothing to compare".
run_case "a template with no markers at all" 1 "A,B,C,D,E" \
    "en:A,B,C,D,E|es:__none__"

# FIRING. The other direction: a marker for a letter §1 does not define -- a typo, or a category
# removed from the canonical policy and left behind on the served page.
run_case "a template marks a letter §1 does not define" 1 "A,B,C" \
    "en:A,B,C|es:A,B,C,Q"

# FIRING. A marker on a placeholder. Satisfying the letter set with "TODO" is the cheapest way to
# make this gate green while disclosing nothing, so the floor has to be real.
run_case "markers present but the sections are stubs" 1 "A,B,C,D,E" \
    "en:A,B,C,D,E|es:__stub__"

# FIRING. A nested <section> makes the reader's non-greedy close the wrong one, so the block it
# measured is not the block that exists. It must fail rather than measure the wrong thing.
run_case "a nested section is not silently mis-read" 1 "A" \
    "en:A|es:__nested__"

# FIRING. The same letter twice is ambiguous about which block is the disclosure.
run_case "a duplicated marker is rejected" 1 "A" \
    "en:A|es:__dup__"

# BOUNDING. Order is not the contract. The served page groups for reading and the canonical policy
# is written in its own order; requiring them to match would fail for a difference nobody cares
# about.
run_case "a different marker order still agrees" 0 "A,B,C,D,E" \
    "en:E,D,C,B,A|es:C,A,E,B,D"

# BOUNDING. THE LIMIT OF THIS GATE, pinned as a case rather than left in a comment (§5b rule 6).
# The gate reads structure, not prose: the fixture's section text says nothing that matches the
# canonical wording, and that passes. An edit to the BODY of an existing subsection -- §1.D's
# retention changing from 30 days to 60, say -- changes no heading and this stays green. If this
# case ever goes red, someone taught the gate to read prose; confirm that is wanted rather than
# narrowing it back.
run_case "unrelated prose under a correct marker still passes" 0 "A,B,C,D,E" \
    "en:A,B,C,D,E|es:A,B,C,D,E"

# BOUNDING. A `###` heading outside §1 is not a category of processing. Counting it would demand a
# counterpart for §4's purge protocol, which is not something the served page enumerates.
run_case "a ### heading outside §1 creates no requirement" 0 "__outside__" \
    "en:A,B|es:A,B"

# CONTROL. Nothing to compare on the canonical side. Without a floor the letter set is empty, every
# template trivially covers it, and the gate passes forever on a policy it cannot parse.
run_case "§1 parsing to zero subsections must NOT pass" 1 "__no_subsections__" \
    "en:__none__|es:__none__"

# CONTROL. The canonical document restructured so "## 1." no longer exists. Same failure shape,
# reached a different way.
run_case "no '## 1.' section must NOT pass" 1 "__no_section_one__" \
    "en:__none__|es:__none__"

# CONTROL. Nothing to compare on the served side. A full letter set checked against zero files is
# the vacuous pass this repo has found five of (#1779).
run_case "no templates found must NOT pass" 1 "A,B,C,D,E" "__empty__"

echo ""

# The real tree, not a fixture. The cases above all run against generated files, so they would all
# pass against a gate wired to the wrong paths. This one asserts the gate is pointed at what ships.
if ( cd "$REPO_ROOT" && node scripts/check-privacy-disclosures.cjs >/dev/null 2>&1 ); then
    pass "the gate passes against the real repository tree"
else
    fail "the gate fails against the real repository tree"
    ( cd "$REPO_ROOT" && node scripts/check-privacy-disclosures.cjs 2>&1 | sed 's/^/        /' )
fi

# And that it is reading the shipped templates rather than a stray copy: the served page exists in
# more than one language, and a gate that found only English would pass every case above.
served=$(find "${REPO_ROOT}/pkg/server/templates" -mindepth 2 -maxdepth 2 -name 'privacy.html' | wc -l | tr -d ' ')
if [ "$served" -ge 2 ]; then
    pass "found $served served privacy.html templates to cover"
else
    fail "found $served served privacy.html templates -- the gate would be checking almost nothing"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
