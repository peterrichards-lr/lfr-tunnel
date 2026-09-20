#!/usr/bin/env python3
"""Fail when a code fence is left open and swallows the prose that follows it.

Found by building the site and reading the OUTPUT (#2081). docs/server/setup_guide.md opened
```bash to show two Nginx commands and never closed it, so `### 3.4. Configure Nginx
Maintenance Mode` rendered as a bash COMMENT inside a code block: not a heading, absent from
the page's table of contents, no anchor to link to. Nine lines of a documented procedure were
shown to readers as shell script.

Two assertions were tried before this one, and both were wrong in instructive ways:

  * "fences must balance" -- they DID balance in the broken file. A stray ```bash further down
    was eventually closed by a later bare ```, so the document self-healed globally while
    staying broken locally.
  * "no heading may sit inside a fence" -- fires on every `#` shell or YAML comment in every
    normal code sample. Hundreds of false positives on a healthy corpus.

What discriminates is a fence OPENER met while already inside a fence. CommonMark closes a
fence only with the same character, at least as long, and no info string -- so ```bash cannot
close anything. Meeting one mid-block means the block above it was never closed. Measured
across all 36 tracked markdown files: exactly 2 occurrences, both genuine defects, no false
positives.
"""

import re
import subprocess
import sys

FENCE = re.compile(r"^(\s*)(`{3,}|~{3,})(.*)$")


def unclosed_fences(path):
    """Yield (kind, line, text, opened_at) for every fence that is never closed.

    Two shapes, both the same defect:
      "stray-opener" -- a fence opener met while already inside a fence
      "eof"          -- a fence still open when the file ends

    The second was missed by the first version of this check. It was only found by
    deliberately appending an unterminated block and watching the guard stay green -- which
    is the whole argument for breaking a guard on purpose before trusting it.
    """
    with open(path, encoding="utf-8") as handle:
        lines = handle.read().split("\n")

    in_fence = False
    char = ""
    length = 0
    opened_at = 0

    for number, line in enumerate(lines, 1):
        fence = FENCE.match(line)
        if fence is None:
            continue

        marker, info = fence.group(2), fence.group(3).strip()
        if not in_fence:
            in_fence, char, length, opened_at = True, marker[0], len(marker), number
            continue

        if marker[0] == char and len(marker) >= length and not info:
            in_fence = False
        else:
            # Cannot be a close, so the block opened above was never terminated.
            yield "stray-opener", number, line.strip(), opened_at
            # Treat the stray opener as the start of the next block, so one missing close
            # does not cascade into a complaint about every fence below it.
            char, length, opened_at = marker[0], len(marker), number

    if in_fence:
        yield "eof", len(lines), "", opened_at


def main():
    paths = sys.argv[1:]
    if not paths:
        listed = subprocess.run(
            ["git", "ls-files", "*.md"], capture_output=True, text=True, check=True
        ).stdout.split()
        paths = [p for p in listed if "node_modules" not in p]

    issues = 0
    for path in paths:
        try:
            for kind, number, text, opened_at in unclosed_fences(path):
                issues += 1
                if kind == "stray-opener":
                    why = (
                        f"    Line {number} ({text!r}) opens another one, and a fence carrying "
                        f"an info string cannot close a block."
                    )
                else:
                    why = f"    The file ends at line {number} with the block still open."
                print(
                    f"[UNCLOSED-FENCE] {path}:{opened_at}: this code fence is never closed.\n"
                    f"{why}\n"
                    f"    Everything below it renders as code -- headings included, which then "
                    f"vanish from the table of contents."
                )
        except (OSError, UnicodeDecodeError) as err:
            print(f"[UNREADABLE] {path}: {err}")
            issues += 1

    if issues:
        print(f"\n❌ {issues} unclosed code fence(s).")
        return 1

    print(f"✅ {len(paths)} markdown file(s): every code fence is closed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
