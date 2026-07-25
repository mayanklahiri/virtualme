#!/usr/bin/env bash
# Propagate docs/src/screenshots assets into README (and future doc) image
# markers. Re-runnable; append new transforms below.
#
# Usage: bash scripts/update-doc-images.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

README="$ROOT/README.md"
SHOT_DIR="$ROOT/docs/src/screenshots"

need() { command -v "$1" >/dev/null 2>&1 || { echo "update-doc-images: missing $1" >&2; exit 1; }; }
need python3

[[ -f "$README" ]] || { echo "update-doc-images: $README missing" >&2; exit 1; }

# --- README screenshot strip (path embeds, display width 480) ---------------
# Prefer HTML <img width="480"> so the strip renders at the canonical
# screenshot width. Markers stay single-line for stable rewrites.

wire_shot() {
  local slug="$1" alt="$2" rel="docs/src/screenshots/${1}.jpg"
  local src="$SHOT_DIR/${1}.jpg"
  [[ -s "$src" ]] || { echo "update-doc-images: $src missing or empty" >&2; exit 1; }
  echo "update-doc-images: wiring ${rel} into README.md"
  python3 - "$README" "$slug" "$rel" "$alt" <<'PY'
import pathlib, re, sys

readme = pathlib.Path(sys.argv[1])
slug, rel, alt = sys.argv[2], sys.argv[3], sys.argv[4]
img = f'<img src="{rel}" alt="{alt}" width="480">'
block = f"<!-- update-doc-images:{slug} -->{img}<!-- /update-doc-images:{slug} -->"
text = readme.read_text(encoding="utf-8")
pat = re.compile(
    rf"<!-- update-doc-images:{re.escape(slug)} -->.*?<!-- /update-doc-images:{re.escape(slug)} -->"
    rf"|!\[[^\]]*\]\({re.escape(rel)}\)"
    rf'|<img src="{re.escape(rel)}"[^>]*>',
    re.S,
)
if not pat.search(text):
    raise SystemExit(
        f"update-doc-images: README {slug} anchor not found "
        "(expected markers, markdown image, or img tag)"
    )
readme.write_text(pat.sub(block, text, count=1), encoding="utf-8")
print(f"update-doc-images: {slug} embed -> {rel} (width=480)")
PY
}

wire_shot "home-route" "Virtual Me home"
wire_shot "chat" "Virtual Me chat"
wire_shot "desktop" "Virtual Me desktop"

echo "update-doc-images: OK"
