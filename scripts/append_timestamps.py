#!/usr/bin/env python3

import os
import re
from pathlib import Path
from datetime import datetime

IGNORE_DIRS = {".git", ".venv", "node_modules", ".smoke_venv"}


def is_nested_worktree_root(path):
    """True if path is the root of a git worktree nested inside the tree being walked.

    A worktree root carries `.git` as a FILE (a `gitdir:` pointer); a real repository root
    carries it as a directory. Testing for that identifies the class itself rather than a
    directory name, so it keeps working whatever the tooling calls its worktree directory --
    where hardcoding `.claude` would not (#1815).
    """
    return os.path.isfile(os.path.join(path, ".git"))

def append_timestamps(root_dir="."):
    root_path = Path(root_dir)
    today = datetime.now().strftime("%Y-%m-%d")
    
    timestamp_block = f"""
<!-- markdownlint-disable MD049 -->
---
*Last Updated: {today}* | *Last Reviewed: {today}*
"""
    
    # Regex to check if a footer already exists
    footer_regex = re.compile(r'\*Last Updated: ([\d\-]+)\* \| \*Last Reviewed: ([\d\-]+)\*')
    
    files_updated = 0
    
    # os.walk rather than rglob so a directory can be PRUNED rather than each file under it
    # filtered afterwards. That matters here more than in a read-only checker: this script
    # WRITES, and a nested git worktree is a second checkout of this same repo, so descending
    # into one stamps footers into copies of files that already have them (#1815).
    for dirpath, dirnames, filenames in os.walk(root_path):
        dirnames[:] = [
            d for d in dirnames
            if d not in IGNORE_DIRS
            and not is_nested_worktree_root(os.path.join(dirpath, d))
        ]
        for name in sorted(filenames):
            if not name.endswith(".md"):
                continue
            md_file = Path(dirpath) / name

            with open(md_file, "r+", encoding="utf-8") as f:
                content = f.read()
                if not footer_regex.search(content):
                    f.write("\n" + timestamp_block)
                    files_updated += 1
                    print(f"Appended timestamps to: {md_file}")
                
    print(f"Process completed. Updated {files_updated} markdown files.")

if __name__ == "__main__":
    append_timestamps()
