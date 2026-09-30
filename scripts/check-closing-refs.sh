#!/usr/bin/env bash
# check-closing-refs.sh — reject a REVERSED closing reference, which GitHub reads as a closure
#
# GitHub's linked-issue parser matches close/fixes/resolves followed by #<N> and IGNORES
# whatever stands in front of it. So this, written to be helpful:
#
#     Does not close #1521.
#
# closes #1521 on merge. That is not hypothetical -- it is how #1521 was closed with two of its
# three parts outstanding (#1540), by a PR whose whole purpose was to say it was not finishing
# the work.
#
# The same phrasing produced the opposite failure one PR later: "Does not close the issue", with
# no #N, so the issue-link check found no reference at all and failed the build. Same intent,
# two different wrong outcomes, neither obvious from the rule as written -- which is why this is
# a check rather than another paragraph. The paragraph existed; it was read; it was tripped three
# times in an hour, once while actively writing the warning about it.
#
# KNOWN AND DELIBERATE: quoting the bad phrasing with a REAL issue number trips this, even in
# prose describing the problem. That is the right call rather than a rough edge -- whether
# GitHub's parser ignores a reference inside backticks or a code fence is not something to bet an
# issue's state on. Write it with a placeholder (#<N>) when describing it, which is also what the
# documentation does.
#
# Reads the text to check from a file argument, or from stdin.
#
# Exit codes:
#   0  no reversed closing reference
#   1  found one
set -uo pipefail

if [ "$#" -gt 1 ]; then
    echo "Usage: $0 [file]   (reads stdin when no file is given)" >&2
    exit 2
fi

if [ "$#" -eq 1 ]; then
    if [ ! -f "$1" ]; then
        echo "check-closing-refs: $1 not found" >&2
        exit 2
    fi
    TEXT=$(cat "$1")
else
    TEXT=$(cat)
fi

# What people actually write in front of a closing keyword when they mean the opposite.
# Deliberately narrow: it catches the adjacent forms that GitHub itself acts on, and does not
# try to parse English. A sentence with words between it and the keyword ("does not, in this
# case, close #12") is not caught -- and is also not a phrasing anyone reaches for by accident.
#
# TWO KINDS, and the second was missing until it cost something. Negations were enumerated
# from the start; CONTRASTS were not, and a contrast reverses the sense just as completely:
#
#     Filed rather than fixed: #<N>
#
# has no "not", no "never" and no "without" anywhere in it, read as a refusal by every human
# who saw it, and closed the issue on merge. This script ran on that commit and passed. It is
# the third instance of the class (#1533, #1538, #2316) and the first where the gate existed.
REVERSED='(does|do|did|will|would|can|could|should)( ?n.?t| not)|\bnot\b|\bnever\b|\bwithout\b'
REVERSED="${REVERSED}|\brather than\b|\binstead of\b|\bother than\b|\bas opposed to\b|\bin place of\b"
KEYWORD='close[sd]?|fix(e[sd])?|resolve[sd]?'

MATCHES=$(printf '%s\n' "$TEXT" \
    | grep -inE "(${REVERSED})[[:space:]]+(${KEYWORD})[[:space:]]*:?[[:space:]]*#[0-9]+" || true)

# The second shape, which closed #1538 the same day this script was written (#1543).
#
# A PLACEHOLDER between the keyword and a real number bridges them. GitHub skips a #token that is
# not a valid issue reference and matches the next one that is, so a squashed title reading
#
#     ... closes #N (#1538) (#1539)
#
# closes 1538. Not contrived: `#N` is the placeholder the documentation recommends for describing
# this very trap, and every squashed commit here gains a `(#PR)` trailer -- so a stray keyword in
# a title always has a real number waiting at the end of the line to reach.
BRIDGED=$(printf '%s\n' "$TEXT" \
    | grep -inE "(${KEYWORD})[[:space:]]*:?[[:space:]]*#[^0-9[:space:]][^[:space:]]*.*#[0-9]+" || true)

if [ -z "$MATCHES" ] && [ -z "$BRIDGED" ]; then
    exit 0
fi

echo "::error::This text would close an issue you did not mean to close."
echo

if [ -n "$MATCHES" ]; then
    echo "A REVERSED closing reference. GitHub matches close/fixes/resolves followed by #<N> and"
    echo "ignores whatever stands in front of it -- a negation OR a contrast -- so these would"
    echo "CLOSE the issues they name:"
    echo
    printf '%s\n' "$MATCHES" | sed 's/^/    /'
    echo
fi

if [ -n "$BRIDGED" ]; then
    echo "A PLACEHOLDER bridging the keyword to a real issue. GitHub skips a #token that is not a"
    echo "number and matches the next one that is -- and every squashed commit here gains a (#PR)"
    echo "trailer, so a title always has a real number waiting at the end of the line:"
    echo
    printf '%s\n' "$BRIDGED" | sed 's/^/    /'
    echo
    echo "Keep a closing keyword off any line that also carries a real issue number. In a commit"
    echo "TITLE that is every line, because of the trailer -- put closing references in the body."
    echo
fi
echo "If the PR genuinely does not finish that issue, name it without the keyword beside it:"
echo
echo "    Part 2 of #1521          rather than   Does not close #1521"
echo "    Follow-up to #1521       rather than   Doesn't fix #1521"
echo "    Filed separately as #1521  rather than   Filed rather than fixed: #1521"
echo
echo "And give the PR its own sub-issue to close -- every PR here must close something, so an"
echo "intermediate PR needs one of its own. See .agents/skills/github-workflow/SKILL.md."
exit 1
