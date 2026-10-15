#!/usr/bin/env python3
"""Assert the shared home-page layout on a BUILT home page (#2401).

Usage: check-docs-home-layout.py <site>/index.html "<H1 text>" "<this tool's name>"

Called by check-docs-rendered.sh. The three Liferay tools sites give their home pages one
structure, so a reader moving between them finds the same headings, anchors and tables. It reads
the built page, not the markdown: the anchors are what other sites and bookmarks link to, and
they are generated, so only the output can say what they are. Each site keeps its own rows and
subsections; this asserts the shared skeleton only. Prints one "ok" or "FAIL" line per assertion
and exits 1 if any failed.
"""

import sys, unicodedata
from html.parser import HTMLParser

path, want_h1, self_name = sys.argv[1:]
RELATED_INTRO = ("Three community tools from the same maintainer. None is an official Liferay "
                 "product or covered by Liferay Support.")
RELATED_ORDER = ["Liferay Docker Manager", "Liferay AI Commerce Accelerator", "Liferay Tunnel"]

class Page(HTMLParser):
    """The article's headings, paragraphs and tables, in document order."""
    def __init__(self):
        super().__init__()
        self.items, self.depth, self.cur, self.cell, self.skip = [], 0, None, None, 0
    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if tag == "article":
            self.depth += 1
        if not self.depth:
            return
        if tag == "a" and "headerlink" in (a.get("class") or ""):
            self.skip += 1
        if tag in ("h1", "h2", "h3"):
            self.cur = {"kind": tag, "id": a.get("id", ""), "text": ""}
        elif tag == "p" and self.cur is None and self.cell is None:
            self.cur = {"kind": "p", "text": ""}
        elif tag == "table":
            self.items.append({"kind": "table", "head": [], "rows": []})
        elif tag == "tr" and self.items and self.items[-1]["kind"] == "table":
            self.items[-1]["rows"].append([])
        elif tag in ("th", "td"):
            self.cell = {"tag": tag, "text": "", "link": False}
        elif tag == "a" and self.cell is not None:
            self.cell["link"] = True
    def handle_endtag(self, tag):
        if tag == "article" and self.depth:
            self.depth -= 1
        if not self.depth:
            return
        if tag == "a" and self.skip:
            self.skip -= 1
        if self.cur is not None and tag == self.cur["kind"]:
            self.cur["text"] = " ".join(self.cur["text"].split())
            self.items.append(self.cur)
            self.cur = None
        elif tag in ("th", "td") and self.cell is not None:
            t = self.items[-1]
            text = " ".join(self.cell["text"].split())
            if self.cell["tag"] == "th":
                t["head"].append(text)
            else:
                t["rows"][-1].append((text, self.cell["link"]))
            self.cell = None
    def handle_data(self, data):
        if self.skip:
            return
        if self.cell is not None:
            self.cell["text"] += data
        elif self.cur is not None:
            self.cur["text"] += data

p = Page()
p.feed(open(path, encoding="utf-8").read())
items = p.items
for t in items:
    if t["kind"] == "table":
        t["rows"] = [r for r in t["rows"] if r]   # the header row holds <th> only
heads = [i for i in items if i["kind"] in ("h1", "h2", "h3")]
h2 = [h for h in heads if h["kind"] == "h2"]
ok, bad = [], []
def check(cond, good, problem):
    (ok if cond else bad).append(good if cond else problem)

def after(anchor, kind):
    """The first item of `kind` after the h2 with this id, before the next h2."""
    seen = False
    for i in items:
        if seen and i["kind"] == "h2":
            return None
        if seen and i["kind"] == kind:
            return i
        if i["kind"] == "h2" and i["id"] == anchor:
            seen = True
    return None

h1 = [h["text"] for h in heads if h["kind"] == "h1"]
check(h1 == [want_h1], f'H1 is "{want_h1}"', f'H1 is {h1!r}, expected ["{want_h1}"]')

ids = [h["id"] for h in h2]
order = [a for a in ids if a in ("i-want-to", "by-subject", "related-liferay-tools")]
check(order == ["i-want-to", "by-subject", "related-liferay-tools"],
      "H2 ids #i-want-to, #by-subject, #related-liferay-tools exist, in that order",
      f"H2 ids in order are {ids!r}; need #i-want-to, then #by-subject, then #related-liferay-tools")
check(bool(ids) and ids[-1] == "related-liferay-tools",
      "#related-liferay-tools is the last H2",
      f"the last H2 is #{ids[-1] if ids else '(none)'}, not #related-liferay-tools")
related = [h["text"] for h in heads if "related" in h["text"].lower()]
check(related == ["Related Liferay tools"], 'exactly one "Related" heading',
      f'headings mentioning "Related": {related!r}')

want = after("i-want-to", "table")
check(want is not None and want["head"] == ["I want to…", "Go to"],
      'the #i-want-to table is headed "I want to…" | "Go to"',
      f'the #i-want-to table headers are {want["head"] if want else None!r}')
rows = [r[0][0] for r in (want or {}).get("rows", []) if r]
odd = [r for r in rows if not r[:1].isupper() or r.startswith("…") or r.endswith(".")]
check(bool(rows) and not odd,
      f"{len(rows)} task rows start with a capital, no leading '…', no trailing full stop",
      f"task rows not in sentence case, or with a leading '…' or trailing full stop: {odd or 'no rows'!r}")

subs, inside = [], False
for h in heads:
    if h["kind"] == "h2":
        inside = h["id"] == "by-subject"
    elif h["kind"] == "h3" and inside:
        subs.append(h["text"])
numbered = [s for s in subs if s[:1].isdigit()]
check(bool(subs) and not numbered, f"#by-subject has {len(subs)} unnumbered ### subsections",
      f"#by-subject subsections: {subs!r} (need at least one, none numbered)")

intro = after("related-liferay-tools", "p")
check(intro is not None and intro["text"] == RELATED_INTRO,
      "the related-tools intro line is word for word",
      f'the related-tools intro is {intro["text"] if intro else None!r}')
rel = after("related-liferay-tools", "table")
check(rel is not None and rel["head"] == ["Tool", "What it does"],
      'the #related-liferay-tools table is headed "Tool" | "What it does"',
      f'the #related-liferay-tools table headers are {rel["head"] if rel else None!r}')
rrows = (rel or {}).get("rows", [])
names = [r[0][0] for r in rrows if r]
check(names == RELATED_ORDER, "related tools: 3 rows, in the order LDM, AICA, Liferay Tunnel",
      f"related-tools rows are {names!r}, expected {RELATED_ORDER!r}")
here = [r[0][0] for r in rrows if len(r) > 1 and "You are here." in r[1][0]]
check(here == [self_name], f'exactly one "You are here." row, {self_name}\'s',
      f'"You are here." rows: {here!r}, expected [{self_name!r}]')
links = {r[0][0]: r[0][1] for r in rrows if r}
check(links.get(self_name) is False and all(v for k, v in links.items() if k != self_name),
      "this site's row is unlinked, the other two are links",
      f"related-tools links per row: {links!r} (only this site's row may be unlinked)")

def has_emoji(s):
    return any(unicodedata.category(c) == "So" or c == "️" for c in s)
emoji = [h["text"] for h in heads if has_emoji(h["text"])]
check(not emoji, "no emoji in any h1-h3", f"headings with emoji: {emoji!r}")

for line in bad:
    print("FAIL " + line)
for line in ok:
    print("ok   " + line)
sys.exit(1 if bad else 0)
