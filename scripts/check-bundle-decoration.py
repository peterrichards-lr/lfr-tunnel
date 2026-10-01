#!/usr/bin/env python3
"""A translated value carries words, not decoration (#2327).

TWO RULES, from one reported defect with two faces.

1. NO MARKUP IN A BUNDLE VALUE. Three values read
   `Status: <span style="...">ACTIVE </span>`. V1 put them into `innerHTML` so the markup
   rendered; V2 renders the same keys as text, so React escaped the tags and an admin saw them.
   A value that is HTML works in exactly one consumer and breaks in every other.

2. NO LEADING ICON ON A KEY WHOSE VIEW ALREADY DRAWS ONE. Eight keys began with an icon that
   V2 also rendered in JSX, so the heading showed two -- and in one case two DIFFERENT ones,
   because V2 drew a compass where the bundle supplied a waving hand.

Both are the same mistake: decoration belongs to the view, which knows the layout, not to the
string, which is shared by every view.

WHAT THIS DOES NOT SAY. It is not an opinion about WHICH headings should carry an icon. 14 of 84
heading keys do, with no evident rule, and that is a design question recorded on #2327 and
deliberately left open. This only refuses the case where one is drawn twice.

KNOWN LIMITS, stated rather than found later:
  - rule 2 matches a literal icon immediately before a `t('key', ...)` call in a .tsx file. An
    icon rendered from a variable, or two lines away, is invisible to it.
  - only V2 is scanned for rule 2. V1 replaces the whole element via `el.innerText`, so an icon
    inline beside a data-i18n attribute is overwritten rather than doubled -- the defect cannot
    occur there, and `tests/hooks/test-bundle-decoration.sh` pins that as the reason.
"""

import glob
import io
import os
import re
import sys

BUNDLES = "pkg/server/i18n/Language*.properties"
V2 = "ui/src/**/*.tsx"

MARKUP = re.compile(r"<\s*/?\s*[A-Za-z]")
ICON = (
    "[\U0001F000-\U0001FAFF←-⇿☀-➿⬀-⯿"
    "⚠✨️‍]"
)
LEADING_ICON = re.compile("^" + ICON)
# A literal icon rendered immediately before a translated string.
V2_ICON_BEFORE_KEY = re.compile(ICON + r"+\s*\{?'?\s*'?\}?\s*\n?\s*\{?\s*t\(\s*'([a-z0-9_]+)'")


def read_bundle(path):
    out = {}
    for line in io.open(path, encoding="utf-8"):
        line = line.rstrip("\n")
        if line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        out[k] = v
    return out


def main():
    bundles = sorted(glob.glob(BUNDLES))
    specs = sorted(glob.glob(V2, recursive=True))

    # Anti-vacuity (#1779): a scan that read nothing reports a clean tree and means nothing.
    if len(bundles) < 5:
        print("check-bundle-decoration: only %d bundle(s) found -- run from the repository root." % len(bundles))
        return 1
    if len(specs) < 20:
        print("check-bundle-decoration: only %d V2 source file(s) found -- rule 2 would be vacuous." % len(specs))
        return 1

    english = read_bundle("pkg/server/i18n/Language.properties")
    if len(english) < 500:
        print("check-bundle-decoration: the English bundle parsed to %d value(s), expected hundreds." % len(english))
        return 1

    markup_hits = []
    for path in bundles:
        for k, v in sorted(read_bundle(path).items()):
            if MARKUP.search(v):
                markup_hits.append((os.path.basename(path), k, v[:70]))

    # Which keys does V2 draw an icon beside?
    drawn = {}
    for f in specs:
        src = io.open(f, encoding="utf-8").read()
        for m in V2_ICON_BEFORE_KEY.finditer(src):
            drawn.setdefault(m.group(1), os.path.basename(f))

    double_hits = []
    for key, where in sorted(drawn.items()):
        v = english.get(key, "")
        if v and LEADING_ICON.match(v):
            double_hits.append((where, key, v[:50]))

    if not markup_hits and not double_hits:
        print("check-bundle-decoration: OK -- %d bundle(s), %d V2 file(s); no markup in any value, "
              "and none of the %d icon-drawn key(s) supplies one of its own."
              % (len(bundles), len(specs), len(drawn)))
        return 0

    print("check-bundle-decoration: FAILED")
    if markup_hits:
        print()
        print("A bundle value containing MARKUP. It renders only where the consumer uses innerHTML;")
        print("anywhere else the tags are escaped and shown to the user as text:")
        print()
        for b, k, v in markup_hits:
            print("  %-26s %-26s %r" % (b, k, v))
    if double_hits:
        print()
        print("A bundle value beginning with an ICON that V2 already draws beside it, so the")
        print("heading renders two:")
        print()
        for where, k, v in double_hits:
            print("  %-34s %-26s %r" % (where, k, v))
        print()
        print("Put the icon in the view and leave the words in the bundle.")
    return 1


if __name__ == "__main__":
    sys.exit(main())
