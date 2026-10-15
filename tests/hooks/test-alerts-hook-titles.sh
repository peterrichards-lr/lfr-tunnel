#!/usr/bin/env bash
# test-alerts-hook-titles.sh — the GitHub-alerts hook titles a callout only when asked (#2391)
#
# scripts/mkdocs_github_alerts.py lifts an alert's first-line lead -- an emoji, a bold phrase, an
# em dash -- into the admonition's title, so the home page's notice renders titled "Community
# project" while its source stays GitHub-native. The emoji is the opt-in: aws_setup_guide.md
# opens a NOTE with a bold phrase and an em dash in the middle of a sentence, and titling that
# would cut the sentence in half. Each case below pins one side of that line.
#
# bash 3.2 compatible (macOS /bin/bash). See AGENTS.md.

set -uo pipefail

REPO_ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$REPO_ROOT" || exit 1

OUT=$(python3 - <<'PY' 2>&1
import sys
sys.path.insert(0, "scripts")
from mkdocs_github_alerts import _convert

def first_admonition(src):
    out = _convert(src).split("\n")
    i = next(n for n, l in enumerate(out) if l.startswith("!!!"))
    body = next(l for l in out[i + 1:] if l.strip())
    return out[i], body.strip()

cases = [
    # (label, source, expected header, expected first body line)
    ("emoji + bold lead + em dash is titled",
     "> [!NOTE]\n> ℹ️ **Community project** — not an official product. [About](about.md)\n",
     '!!! info "Community project"', "Not an official product. [About](about.md)"),
    ("ℹ️ does not downgrade a CAUTION to info (#2393: only a NOTE is lifted)",
     "> [!CAUTION]\n> ℹ️ **Data loss** — deletes everything.\n",
     '!!! danger "Data loss"', "Deletes everything."),
    ("another emoji keeps the alert's own type (#2393: only ℹ️ picks info)",
     "> [!NOTE]\n> ⚠️ **Heads up** — check first.\n",
     '!!! note "Heads up"', "Check first."),
    ("a bold lead without the emoji is left alone",
     "> [!NOTE]\n> **Cost Explorer must be enabled** — a one-time toggle\n> — before any of the above will work.\n",
     "!!! note", "**Cost Explorer must be enabled** — a one-time toggle"),
    ("an emoji without a bold lead is left alone",
     "> [!TIP]\n> ⚠️ plain text — with a dash.\n",
     "!!! tip", "⚠️ plain text — with a dash."),
    ("a non-emoji symbol lead (an arrow) is left alone",
     "> [!NOTE]\n> → **Next step** — run it\n",
     "!!! note", "→ **Next step** — run it"),
    ("a non-ASCII word lead is left alone, not dropped",
     "> [!NOTE]\n> Важно **Внимание** — y\n",
     "!!! note", "Важно **Внимание** — y"),
    ("a remainder starting with a cased name keeps its case",
     "> [!NOTE]\n> ⚠️ **Mac** — macOS only.\n",
     '!!! note "Mac"', "macOS only."),
    ("a bold lead with a hyphen, not an em dash, is left alone",
     "> [!WARNING]\n> ⚠️ **Careful** - hyphen only.\n",
     "!!! warning", "⚠️ **Careful** - hyphen only."),
]
bad = 0
for label, src, head, body in cases:
    got = first_admonition(src)
    if got == (head, body):
        print(f"  PASS: {label}")
    else:
        print(f"  FAIL: {label}: got {got!r}, want {(head, body)!r}")
        bad += 1

# The real sources: only the home page's notice is titled, anywhere under docs/.
import glob
titled = [(f, l) for f in sorted(glob.glob("docs/**/*.md", recursive=True))
          for l in _convert(open(f, encoding="utf-8").read()).split("\n")
          if l.startswith("!!!") and '"' in l]
if titled == [("docs/README.md", '!!! info "Community project"')]:
    print("  PASS: only docs/README.md's notice is titled across the real docs/")
else:
    print(f"  FAIL: titled callouts across docs/ are {titled!r}")
    bad += 1
sys.exit(1 if bad else 0)
PY
)
RC=$?
echo "$OUT"
if [ "$RC" -ne 0 ]; then
    echo "❌ alerts-hook title cases failed"
    exit 1
fi
echo "✅ alerts-hook titles: opt-in by emoji lead only"
