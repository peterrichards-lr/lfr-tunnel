#!/usr/bin/env python3
"""Fail when mkdocs.yml stops asking the built-site check for what this site promises.

Usage: check-docs-config.py [mkdocs.yml]

scripts/check_docs_rendered.py is the template's shared check, and each of its site-specific
checks runs only when mkdocs.yml configures it. Since v2.3.1, extra.docs_standard.required stops a
deleted top-level key from switching a check off (#2409). Three sub-keys are not covered: delete
one and its part of the check is skipped while the build stays green. Measured on this site's
build at v2.3.1, each deletion exits 0. Raised as ai-agent-template#155.

So this holds those three until the template does; then delete this file and its CI step.
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
)


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

if missing:
    print(f"[DOCS-CONFIG] {config_path} no longer switches on checks this site promises:")
    for dotted, check, why in missing:
        print(f"  extra.{dotted} is missing or empty, so {check} does not run -- {why}")
    print("  Restore the key. If dropping the check is intended, remove it from REQUIRED in")
    print("  scripts/check-docs-config.py in the same PR, so the decision is visible.")
    sys.exit(1)

print(f"✅ {config_path}: all {len(REQUIRED)} keys that switch on this site's built-site checks are set.")
