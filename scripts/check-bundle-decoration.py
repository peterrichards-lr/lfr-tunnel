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


# RULE 3: which headings may carry an icon at all (#2331).
#
# 14 of 84 headings had one, with no rule separating them from the other 70. The owner chose:
# modal and dialog titles keep an icon, in-page section headings do not. These nine are the
# whole allowed set, and the list works both ways -- an entry that no longer carries an icon
# anywhere is reported as stale, so it can only shrink (SKILL 5b rule 5).
#
# NOT an aesthetic judgement about which icon. Only about where one may appear.
ICON_ALLOWED = {
    "mfa_setup_title": "modal",
    "mfa_intercept_title": "modal",
    "detail_modal_title": "modal",
    "user_modal_title": "modal",
    "tunnel_override_modal_title": "modal",
    "guide_title": "modal (client installation)",
    "guide_macos_title": "modal (client installation)",
    "guide_windows_title": "modal (client installation)",
    "guide_linux_title": "modal (client installation)",
}

HEADING_KEY = re.compile(r"(_title|_heading)$")
# An icon sitting immediately before a translated span in V1's markup, where #2327 put the ones
# the view owns.
V1_ICON_BEFORE_KEY = re.compile(ICON + r"+\s*<span data-i18n=\"([a-z0-9_]+)\"")


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

    # Rule 3. A heading carries an icon if the bundle value starts with one, or either arm draws
    # one beside it.
    iconed = set()
    for k, v in english.items():
        if HEADING_KEY.search(k) and LEADING_ICON.match(v):
            iconed.add(k)
    for k in drawn:
        if HEADING_KEY.search(k):
            iconed.add(k)
    try:
        v1 = io.open("pkg/server/dashboard.html", encoding="utf-8").read()
        for m in V1_ICON_BEFORE_KEY.finditer(v1):
            if HEADING_KEY.search(m.group(1)):
                iconed.add(m.group(1))
    except OSError:
        pass

    rule_hits = sorted(k for k in iconed if k not in ICON_ALLOWED)
    stale_allowed = sorted(k for k in ICON_ALLOWED if k not in iconed)

    if not markup_hits and not double_hits and not rule_hits and not stale_allowed:
        print("check-bundle-decoration: OK -- %d bundle(s), %d V2 file(s); no markup in any value, "
              "none of the %d icon-drawn key(s) supplies one of its own, and all %d iconed "
              "heading(s) are modal titles."
              % (len(bundles), len(specs), len(drawn), len(iconed)))
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
    if rule_hits:
        print()
        print("A heading carrying an icon that is NOT a modal or dialog title. The rule (#2331) is")
        print("that modal titles keep an icon and in-page section headings do not:")
        print()
        for k in rule_hits:
            print("  %s" % k)
        print()
        print("Remove it, or add the key to ICON_ALLOWED here with the reason it is a dialog.")
    if stale_allowed:
        print()
        print("STALE ICON_ALLOWED entries -- these are permitted an icon and no longer carry one")
        print("anywhere, so the exemption is holding nothing. Remove them:")
        print()
        for k in stale_allowed:
            print("  %s" % k)

    return 1


if __name__ == "__main__":
    sys.exit(main())
