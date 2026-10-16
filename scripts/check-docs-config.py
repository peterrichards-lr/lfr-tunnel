#!/usr/bin/env python3
"""Fail when mkdocs.yml stops asking the built-site check for what this site promises.

Usage: check-docs-config.py [mkdocs.yml]

scripts/check_docs_rendered.py is the template's shared check, and each of its site-specific
checks runs only when mkdocs.yml configures it. Since v2.3.1, extra.docs_standard.required stops a
deleted top-level key from switching a check off (#2409). Three sub-keys are not covered: delete
one and its part of the check is skipped while the build stays green. Measured on this site's
build at v2.3.1, each deletion exits 0. Raised as ai-agent-template#155.

So this holds those three until the template does. It also holds the `required` list itself:
since that list is what keeps the other checks on, deleting it, or one name from it, would
switch them off as silently as a deleted key once did (review of #2410). And it holds
google_site_verification by name, because `required` accepts Bing's token in its place and
#2381 was Google's. When #155 lands, keep only these two.
Removing one is a decision to make in the PR that removes it, by editing this list.
Exits 1 with one line per missing key.
"""

import sys

import yaml

# (dotted path under extra, what it switches on, why this site needs it)
REQUIRED = (
    ("docs_standard.notice.text", "NO-NOTICE",
     "without it the notice's disclaimer is not checked (ai-agent-template#155)"),
    ("docs_standard.notice.link_text", "NO-NOTICE",
     "without it the notice's About link is not checked (ai-agent-template#155)"),
    ("docs_standard.related.heading", "HOME-LAYOUT",
     "without it the whole Related Liferay tools section is not checked (ai-agent-template#155)"),
    ("google_site_verification", "MISSING-VERIFICATION",
     "Search Console ownership: Google unverifies the site without the tag (#2381); `required` "
     "would accept Bing's token instead"),
)

# What extra.docs_standard.required must name: every check this site cannot do without.
REQUIRED_CHECKS = {"MISSING-VERIFICATION", "MISSING-SOCIAL", "NO-NOTICE", "HOME-LAYOUT"}


def load(path):
    """mkdocs.yml, tolerating MkDocs' `!!python/name:` tags."""

    class Tolerant(yaml.SafeLoader):
        pass

    Tolerant.add_multi_constructor("tag:yaml.org,2002:python/name:", lambda *_: None)
    Tolerant.add_multi_constructor("!", lambda *_: None)
    with open(path, encoding="utf-8") as handle:
        return yaml.load(handle, Loader=Tolerant) or {}


def lookup(tree, dotted):
    for key in dotted.split("."):
        if not isinstance(tree, dict) or key not in tree:
            return None
        tree = tree[key]
    return tree


config_path = sys.argv[1] if len(sys.argv) > 1 else "mkdocs.yml"
extra = load(config_path).get("extra") or {}
missing = [(dotted, check, why) for dotted, check, why in REQUIRED if not lookup(extra, dotted)]
listed = lookup(extra, "docs_standard.required")
unlisted = sorted(REQUIRED_CHECKS - set(listed if isinstance(listed, list) else []))

if missing or unlisted:
    print(f"[DOCS-CONFIG] {config_path} no longer switches on checks this site promises:")
    for dotted, check, why in missing:
        print(f"  extra.{dotted} is missing or empty, so {check} does not run -- {why}")
    if listed is None:
        print("  extra.docs_standard.required is missing, so deleting the key of any of "
              f"{unlisted} skips that check instead of failing")
    elif unlisted:
        print(f"  extra.docs_standard.required does not list {unlisted}, so deleting their key "
              "skips them instead of failing")
    print("  Restore the key. If dropping the check is intended, remove it from REQUIRED in")
    print("  scripts/check-docs-config.py in the same PR, so the decision is visible.")
    sys.exit(1)

print(f"✅ {config_path}: all {len(REQUIRED)} guarded keys are set, and docs_standard.required "
      f"lists all {len(REQUIRED_CHECKS)} checks.")
