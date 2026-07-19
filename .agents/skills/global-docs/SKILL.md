---
name: global-docs
description: Global requirements for Markdown documentation timestamps and review processes. Activate this skill when creating, modifying, or reviewing markdown files.
---

# Global Documentation Timestamps Rule

**Objective**: Ensure that all markdown documents across all projects maintain a consistent "Last Updated" and "Last Reviewed" timestamp footer to track documentation decay and relevance.

**Rules**:
1. Every time you create or modify a Markdown (`.md`) file, you MUST ensure it has a footer block at the very end in the exact format:
   `<!-- markdownlint-disable MD049 -->`
   `---`
   `*Last Updated: YYYY-MM-DD* | *Last Reviewed: YYYY-MM-DD*`
2. If working in a new repository without these footers, implement a Python script named `scripts/append_timestamps.py` using `Path.rglob` to recursively scan all `.md` files (ignoring `.venv`, `node_modules`, `.smoke_venv` etc.) and append this block if it does not exist.
3. You must also establish a `scripts/check_docs_review.py` script that parses this footer using the regex `r"\*Last Updated: ([\d\-]+)\* \| \*Last Reviewed: ([\d\-]+)\*"` and accepts arguments for `--max-review-days`, `--max-update-days`, and `--max-gap-days`. 
4. The script should alert the user via `sys.exit(1)` if any documents have exceeded these threshold values.

If you ever ask the AI to "review the project documentation for outdated files", it will automatically know to look for or construct these scripts.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-07-19* | *Last Reviewed: 2026-07-19*
