#!/usr/bin/env bash
# Assert that markup the SOURCE clearly intends was actually consumed by the build.
#
# This is the check that found #2081, and nothing else would have. Reading mkdocs.yml told us
# "no markdown_extensions configured"; only grepping the built HTML showed what that cost
# readers: 33 GitHub alert markers printed as literal "[!WARNING]" text inside ordinary
# blockquotes, and every architecture diagram rendered as a wall of code.
#
# Asserts the CLASS, not the instances: no alert marker of any type may survive anywhere, and
# no mermaid block may render as code anywhere. Adding a sixth alert type or a tenth diagram
# needs no change here.
#
# Takes the built site directory (default _site, which is what CI builds).
set -euo pipefail

SITE="${1:-_site}"

if [ ! -d "$SITE" ]; then
  echo "[NO-SCAN] '$SITE' is not a directory -- nothing was checked."
  echo "  Build the site first: mkdocs build --site-dir $SITE --strict"
  exit 1
fi

pages=$(find "$SITE" -name '*.html' | wc -l | tr -d ' ')
if [ "$pages" -lt 5 ]; then
  echo "[NO-SCAN] only $pages HTML page(s) under '$SITE'. That is too few to be a real build,"
  echo "  and a near-empty directory would pass every assertion below."
  exit 1
fi

fail=0

# GitHub alert syntax. Native on github.com, unknown to Python-Markdown: without the build-time
# conversion these reach the reader as text.
# `|| true` is load-bearing: grep exits 1 when it finds nothing, which under `set -e` with
# `pipefail` is exactly the HEALTHY case. Without it this guard aborted, silently, on a clean
# build -- failing CI on correct documentation and reporting nothing at all.
markers=$(grep -rho '\[!\(NOTE\|IMPORTANT\|TIP\|WARNING\|CAUTION\)\]' "$SITE" --include='*.html' | wc -l | tr -d ' ' || true)
if [ "$markers" -ne 0 ]; then
  echo "[LEAKED-ALERT] $markers GitHub alert marker(s) are visible to readers as literal text."
  echo "  They render inside an ordinary blockquote instead of a callout, so the warnings that"
  echo "  matter most read as ordinary prose. Pages affected:"
  grep -rl '\[!\(NOTE\|IMPORTANT\|TIP\|WARNING\|CAUTION\)\]' "$SITE" --include='*.html' | sed "s|^$SITE/|    |" || true
  fail=1
fi

# Mermaid handed to the syntax highlighter instead of to mermaid.js.
diagrams=$(grep -rho 'class="language-mermaid"' "$SITE" --include='*.html' | wc -l | tr -d ' ' || true)
if [ "$diagrams" -ne 0 ]; then
  echo "[UNRENDERED-DIAGRAM] $diagrams mermaid block(s) rendered as a code block."
  echo "  Readers get the diagram's source text instead of the diagram. Pages affected:"
  grep -rl 'class="language-mermaid"' "$SITE" --include='*.html' | sed "s|^$SITE/|    |" || true
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo
  echo "❌ The published output does not match what the source asks for."
  exit 1
fi

echo "✅ $pages page(s): no alert markers leaked, no mermaid rendered as code."
