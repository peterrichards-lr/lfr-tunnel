"""Convert GitHub alert blockquotes into Material admonitions at build time.

The docs are read on two surfaces. In the repository and on github.com, `> [!WARNING]` is
native syntax and renders as a callout. In the built site it is not: Python-Markdown sees an
ordinary blockquote, so the marker is printed to the reader as literal text and the callout
loses the styling that made it a callout.

Measured before this hook existed: 33 markers visible in the published HTML -- 14 NOTE,
9 IMPORTANT, 5 WARNING, 3 CAUTION, 2 TIP. Disproportionately the ones that matter; the AWS
guide alone leaked 12.

Converting the SOURCE to Material's `!!! warning` syntax would fix the site and break GitHub,
which is where contributors read these files. So the source stays GitHub-native and the site
build translates. Same reasoning as the anchor rule: when two renderers disagree, the fix is
the build, not picking a side.
"""

import re

# GitHub's five types, mapped to the Material admonition that carries the same weight.
# IMPORTANT has no exact Material twin; `info` is the closest in both colour and intent.
_TYPES = {
    "NOTE": "note",
    "TIP": "tip",
    "IMPORTANT": "info",
    "WARNING": "warning",
    "CAUTION": "danger",
}

_MARKER = re.compile(r"^>\s*\[!(" + "|".join(_TYPES) + r")\]\s*(.*)$")

# CommonMark fences, and the rule that matters here: a fence is CLOSED only by a line of the
# same character, at least as long, carrying no info string. `` ```bash `` therefore cannot
# close a block -- it is content inside one.
#
# Tracking fences as a naive toggle instead got this wrong on the largest page in the repo.
# setup_guide.md has a ```bash line sitting inside another bash block; the toggle desynced
# there and stayed desynced, so every alert in the remaining 1200 lines was treated as being
# inside code and left unconverted. Three survived the first build because of it.
_FENCE_OPEN = re.compile(r"^(\s*)(`{3,}|~{3,})(.*)$")


def _convert(markdown: str) -> str:
    lines = markdown.split("\n")
    out: list[str] = []
    i = 0
    in_fence = False
    fence_char, fence_len = "", 0

    while i < len(lines):
        line = lines[i]

        # Never rewrite inside a fenced block: a doc that *documents* this syntax would
        # otherwise have its example silently converted.
        fence = _FENCE_OPEN.match(line)
        if fence is not None:
            marker = fence.group(2)
            info = fence.group(3).strip()
            if in_fence:
                # Closes only if it is the same character, no shorter, and bare.
                if marker[0] == fence_char and len(marker) >= fence_len and not info:
                    in_fence = False
            else:
                in_fence, fence_char, fence_len = True, marker[0], len(marker)
            out.append(line)
            i += 1
            continue

        match = None if in_fence else _MARKER.match(line)
        if match is None:
            out.append(line)
            i += 1
            continue

        kind = _TYPES[match.group(1)]
        trailing = match.group(2).strip()

        i += 1
        body = [trailing] if trailing else []
        while i < len(lines) and lines[i].startswith(">"):
            body.append(re.sub(r"^>\s?", "", lines[i]))
            i += 1

        out.append(f"!!! {kind}")
        out.append("")
        # Blank lines stay blank: indenting them would end the admonition early on some
        # Python-Markdown versions and is invisible either way.
        out.extend("    " + b if b.strip() else "" for b in body)
        out.append("")

    return "\n".join(out)


def on_page_markdown(markdown, page=None, config=None, files=None):  # noqa: ARG001
    return _convert(markdown)
