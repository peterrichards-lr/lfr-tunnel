#!/usr/bin/env python3
# scripts/prioritize_issues.py
# Zero-dependency Python script to query issues and prioritize them based on 👍 reactions.
#
# Votes RAISE priority and never lower it (#1338). The label already on an issue is the
# baseline -- a priority someone set deliberately, e.g. p1 on an exploitable bug -- and the
# vote tier can only promote from there. Before this, the vote tier was applied
# unconditionally, so every issue with no reactions was forced to p3 within 6 hours,
# including security work.

import json
import subprocess
import sys

# Lower number == more urgent. Used to compare a deliberate baseline against a vote tier.
PRIORITY_LEVELS = {
    "priority: p1": 1,
    "priority: p2": 2,
    "priority: p3": 3,
}
LEVEL_LABELS = {level: label for label, level in PRIORITY_LEVELS.items()}
DEFAULT_LEVEL = 3  # An issue carrying no priority label yet is treated as p3.


def vote_level(thumbs_up):
    """Map a 👍 count to a priority level, computed absolutely from the live count.

    Absolute rather than relative is what keeps the whole thing idempotent: the result never
    depends on the previous run's output, so repeated runs with unchanged votes converge
    instead of ratcheting an issue up one tier at a time until it hits p1.
    """
    if thumbs_up >= 10:
        return 1
    if thumbs_up >= 5:
        return 2
    return 3


def resolve_priority(labels, thumbs_up):
    """Return the priority label this issue should carry, or None to leave it alone.

    Takes the more urgent of (label already present, tier the votes justify). A manually set
    p1 with no reactions stays p1; a p3 with 12 votes becomes p1.
    """
    present = [PRIORITY_LEVELS[l] for l in labels if l in PRIORITY_LEVELS]
    baseline = min(present) if present else DEFAULT_LEVEL
    return LEVEL_LABELS[min(baseline, vote_level(thumbs_up))]


def thumbs_up_count(issue):
    for group in issue.get("reactionGroups", []):
        if group.get("content", "").upper() in ("THUMB_UP", "+1", "THUMBS_UP"):
            return group.get("users", {}).get("totalCount", 0)
    return 0


def get_open_issues():
    try:
        # Run gh command to get open issues
        cmd = ["gh", "issue", "list", "--state", "open", "--limit", "100", "--json", "number,reactionGroups,labels"]
        result = subprocess.run(cmd, capture_output=True, text=True, check=True)
        return json.loads(result.stdout)
    except subprocess.CalledProcessError as e:
        print(f"Error fetching issues: {e.stderr}", file=sys.stderr)
        return []


def update_issue_priority(issue_num, add_label, remove_labels, dry_run=False):
    try:
        cmd = ["gh", "issue", "edit", str(issue_num)]
        for r_label in remove_labels:
            cmd.extend(["--remove-label", r_label])
        if add_label:
            cmd.extend(["--add-label", add_label])

        # Only run if we actually have changes
        if add_label or remove_labels:
            if dry_run:
                print(f"[DRY RUN] Would update issue #{issue_num}: Added '{add_label}', Removed {remove_labels}")
                return
            subprocess.run(cmd, check=True)
            print(f"Updated issue #{issue_num}: Added '{add_label}', Removed {remove_labels}")
    except subprocess.CalledProcessError as e:
        print(f"Error editing issue #{issue_num}: {e}", file=sys.stderr)


def ensure_priority_labels(dry_run=False):
    labels_to_create = {
        "priority: p1": ("d93f0b", "High priority -- set deliberately, or reached by 10+ 👍"),
        "priority: p2": ("e99695", "Medium priority -- set deliberately, or reached by 5+ 👍"),
        "priority: p3": ("fef2c0", "Low priority -- the default when nothing else applies"),
    }
    try:
        # Check if labels already exist
        cmd = ["gh", "label", "list", "--json", "name"]
        result = subprocess.run(cmd, capture_output=True, text=True, check=True)
        existing_labels = {l.get("name") for l in json.loads(result.stdout)}

        for name, (color, desc) in labels_to_create.items():
            if name not in existing_labels:
                if dry_run:
                    print(f"[DRY RUN] Would create label '{name}'")
                    continue
                create_cmd = ["gh", "label", "create", name, "--color", color, "--description", desc]
                subprocess.run(create_cmd, check=True)
                print(f"Created label '{name}'")
    except subprocess.CalledProcessError as e:
        print(f"Error checking or creating labels: {e.stderr if e.stderr else e}", file=sys.stderr)


def self_test():
    """Table-driven check of resolve_priority, runnable without network access."""
    cases = [
        # (labels, thumbs_up, expected, description)
        (["priority: p1"], 0, "priority: p1", "deliberate p1 with no votes is preserved"),
        (["priority: p2"], 0, "priority: p2", "deliberate p2 with no votes is preserved"),
        ([], 0, "priority: p3", "no label and no votes defaults to p3"),
        (["priority: p3"], 12, "priority: p1", "votes promote p3 to p1"),
        (["priority: p2"], 6, "priority: p2", "vote tier equal to baseline is a no-op"),
        (["priority: p1"], 12, "priority: p1", "already at the top stays there"),
        (["priority: p1"], 6, "priority: p1", "votes never demote a higher baseline"),
        (["bug", "security"], 0, "priority: p3", "non-priority labels are ignored"),
    ]

    failures = 0
    for labels, thumbs, expected, desc in cases:
        got = resolve_priority(labels, thumbs)
        ok = got == expected
        # Idempotency: feeding the result back in must not move it again. This is the property
        # that a naive "escalate one tier" implementation silently violates.
        stable = resolve_priority([got], thumbs) == got
        if not ok or not stable:
            failures += 1
            print(f"FAIL: {desc}: got {got!r}, want {expected!r}, stable={stable}", file=sys.stderr)
        else:
            print(f"ok: {desc}")

    if failures:
        print(f"\n{failures} case(s) failed.", file=sys.stderr)
        return 1
    print(f"\nAll {len(cases)} cases passed.")
    return 0


def main():
    args = sys.argv[1:]
    if "--self-test" in args:
        sys.exit(self_test())
    dry_run = "--dry-run" in args

    if dry_run:
        print("=== Prioritize Issues (DRY RUN -- no changes will be made) ===")

    ensure_priority_labels(dry_run)
    issues = get_open_issues()
    if not issues:
        print("No open issues found or failed to fetch.")
        return

    for issue in issues:
        num = issue.get("number")
        labels = [l.get("name") for l in issue.get("labels", [])]
        thumbs_up = thumbs_up_count(issue)

        target_label = resolve_priority(labels, thumbs_up)

        # Strip any other priority labels so an issue never carries two.
        to_remove = [l for l in labels if l in PRIORITY_LEVELS and l != target_label]
        to_add = target_label if target_label not in labels else None

        if to_add or to_remove:
            print(f"Issue #{num} has {thumbs_up} thumbs-up reactions. Target: '{target_label}'")
            update_issue_priority(num, to_add, to_remove, dry_run)


if __name__ == "__main__":
    main()
