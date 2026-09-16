#!/usr/bin/env bash
# test-mkdocs-nav.sh -- assert check-mkdocs-nav.cjs actually fires (#1918).
#
# The gate compares mkdocs.yml's hand-written `nav:` against the pages on disk under docs/, with
# an exclusion list carried as `# nav-exclude: <path> -- <reason>` comments in the same file.
#
# A comparison that always passes is the failure this repo keeps finding (#1779), so every case
# below plants a whole fixture tree and requires a specific verdict -- including the vacuity case,
# where both sides parse to nothing and a naive implementation reports that they agree.
#
# Kept to bash 3.2 (see AGENTS.md): no associative arrays, no mapfile, no ${var^^}.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GATE="${REPO_ROOT}/scripts/check-mkdocs-nav.cjs"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/mkdocs-nav.XXXXXX")"
# The last case plants a page in the REAL docs/ tree, so the trap has to remove that too --
# an interrupted run must not leave a stray page behind for the next commit to pick up.
PLANTED="${REPO_ROOT}/docs/nav-gate-control-$$.md"
trap 'rm -rf "$WORK"; rm -f "$PLANTED"' EXIT INT TERM

if [ ! -f "$GATE" ]; then
    fail "scripts/check-mkdocs-nav.cjs is missing -- if it moved, move this guard with it"
    echo ""
    echo "passed: $PASS  failed: $FAIL"
    exit 1
fi

# run_case <label> <expected-exit> <files> <nav entries> <nav-exclude payloads>
#
#   files    ';'-separated paths, created under the fixture's docs/. Empty means an empty tree.
#   nav      ';'-separated lines written verbatim under `nav:`, indented two spaces. The literal
#            __none__ omits the `nav:` key entirely.
#   excludes ';'-separated payloads, each written as `# nav-exclude: <payload>`. Empty means none.
#
# The gate itself is production: it is copied in unchanged and resolves its own repo root from
# its location, so the fixture is only ever the corpus. Same arrangement as
# tests/hooks/test-status-vocabulary.sh.
run_case() {
    local label="$1" want="$2" files="$3" nav="$4" excludes="$5"
    local dir
    dir="$WORK/$(printf '%s' "$label" | tr -c 'a-zA-Z0-9' '_')"
    mkdir -p "$dir/docs" "$dir/scripts"
    cp "$GATE" "$dir/scripts/"

    # Set once for the whole function rather than around each loop: `unset IFS` inside a function
    # drops the local and exposes the caller's, which is a different value from the default.
    # Every other expansion below is quoted, so nothing else is affected by the split character.
    local IFS=';'

    local f
    for f in $files; do
        [ -n "$f" ] || continue
        mkdir -p "$dir/docs/$(dirname "$f")"
        printf '# %s\n' "$f" > "$dir/docs/$f"
    done

    {
        echo "site_name: Fixture"
        echo ""
        local e
        for e in $excludes; do
            [ -n "$e" ] || continue
            printf '# nav-exclude: %s\n' "$e"
        done
        if [ "$nav" != "__none__" ]; then
            echo "nav:"
            local n
            for n in $nav; do
                [ -n "$n" ] || continue
                printf '  %s\n' "$n"
            done
        fi
        echo ""
        echo "theme:"
        echo "  name: material"
    } > "$dir/mkdocs.yml"

    ( cd "$dir" && node scripts/check-mkdocs-nav.cjs >/dev/null 2>&1 )
    local got=$?

    if [ "$got" -eq "$want" ]; then
        pass "$label (exit $got)"
    else
        fail "$label (expected exit $want, got $got)"
        ( cd "$dir" && node scripts/check-mkdocs-nav.cjs 2>&1 | sed 's/^/        /' )
    fi
}

echo "mkdocs nav gate cases:"
echo ""

# PREMISE. Without this, every failing case below could be failing because the gate is broken for
# everything rather than because it detects the specific defect.
run_case "every page is in nav" 0 \
    "README.md;getting_started.md" \
    "- Home: README.md;- Getting Started: getting_started.md" \
    ""

# FIRING. The #1918 defect exactly: a page builds and deploys, and is reachable only by direct URL
# or site search, because nobody remembered the nav entry.
run_case "a page on disk missing from nav" 1 \
    "README.md;getting_started.md;orphan.md" \
    "- Home: README.md;- Getting Started: getting_started.md" \
    ""

# PREMISE. The same page, excluded on purpose with a reason. This is the distinction the issue
# asks for: "deliberately out" must be tellable from "forgotten".
run_case "an excluded page with a reason is accepted" 0 \
    "README.md;orphan.md" \
    "- Home: README.md" \
    "orphan.md -- written for somewhere else, not the site"

# FIRING. An exclusion with no reason. A bare path is the same silence as an omission, just
# written down, so the gate must not accept it.
run_case "an exclusion with no reason is rejected" 1 \
    "README.md;orphan.md" \
    "- Home: README.md" \
    "orphan.md"

# FIRING. A stale exclusion: the page it names is gone. Left alone, it silently covers the next
# page that happens to be given that name.
run_case "an exclusion naming a page that is gone" 1 \
    "README.md" \
    "- Home: README.md" \
    "departed.md -- was written for somewhere else"

# FIRING. Both at once. Which list wins would then depend on reading order rather than intent.
run_case "a page both in nav and excluded" 1 \
    "README.md;getting_started.md" \
    "- Home: README.md;- Getting Started: getting_started.md" \
    "getting_started.md -- some reason"

# FIRING. The other direction: nav names a file that does not exist, e.g. after a rename.
run_case "nav names a file that does not exist" 1 \
    "README.md" \
    "- Home: README.md;- Ghost: ghost.md" \
    ""

# BOUNDING. The nav's external entries -- CONTRIBUTING.md, SECURITY.md and CODE_OF_CONDUCT.md are
# https:// links to github.com, not local files. Reading them as paths would report three missing
# pages on a correct tree, which is a gate nobody can leave switched on.
run_case "https:// nav entries are not local files" 0 \
    "README.md" \
    "- Home: README.md;- Contributing: https://github.com/o/r/blob/master/CONTRIBUTING.md" \
    ""

# BOUNDING. Nested sections and subdirectories, which is the real nav's shape: a heading line has
# no target of its own and must not be read as one, and server/*.md must resolve under docs/.
run_case "nested sections and subdirectories resolve" 0 \
    "README.md;server/setup_guide.md" \
    "- Home: README.md;- Server Guides:;  - Control Plane Setup: server/setup_guide.md" \
    ""

# FIRING. The same nesting with the subdirectory page left out -- so the case above is passing
# because the paths resolve, not because subdirectories are skipped entirely.
run_case "a subdirectory page missing from nav" 1 \
    "README.md;server/setup_guide.md;server/orphan.md" \
    "- Home: README.md;- Server Guides:;  - Control Plane Setup: server/setup_guide.md" \
    ""

# BOUNDING. Order is not the contract. The nav is grouped for readers; the disk walk is
# alphabetical. Requiring them to agree would fail for a difference nobody cares about.
run_case "a different order still agrees" 0 \
    "README.md;architecture.md;getting_started.md" \
    "- Getting Started: getting_started.md;- Home: README.md;- System Architecture: architecture.md" \
    ""

# CONTROL. Both sides empty. A comparison with no anti-vacuity floor reports that they agree, and
# the gate then passes forever on a tree it cannot parse.
run_case "empty on both sides must NOT be reported as agreement" 1 \
    "" "__none__" ""

# CONTROL. Half-empty, each way round. Either alone is enough to make the comparison meaningless.
run_case "pages on disk but no nav at all" 1 \
    "README.md;getting_started.md" "__none__" ""

run_case "a nav block that parses to no local pages" 1 \
    "README.md" \
    "- Contributing: https://github.com/o/r/blob/master/CONTRIBUTING.md" \
    ""

# The counterpart to every firing case above: the gate must still pass against the real tree. A
# guard that fails everywhere is not a guard, it is an outage.
if (cd "$REPO_ROOT" && node scripts/check-mkdocs-nav.cjs >/dev/null 2>&1); then
    pass "the real repository passes"
else
    fail "the real repository FAILS -- a docs page is unreachable, or the floor is too high"
    ( cd "$REPO_ROOT" && node scripts/check-mkdocs-nav.cjs 2>&1 | sed 's/^/        /' )
fi

# And the control on that: plant a page in the REAL docs tree and require the real gate to notice.
# The fixture cases above all run against a synthetic mkdocs.yml, so without this the whole suite
# could be green while the production config is parsed wrongly.
printf '# control\n' > "$PLANTED"
if (cd "$REPO_ROOT" && node scripts/check-mkdocs-nav.cjs >/dev/null 2>&1); then
    fail "a page planted in the real docs/ tree did NOT fail the gate"
else
    pass "a page planted in the real docs/ tree fails the gate"
fi
rm -f "$PLANTED"

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
