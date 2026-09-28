#!/bin/bash
#
# test-decisions-integrity.sh -- .agents/decisions/ records stay true, or the build fails (#2287).
#
# A decision log is prose, and prose does not fail (github-workflow section 5b rule 6). The
# in-tree warning about what that becomes is `.agents/gemini.md`, which now has to open with
# "This is a historical record, not current guidance ... the completed-task lists in particular
# record decisions that have since been reversed." A decision record that has quietly stopped
# being true is worse than no record: it is read with the same confidence as a current one.
#
# So three properties are asserted over every record, each chosen because it is the half a human
# reader cannot check by eye:
#
#   STATUS        present, on line 1, and one of the three forms -- so a superseded record
#                 announces itself in `head -1` rather than in a paragraph someone skims past.
#   Enforced by   present, and every PATH it names resolves. This is the one that rots: the gate
#                 or test that made a decision real gets renamed, and the record goes on claiming
#                 enforcement that no longer exists. `unenforced by design` is a legitimate answer
#                 and is accepted as-is -- refusing it would push people to name something
#                 tangential just to satisfy the gate.
#   Superseded by the record it names must exist, or the supersession chain is broken.
#
# What this deliberately does NOT assert: that the decision is still a good one, or that the
# enforcement it names is any good. Those need a person. Stating the boundary rather than leaving
# it implied, per section 5b rule 6.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "$REPO_ROOT" || exit 1

DECISIONS=".agents/decisions"

PASS=0
FAIL=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }

# records_in <dir> -- decision records, README excluded (it is the format spec, not a record).
records_in() {
    find "$1" -maxdepth 1 -name '*.md' ! -name 'README.md' 2>/dev/null | sort
}

# check_dir <dir> -- prints one line per problem found. Empty output means the directory is sound.
#
# Paths are taken from backticked tokens inside the "Enforced by" section that actually look like
# paths: they contain a "/" and end in an extension. That narrowness is deliberate -- the section
# legitimately contains backticked things that are NOT paths (`make check-portal-parity`,
# `unenforced by design`, `s.db == nil`), and a matcher that tried to resolve those would be
# mostly false positives, which is how a gate ends up with exemptions bolted on until it says
# nothing.
check_dir() {
    local dir="$1" f status section token path base

    for f in $(records_in "$dir"); do
        base="$(basename "$f")"

        # -- STATUS, on line 1
        status="$(head -1 "$f")"
        case "$status" in
            "STATUS: Accepted "*|"STATUS: Reversed "*|"STATUS: Superseded by "*)
                ;;
            STATUS:*)
                echo "$base: STATUS is not one of Accepted/Reversed/Superseded by -- got: $status"
                ;;
            *)
                echo "$base: line 1 is not a STATUS: line -- got: $status"
                ;;
        esac

        # -- Superseded by NNNN must name a record that exists
        case "$status" in
            "STATUS: Superseded by "*)
                token="$(printf '%s' "$status" | sed -E 's/^STATUS: Superseded by ([0-9]{4}).*/\1/')"
                if [ -z "$(find "$dir" -maxdepth 1 -name "${token}-*.md" 2>/dev/null)" ]; then
                    echo "$base: superseded by $token, but no ${token}-*.md exists"
                fi
                ;;
        esac

        # -- README rule 4: cite issues and PRs, never the gitignored state file. A record that
        # cites it cannot be followed from a fresh clone, which is the exact problem this directory
        # exists to fix, so a violation here reintroduces it.
        if grep -q '\.agent-state\.md' "$f"; then
            echo "$base: cites .agent-state.md, which is gitignored and unreadable from a fresh clone"
        fi

        # -- README rule 5: Revisit when is what separates a decision from a gag order.
        grep -q '^## Revisit when' "$f" || echo "$base: has no '## Revisit when' section"

        grep -q '^## Cost' "$f" || echo "$base: has no '## Cost' section"

        # -- Enforced by, and the paths it names
        if ! grep -q '^## Enforced by' "$f"; then
            echo "$base: has no '## Enforced by' section -- name the gate, or say 'unenforced by design'"
            continue
        fi

        # awk rather than a sed range, because a sed `/start/,/end/` range with no end match runs
        # to EOF and the `$d` that trims the next heading then eats the last CONTENT line instead.
        # That is invisible on a record where "Enforced by" is followed by "Revisit when" -- i.e.
        # on every real record -- and silently empties the section on one where it is last. The
        # FIRING fixtures below are exactly that shape, which is how this was found.
        section="$(awk '/^## Enforced by/{f=1;next} /^## /{f=0} /^---$/{f=0} f' "$f")"
        if [ -z "$(printf '%s' "$section" | tr -d '[:space:]')" ]; then
            echo "$base: '## Enforced by' is empty"
            continue
        fi

        # The WHOLE record, not just this section. 0002's only named mechanism sat in `## Cost`
        # and its `Enforced by` named no path at all, so renaming that skill would have left the
        # record lying with the gate green.
        for token in $(grep -oE '`[^`]+`' "$f" | tr -d '`'); do
            case "$token" in
                */*) ;;   # only backticked tokens that look like paths
                *) continue ;;
            esac
            # A trailing "/" is a directory reference (`tests/e2e/ui/tests/`), which `[ -e ]`
            # resolves perfectly well and the extension-only form silently skipped.
            printf '%s' "$token" | grep -qE '\.[A-Za-z0-9]+$|\.[A-Za-z0-9]+:[0-9]+$|/$' || continue
            path="$(printf '%s' "$token" | sed -E 's/:[0-9]+$//')"
            [ -e "$path" ] || echo "$base: 'Enforced by' names $path, which does not exist"
        done
    done
}

echo "-- premise: there is something to check"

if [ ! -d "$DECISIONS" ]; then
    fail "PREMISE: $DECISIONS does not exist -- if it moved, move this guard with it"
    echo ""; echo "passed: $PASS  failed: $FAIL"; exit 1
fi

# Anti-vacuity. Every assertion below loops over the records; with no records the loop body never
# runs and this suite reports a clean pass over nothing (#1779, #1929).
count="$(records_in "$DECISIONS" | grep -c . )"
if [ "$count" -lt 1 ]; then
    fail "PREMISE: no records in $DECISIONS -- every assertion below would be vacuous"
else
    pass "PREMISE: $count record(s) to check"
fi

if [ -f "$DECISIONS/README.md" ]; then
    pass "PREMISE: $DECISIONS/README.md exists (the format spec the records are checked against)"
else
    fail "PREMISE: $DECISIONS/README.md is missing -- the format is then undefined"
fi

echo ""
echo "-- every record has a valid STATUS, a resolvable 'Enforced by', and an intact supersession"

problems="$(check_dir "$DECISIONS")"
if [ -z "$problems" ]; then
    pass "all $count record(s) sound"
else
    while IFS= read -r p; do
        [ -n "$p" ] && fail "$p"
    done <<EOF
$problems
EOF
fi

# --- FIRING cases -------------------------------------------------------------------------
#
# Everything above is "grep found nothing wrong", which a checker that cannot fail satisfies just
# as well. Each case below plants one specific defect and requires the checker to name THAT
# defect -- reading "it failed" is not the check (section 5c rule 2).

echo ""
echo "-- FIRING: each defect must be detected, and named"

FIXTURE="$(mktemp -d "${TMPDIR:-/tmp}/lft-decisions-fixture.XXXXXX")"
cleanup() { rm -rf "$FIXTURE"; }
trap cleanup EXIT
cp "$DECISIONS/README.md" "$FIXTURE/README.md"

# 1. an "Enforced by" naming a path that does not exist
cat > "$FIXTURE/0001-dangling.md" <<'FIXEOF'
STATUS: Accepted 2026-01-01

# 0001. A record whose enforcement has been renamed away

## Enforced by
`scripts/this-gate-was-deleted.cjs`
FIXEOF

# 2. a STATUS that is not one of the three forms
cat > "$FIXTURE/0002-badstatus.md" <<'FIXEOF'
STATUS: Probably fine

# 0002. A record with an invented status

## Enforced by
`unenforced by design`
FIXEOF

# 3. a supersession pointing at a record that does not exist
cat > "$FIXTURE/0003-broken-chain.md" <<'FIXEOF'
STATUS: Superseded by 0099 2026-01-01

# 0003. A record superseded by nothing

## Enforced by
`unenforced by design`
FIXEOF

# 4. cites the gitignored state file (README rule 4)
cat > "$FIXTURE/0004-cites-state.md" <<'FIXEOF'
STATUS: Accepted 2026-01-01

# 0004. A record whose reasoning points at a file a fresh clone does not have

## Cost
None stated.

## Enforced by
`unenforced by design`

## Revisit when
See .agent-state.md for the rest.
FIXEOF

# 5. no Revisit when -- a decision without one is a gag order (README rule 5)
cat > "$FIXTURE/0005-no-revisit.md" <<'FIXEOF'
STATUS: Accepted 2026-01-01

# 0005. A record that closes a question permanently

## Cost
None stated.

## Enforced by
`unenforced by design`
FIXEOF

# 6. no Enforced by section at all -- the branch N11 left unproven
cat > "$FIXTURE/0006-no-enforced.md" <<'FIXEOF'
STATUS: Accepted 2026-01-01

# 0006. A record that names no mechanism and does not say so

## Cost
None stated.

## Revisit when
Never.
FIXEOF

fixture_problems="$(check_dir "$FIXTURE")"

if printf '%s\n' "$fixture_problems" | grep -q 'scripts/this-gate-was-deleted.cjs, which does not exist'; then
    pass "FIRING: a dangling 'Enforced by' path is named"
else
    fail "FIRING: a dangling 'Enforced by' path was NOT detected -- this guard proves nothing"
fi

if printf '%s\n' "$fixture_problems" | grep -q '0002-badstatus.md: STATUS is not one of'; then
    pass "FIRING: an invalid STATUS is named"
else
    fail "FIRING: an invalid STATUS was NOT detected"
fi

if printf '%s\n' "$fixture_problems" | grep -q 'superseded by 0099, but no 0099-\*.md exists'; then
    pass "FIRING: a broken supersession chain is named"
else
    fail "FIRING: a broken supersession chain was NOT detected"
fi

if printf '%s\n' "$fixture_problems" | grep -q '0004-cites-state.md: cites .agent-state.md'; then
    pass "FIRING: a record citing the gitignored state file is named"
else
    fail "FIRING: a citation of .agent-state.md was NOT detected"
fi

if printf '%s\n' "$fixture_problems" | grep -q "0005-no-revisit.md: has no '## Revisit when'"; then
    pass "FIRING: a missing 'Revisit when' is named"
else
    fail "FIRING: a missing 'Revisit when' was NOT detected"
fi

if printf '%s\n' "$fixture_problems" | grep -q "0006-no-enforced.md: has no '## Enforced by'"; then
    pass "FIRING: a record naming no mechanism at all is named"
else
    fail "FIRING: a missing 'Enforced by' section was NOT detected"
fi

# --- BOUNDING -----------------------------------------------------------------------------
#
# Passes before and after, on purpose: it pins two deliberate edges so that crossing either turns
# this suite red rather than silently changing what the gate means.

echo ""
echo "-- BOUNDING: the deliberate edges"

cat > "$FIXTURE/0004-unenforced.md" <<'FIXEOF'
STATUS: Accepted 2026-01-01

# 0004. An accepted trade-off with nothing to enforce it

## Cost
Nothing enforces it, so it relies on being read.

## Enforced by
`unenforced by design` -- this is a trade-off, not a rule an agent can violate.

## Revisit when
Someone proposes a mechanism that could enforce it.
FIXEOF

bounding_problems="$(check_dir "$FIXTURE")"
if grep -q '0004-unenforced.md' <<<"$bounding_problems"; then
    fail "BOUNDING: 'unenforced by design' was rejected -- it is a legitimate answer (see README)"
else
    pass "BOUNDING: 'unenforced by design' is accepted without naming a path"
fi

# The README is the format spec, not a record. If it were ever treated as one it would fail every
# assertion above, and the obvious "fix" would be to weaken them.
if grep -q '^README.md:' <<<"$bounding_problems"; then
    fail "BOUNDING: README.md is being checked as a record -- it is the format spec"
else
    pass "BOUNDING: README.md is excluded from the record set"
fi

echo ""
echo "passed: $PASS  failed: $FAIL"
[ "$FAIL" -eq 0 ] || exit 1
