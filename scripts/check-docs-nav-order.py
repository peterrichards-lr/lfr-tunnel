#!/usr/bin/env python3
"""Assert the shared top-level navigation on a BUILT home page (#2405).

Usage: check-docs-nav-order.py <site>/index.html

Called by check-docs-rendered.sh. The three Liferay tools sites share one top-level navigation,
so a reader moving between them finds the same sections in the same order. The rule is the
template's (peterrichards-lr/ai-agent-template#135, docs-site-standard): the labels are an
ORDERED SUBSET of STANDARD_NAV -- a section with no pages is omitted, never renamed or reordered
-- with "About these docs" present and last, and every label in the nav, top level and below,
in sentence case.

It reads Material's primary navigation in the built page, not mkdocs.yml: the labels a reader
sees are the output, and a theme feature (tabs, section indexes) can change what reaches it.
Prints one "ok" or "FAIL" line per assertion and exits 1 if any failed.
"""

import re
import sys
from html.parser import HTMLParser

STANDARD_NAV = (
    "Home",
    "Tutorials",
    "How-to guides",
    "Reference",
    "Explanation",
    "Troubleshooting",
    "Showcase",
    "Project and contributing",
    "About these docs",
)
ABOUT = "About these docs"
_WORD = re.compile(r"[A-Za-z][A-Za-z'’]*")
# Proper nouns a sentence-case label may still capitalise mid-label. Acronyms (SSO, MCP) and
# names with an inner capital (InfoSec, macOS) are recognised by their shape and need no entry.
NAMES = {"Liferay"}


class PrimaryNav(HTMLParser):
    """Material's primary navigation: the top-level labels in order, and every label in it.

    The page's own table of contents is embedded in the primary nav (nav.md-nav--secondary) and
    is skipped: those are the page's headings, not navigation labels.
    """

    def __init__(self):
        super().__init__()
        self.labels, self.all_labels = [], []
        self.nav_depth, self.ul_depth, self.toc_depth = 0, 0, 0
        self.in_item, self.in_label, self.top, self.buffer = False, False, False, ""

    def handle_starttag(self, tag, attrs):
        cls = (dict(attrs).get("class") or "").split()
        if tag == "nav":
            if self.nav_depth or "md-nav--primary" in cls:
                self.nav_depth += 1
                if self.toc_depth or "md-nav--secondary" in cls:
                    self.toc_depth += 1
            return
        if not self.nav_depth or self.toc_depth:
            return
        if tag == "ul":
            self.ul_depth += 1
        elif tag == "li" and self.ul_depth == 1:
            self.in_item, self.buffer = True, None
        elif tag == "span" and "md-ellipsis" in cls and not self.in_label:
            # The first label inside a top-level item is that item's own; any later one is a
            # page or group below it.
            self.top = self.in_item and self.ul_depth == 1 and self.buffer is None
            self.in_label, self.buffer = True, ""

    def handle_endtag(self, tag):
        if not self.nav_depth:
            return
        if tag == "nav":
            self.nav_depth -= 1
            self.toc_depth = max(0, self.toc_depth - 1)
            return
        if self.toc_depth:
            return
        if tag == "ul":
            self.ul_depth -= 1
        elif tag == "span" and self.in_label:
            self.in_label = False
            label = " ".join(self.buffer.split())
            self.all_labels.append(label)
            if self.top:
                self.labels.append(label)
        elif tag == "li" and self.ul_depth == 1:
            self.in_item = False

    def handle_data(self, data):
        if self.in_label:
            self.buffer += data


def is_title_case(label):
    """A word after the first is capitalised like an ordinary word (a capital, then lower case)
    and is not a known proper noun. Acronyms and inner-capital names do not count."""
    return any(w[0].isupper() and w[1:].islower() and w not in NAMES
               for w in _WORD.findall(label)[1:])


parser = PrimaryNav()
parser.feed(open(sys.argv[1], encoding="utf-8").read())
labels = parser.labels
ok, bad = [], []


def check(cond, good, problem):
    (ok if cond else bad).append(good if cond else problem)


check(bool(labels), f"{len(labels)} top-level labels found", "no top-level navigation found")
unknown = [lab for lab in labels if lab not in STANDARD_NAV]
check(not unknown, "every label is from the standard list",
      f"labels not in the standard list: {unknown!r}")
known = [lab for lab in labels if lab in STANDARD_NAV]
check(known == sorted(known, key=STANDARD_NAV.index), "in the standard order",
      f"out of the standard order: {known!r}")
check(labels[-1:] == [ABOUT], f'"{ABOUT}" present and last',
      f'"{ABOUT}" must be present and last; the labels end {labels[-1:]!r}')
titled = [lab for lab in dict.fromkeys(parser.all_labels) if is_title_case(lab)]
check(not titled, f"all {len(set(parser.all_labels))} nav labels in sentence case",
      f"title case (use sentence case): {titled!r}")

for line in bad:
    print("FAIL " + line)
if bad:
    print(f"FAIL top-level labels are {labels!r}; the standard order is {list(STANDARD_NAV)!r}")
for line in ok:
    print("ok   " + line)
if not bad:
    print("ok   nav: " + " › ".join(labels))
sys.exit(1 if bad else 0)
