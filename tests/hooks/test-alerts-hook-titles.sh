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
    ("a lifted title renders as info whatever the type (option B, #2391)",
     "> [!CAUTION]\n> ⚠️ **Heads up** — check first.\n",
     '!!! info "Heads up"', "Check first."),
    ("an untitled alert keeps its own type",
     "> [!CAUTION]\n> Deletes everything.\n",
     '!!! danger "Caution"', "Deletes everything."),
    ("[!NOTE] keeps GitHub's label \"Note\" in a note box",
     "> [!NOTE]\n> Body.\n",
     '!!! note "Note"', "Body."),
    ("[!TIP] keeps GitHub's label \"Tip\" in a tip box",
     "> [!TIP]\n> Body.\n",
     '!!! tip "Tip"', "Body."),
    ("[!IMPORTANT] keeps GitHub's label \"Important\" in a info box",
     "> [!IMPORTANT]\n> Body.\n",
     '!!! info "Important"', "Body."),
    ("[!WARNING] keeps GitHub's label \"Warning\" in a warning box",
     "> [!WARNING]\n> Body.\n",
     '!!! warning "Warning"', "Body."),
    ("[!CAUTION] keeps GitHub's label \"Caution\" in a danger box",
     "> [!CAUTION]\n> Body.\n",
     '!!! danger "Caution"', "Body."),
    ("a bold lead without the emoji is left alone",
     "> [!NOTE]\n> **Cost Explorer must be enabled** — a one-time toggle\n> — before any of the above will work.\n",
     '!!! note "Note"', "**Cost Explorer must be enabled** — a one-time toggle"),
    ("an emoji without a bold lead is left alone",
     "> [!TIP]\n> ⚠️ plain text — with a dash.\n",
     '!!! tip "Tip"', "⚠️ plain text — with a dash."),
    ("a non-emoji symbol lead (an arrow) is left alone",
     "> [!NOTE]\n> → **Next step** — run it\n",
     '!!! note "Note"', "→ **Next step** — run it"),
    ("a non-ASCII word lead is left alone, not dropped",
     "> [!NOTE]\n> Важно **Внимание** — y\n",
     '!!! note "Note"', "Важно **Внимание** — y"),
    ("a remainder starting with a cased name keeps its case",
     "> [!NOTE]\n> ⚠️ **Mac** — macOS only.\n",
     '!!! info "Mac"', "macOS only."),
    ("a bold lead with a hyphen, not an em dash, is left alone",
     "> [!WARNING]\n> ⚠️ **Careful** - hyphen only.\n",
     '!!! warning "Warning"', "⚠️ **Careful** - hyphen only."),
]
bad = 0
for label, src, head, body in cases:
    got = first_admonition(src)
    if got == (head, body):
        print(f"  PASS: {label}")
    else:
        print(f"  FAIL: {label}: got {got!r}, want {(head, body)!r}")
        bad += 1

# The real sources: only the home page's notice has a LIFTED title, anywhere under docs/. Every
# alert now carries an explicit title -- GitHub's own label (#2391) -- so a lifted title is one
# that is not one of those five labels.
import glob, re
LABELS = {"Note", "Tip", "Important", "Warning", "Caution"}
titled = [(f, l) for f in sorted(glob.glob("docs/**/*.md", recursive=True))
          for l in _convert(open(f, encoding="utf-8").read()).split("\n")
          if (m := re.match(r'!!! \S+ "(.*)"$', l)) and m.group(1) not in LABELS]
if titled == [("docs/README.md", '!!! info "Community project"')]:
    print("  PASS: only docs/README.md's notice has a lifted title across the real docs/")
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
