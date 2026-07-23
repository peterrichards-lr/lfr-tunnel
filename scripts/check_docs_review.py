#!/usr/bin/env python3

import os
import re
import sys
import argparse
from datetime import datetime

def find_md_files(root_dir):
    md_files = []
    for dirpath, _, filenames in os.walk(root_dir):
        if '.git' in dirpath or 'node_modules' in dirpath or '.venv' in dirpath:
            continue
        for f in filenames:
            if f.endswith('.md'):
                md_files.append(os.path.join(dirpath, f))
    return md_files

def parse_timestamps(filepath):
    pattern = re.compile(r'\*Last Updated:\s*([\d\-]+)\*\s*\|\s*\*Last Reviewed:\s*([\d\-]+)\*')
    with open(filepath, 'r', encoding='utf-8') as f:
        content = f.read()
        match = pattern.search(content)
        if match:
            try:
                updated = datetime.strptime(match.group(1), "%Y-%m-%d")
                reviewed = datetime.strptime(match.group(2), "%Y-%m-%d")
                return updated, reviewed
            except ValueError:
                pass
    return None, None

def main():
    parser = argparse.ArgumentParser(description="Check documentation files for review staleness.")
    parser.add_argument('--max-review-days', type=int, default=90, help="Max days since last review before warning")
    parser.add_argument('--max-update-days', type=int, default=14, help="Max days between update and review before warning")
    parser.add_argument('--dir', type=str, default=".", help="Directory to scan")
    
    args = parser.parse_args()
    now = datetime.now()
    
    files = find_md_files(args.dir)
    issues_found = False
    
    print(f"Scanning {len(files)} markdown files...")
    print(f"Rules: Max Review Days = {args.max_review_days}, Max Update Days = {args.max_update_days}\n")
    
    for f in files:
        updated, reviewed = parse_timestamps(f)
        
        if updated is None or reviewed is None:
            continue
            
        review_age_days = (now - reviewed).days
        drift_days = (now - updated).days
        
        if review_age_days > args.max_review_days:
            print(f"[STALE] {f}: Last reviewed {review_age_days} days ago (limit {args.max_review_days}).")
            issues_found = True
            
        if drift_days > args.max_update_days:
            print(f"[OUTDATED] {f}: Last updated {drift_days} days ago (limit {args.max_update_days}).")
            issues_found = True

    if not issues_found:
        print("✅ All documentation files are up to date and well-reviewed.")
    else:
        print("\n❌ Found documentation review issues.")
        sys.exit(1)

if __name__ == "__main__":
    main()
