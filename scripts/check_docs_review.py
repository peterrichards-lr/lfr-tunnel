#!/usr/bin/env python3
"""Check markdown documentation files for stale review/update timestamps.

Two independent modes:

1. Full-repo audit (default, no FILES args): checks every .md file's
   self-reported staleness -- "Last Reviewed" older than --max-review-days,
   or "Last Updated" older than --max-update-days. Informational; not meant
   to gate a PR on the whole repo's pre-existing backlog.

2. Changed-files mode (pass explicit FILES, e.g. from a PR diff, with
   --changed-files): only checks that each given file has a footer at all,
   and that the footer's "Last Updated" date isn't lagging behind git's real
   last-modified date for that file -- i.e. "did whoever just edited this
   file's content forget to bump its own footer?" This is the check that
   actually catches the bug class in issue #933: CONTRIBUTING.md's footer
   claimed 2026-07-10 while `git log -1 --format=%ad -- CONTRIBUTING.md`
   showed the file was really last modified 2026-07-28 -- self-reported-only
   checks can never catch that; only a cross-check against real git history
   can.
"""

import os
import re
import sys
import argparse
import subprocess
from datetime import datetime

FOOTER_PATTERN = re.compile(r'\*Last Updated:\s*([\d\-]+)\*\s*\|\s*\*Last Reviewed:\s*([\d\-]+)\*')
IGNORE_DIRS = {'.git', 'node_modules', '.venv', '.smoke_venv'}


def is_nested_worktree_root(path):
    """True if path is the root of a git worktree nested inside the tree being walked.

    Its contents are a second checkout of THIS repository, so every file under it is a duplicate.
    A walk that descends into one reports each finding once per worktree, naming paths that are
    copies of the file rather than the file itself (#1815).

    The test is `.git` present as a FILE: a real repository root carries `.git` as a directory, a
    worktree root carries it as a regular file holding a `gitdir:` pointer. That identifies the
    class itself rather than a directory name, so it keeps working whatever the tooling calls its
    worktree directory -- where hardcoding `.claude` would not.
    """
    return os.path.isfile(os.path.join(path, '.git'))


def find_md_files(root_dir):
    md_files = []
    for dirpath, dirnames, filenames in os.walk(root_dir):
        dirnames[:] = [
            d for d in dirnames
            if d not in IGNORE_DIRS
            and not is_nested_worktree_root(os.path.join(dirpath, d))
        ]
        for f in filenames:
            if f.endswith('.md'):
                md_files.append(os.path.join(dirpath, f))
    return md_files


def parse_timestamps(filepath):
    with open(filepath, 'r', encoding='utf-8') as f:
        content = f.read()
    match = FOOTER_PATTERN.search(content)
    if not match:
        return None, None
    try:
        updated = datetime.strptime(match.group(1), "%Y-%m-%d")
        reviewed = datetime.strptime(match.group(2), "%Y-%m-%d")
        return updated, reviewed
    except ValueError:
        return None, None


def git_last_modified(filepath):
    """Returns the real last-modified date for filepath per git history, or
    None if the file isn't tracked, has no history in this checkout (e.g. a
    shallow clone), or git isn't available at all."""
    try:
        result = subprocess.run(
            ['git', 'log', '-1', '--format=%ad', '--date=short', '--', filepath],
            capture_output=True, text=True, timeout=10,
        )
    except (OSError, subprocess.TimeoutExpired):
        return None
    if result.returncode != 0 or not result.stdout.strip():
        return None
    try:
        return datetime.strptime(result.stdout.strip(), "%Y-%m-%d")
    except ValueError:
        return None


def check_changed_files(files, max_git_drift_days):
    """Changed-files mode: require a footer, and flag it if it lags behind
    git's real last-modified date by more than max_git_drift_days.

    Anti-vacuity (#1779). Measured before the guard existed: given two .md paths that
    do not exist, this printed

        ✅ All documentation files are up to date and well-reviewed.

    and exited 0. Every named file was dropped by the `os.path.isfile` filter without a
    word. CI builds the list with `git diff --name-only ... -- '*.md'` and passes it
    unquoted from the repository root, so a change of working directory, a path with a
    space in it, or a diff computed against the wrong base all arrive here as paths that
    do not resolve -- and the gate reported a clean bill of health for zero files.

    A file the diff names but disk does not have is not automatically an error, though:
    a PR that DELETES a markdown file names it too, and failing that would be a false
    refusal on a legitimate change. The two are separated by asking git, which is the
    same question `git_last_modified` already asks -- a deleted file has history, a
    mistyped or wrongly-rooted path has none.
    """
    issues_found = False
    md_named = 0
    md_examined = 0
    unresolvable = []
    for f in files:
        if not f.endswith('.md'):
            # Not markdown. CI passes its whole changed-file list, so this is the normal,
            # correct skip and must not count towards the floor in either direction.
            continue
        md_named += 1
        if not os.path.isfile(f):
            if git_last_modified(f) is None:
                unresolvable.append(f)
            # else: git knows this path, so it is a deletion -- nothing left to check.
            continue
        md_examined += 1
        updated, _reviewed = parse_timestamps(f)
        if updated is None:
            print(f"[MISSING-FOOTER] {f}: no 'Last Updated / Last Reviewed' footer found. "
                  f"Every markdown file must have one (see .agents/skills/global-docs/SKILL.md).")
            issues_found = True
            continue

        real_last_modified = git_last_modified(f)
        if real_last_modified is None:
            continue  # untracked, or no history visible in this checkout -- can't cross-check

        git_drift_days = (real_last_modified - updated).days
        if git_drift_days > max_git_drift_days:
            print(f"[GIT-DRIFT] {f}: footer says Last Updated {updated.date()}, but git shows the file "
                  f"was actually last modified {real_last_modified.date()} "
                  f"({git_drift_days} days newer than the footer claims). Bump the footer's dates.")
            issues_found = True

    if unresolvable:
        print(f"[NO-SCAN] {len(unresolvable)} markdown path(s) were named but could not be read, "
              f"and git has no history for them either, so they are not deletions:")
        for f in unresolvable[:10]:
            print(f"    {f}")
        if len(unresolvable) > 10:
            print(f"    …and {len(unresolvable) - 10} more")
        print("  Nothing was checked for these. Most likely the caller's working directory is not "
              "the repository root, or the changed-file list was built against the wrong base.")
        issues_found = True
    elif md_named > 0 and md_examined == 0:
        # Every named .md was a deletion. Deliberately NOT a failure: a PR that only removes
        # markdown has nothing left to read, and refusing it would be a false refusal on a
        # legitimate change. Said out loud so the run is distinguishable from one that examined
        # something -- which is the whole property, and is not the same as failing.
        print(f"[NO-SCAN] {md_named} markdown path(s) were named and all of them are deletions, "
              "so nothing was examined. Not a failure, but not evidence of anything either.")

    return issues_found


def check_full_repo(root_dir, max_review_days, max_update_days):
    files = find_md_files(root_dir)
    now = datetime.now()
    issues_found = False

    print(f"Scanning {len(files)} markdown files...")
    print(f"Rules: Max Review Days = {max_review_days}, Max Update Days = {max_update_days}\n")

    # Anti-vacuity floor (#1779). Measured before the guard existed: pointed at an empty
    # directory this printed "Scanning 0 markdown files..." followed by
    # "✅ All documentation files are up to date and well-reviewed." and exited 0 -- a
    # green audit of nothing, reading exactly like a green audit of the repo.
    #
    # The routes there are all quiet ones: a wrong --dir, being run from somewhere other
    # than the repository root, or the nested-worktree skip in find_md_files() widening
    # until it swallows the tree it was meant to walk (#1815 added that skip; a bug in it
    # would land here). 37 tracked .md files today, so the floor has generous headroom and
    # only a genuine collapse of the walk reaches it.
    min_files = int(os.environ.get('LFT_DOCS_MIN_FILES', '10'))
    if len(files) < min_files:
        print(f"[NO-SCAN] found {len(files)} markdown file(s) under {root_dir}, below the floor "
              f"of {min_files}. A scan of nothing must not report success (#1779).")
        print("  Check the working directory and --dir. If the repository genuinely has fewer "
              "docs now, lower LFT_DOCS_MIN_FILES deliberately.")
        return True

    for f in files:
        updated, reviewed = parse_timestamps(f)
        if updated is None or reviewed is None:
            continue

        review_age_days = (now - reviewed).days
        update_age_days = (now - updated).days

        if review_age_days > max_review_days:
            print(f"[STALE] {f}: Last reviewed {review_age_days} days ago (limit {max_review_days}).")
            issues_found = True
        if update_age_days > max_update_days:
            print(f"[OUTDATED] {f}: Last updated {update_age_days} days ago (limit {max_update_days}).")
            issues_found = True
    return issues_found


def main():
    parser = argparse.ArgumentParser(description="Check documentation files for review staleness.")
    parser.add_argument('files', nargs='*', help="Specific files to check (implies --changed-files mode)")
    parser.add_argument('--changed-files', action='store_true',
                         help="Only run the footer-presence + git-drift checks against the given FILES "
                              "(e.g. a PR's changed files), not a full-repo age audit")
    parser.add_argument('--max-review-days', type=int, default=90, help="Max days since last review before warning")
    parser.add_argument('--max-update-days', type=int, default=14, help="Max days since last update before warning")
    parser.add_argument('--max-git-drift-days', type=int, default=3,
                         help="Max days the footer's Last Updated date may lag behind git's real last-modified "
                              "date for that file before warning")
    parser.add_argument('--dir', type=str, default=".", help="Directory to scan in full-repo mode")

    args = parser.parse_args()

    if args.files or args.changed_files:
        if not args.files:
            print("No changed markdown files to check.")
            sys.exit(0)
        issues_found = check_changed_files(args.files, args.max_git_drift_days)
    else:
        issues_found = check_full_repo(args.dir, args.max_review_days, args.max_update_days)

    if not issues_found:
        print("✅ All documentation files are up to date and well-reviewed.")
    else:
        print("\n❌ Found documentation review issues.")
        sys.exit(1)


if __name__ == "__main__":
    main()
