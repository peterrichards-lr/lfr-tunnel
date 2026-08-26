#!/bin/bash
# Refuses a commit whose author email GitHub cannot map to an account (#1384).
#
# The master ruleset sets require_extra_approval_for_unattributed_changes with
# required_approving_review_count: 0. On a single-maintainer repo that is unsatisfiable --
# an unattributed head commit needs one approving review, and the only maintainer cannot
# approve their own PR. So it is not a delay, it is a dead end: no admin override, no
# merge, and every required check green the whole time.
#
# That is why this runs at commit rather than push: the fix is a history rewrite, and one
# commit is far cheaper to redo than ten.
#
# What it can and cannot know: attribution is decided by GitHub, from the addresses
# verified on the account. A local hook cannot query that without network and auth, so
# this checks the shape instead -- a users.noreply.github.com address always attributes,
# and anything else is accepted only if it has been explicitly allowlisted. Deliberately
# conservative: a false refusal costs one `git config` command, a false pass costs a
# permanently stuck PR that nothing else in the repo will explain.

set -uo pipefail

EMAIL=$(git config user.email 2>/dev/null || true)

if [ -z "$EMAIL" ]; then
    echo "❌ No git user.email is set, so the commit cannot be attributed." >&2
    echo "   Set one GitHub can map to your account:" >&2
    echo "     git config user.email '<id>+<username>@users.noreply.github.com'" >&2
    exit 1
fi

# Always attributable: GitHub issues these addresses itself.
case "$EMAIL" in
    *@users.noreply.github.com)
        exit 0
        ;;
esac

# Escape hatch for an address verified on the account that this hook cannot see.
# Comma-separated, e.g.
#   git config lft.attributableEmails 'me@example.com,other@example.com'
ALLOWED=$(git config lft.attributableEmails 2>/dev/null || true)
if [ -n "$ALLOWED" ]; then
    # Comma-delimited membership test with no loop and no subshell. A `while read` in a
    # pipeline runs in a subshell, so an `exit` inside it cannot end this script -- the
    # first version of this check did exactly that and silently refused an allowlisted
    # address. `case` is also bash 3.2 clean, which the portability test requires.
    case ",${ALLOWED}," in
        *",${EMAIL},"*)
            exit 0
            ;;
    esac
fi

echo "❌ Commit refused: git user.email is '$EMAIL'." >&2
echo "" >&2
echo "   GitHub cannot map that to an account, so the commit lands unattributed." >&2
echo "   master requires an extra approval for unattributed changes and there is no" >&2
echo "   second maintainer to give it, so the PR would be permanently unmergeable" >&2
echo "   with every check green (#1384)." >&2
echo "" >&2
echo "   Fix:" >&2
echo "     git config user.email '<id>+<username>@users.noreply.github.com'" >&2
echo "" >&2
echo "   Or, if this address really is verified on the account:" >&2
echo "     git config lft.attributableEmails '$EMAIL'" >&2
exit 1
