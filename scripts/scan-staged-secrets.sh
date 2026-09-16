#!/bin/bash
#
# Scan the staged changes for secrets with gitleaks, in Docker.
#
# Extracted from scripts/pre-commit-hook.sh (#1377) so the logic below can be tested directly
# rather than only through a real commit. The hook is the only caller today; keeping it separate
# is the same arrangement as scripts/check-edr-safety.sh.
#
# Two defects motivated this, both of which made the hook report success without scanning:
#
#   1. Only the working directory was mounted. In a linked worktree, `.git` is a FILE holding an
#      absolute path to <main>/.git/worktrees/<name>, which is outside that mount, so git inside
#      the container cannot resolve the repository at all.
#
#   2. gitleaks exits 0 when its own git subprocess fails. So the hook printed
#      "No secrets detected" on a scan of zero bytes. Measured, same staged token both times:
#
#        ordinary clone : leaks found: 1, scanned ~54 bytes, exit 1
#        worktree       : fatal: not a git repository ..., scanned ~0 bytes, exit 0
#
# Fixing only (1) would leave the silent-pass mode intact for the next unforeseen cause, so the
# result is also checked for evidence that a scan actually happened. This check has to fail
# closed: the hook's whole justification is that a secret reaching a commit is in history.
#
# A third defect (#1923) was about what it SAYS rather than whether it blocks. `docker run` also
# exits non-zero when the daemon is down, so with colima stopped every commit was refused with
# "a secret or private token was detected" and told to add the value to .gitleaksignore -- for a
# scan that had never run, over a secret that did not exist. Blocking was right; the sentence was
# not, and following its advice would have meant inventing an ignore entry. See the
# classification below.

set -uo pipefail

# Pinned rather than :latest so Docker resolves from cache instead of hitting the registry on
# every commit, and so a gitleaks release cannot change what this hook accepts (#1343).
GITLEAKS_IMAGE="${GITLEAKS_IMAGE:-zricethezav/gitleaks:v8.30.1}"

# Only catches a missing CLI. It does NOT catch a stopped daemon: with colima installed the
# docker binary is on PATH and only the socket is dead, so this passes and the `docker run` below
# is what fails (#1923). The classification after the scan is what covers that case.
if ! command -v docker >/dev/null 2>&1; then
    echo "❌ Error: docker was not found, so gitleaks could not run."
    echo "   Refusing to certify this commit as scanned. Install Docker, or use --no-verify"
    echo "   deliberately if you have checked the diff yourself."
    exit 1
fi

TOPLEVEL=$(git rev-parse --show-toplevel) || exit 1

# --path-format=absolute is required. A bare --git-common-dir returns the relative ".git" when run
# from the main worktree (verified), which is useless as a mount source.
GIT_COMMON=$(git rev-parse --path-format=absolute --git-common-dir) || exit 1

MOUNTS=(-v "$TOPLEVEL":/app)
case "$GIT_COMMON" in
    "$TOPLEVEL"/*)
        # Ordinary clone: the gitdir is inside the working tree, already covered by the mount.
        ;;
    *)
        # Linked worktree. Mount the real gitdir at its OWN absolute path, not somewhere tidier:
        # the `gitdir:` pointer inside .git is absolute, so the path has to match for git to
        # resolve it. Read-only -- scanning never needs to write to the object store.
        MOUNTS+=(-v "$GIT_COMMON":"$GIT_COMMON":ro)
        ;;
esac

SCAN_OUT=$(docker run --rm "${MOUNTS[@]}" -w /app "$GITLEAKS_IMAGE" \
    protect --source=/app --verbose --staged 2>&1)
SCAN_RC=$?

printf '%s\n' "$SCAN_OUT"

# A non-zero exit means "blocked" either way, and that does not change (#1343: this hook's whole
# point is that it cannot be silently weakened). What changes is WHICH of two very different
# states is reported, because they were previously indistinguishable to the person reading it.
#
# gitleaks exits 1 for a finding -- and so does `docker run` when it never reached gitleaks at
# all. Measured on this machine, same CLI:
#
#   DOCKER_HOST=unix:///tmp/no-such.sock docker run ...  -> exit 1
#     "failed to connect to the docker API at unix:///...; check if the path is correct and if
#      the daemon is running"
#   docker run <unpullable image> ...                    -> exit 125
#   gitleaks with a staged ghp_ token                    -> exit 1, "leaks found: 1"
#
# So the exit STATUS cannot tell them apart -- 1 is both. The evidence that gitleaks itself
# reached a verdict is in its output: a `protect --verbose` run always prints "scanned ~N bytes",
# and a finding also prints "leaks found: N". Docker's own failures print neither.
scan_reached_gitleaks() {
    printf '%s' "$SCAN_OUT" | grep -qE 'scanned ~|leaks found'
}

# Best-effort naming of the cause, from docker's own wording. Only ever reached when gitleaks
# produced no verdict, so a successful pull's "Unable to find image" line cannot land here.
docker_failure_cause() {
    case "$SCAN_OUT" in
        *"annot connect to the Docker daemon"*|*"ailed to connect to the docker API"*|*"s the docker daemon running"*)
            echo "the Docker daemon is unreachable" ;;
        *"pull access denied"*|*"failed to resolve reference"*|*"manifest unknown"*|*"Unable to find image"*)
            echo "the $GITLEAKS_IMAGE image could not be pulled" ;;
        *"invalid mount"*|*"mount denied"*|*"bind source path does not exist"*|*"error while creating mount source"*)
            echo "a bind mount was refused" ;;
        *)
            echo "docker exited $SCAN_RC without producing a gitleaks result" ;;
    esac
}

if [ "$SCAN_RC" -ne 0 ]; then
    if scan_reached_gitleaks; then
        echo ""
        echo "❌ Error: Git commit blocked because a secret or private token was detected."
        echo "If this is a false positive, add the secret value to '.gitleaksignore' to allow it."
        echo ""
        exit 1
    fi

    # Deliberately says nothing about allowing a value: there is no finding, so that advice would
    # send someone hunting for a credential that does not exist and inventing an entry to silence
    # a scan that never ran (#1923).
    echo ""
    echo "❌ Error: gitleaks never ran, so NOTHING was scanned -- commit blocked."
    echo "   Cause: $(docker_failure_cause)."
    echo "   This is not a finding. No secret was detected, because nothing was examined."
    echo "   Start Docker (e.g. 'colima start') and commit again, or use --no-verify"
    echo "   deliberately if you have checked the diff yourself."
    echo ""
    exit 1
fi

# Below here gitleaks claims success. Do not take its word for it.

if printf '%s' "$SCAN_OUT" | grep -qiE 'fatal:|not a git repository'; then
    echo ""
    echo "❌ Error: gitleaks could not read this repository, so nothing was scanned."
    echo "   Treating that as a failure rather than as a clean result (#1377)."
    echo ""
    exit 1
fi

# A scan of nothing is legitimate when there is nothing staged, or when the change is only
# deletions -- so this only fires when there is added content that gitleaks should have seen.
#
# Counting ADDED LINES, not staged files (#1649). --diff-filter=ACM still lists a
# deletion-only edit as M, so the old `[ -n "$STAGED" ]` test treated "nothing to scan by
# construction" as "the scan failed to run" and blocked every deletion-only commit. The only
# ways past were --no-verify, which disables the scan entirely and is the opposite of what
# this guard wants, or padding the diff with a cosmetic addition.
#
# The #1377 case this exists for is unaffected: in a worktree there ARE added lines and
# gitleaks still scans 0 bytes, so it still fails closed.
ADDED_LINES=$(git diff --cached --diff-filter=ACM -U0 | grep -cE '^\+[^+]' || true)
if [ "${ADDED_LINES:-0}" -gt 0 ] && printf '%s' "$SCAN_OUT" | grep -qE 'scanned ~0 bytes'; then
    echo ""
    echo "❌ Error: gitleaks scanned 0 bytes although files are staged, so the scan did not run."
    echo "   Treating that as a failure rather than as a clean result (#1377)."
    echo ""
    exit 1
fi

echo "✅ No secrets detected."
exit 0
