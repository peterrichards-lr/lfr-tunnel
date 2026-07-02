#!/usr/bin/env python3

import os
import re
from pathlib import Path
from datetime import datetime

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
    
    for md_file in root_path.rglob("*.md"):
        # Ignore common non-project directories
        parts = md_file.parts
        if any(ignore_dir in parts for ignore_dir in [".venv", "node_modules", ".smoke_venv", ".git"]):
            continue
            
        with open(md_file, "r+", encoding="utf-8") as f:
            content = f.read()
            if not footer_regex.search(content):
                f.write("\n" + timestamp_block)
                files_updated += 1
                print(f"Appended timestamps to: {md_file}")
                
    print(f"Process completed. Updated {files_updated} markdown files.")

if __name__ == "__main__":
    append_timestamps()
