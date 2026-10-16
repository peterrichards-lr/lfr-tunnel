#!/usr/bin/env python3
"""Fail when mkdocs.yml drops the related-projects block while HOME-LAYOUT is required.

Usage: check-docs-config.py [mkdocs.yml]

TEMPORARY. Delete this file, its CI step in ci.yml, its docs-filter entry and its fixtures in
tests/hooks/test-gate-fires.sh when ai-agent-template#163 ships.

scripts/check_docs_rendered.py (the template's shared check, v2.4.0) requires HOME-LAYOUT on this
site through the Liferay profile, and with it related.heading, intro and rows -- but only when a
`related:` block exists. Deleting the whole block switches the related-section check off: the
review of #2412 deleted it and the "Related Liferay tools" section, built, and HOME-LAYOUT passed
with no related section on the site. That is #2381's failure class (a deleted key turns a check
off and every build stays green), so this holds the one case until the template does.

HOME-LAYOUT counts as required exactly as the shared check decides it -- the profile's defaults
(its stylesheet in extra_css) plus extra.docs_standard.required, less a waive with a reason --
by calling the shared check's own profile_required(), so the two cannot disagree. A site that
waives HOME-LAYOUT, or is not on the profile and does not list it, passes.
Exits 1 when the block is missing and HOME-LAYOUT is required.
"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from check_docs_rendered import load_config, profile_required  # noqa: E402

config_path = sys.argv[1] if len(sys.argv) > 1 else "mkdocs.yml"
config = load_config(config_path)
standard = (config.get("extra") or {}).get("docs_standard") or {}

_, by_profile = profile_required(config)
listed = standard.get("required") if isinstance(standard.get("required"), list) else []
waive = standard.get("waive") if isinstance(standard.get("waive"), dict) else {}
waived = isinstance(waive.get("HOME-LAYOUT"), str) and waive["HOME-LAYOUT"].strip()
home_layout_required = ("HOME-LAYOUT" in by_profile or "HOME-LAYOUT" in listed) and not waived

if home_layout_required and not standard.get("related"):
    print(f"[DOCS-CONFIG] {config_path}: extra.docs_standard.related is missing while HOME-LAYOUT "
          "is required,")
    print("  so the shared check skips the Related Liferay tools section entirely "
          "(ai-agent-template#163).")
    print("  Restore the block, or waive HOME-LAYOUT with a reason in extra.docs_standard.waive.")
    sys.exit(1)

print(f"✅ {config_path}: the related-projects block is present"
      if home_layout_required else
      f"✅ {config_path}: HOME-LAYOUT is not required here, so the related block is not needed")
