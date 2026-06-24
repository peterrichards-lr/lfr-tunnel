#!/usr/bin/env python3
# scripts/prioritize_issues.py
# Zero-dependency Python script to query issues and prioritize them based on 👍 reactions.

import json
import subprocess
import sys

def get_open_issues():
    try:
        # Run gh command to get open issues
        cmd = ["gh", "issue", "list", "--state", "open", "--limit", "100", "--json", "number,reactionGroups,labels"]
        result = subprocess.run(cmd, capture_output=True, text=True, check=True)
        return json.loads(result.stdout)
    except subprocess.CalledProcessError as e:
        print(f"Error fetching issues: {e.stderr}", file=sys.stderr)
        return []

def update_issue_priority(issue_num, add_label, remove_labels):
    try:
        cmd = ["gh", "issue", "edit", str(issue_num)]
        for r_label in remove_labels:
            cmd.extend(["--remove-label", r_label])
        if add_label:
            cmd.extend(["--add-label", add_label])
        
        # Only run if we actually have changes
        if add_label or remove_labels:
            subprocess.run(cmd, check=True)
            print(f"Updated issue #{issue_num}: Added '{add_label}', Removed {remove_labels}")
    except subprocess.CalledProcessError as e:
        print(f"Error editing issue #{issue_num}: {e}", file=sys.stderr)

def main():
    issues = get_open_issues()
    if not issues:
        print("No open issues found or failed to fetch.")
        return

    for issue in issues:
        num = issue.get("number")
        labels = [l.get("name") for l in issue.get("labels", [])]
        reactions = issue.get("reactionGroups", [])
        
        # Calculate thumbs up count
        thumbs_up = 0
        for group in reactions:
            content = group.get("content", "").upper()
            if content in ("THUMB_UP", "+1", "THUMBS_UP"):
                thumbs_up = group.get("users", {}).get("totalCount", 0)
                break
        
        # Determine target label
        if thumbs_up >= 10:
            target_label = "priority: p1"
        elif thumbs_up >= 5:
            target_label = "priority: p2"
        else:
            target_label = "priority: p3"

        # Check existing priority labels
        priority_labels = ["priority: p1", "priority: p2", "priority: p3"]
        current_priorities = [l for l in labels if l in priority_labels]

        # Calculate labels to remove
        to_remove = [l for l in current_priorities if l != target_label]
        
        # Determine if we need to add the correct target label
        if target_label not in labels:
            to_add = target_label
        else:
            to_add = None

        if to_add or to_remove:
            print(f"Issue #{num} has {thumbs_up} thumbs-up reactions. Target: '{target_label}'")
            update_issue_priority(num, to_add, to_remove)

if __name__ == "__main__":
    main()
