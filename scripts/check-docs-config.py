#!/usr/bin/env python3
"""Fail when mkdocs.yml stops asking the built-site check for what this site promises.

Usage: check-docs-config.py [mkdocs.yml]

scripts/check_docs_rendered.py is the template's shared check (ai-agent-template v2.2.0, #2407),
and it is opt-in by design: each of its site-specific checks runs only when mkdocs.yml configures
it. That is right for a template, and wrong for this site, which has promised all of them. The
check it replaced held them unconditionally, so deleting a key from mkdocs.yml now switches its
check off and every build stays green -- exactly the failure #2381 guarded against for the
Search Console token, where Google quietly unverifies the property.

So this holds the configuration itself. Each key below must be present and set; removing one is
a decision to make in the PR that removes it, by editing this list, not a side effect.
Exits 1 with one line per missing key.
"""

import sys

import yaml

# (dotted path under extra, what it switches on, why this site needs it)
REQUIRED = (
    ("google_site_verification", "MISSING-VERIFICATION",
     "Search Console ownership: without the tag on every page Google unverifies the site (#2381)"),
    ("social_image", "MISSING-SOCIAL", "the link-preview card on Slack, Teams and LinkedIn"),
    ("docs_standard.project_name", "HOME-LAYOUT", "the H1 and this site's related-tools row"),
    ("docs_standard.home_layout", "HOME-LAYOUT", "the shared home-page layout (#2401)"),
    ("docs_standard.notice.title", "NO-NOTICE", "the community-project notice (#2385, #2391)"),
    ("docs_standard.notice.text", "NO-NOTICE", "the disclaimer the notice must carry"),
    ("docs_standard.notice.link_text", "NO-NOTICE", "the notice's link to the About page"),
    ("docs_standard.related.heading", "HOME-LAYOUT", "the Related Liferay tools section"),
    ("docs_standard.related.intro", "HOME-LAYOUT", "its shared intro line"),
    ("docs_standard.related.rows", "HOME-LAYOUT", "its three shared rows"),
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
