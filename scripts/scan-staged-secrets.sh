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

set -uo pipefail

# Pinned rather than :latest so Docker resolves from cache instead of hitting the registry on
# every commit, and so a gitleaks release cannot change what this hook accepts (#1343).
GITLEAKS_IMAGE="${GITLEAKS_IMAGE:-zricethezav/gitleaks:v8.30.1}"

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

STAGED=$(git diff --cached --name-only --diff-filter=ACM)

SCAN_OUT=$(docker run --rm "${MOUNTS[@]}" -w /app "$GITLEAKS_IMAGE" \
    protect --source=/app --verbose --staged 2>&1)
SCAN_RC=$?

printf '%s\n' "$SCAN_OUT"

if [ "$SCAN_RC" -ne 0 ]; then
    echo ""
    echo "❌ Error: Git commit blocked because a secret or private token was detected."
    echo "If this is a false positive, add the secret value to '.gitleaksignore' to allow it."
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
# deletions -- so this only fires when files were actually staged for add/copy/modify.
if [ -n "$STAGED" ] && printf '%s' "$SCAN_OUT" | grep -qE 'scanned ~0 bytes'; then
    echo ""
    echo "❌ Error: gitleaks scanned 0 bytes although files are staged, so the scan did not run."
    echo "   Treating that as a failure rather than as a clean result (#1377)."
    echo ""
    exit 1
fi

echo "✅ No secrets detected."
exit 0
