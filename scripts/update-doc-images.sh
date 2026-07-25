#!/usr/bin/env bash
# Propagate docs/src/screenshots assets into README (and future doc) image
# markers. Re-runnable; append new transforms below.
#
# Usage: bash scripts/update-doc-images.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

README="$ROOT/README.md"
HOME_ROUTE="$ROOT/docs/src/screenshots/home-route.jpg"
HOME_ROUTE_REL="docs/src/screenshots/home-route.jpg"

need() { command -v "$1" >/dev/null 2>&1 || { echo "update-doc-images: missing $1" >&2; exit 1; }; }
need python3

[[ -s "$HOME_ROUTE" ]] || { echo "update-doc-images: $HOME_ROUTE missing or empty" >&2; exit 1; }
[[ -f "$README" ]] || { echo "update-doc-images: $README missing" >&2; exit 1; }

# --- 1) README Quick start: home-route.jpg path embed -----------------------
# Large JPEG data-URLs break Markdown previewers (wrap after `]`); GitHub also
# strips data: image URIs. Keep a relative-path Markdown image.

echo "update-doc-images: wiring ${HOME_ROUTE_REL} into README.md"
python3 - "$README" "$HOME_ROUTE_REL" <<'PY'
import pathlib, re, sys

readme = pathlib.Path(sys.argv[1])
rel = sys.argv[2]
img = f"![Virtual Me home]({rel})"
block = (
    f"<!-- update-doc-images:home-route -->\n"
    f"{img}\n"
    f"<!-- /update-doc-images:home-route -->"
)
text = readme.read_text(encoding="utf-8")
pat = re.compile(
    r"<!-- update-doc-images:home-route -->.*?<!-- /update-doc-images:home-route -->"
    r"|!\[[^\]]*\]\(docs/(?:src/)?screenshots/home-route\.jpg\)"
    r"|!\[[^\]]*\]\(data:image/jpeg;base64,[^)]+\)",
    re.S,
)
if not pat.search(text):
    raise SystemExit(
        "update-doc-images: README home-route anchor not found "
        "(expected markers, path image, or jpeg data-URL)"
    )
readme.write_text(pat.sub(block, text, count=1), encoding="utf-8")
print(f"update-doc-images: home-route embed -> {rel}")
PY

echo "update-doc-images: OK"
