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
3. You must also establish a `scripts/check_docs_review.py` script that parses this footer using the regex `r"\*Last Updated: ([\d\-]+)\* \| \*Last Reviewed: ([\d\-]+)\*"` and accepts arguments for `--max-review-days` and `--max-update-days`. This is the single canonical checker for this topic -- do not create a second near-duplicate script; if you find one, consolidate it into this one instead (see issue #933, which is exactly that mistake happening once already).
4. In full-repo mode the script alerts via `sys.exit(1)` if any documents have exceeded these self-reported-age thresholds. It also has a **changed-files mode** (`--changed-files <paths...>`) that instead cross-checks each given file's footer against `git log`'s real last-modified date for that file, flagging drift beyond `--max-git-drift-days`. This is the check that actually catches someone editing a file's content without bumping its own footer -- a self-reported-only check can never catch that, since the footer can simply be wrong and nothing before checked it against anything external. CI runs this mode automatically against every PR's changed `.md` files ("Documentation Review Check" in `.github/workflows/ci.yml`), so this is no longer purely an on-demand, agent-invoked check for files a PR actually touches. The full-repo audit over the whole repository is still agent-invoked only, by design, since it would otherwise gate every PR on the repo's pre-existing staleness backlog. The published pages are the exception: CI runs `--review-only` over `docs/` on every PR (see below).

**Public commitment.** The site's [About page](../../../docs/about.md) tells readers that every page shows when it was last updated and last reviewed. Changing or bypassing this rule requires updating the About page in the same PR. On the site side it is held by `scripts/check_docs_rendered.py` (`[UNDATED-PAGE]`, the template's shared check, #2407), which fails any built page that does not render the footer. The review date is held to **180 days** on every PR, whatever the PR changes, by `check_docs_review.py --dir docs --review-only --max-review-days 180` in the Documentation Review Check job (#2389). When a page passes that limit, the next PR fails until someone reviews the page and bumps its `Last Reviewed` date. The footer must be the page's last line; a missing, unparseable or future-dated one fails too, as does an earlier footer-shaped example standing in for a stale real one.

## Documentation Review Requirements (Active Rule)
5. **Post-Implementation Review**: After implementing any code or feature change, you MUST review the project documentation to see if updates are needed. Note that a single code change may require updates to multiple documents (e.g., `README.md`, `architecture.md`, `spec.md`).
6. **Timestamp Updates**:
   - If no changes are needed but you reviewed a document to verify this, you MUST update its `Last Reviewed` date.
   - If an update is required, you MUST update both the `Last Reviewed` and `Last Updated` dates to reflect the fact the documentation was reviewed and updated.
7. **New Documentation**: If no documentation exists around the implemented change and it makes sense to document it, you MUST create a new document for it unless it can be logically added as a new section to an existing document.

If you ever ask the AI to "review the project documentation for outdated files", it will automatically know to look for or construct these scripts.

<!-- markdownlint-disable MD049 -->
---
*Last Updated: 2026-10-16* | *Last Reviewed: 2026-10-16*
