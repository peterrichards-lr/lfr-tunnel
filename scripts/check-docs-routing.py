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


# The community-project notice (#2385). A reader decides whether to adopt the tool on the first
# screen, and the Liferay name and logo can read as an official, supported product -- so the
# disclaimer has to come before that decision, not on the About page they may never open.
#
# Each landing page must open with exactly: its H1, its description paragraph (unchanged -- it is
# the search-result snippet), then the notice block below. Checked by walking the top of the file
# rather than by asking "is it somewhere above the first `##`?": that question was satisfied by a
# notice above the H1, between the H1 and the description, below a `---` rule, inside an HTML
# comment or <details>, or as a plain paragraph -- and answering it needed a heading and fence
# parser that CommonMark can outflank (setext headings, info-string fences).
#
# The README's link is absolute because it is read on github.com and in search results; the hub's
# is relative because mkdocs resolves it. The hub carries it as a GitHub alert so that it is a
# callout on both github.com and the site (the alerts hook converts it).
NOTICE = ("ℹ️ **Community project** — not an official Liferay product and not covered by "
          "Liferay Support. [About this project]({})")
NOTICE_BLOCKS = {
    ROOT_README: ["> " + NOTICE.format("https://peterrichards-lr.github.io/lfr-tunnel/about/")],
    HUB: ["> [!IMPORTANT]", "> " + NOTICE.format("about.md")],
}


def blocks(lines):
    """Consecutive non-blank lines, grouped; blank lines separate blocks."""
    out, current = [], []
    for line in lines:
        if line.strip():
            current.append(line.rstrip())
        elif current:
            out.append(current)
            current = []
    if current:
        out.append(current)
    return out


def notice_problem(path, expected):
    """None if `path` opens H1, description paragraph, notice block; else what is wrong."""
    with open(path, encoding="utf-8") as handle:
        top = blocks(handle.read().splitlines())

    if not top or len(top[0]) != 1 or not top[0][0].startswith("# "):
        return "does not open with its H1 on a line of its own"
    # Anything that is not prose -- a quote, heading, rule or list, HTML, a fence, a table. A
    # single backtick is allowed: the README's description opens with inline code.
    if len(top) < 2 or top[1][0].startswith((">", "#", "-", "*", "<", "```", "~~~", "|")):
        return "has no description paragraph straight after its H1"
    if len(top) < 3 or top[2] != expected:
        found = "elsewhere in the file" if any("**Community project**" in l for b in top for l in b) \
            else "nowhere"
        return (f"does not carry the notice block straight after its description paragraph "
                f"(the notice text appears {found})")
    return None


# The full disclaimer (#2387). The README closes with the About page's text -- who maintains the
# project, that it is not an official or supported Liferay product, and how the docs are written
# and reviewed. docs/about.md is the one source, and the README must END with exactly
# `## About this project` followed by its body blocks, in order, with nothing after them.
#
# Exactly, not "each paragraph appears somewhere in the last section": that version passed an
# added paragraph contradicting the About text, the paragraphs reordered, the whole text inside
# an HTML comment, a fence or <details>, and other content following it (a setext heading, an
# H3) -- the same "is it somewhere in region X?" shape [NO-NOTICE] above already had to abandon.
# A wrapping `-->`, fence or `</details>` becomes the last block, so this rejects those too.
#
# Comparison is per line: each paragraph is one line in both files. Re-wrapping one over several
# lines, even without changing a word, fails -- keep them one line each.
ABOUT = os.path.join("docs", "about.md")
FOOTER_MARK = "<!-- markdownlint-disable MD049 -->"
DISCLAIMER_HEADING = "## About this project"


def about_paragraphs():
    """docs/about.md's body: the blocks between its H1 and its review footer."""
    with open(ABOUT, encoding="utf-8") as handle:
        text = handle.read().split(FOOTER_MARK)[0]
    return [b for b in blocks(text.splitlines()) if not b[0].startswith("# ")]


def disclaimer_problem():
    """None if the README ends with the heading and every About block in order, else what is wrong."""
    wanted = about_paragraphs()
    if len(wanted) < 2:
        return f"cannot be checked: {ABOUT} yielded {len(wanted)} paragraph(s), too few to be the page"
    if any(b[0].startswith("#") for b in wanted):
        return (f"cannot be checked: {ABOUT}'s body contains a heading ('{wanted[0][0][:40]}'), "
                f"which would start a new README section. Keep the About page to paragraphs")

    with open(ROOT_README, encoding="utf-8") as handle:
        body = blocks(handle.read().split(FOOTER_MARK)[0].splitlines())
    expected = [[DISCLAIMER_HEADING]] + wanted
    tail = body[-len(expected):]
    if tail == expected:
        return None

    if [DISCLAIMER_HEADING] not in body:
        return f"has no '{DISCLAIMER_HEADING}' section"
    after = body[body.index([DISCLAIMER_HEADING]) + 1:]
    for n, block in enumerate(wanted):
        if n >= len(after):
            return f"stops after {n} of the {len(wanted)} About paragraph(s)"
        if after[n] != block:
            return (f"differs from {ABOUT} at block {n + 1} of its closing section: has "
                    f"'{after[n][0][:60]}…', expected '{block[0][:60]}…'")
    return (f"has {len(after) - len(wanted)} block(s) after the About text, starting "
            f"'{after[len(wanted)][0][:60]}…' -- it must be the last thing before the footer")


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

    for landing, block in NOTICE_BLOCKS.items():
        problem = notice_problem(landing, block)
        if problem:
            issues += 1
            print(f"[NO-NOTICE] {landing} {problem}.")
            print(f"    Readers must see that this is not an official Liferay product before they "
                  f"decide to adopt it. Expected, straight after the opening paragraph:")
            for line in block:
                print(f"    {line}")

    problem = disclaimer_problem()
    if problem:
        issues += 1
        print(f"[DISCLAIMER-DRIFT] {ROOT_README} {problem}.")
        print(f"    The README's closing section must hold {ABOUT}'s body verbatim. Change both, "
              f"or neither.")

    if issues:
        print(f"\n❌ {issues} landing-page problem(s).")
        return 1

    print(f"✅ {len(expected)} page(s): every one is reachable from both landing pages "
          f"({len(skip)} deliberately excluded), both carry the community-project notice, and the README ends with the About text.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
