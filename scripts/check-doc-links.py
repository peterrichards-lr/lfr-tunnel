#!/usr/bin/env python3
"""Fail when source code links to a documentation file or heading that does not exist.

These rot faster than the documents themselves, because nothing treats them as documentation.
`pkg/config/metadata.go` held a URL ending `#using-the-docker-wrapper-edr-bypass` that no
heading has ever matched -- and the gateway serves it to the portal, where the dashboard makes
it the href of a live link (#2081). A reader who clicked it landed at the top of the guide
instead of at the Docker method, and nothing anywhere failed.

Asserts the CLASS: every link into this repository's own docs, from any source file, must name
a file that exists and (when it carries a fragment) a heading that exists. Adding a new link
needs no change here.

Anchors are computed with GITHUB's slugifier, because these are github.com/blob/ URLs. A
static-site generator slugifies differently, so a link to one surface must not be "fixed"
against the other's rules -- that is how an anchor gets broken while looking corrected.
"""

import re
import subprocess
import sys

# https://github.com/<owner>/<repo>/blob/<ref>/<path>[#anchor]
BLOB = re.compile(
    r"https://github\.com/[\w.-]+/[\w.-]+/blob/[\w.-]+/"
    r"(?P<path>[\w./-]+\.md)"
    r"(?:\#(?P<anchor>[\w-]+))?"
)

SOURCE_SUFFIXES = (
    ".go", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".html", ".sh", ".ps1", ".yml", ".yaml"
)


def github_slug(heading):
    """GitHub's anchor rules: lowercase, drop punctuation and emoji, spaces to hyphens."""
    text = heading.strip().lower()
    text = re.sub(r"[^\w\- ]", "", text)
    return text.replace(" ", "-")


def headings(path):
    with open(path, encoding="utf-8") as handle:
        return {
            github_slug(line.lstrip("#").strip())
            for line in handle
            if re.match(r"^#{1,6}\s", line)
        }


def main():
    listed = subprocess.run(
        ["git", "ls-files"], capture_output=True, text=True, check=True
    ).stdout.split()
    sources = [
        p
        for p in listed
        if p.endswith(SOURCE_SUFFIXES) and "node_modules" not in p and "/vendor/" not in p
    ]

    issues = 0
    checked = 0
    cache = {}

    for source in sources:
        try:
            with open(source, encoding="utf-8") as handle:
                body = handle.read()
        except (OSError, UnicodeDecodeError):
            continue

        for match in BLOB.finditer(body):
            path, anchor = match.group("path"), match.group("anchor")
            checked += 1

            try:
                if path not in cache:
                    cache[path] = headings(path)
            except FileNotFoundError:
                issues += 1
                print(
                    f"[DEAD-LINK] {source}: links to {path!r}, which does not exist.\n"
                    f"    Anyone following it gets a 404."
                )
                continue

            if anchor and anchor not in cache[path]:
                issues += 1
                print(
                    f"[DEAD-ANCHOR] {source}: links to {path}#{anchor}\n"
                    f"    No heading in that file slugifies to {anchor!r}. GitHub silently "
                    f"drops an unknown fragment, so the reader lands at the top of the page "
                    f"with no error -- looking like the link worked."
                )

    if issues:
        print(f"\n❌ {issues} broken documentation link(s) in source.")
        return 1

    print(f"✅ {checked} documentation link(s) in source: every file and heading resolves.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
