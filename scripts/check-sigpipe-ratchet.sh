#!/usr/bin/env bash
# Ceiling on `producer | grep -q` pipelines in scripts that set pipefail, so the count can fall
# but not rise (#2290).
set -euo pipefail

# WHY THIS IS A FAULT, not a style preference.
#
# `grep -q` exits the moment it matches. If the producer is still writing it takes SIGPIPE, and
# `pipefail` promotes that to a non-zero pipeline status -- so a SUCCESSFUL MATCH reads as a
# failure. Which way that breaks depends on the caller:
#
#   if producer | grep -q X; then      a match becomes "no match"   -> false negative
#   producer | grep -q X || echo …     a match becomes "missing"    -> false positive
#
# Both directions happened on 2026-09-28, in guards written that day:
#
#   tests/hooks/test-edr-deny-list.sh        false FAIL, once its producer grew 43 -> 79 entries
#   tests/hooks/test-decisions-integrity.sh  false PASS: a BOUNDING case that could never go red,
#                                            green on macOS and red on Linux CI
#
# The second is why this exists. It is silent, timing-dependent, and invisible to whoever ran it
# green. A guard corpus whose value is not lying had a guard that could not fail.
#
# WHY A RATCHET AND NOT A REWRITE.
#
# The fault is dormant almost everywhere: the producer only SIGPIPEs if it is still writing when
# grep exits, and output that fits the pipe buffer never does. Rewriting 150 pipelines across 48
# guard files to fix a fault latent in most of them is the cure being worse than the disease --
# AGENTS.md's "Surgical Fixes & Minimal Diffs", and a bad transform here means guards that lie.
#
# So: stop the count growing, make it visible, let it fall as files are touched anyway. Nobody is
# forced into a 48-file diff to land an unrelated change. Lower the ceiling whenever the real
# count drops and the ratchet tightens on its own.
#
# THE SAFE IDIOM, for whoever is here because this went red:
#
#   out="$(producer)"
#   if grep -q PATTERN <<<"$out"; then …
#
# A here-string is not a pipe, so there is no reader to close early and nothing to SIGPIPE.

# 148, not the 150 quoted in #2290. That first figure was a quick count that included comment
# lines and this script's own two exclusions; the number below is what the counter above actually
# measures. Recorded rather than quietly corrected, because a ceiling nobody can reproduce is a
# ceiling nobody will lower.
CEILING="${LFT_SIGPIPE_CEILING:-148}"

# SELF-MATCH. This script and its test both have to SPELL the pattern they are about -- in this
# comment, and in the counting expression below -- so a naive scan counts its own documentation
# and its own implementation. scripts/check-nolint-ratchet.sh learned this the hard way and left
# the note: "the count above is a grep, so it counts the suppression's spelling in COMMENTS too
# ... a real removal cancelled by prose about it."
#
# Two defences, both needed: skip this file and its test by name, and skip comment lines.
SELF="$(basename "${BASH_SOURCE[0]}")"

count() {
    local f n total=0
    for f in tests/hooks/*.sh scripts/*.sh; do
        [ -f "$f" ] || continue
        case "$(basename "$f")" in
            "$SELF"|test-sigpipe-ratchet.sh) continue ;;
        esac
        # Only files that actually set pipefail: without it the pipeline reports grep's status
        # alone and the fault cannot occur. Counting them anyway would make the ceiling a measure
        # of shell style rather than of this defect.
        grep -q 'pipefail' "$f" || continue
        # `grep -v '^[[:space:]]*#'` drops comment lines, so prose describing the idiom does not
        # inflate the count -- the cancelled-removal trap from #2032.
        n="$(grep -v '^[[:space:]]*#' "$f" | grep -cE '\| *grep -q' || true)"
        total=$((total + n))
    done
    printf '%s' "$total"
}

# How many files the count actually looked at.
#
# Without this the ratchet passes on a scan of NOTHING (#1779): a wrong working directory makes
# both globs match literally, `[ -f ]` skips them, the total is 0, 0 is under any ceiling, and the
# gate reports success -- then invites LOWERING the ceiling to 0, which disarms it permanently.
corpus_size() {
    local f n=0
    for f in tests/hooks/*.sh scripts/*.sh; do
        [ -f "$f" ] && n=$((n + 1))
    done
    printf '%s' "$n"
}

MIN_FILES="${LFT_SIGPIPE_MIN_FILES:-40}"
CORPUS="$(corpus_size)"
if [ "$CORPUS" -lt "$MIN_FILES" ]; then
    echo "FAILED: only $CORPUS shell files found under tests/hooks/ and scripts/ (expected at least $MIN_FILES)."
    echo "The pipeline count is therefore meaningless -- a scan of nothing counts zero and would"
    echo "pass. Run this from the repository root."
    exit 1
fi

ACTUAL="$(count)"

echo "pipefail + 'grep -q' pipelines: $ACTUAL (ceiling $CEILING)"

if [ "$ACTUAL" -gt "$CEILING" ]; then
    echo
    echo "FAILED: $((ACTUAL - CEILING)) more than the ceiling allows."
    echo
    echo "A 'producer | grep -q X' under 'set -o pipefail' can report the OPPOSITE of the truth:"
    echo "grep -q exits on its first match, the producer takes SIGPIPE, and pipefail turns that"
    echo "into a non-zero status. Use a here-string instead, which has no pipe:"
    echo
    echo "    out=\"\$(producer)\""
    echo "    if grep -q PATTERN <<<\"\$out\"; then ..."
    echo
    echo "See #2290. If you genuinely need the pipeline, raise the ceiling here deliberately and"
    echo "say why -- do not bump it silently."
    exit 1
fi

if [ "$ACTUAL" -lt "$CEILING" ]; then
    echo "The count has fallen. Lower the ceiling in $SELF to $ACTUAL so it cannot drift back up."
fi
