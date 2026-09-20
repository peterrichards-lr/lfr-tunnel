#!/bin/bash
#
# Pre-push hook: the checks worth running before work becomes shared, but too slow to sit in
# front of every local checkpoint (#1343).
#
# Split out of pre-commit, which ran go vet, the test suite, a full React build and
# golangci-lint on every commit. golangci-lint is not repeated here -- CI runs it, and running
# it twice buys nothing but latency.
#
# The UI block is skipped entirely unless the push actually contains a ui/ change, which is
# what removes a pnpm install + React build from every backend push.

set -uo pipefail

ZERO="0000000000000000000000000000000000000000"

# Which commits is this push actually adding? (#1816)
#
# Two ways this got it wrong, both of which ended in "Nothing to check" and exit 0 -- a green
# pre-push that had run no checks at all:
#
#   1. The fallback `origin/master..HEAD` was installed WITHOUT verifying origin/master
#      resolves. The `rev-parse --verify` guard below it checked the left side of the range being
#      REPLACED, not the fallback being installed. Where that ref is absent -- a fresh clone that
#      has not fetched, a differently-named default branch -- `git diff` exits 128, stderr was
#      discarded, CHANGED came back empty, and the hook passed.
#
#   2. On a FORCE-PUSH after a rebase, remote_oid and local_oid are divergent commits rather than
#      ancestor and descendant. A rebase that preserves content makes their trees near-identical,
#      so the two-dot diff between them is empty even though the push carries real work. Measured
#      on 2026-09-08: `git diff --name-only 997e125f..e83a15ed` -> 0 files, while
#      `git diff --name-only origin/master..e83a15ed` -> 3. go vet, the test suite and the React
#      build were all skipped, on a branch whose base had just changed completely -- which is
#      exactly when you most want them to run.
#
# The rule now: prefer the narrow remote_oid..local_oid range ONLY when it genuinely describes
# the push (a fast-forward), otherwise ask what this branch adds to the target. And when the
# range cannot be evaluated, REFUSE -- a hook that cannot tell what changed must not report that
# nothing did.
BASE_REF="${LFT_PREPUSH_BASE:-origin/master}"

resolves() { git rev-parse --quiet --verify "$1" >/dev/null 2>&1; }

# git feeds pre-push "<local ref> <local oid> <remote ref> <remote oid>" per ref on stdin.
RANGE=""
while read -r _local_ref local_oid _remote_ref remote_oid; do
    [ "$local_oid" = "$ZERO" ] && continue          # deleting a remote ref; nothing to check
    if [ "$remote_oid" = "$ZERO" ] || ! resolves "$remote_oid"; then
        RANGE="$BASE_REF..$local_oid"               # new branch: everything not on the target
    elif git merge-base --is-ancestor "$remote_oid" "$local_oid" 2>/dev/null; then
        RANGE="$remote_oid..$local_oid"             # fast-forward: exactly the new commits
    else
        # Rewritten history (force-push after rebase/amend). The old remote head is not an
        # ancestor, so the narrow range does not describe what is being pushed.
        RANGE="$BASE_REF..$local_oid"
    fi
done

# Run by hand, with no stdin.
if [ -z "$RANGE" ]; then
    RANGE="$BASE_REF..HEAD"
fi

# Refuse rather than pass when the range cannot be evaluated. This is the whole point of #1816:
# "I could not tell" and "nothing changed" produced identical output and identical exit codes.
LEFT="${RANGE%%..*}"
if ! resolves "$LEFT"; then
    echo "[Git Hook] REFUSING: cannot resolve '$LEFT', so the range '$RANGE' cannot be" >&2
    echo "           evaluated and there is no way to tell what this push contains." >&2
    echo "           Fetch the target branch (git fetch origin), or set LFT_PREPUSH_BASE to a" >&2
    echo "           ref that exists. Skipping the checks silently is what #1816 was about." >&2
    exit 1
fi

if ! CHANGED=$(git diff --name-only "$RANGE" 2>&1); then
    echo "[Git Hook] REFUSING: 'git diff --name-only $RANGE' failed:" >&2
    echo "$CHANGED" >&2
    exit 1
fi

if [ -z "$CHANGED" ]; then
    echo "[Git Hook] Nothing to check in $RANGE."
    exit 0
fi

changed_matching() {
    echo "$CHANGED" | grep -qE "$1"
}

if changed_matching '\.go$|^go\.(mod|sum)$'; then
    echo "[Git Hook] Running go vet..."
    if ! go vet ./...; then
        echo "❌ Error: 'go vet' failed. Please fix before pushing."
        exit 1
    fi

    echo "[Git Hook] Running tests..."
    # Via make test, which exports GOTMPDIR and asserts it before building (#1334, #1335).
    # Never open-code the loop here; that divergence is what put unsigned binaries outside the
    # EDR whitelist on every commit.
    #
    # pkg/server stays excluded: its tests open real listeners and are slow enough to make even
    # a push hook painful. CI runs the full set on every PR.
    PKGS=$(go list ./... | grep -v /pkg/server | tr '\n' ' ')
    if ! make test PKG="$PKGS"; then
        echo "❌ Error: Tests failed. Please fix before pushing."
        exit 1
    fi
else
    echo "[Git Hook] No Go changes in $RANGE; skipping vet and tests."
fi

if changed_matching '^ui/'; then
    if command -v pnpm >/dev/null 2>&1; then
        echo "[Git Hook] Checking React UI syntax and types..."
        if ! (cd ui && pnpm install && pnpm run lint && pnpm run build); then
            echo "❌ Error: React UI lint or build failed. Please fix before pushing."
            exit 1
        fi
        echo "✅ React UI checks passed."
    else
        echo "⚠️ Warning: 'pnpm' not found in PATH. Skipping React UI checks."
        echo "   pnpm 11 needs Node >= 22.13; see ui/.nvmrc."
    fi
else
    echo "[Git Hook] No ui/ changes in $RANGE; skipping the UI build."
fi

# --- The static comparison gates (#2027) ------------------------------------------------------
#
# Sixteen of these existed and ran ONLY in CI, so the person who broke one found out thirteen
# minutes after pushing, from a job called "Lint & Format Check" that does not say which of
# sixteen fired. Two of the three red runs on 2026-09-17 were check-nolint-ratchet.sh -- a 716ms
# grep -- on branches whose subject had nothing to do with suppressions.
#
# This is #1929's shape one level up: that issue found four gates running NOWHERE and concluded a
# gate nobody runs is documentation with a shebang. It wired them into CI. Running only in CI is
# the same defect with a longer feedback loop.
#
# Measured on the machine this was written on: all of them together are ~4.5s, against a hook that
# already runs `make test` in minutes. They scan the whole tree rather than the diff, so they run
# unconditionally -- gating them on "did a relevant file change" is how a gate comes not to run on
# the push that needed it.
echo "[Git Hook] Running the static gates..."

# node is NOT on PATH in every shell this hook runs from: a non-interactive shell never reads
# .zshrc, so an nvm-managed node is invisible and `node foo.cjs` exits 127. That is indisputably
# the worst outcome available -- 127 is a FAILURE exit, so the gate reads as having found
# something. Resolve it explicitly, the same reasoning as the `go` PATH shim (#1860).
NODE_BIN=""
for candidate in node /opt/homebrew/bin/node /usr/local/bin/node; do
    # --version, not just existence: a stale nvm shim or a node too old to parse these gates
    # would otherwise be chosen and fail with a syntax error, which reads as a gate FINDING
    # rather than as the wrong interpreter. Picking nothing is better than picking wrong.
    if command -v "$candidate" >/dev/null 2>&1 && "$candidate" --version >/dev/null 2>&1; then
        NODE_BIN="$candidate"
        break
    fi
done

# Discovered ONCE, and used by both the loop and the warning below. Two separate `find`
# invocations is how a guard on this file came to match the wrong one and report coverage that
# was not there -- measured while writing #2027's test.
JS_GATES="$(find scripts -maxdepth 1 \( -name 'check-*.cjs' -o -name 'check-*.mjs' \) -exec basename {} \; | sort)"

GATE_FAILED=0

# Shell gates first: no interpreter to find, and the ratchet is the one that bites most often.
for gate in check-nolint-ratchet.sh check-test-home-isolation.sh check-required-contexts.sh; do
    if [ -x "scripts/$gate" ]; then
        if ! "./scripts/$gate"; then
            echo "❌ scripts/$gate failed."
            GATE_FAILED=1
        fi
    fi
done

# Python gates. Source-level only, so there is nothing to build and nothing to install beyond
# python3 itself. check-docs-fences.py catches an unclosed fence, which silently turns a heading
# into a code comment and drops it out of the table of contents (#2081).
if command -v python3 >/dev/null 2>&1; then
    for gate in check-docs-fences.py; do
        if ! python3 "scripts/$gate" >/dev/null; then
            echo "❌ scripts/$gate failed. Re-run it for the detail:  python3 scripts/$gate"
            GATE_FAILED=1
        fi
    done
else
    echo "⚠️ Warning: no python3 found, so the Python gates did NOT run. They will run in CI."
fi

if [ -n "$NODE_BIN" ]; then
    for gate in $JS_GATES; do
        if ! "$NODE_BIN" "scripts/$gate" >/dev/null; then
            echo "❌ scripts/$gate failed. Re-run it for the detail:  node scripts/$gate"
            GATE_FAILED=1
        fi
    done
else
    # Loud, not silent. A skipped gate that says nothing is the #1929 defect wearing a warning's
    # clothes -- and unlike the pnpm block below, these are cheap enough that there is no reason
    # to skip them other than a missing interpreter.
    echo "⚠️ Warning: no node found, so the $(echo "$JS_GATES" | wc -l | tr -d ' ') JavaScript gates did NOT run."
    echo "   They will run in CI instead. Install node, or set PATH in your git hook environment."
fi

if [ "$GATE_FAILED" -ne 0 ]; then
    echo "❌ Error: a static gate failed. These are the same gates CI runs -- fixing them here"
    echo "   costs seconds; finding out from CI costs a 13-minute cycle."
    exit 1
fi

echo "✅ Pre-push checks passed."
exit 0
