#!/usr/bin/env python3

import os
import re
import sys
import argparse
from datetime import datetime

def find_md_files(root_dir):
    md_files = []
    for dirpath, _, filenames in os.walk(root_dir):
        if '.git' in dirpath or 'node_modules' in dirpath:
            continue
        for f in filenames:
            if f.endswith('.md'):
                md_files.append(os.path.join(dirpath, f))
    return md_files

def parse_timestamps(filepath):
    updated = None
    reviewed = None
    updated_pattern = re.compile(r'\*Last Updated:\s*([0-9]{4}-[0-9]{2}-[0-9]{2})\*')
    reviewed_pattern = re.compile(r'\*Last Reviewed:\s*([0-9]{4}-[0-9]{2}-[0-9]{2})\*')
    
    with open(filepath, 'r', encoding='utf-8') as f:
        content = f.read()
        u_match = updated_pattern.search(content)
        r_match = reviewed_pattern.search(content)
        if u_match:
            try:
                updated = datetime.strptime(u_match.group(1), "%Y-%m-%d")
            except ValueError:
                pass
        if r_match:
            try:
                reviewed = datetime.strptime(r_match.group(1), "%Y-%m-%d")
            except ValueError:
                pass
    return updated, reviewed

def main():
    parser = argparse.ArgumentParser(description="Check documentation files for review staleness.")
    parser.add_argument('--max-review-age', type=int, default=90, help="Max days since last review before warning")
    parser.add_argument('--max-drift', type=int, default=14, help="Max days between update and review before warning")
    parser.add_argument('--dir', type=str, default=".", help="Directory to scan")
    
    args = parser.parse_args()
    now = datetime.now()
    
    files = find_md_files(args.dir)
    issues_found = False
    
    print(f"Scanning {len(files)} markdown files...")
    print(f"Rules: Max Review Age = {args.max_review_age} days, Max Drift = {args.max_drift} days\n")
    
    for f in files:
        updated, reviewed = parse_timestamps(f)
        
        if updated is None and reviewed is None:
            # We don't report files that just don't have timestamps at all, or we could?
            continue
            
        if updated is None or reviewed is None:
            print(f"[WARNING] {f}: Missing either Updated or Reviewed timestamp.")
            issues_found = True
            continue
            
        review_age_days = (now - reviewed).days
        drift_days = (updated - reviewed).days
        
        if review_age_days > args.max_review_age:
            print(f"[STALE] {f}: Last reviewed {review_age_days} days ago (limit {args.max_review_age}).")
            issues_found = True
            
        if drift_days > args.max_drift:
            print(f"[DRIFT] {f}: Last updated is {drift_days} days newer than last review (limit {args.max_drift}).")
            issues_found = True

    if not issues_found:
        print("✅ All documentation files are up to date and well-reviewed.")
    else:
        print("\n❌ Found documentation review issues.")
        sys.exit(1)

if __name__ == "__main__":
    main()
