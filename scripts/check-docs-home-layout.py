#!/usr/bin/env python3
"""Assert the shared home-page layout on a BUILT home page (#2401).

Usage: check-docs-home-layout.py <site>/index.html "<H1 text>" "<this tool's name>"

Called by check-docs-rendered.sh. The three Liferay tools sites give their home pages one
structure, so a reader moving between them finds the same headings, anchors and tables. It reads
the built page, not the markdown: the anchors are what other sites and bookmarks link to, and
they are generated, so only the output can say what they are. Each site keeps its own task rows
and subsections; the skeleton, and the related-tools section word for word, are shared. Prints
one "ok" or "FAIL" line per assertion and exits 1 if any failed.
"""

import re
import sys
import unicodedata
from html.parser import HTMLParser

path, want_h1, self_name = sys.argv[1:]

# The shared section, word for word as issue #2401 gives it. On each site its own row is
# unlinked and its description starts "You are here. ".
RELATED_INTRO = ("Three community tools from the same maintainer. None is an official Liferay "
                 "product or covered by Liferay Support.")
RELATED_ROWS = [
    ("Liferay Docker Manager", "https://peterrichards-lr.github.io/liferay-docker-manager/",
     "Run, manage and develop Liferay DXP and Portal environments in Docker."),
    ("Liferay AI Commerce Accelerator",
     "https://peterrichards-lr.github.io/liferay-ai-commerce-accelerator/",
     "Generate and manage AI-driven commerce data in Liferay DXP."),
    ("Liferay Tunnel", "https://peterrichards-lr.github.io/lfr-tunnel/",
     "Securely expose a local Liferay environment on a public HTTPS subdomain."),
]
# The three shared H2s, by id and exact text. Repo-specific sections may sit only between
# "By subject" and "Related Liferay tools".
H2_FIRST = [("i-want-to", "I want to…"), ("by-subject", "By subject")]
H2_LAST = ("related-liferay-tools", "Related Liferay tools")


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
        if tag in ("h1", "h2", "h3", "h4", "h5", "h6"):
            self.cur = {"kind": tag, "id": a.get("id", ""), "text": ""}
        elif tag == "p" and self.cur is None and self.cell is None:
            self.cur = {"kind": "p", "text": ""}
        elif tag == "table":
            self.items.append({"kind": "table", "head": [], "rows": []})
        elif tag == "tr" and self.items and self.items[-1]["kind"] == "table":
            self.items[-1]["rows"].append([])
        elif tag in ("th", "td"):
            self.cell = {"tag": tag, "text": "", "href": None}
        elif tag == "a" and self.cell is not None and self.cell["href"] is None:
            self.cell["href"] = a.get("href", "")

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
                t["rows"][-1].append((text, self.cell["href"]))
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
heads = [i for i in items if i["kind"] in ("h1", "h2", "h3", "h4", "h5", "h6")]
h2 = [h for h in heads if h["kind"] == "h2"]
ok, bad = [], []


def check(cond, good, problem):
    (ok if cond else bad).append(good if cond else problem)


def following(anchor, n=1):
    """The n items directly after the h2 with this id -- not merely somewhere under it."""
    for i, it in enumerate(items):
        if it["kind"] == "h2" and it["id"] == anchor:
            return items[i + 1:i + 1 + n]
    return []


h1 = [h["text"] for h in heads if h["kind"] == "h1"]
check(h1 == [want_h1], f'H1 is "{want_h1}"', f'H1 is {h1!r}, expected ["{want_h1}"]')

got = [(h["id"], h["text"]) for h in h2]
check(got[:2] == H2_FIRST and got[-1:] == [H2_LAST],
      'H2s: "I want to…" (#i-want-to) first, "By subject" (#by-subject) second, '
      '"Related Liferay tools" (#related-liferay-tools) last',
      f"H2s (id, text) are {got!r}; the first two must be {H2_FIRST!r} and the last {H2_LAST!r} "
      "(repo-specific sections go between By subject and Related)")
related = [h["text"] for h in heads if re.search(r"\brelated\b", h["text"], re.I)]
check(related == ["Related Liferay tools"], 'exactly one "Related" heading',
      f'headings with the word "Related": {related!r}')

nxt = following("i-want-to")
want = nxt[0] if nxt and nxt[0]["kind"] == "table" else None
check(want is not None and want["head"] == ["I want to…", "Go to"],
      'the table directly under #i-want-to is headed "I want to…" | "Go to"',
      "directly under #i-want-to: "
      + (f'a table headed {want["head"]!r}' if want else f'{nxt[0]["kind"] if nxt else "nothing"}, not the table'))
rows = [r[0][0] for r in (want or {}).get("rows", []) if r]
odd = [r for r in rows if not r[:1].isupper() or r.endswith(".")]
check(bool(rows) and not odd,
      f"{len(rows)} task rows start with a capital letter and have no trailing full stop",
      f"task rows that start lower-case or with a '…', or end with a full stop: {odd or 'no rows'!r}")

subs, inside = [], False
for h in heads:
    if h["kind"] == "h2":
        inside = h["id"] == "by-subject"
    elif h["kind"] == "h3" and inside:
        subs.append(h["text"])
numbered = [s for s in subs if s[:1].isdigit()]
check(bool(subs) and not numbered, f"#by-subject has {len(subs)} unnumbered ### subsections",
      f"#by-subject subsections: {subs!r} (need at least one, none numbered)")

nxt = following("related-liferay-tools", 2)
intro = nxt[0] if nxt and nxt[0]["kind"] == "p" else None
check(intro is not None and intro["text"] == RELATED_INTRO,
      "the line directly under #related-liferay-tools is the shared intro, word for word",
      "directly under #related-liferay-tools: "
      + (f'{intro["text"]!r}' if intro else f'{nxt[0]["kind"] if nxt else "nothing"}, not the intro'))
rel = nxt[1] if len(nxt) > 1 and nxt[1]["kind"] == "table" else None
check(rel is not None and rel["head"] == ["Tool", "What it does"],
      'the table directly after the intro is headed "Tool" | "What it does"',
      "after the related-tools intro: "
      + (f'a table headed {rel["head"]!r}' if rel else "no table directly after it"))

expected = [(n, None, "You are here. " + d) if n == self_name else (n, h, d)
            for n, h, d in RELATED_ROWS]
actual = [(r[0][0], r[0][1], r[1][0] if len(r) > 1 else None) for r in (rel or {}).get("rows", [])]
check(actual == expected,
      f"related tools: the 3 shared rows word for word with their links, in order, {self_name}'s "
      'unlinked and starting "You are here."',
      f"related-tools rows (tool, link, description) are {actual!r}, expected {expected!r}")

# Emoji: symbols (So) other than the typographic marks a heading may legitimately carry, plus
# the pieces emoji are built from that are not So -- the variation selector, the keycap mark,
# and the few emoji that Unicode files under other categories (ℹ is a letter, ‼ and ⁉ are
# punctuation).
NOT_EMOJI = {"©", "®", "™"}            # © ® ™
EMOJI_EXTRA = {"️", "⃣", "ℹ", "‼", "⁉"}


def has_emoji(s):
    return any((unicodedata.category(c) == "So" and c not in NOT_EMOJI) or c in EMOJI_EXTRA
               for c in s)


emoji = [h["text"] for h in heads if has_emoji(h["text"])]
check(not emoji, "no emoji in any heading", f"headings with emoji: {emoji!r}")

for line in bad:
    print("FAIL " + line)
for line in ok:
    print("ok   " + line)
sys.exit(1 if bad else 0)
