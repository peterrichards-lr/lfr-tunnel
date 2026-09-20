#!/usr/bin/env python3
"""Fail when a documentation page is reachable only by search or a direct URL.

The routing layer here is already good -- docs/README.md describes every guide in one line and
groups them by audience, and mkdocs.yml's nav uses the same grouping rather than a second
taxonomy. What nothing checked is whether those landing pages stay COMPLETE.

They had not. `exposing-any-http-service.md` and `server/aws_setup_guide.md` were on the site
and in the hub, and absent from the repository README -- which is where people actually arrive,
since GitHub is the front door for a project distributed through it. Two guides existed that a
reader starting from the README could not find (#2081).

`scripts/check-mkdocs-nav.cjs` already guards the nav this way. This is the same bargain for the
two prose landing pages, and it reuses that file's `# nav-exclude:` list so a page deliberately
kept off the site is not then demanded here.
"""

import os
import re
import sys

HUB = os.path.join("docs", "README.md")
ROOT_README = "README.md"


def excluded_pages(mkdocs="mkdocs.yml"):
    """Pages mkdocs.yml deliberately keeps off the site, by its own `# nav-exclude:` list."""
    with open(mkdocs, encoding="utf-8") as handle:
        return {
            m.group(1).strip()
            for m in re.finditer(r"^#\s*nav-exclude:\s*(\S+)", handle.read(), re.M)
        }


def pages_on_disk(root="docs"):
    found = set()
    for dirpath, _, filenames in os.walk(root):
        for name in filenames:
            if name.endswith(".md"):
                rel = os.path.relpath(os.path.join(dirpath, name), root)
                if rel != "README.md":  # the hub does not need to link itself
                    found.add(rel)
    return found


def linked_from(path, prefix):
    """Markdown link targets under `prefix`, normalised relative to docs/."""
    with open(path, encoding="utf-8") as handle:
        body = handle.read()

    out = set()
    for match in re.finditer(r"\]\(([^)]+)\)", body):
        target = match.group(1).split("#")[0].strip()
        if not target.endswith(".md") or target.startswith("http"):
            continue
        if prefix and not target.startswith(prefix):
            continue
        out.add(os.path.normpath(os.path.relpath(target, prefix) if prefix else target))
    return out


def main():
    skip = excluded_pages()
    expected = pages_on_disk() - skip

    if len(expected) < 5:
        print(f"[NO-SCAN] only {len(expected)} page(s) found under docs/. That is too few to be "
              f"this repository, and an empty set would satisfy every assertion below.")
        return 1

    issues = 0
    for landing, prefix in ((HUB, ""), (ROOT_README, "docs/")):
        missing = sorted(expected - linked_from(landing, prefix))
        if missing:
            issues += len(missing)
            print(f"[UNREACHABLE] {landing} links to none of: {missing}")
            print(f"    A page no landing page names is reachable only by site search or by "
                  f"knowing its URL.")
            print(f"    Add it, or record it in mkdocs.yml as `# nav-exclude: <path> -- <why>`.")

    if issues:
        print(f"\n❌ {issues} page(s) missing from a landing page.")
        return 1

    print(f"✅ {len(expected)} page(s): every one is reachable from both landing pages "
          f"({len(skip)} deliberately excluded).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
