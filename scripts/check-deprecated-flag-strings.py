#!/usr/bin/env python3
"""Fail when a string shown to a user names a flag that has been deprecated.

#2061 renamed -server, -gateway and -region to -pin, -bootstrap and -prefer-region. The old
spellings still work, so nothing breaks and nothing complains -- which is exactly why the rot
spread quietly and had to be found three separate times, each by accident while doing something
else (#2080, #2085, #2090).

The deprecated names are read FROM THE FLAG DEFINITIONS, not hardcoded here. A flag is
deprecated when its usage string says so. Retiring `-pin` in some future rename therefore
updates this guard automatically, instead of leaving it confidently checking for last year's
words.

Scope is the text that describes CURRENT behaviour to a user: the i18n catalogues, the client
inspector page, and the portal's own JavaScript and TSX. Deliberately excluded:

  cmd/lfr-tunnel      naming the old spelling is the entire job of the deprecation warning
  docs/               a migration table has to name the old name to be useful (#2080 added one)
  tests/              a fixture may assert on the deprecated path precisely because it exists
"""

import glob
import os
import re
import sys

MAIN = os.path.join("cmd", "lfr-tunnel", "main.go")

SCANNED = [
    "pkg/server/i18n/Language*.properties",
    "pkg/client/dashboard.html",
    "pkg/server/static/*.js",
    "ui/src/**/*.tsx",
]


def deprecated_flags(main_path=MAIN):
    """Flag names whose own usage text calls them deprecated."""
    with open(main_path, encoding="utf-8") as handle:
        source = handle.read()

    found = set()
    for match in re.finditer(r'flag\.\w+\(\s*"([\w-]+)"\s*,\s*[^,]*,\s*"([^"]*)"', source):
        name, usage = match.group(1), match.group(2)
        if usage.strip().upper().startswith("DEPRECATED"):
            found.add(name)
    return found


def main():
    flags = deprecated_flags()
    if not flags:
        # An empty set would make every assertion below pass on any corpus at all.
        print("[NO-SCAN] no deprecated flags found in " + MAIN + ". Either the deprecation "
              "convention changed (usage text starting 'DEPRECATED') or this is reading the "
              "wrong file -- nothing was checked.")
        return 1

    patterns = {f: re.compile(r"(^|[^\w-])-" + re.escape(f) + r"([^\w-]|$)") for f in flags}

    issues = 0
    scanned = 0
    for spec in SCANNED:
        for path in sorted(glob.glob(spec, recursive=True)):
            scanned += 1
            with open(path, encoding="utf-8") as handle:
                for number, line in enumerate(handle, 1):
                    for flag, pattern in patterns.items():
                        if pattern.search(line):
                            issues += 1
                            print(
                                f"[DEPRECATED-FLAG] {path}:{number} names -{flag}, which is "
                                f"deprecated.\n"
                                f"    {line.strip()[:120]}\n"
                                f"    It still works, so nobody is stopped and nobody is told. "
                                f"Name the current flag instead."
                            )

    if issues:
        print(f"\n❌ {issues} user-visible string(s) naming a deprecated flag.")
        return 1

    print(f"✅ {scanned} file(s) scanned: no user-visible string names any of "
          f"{sorted('-' + f for f in flags)}.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
