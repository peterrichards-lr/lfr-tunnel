#!/usr/bin/env python3
"""An e2e name assertion must not be a strict prefix of a different shipped string (#2311).

THE DEFECT. Playwright matches `getByRole(role, { name })` as a case-insensitive SUBSTRING
unless `exact: true` is passed. So an assertion reading

    page.getByRole('navigation', { name: 'Primary' })

passed whether the landmark was named "Primary" -- the untranslated fallback both portal arms
carried -- or "Primary Navigation", which is what `aria_primary_nav` actually ships. The one
thing those three specs were positioned to catch, that the i18n mechanism ran at all, was the
one thing they could not distinguish. #2303 shipped four days earlier to make both shells
resolve the locale before the bundle runs, and this suite could not have told us whether it
worked.

WHY THIS AND NOT A COUNT. The obvious guard is a ratchet on `getByRole(...{ name })` without
`exact: true`. There are 109 of those and almost all are harmless: a substring match is only a
defect when some OTHER shipped string starts with the same text, and then it is a defect whether
or not anyone remembered the flag. Counting the flag would ratchet a number nobody can act on
and would still not name the four real collisions. This checks the property instead.

WHAT IT READS. Every `name: '...'` literal in tests/e2e/ui/tests/*.spec.ts, against every value
in pkg/server/i18n/Language.properties. A name is reported when a DIFFERENT bundle value starts
with it.

KNOWN LIMITS, stated rather than discovered later:
  - only single-quoted literals, not template strings or variables. A name built at runtime
    cannot be checked against a static bundle at all.
  - only the English bundle. The suite runs in English; a locale whose translation introduces a
    new prefix collision is invisible here.
  - accessible names that are not bundle values (hard-coded English in a component) are not
    compared, because there is nothing to compare them to.
"""

import glob
import io
import os
import re
import sys

BUNDLE = "pkg/server/i18n/Language.properties"
SPECS = "tests/e2e/ui/tests/*.spec.ts"

# Names that ARE a prefix of a shipped string and are allowed to stay that way, each with the
# reason. This is a list whose entries must go STALE LOUDLY: if one stops colliding, the check
# fails and the entry has to be removed, so the list can only shrink (SKILL section 5b rule 5).
#
# All four are scoped CLICKS -- `dialog.`, `table.`, `nav.` -- not assertions about whether the
# bundle ran, which is the distinction that matters. A click that matches the wrong button inside
# a scoped container is a real hazard; it is just not the hazard this issue is about, and none of
# them has a demonstrated failure behind it.
ALLOWED = {
    "Confirm": "portal_v2_custom_domain_release.spec.ts, scoped to the dialog",
    "Dismiss": "portal_banner_reset.spec.ts, the only dismiss button on the page",
    "Release": "portal_v2_custom_domain_release.spec.ts, scoped to the table",
    "Personal Access Tokens": "portal_v2_dashboard_jump_nav.spec.ts, scoped to the jump nav",
}

NAME_RE = re.compile(r"getByRole\([^)]*?\{\s*name:\s*'([^']+)'", re.S)


def bundle_values(path):
    values = set()
    for line in io.open(path, encoding="utf-8"):
        line = line.rstrip("\n")
        if line.startswith("#") or "=" not in line:
            continue
        values.add(line.split("=", 1)[1])
    return values


def main():
    if not os.path.isfile(BUNDLE):
        print("check-e2e-accessible-names: %s not found -- run from the repository root" % BUNDLE)
        return 2

    values = bundle_values(BUNDLE)
    files = sorted(glob.glob(SPECS))

    # Anti-vacuity (#1779), both halves. A scan that read no specs, or a bundle that parsed to
    # nothing, reports a clean tree and means nothing.
    if len(files) < 10:
        print("check-e2e-accessible-names: only %d spec file(s) found (expected at least 10)." % len(files))
        print("A scan of nothing reports no collisions. Run this from the repository root.")
        return 1
    if len(values) < 500:
        print("check-e2e-accessible-names: the bundle parsed to %d value(s), expected hundreds." % len(values))
        return 1

    where = {}
    for f in files:
        text = io.open(f, encoding="utf-8").read()
        for m in NAME_RE.finditer(text):
            where.setdefault(m.group(1), set()).add(os.path.basename(f))

    unexpected, stale = [], []
    for name in sorted(where):
        longer = sorted(v for v in values if v != name and v.startswith(name))
        if longer and name not in ALLOWED:
            unexpected.append((name, sorted(where[name]), longer[:3]))
        if not longer and name in ALLOWED:
            stale.append(name)
    for name in sorted(ALLOWED):
        if name not in where:
            stale.append(name)

    if not unexpected and not stale:
        print("check-e2e-accessible-names: OK -- %d name(s) across %d spec(s); "
              "%d known prefix(es), none new." % (len(where), len(files), len(ALLOWED)))
        return 0

    if unexpected:
        print("check-e2e-accessible-names: FAILED")
        print()
        print("These name assertions are a strict PREFIX of a different shipped string, so")
        print("Playwright's substring matching makes them pass against either one:")
        print()
        for name, specs, longer in unexpected:
            print("  %r  in %s" % (name, ", ".join(specs)))
            for v in longer:
                print("      also matches: %r" % v)
        print()
        print("Pass the full name with exact: true, or add it to ALLOWED in this script with the")
        print("reason it is safe -- a scoped click inside one container is not the same as an")
        print("assertion about whether the bundle ran.")

    if stale:
        print()
        print("STALE ALLOWED entries -- these no longer collide, or no longer appear at all, so")
        print("the exemption is carrying nothing. Remove them:")
        for name in sorted(set(stale)):
            print("  %r" % name)

    return 1


if __name__ == "__main__":
    sys.exit(main())
