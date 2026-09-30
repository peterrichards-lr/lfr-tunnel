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
# grep exits, and output that fits the pipe buffer never does. Rewriting 146 pipelines across 48
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
#
# KNOWN LIMIT OF THE COUNT, and it errs in the unhelpful direction. `grep -c` counts matching
# LINES, not pipelines: two on one line count once, and one split across a `\` continuation counts
# zero. That is conservative about the existing backlog and PERMISSIVE ABOUT GROWTH -- a new
# occurrence appended to an already-counted line does not move the number and this gate stays
# green, which is the one thing a ratchet exists to prevent. It costs exactly 1 today (147
# occurrences on 146 lines). Tightening it means a real parser rather than a grep, which is not
# obviously worth it; knowing which way it fails is.

# 146, against the 150 quoted in #2290. Two separate corrections, both recorded rather than
# quietly applied, because a ceiling nobody can reproduce is a ceiling nobody will lower:
#
#   150 -> 148   comment lines. The issue's figure was a quick count that included prose about the
#                idiom. NOT this script's own exclusions, as an earlier draft of this note said --
#                neither file existed when #2290 was filed, so they could not have contributed.
#   148 -> 146   `x || grep -q y` is not a pipeline. The old regex matched the second `|` of `||`,
#                counting two lines (test-go-guard.sh, test-pull-images.sh) that have no pipe, can
#                never SIGPIPE and can never be "fixed" -- a floor nobody could ever reach.
#
# 146 -> 142 by #2310, which is the first real fall rather than a correction. All eight
# `echo "$OUT" | grep -q ...` in tests/hooks/test-nested-worktree-scope.sh became here-strings
# while that file was open for other reasons -- the opportunistic paydown this ratchet is for.
# Four of the eight were being added by that change, which is how it went red in the first place.
CEILING="${LFT_SIGPIPE_CEILING:-142}"

# SELF-MATCH. This script and its test both have to SPELL the pattern they are about -- in this
# comment, and in the counting expression below -- so a naive scan counts its own documentation
# and its own implementation. scripts/check-nolint-ratchet.sh learned this the hard way and left
# the note: "the count above is a grep, so it counts the suppression's spelling in COMMENTS too
# ... a real removal cancelled by prose about it."
#
# Two defences, both needed: skip these two files BY PATH, and skip comment lines. By path rather
# than by basename: a basename match would skip any file that happened to share the name wherever
# it sat, which is a correctness bug in the exclusion even if contriving it is unlikely.
SELF_GATE="scripts/check-sigpipe-ratchet.sh"
SELF_TEST="tests/hooks/test-sigpipe-ratchet.sh"

# CORPUS. One directory level: `tests/hooks/` and `scripts/`, NOT `scripts/common/`, `scripts/lib/`,
# `scripts/liferay/` or `tests/e2e/`. That narrowness is deliberate -- those are the gate scripts
# this ratchet is about -- but it is a real blind spot and it is asserted, not merely described:
# `scripts/common/` holds 15 files that set pipefail, including the unattended gateway scripts
# (gateway-watchdog.sh, drain-and-wait.sh, restore-backup.sh), where an inverted match means a
# gateway is restarted or is not. They contribute 0 today. A BOUNDING case in the test pins the
# exclusion, so widening the scan later is a decision rather than an accident (section 5b rule 6).
CORPUS_GLOBS="tests/hooks/*.sh scripts/*.sh"

count() {
    local f files total
    # `grep -l` once over the whole corpus rather than a `grep -q` per file: the per-file loop was
    # 2 forks x 79 files = 158 processes on every push. check-nolint-ratchet.sh, the precedent,
    # does its whole job in two greps.
    files="$(grep -lE 'pipefail' $CORPUS_GLOBS 2>/dev/null || true)"
    [ -n "$files" ] || { printf '0'; return; }

    total=0
    for f in $files; do
        case "$f" in "$SELF_GATE"|"$SELF_TEST") continue ;; esac
        # Strip comment lines so prose about the idiom does not inflate the count -- the
        # cancelled-removal trap from #2032. The regex requires a single `|`: `x || grep -q y` has
        # no pipe and cannot SIGPIPE.
        total=$((total + $(grep -vhE '^[[:space:]]*#' "$f" | grep -cE '(^|[^|])\| *grep -q' || true)))
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
    for f in $CORPUS_GLOBS; do
        [ -f "$f" ] && n=$((n + 1))
    done
    printf '%s' "$n"
}

# 70, against a real corpus of 79. The old floor of 40 was barely half, which left #1779's shape
# open at a smaller granularity: a tree holding tests/hooks/ but no scripts/ has 53 files, clears
# a floor of 40, counts 14 fewer pipelines, and the gate then INVITES ratcheting down to a number
# that can never see them again. A floor has to be close enough to the real corpus to notice half
# of it missing.
MIN_FILES="${LFT_SIGPIPE_MIN_FILES:-70}"
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

# $SELF_GATE, not $SELF. This branch aborted with "SELF: unbound variable" under `set -u` from
# the day the script landed (#2312) -- the count sat exactly at its ceiling for two days, so the
# one path the gate exists to encourage was also the one nobody had taken. It failed the build
# on a tree strictly better than required, in CI and pre-push alike.
if [ "$ACTUAL" -lt "$CEILING" ]; then
    echo "The count has fallen. Lower the ceiling in $SELF_GATE to $ACTUAL so it cannot drift back up."
fi
