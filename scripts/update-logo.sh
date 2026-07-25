#!/usr/bin/env bash
# Regenerate committed brand icons, home hero, and README logo embed from
# ./LOGO.png. Source of truth is always the repo-root LOGO.png. Re-runnable.
#
# Usage: bash scripts/update-logo.sh [--skip-github]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SRC="$ROOT/LOGO.png"
BRAND="$ROOT/controller/web/static/brand"
README="$ROOT/README.md"
SOCIAL_DIR="$ROOT/.github"
SOCIAL_OUT="$SOCIAL_DIR/social-preview.png"
SKIP_GITHUB=0
[[ "${1:-}" == "--skip-github" ]] && SKIP_GITHUB=1

need() { command -v "$1" >/dev/null 2>&1 || { echo "update-logo: missing $1" >&2; exit 1; }; }
need gm
need python3
need exiftool

[[ -s "$SRC" ]] || { echo "update-logo: $SRC missing or empty" >&2; exit 1; }

TMP="$(mktemp -d "${TMPDIR:-/tmp}/update-logo.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

# --- helpers -----------------------------------------------------------------

# Resize LOGO into a square PNG with transparent padding; strip profiles.
# Usage: make_square SIZE OUT.png
make_square() {
  local size="$1" out="$2"
  gm convert "$SRC" \
    -filter Lanczos \
    -resize "${size}x${size}" \
    -background none -gravity center -extent "${size}x${size}" \
    +profile '*' \
    "PNG32:${out}.tmp"
  # exiftool strips residual identifying metadata; -all= clears tags
  exiftool -q -overwrite_original -all= "${out}.tmp" >/dev/null
  mv "${out}.tmp" "$out"
}

# Opaque square on a solid tile (apple-touch / android prefer no transparency).
# Usage: make_opaque_square SIZE BG OUT.png
make_opaque_square() {
  local size="$1" bg="$2" out="$3"
  gm convert "$SRC" \
    -filter Lanczos \
    -resize "${size}x${size}" \
    -background "$bg" -gravity center -extent "${size}x${size}" \
    +profile '*' \
    "PNG24:${out}.tmp"
  exiftool -q -overwrite_original -all= "${out}.tmp" >/dev/null
  mv "${out}.tmp" "$out"
}

# Write an SVG that embeds a PNG as a data URL (viewBox 0 0 SIZE SIZE).
# Usage: write_svg_embed PNG SIZE OUT.svg
write_svg_embed() {
  local png="$1" size="$2" out="$3"
  python3 - "$png" "$size" "$out" <<'PY'
import base64, pathlib, sys
png, size, out = pathlib.Path(sys.argv[1]), sys.argv[2], pathlib.Path(sys.argv[3])
b64 = base64.standard_b64encode(png.read_bytes()).decode("ascii")
out.write_text(
    f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {size} {size}">\n'
    f'  <image href="data:image/png;base64,{b64}" width="{size}" height="{size}"'
    f' preserveAspectRatio="xMidYMid meet"/>\n'
    f"</svg>\n",
    encoding="utf-8",
)
PY
}

strip_png() {
  local f="$1"
  gm convert "$f" +profile '*' "PNG32:${f}.tmp"
  exiftool -q -overwrite_original -all= "${f}.tmp" >/dev/null
  mv "${f}.tmp" "$f"
}

# Cover-crop LOGO into a 4:3 JPEG hero (matches .hero-media aspect-ratio).
# Usage: make_hero OUT.jpg
make_hero() {
  local out="$1" w=1280 h=960
  python3 - "$SRC" "$TMP/hero.png" "$w" "$h" <<'PY'
from PIL import Image
import pathlib, sys
src, dest, w, h = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4])
im = Image.open(src).convert("RGBA")
scale = max(w / im.width, h / im.height)
nw, nh = max(1, round(im.width * scale)), max(1, round(im.height * scale))
im = im.resize((nw, nh), Image.Resampling.LANCZOS)
left, top = (nw - w) // 2, (nh - h) // 2
im = im.crop((left, top, left + w, top + h))
# Flatten onto a dark plate so JPEG has no checkerboard in transparent corners.
bg = Image.new("RGB", (w, h), (28, 31, 38))
bg.paste(im, mask=im.split()[3])
bg.save(dest, format="PNG")
PY
  gm convert "$TMP/hero.png" +profile '*' -quality 88 "JPEG:${out}.tmp"
  exiftool -q -overwrite_original -all= "${out}.tmp" >/dev/null
  mv "${out}.tmp" "$out"
}

# --- 0) sanitize source of truth --------------------------------------------

echo "update-logo: sanitizing LOGO.png"
strip_png "$SRC"

# --- 1) raster exports ------------------------------------------------------

mkdir -p "$BRAND" "$SOCIAL_DIR"

echo "update-logo: writing brand PNGs"
make_square 16  "$BRAND/favicon-16.png"
make_square 32  "$BRAND/favicon-32.png"
make_square 48  "$BRAND/favicon-48.png"
make_square 64  "$BRAND/virtualme-mark.png"
make_square 192 "$BRAND/android-chrome-192.png"
make_square 512 "$BRAND/android-chrome-512.png"
make_opaque_square 180 "#18181b" "$BRAND/apple-touch-icon.png"

echo "update-logo: writing hero.jpg"
make_hero "$BRAND/hero.jpg"

# Multi-size ICO (GM has no ICO encoder; Pillow does)
python3 - "$BRAND" <<'PY'
from pathlib import Path
from PIL import Image
brand = Path(__import__("sys").argv[1])
imgs = []
for name, size in [("favicon-16.png", 16), ("favicon-32.png", 32), ("favicon-48.png", 48)]:
    im = Image.open(brand / name).convert("RGBA")
    if im.size != (size, size):
        im = im.resize((size, size), Image.Resampling.LANCZOS)
    imgs.append(im)
# Pillow writes ICO from the largest; pass sizes= for the rest
out = brand / "favicon.ico"
imgs[-1].save(out, format="ICO", sizes=[(16, 16), (32, 32), (48, 48)], append_images=imgs[:-1])
print(f"update-logo: wrote {out} ({out.stat().st_size} bytes)")
PY
# exiftool cannot rewrite ICO; Pillow write has no EXIF payload.

# --- 2) SVG wrappers (favicon + sprite mark) --------------------------------

echo "update-logo: writing SVG embeds"
write_svg_embed "$BRAND/favicon-32.png" 32 "$BRAND/favicon.svg"
write_svg_embed "$BRAND/virtualme-mark.png" 64 "$BRAND/virtualme-mark.svg"
# Normalize mark viewBox to 0 0 24 24 so sidebar/sprite sizing stays familiar
python3 - "$BRAND/virtualme-mark.svg" <<'PY'
from pathlib import Path
import sys
p = Path(sys.argv[1])
text = p.read_text(encoding="utf-8")
text = text.replace('viewBox="0 0 64 64"', 'viewBox="0 0 24 24"', 1)
text = text.replace('width="64" height="64"', 'width="24" height="24"', 1)
p.write_text(text, encoding="utf-8")
PY

# --- 3) GitHub social preview asset (1280x640, solid bg, <1 MiB) ------------

echo "update-logo: writing social preview"
gm convert -size 1280x640 "xc:#1c1f26" +profile '*' "PNG24:$TMP/social-bg.png"
gm convert "$SRC" -filter Lanczos -resize 520x520 +profile '*' "PNG32:$TMP/social-fg.png"
gm composite -gravity center "$TMP/social-fg.png" "$TMP/social-bg.png" "PNG24:${SOCIAL_OUT}.tmp"
exiftool -q -overwrite_original -all= "${SOCIAL_OUT}.tmp" >/dev/null
mv "${SOCIAL_OUT}.tmp" "$SOCIAL_OUT"
# Keep under GitHub's 1 MiB social-preview limit
python3 - "$SOCIAL_OUT" <<'PY'
from pathlib import Path
from PIL import Image
import sys
p = Path(sys.argv[1])
limit = 900_000
if p.stat().st_size <= limit:
    print(f"update-logo: social preview {p.stat().st_size} bytes")
    raise SystemExit(0)
im = Image.open(p).convert("RGB")
for q in (90, 80, 70, 60):
    # recompress via PNG optimize; if still huge, shrink logo plate
    im.save(p, format="PNG", optimize=True, compress_level=9)
    if p.stat().st_size <= limit:
        break
else:
    # last resort: smaller canvas content already baked; just warn
    pass
print(f"update-logo: social preview {p.stat().st_size} bytes")
PY

# --- 4) README data-URL embed -----------------------------------------------

echo "update-logo: updating README.md logo data URL"
README_ICON="$TMP/readme-icon.png"
make_square 64 "$README_ICON"
python3 - "$README" "$README_ICON" <<'PY'
import base64, pathlib, re, sys
readme, icon = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
b64 = base64.standard_b64encode(icon.read_bytes()).decode("ascii")
# Markdown image syntax (not HTML <img>): GitHub renders data-URL embeds
# reliably this way, and the README two-column table stays pure Markdown.
# Single-line Markdown image so README table cells stay valid when re-run.
data_img = f"![Virtual Me](data:image/png;base64,{b64})"
block = f"<!-- update-logo:icon -->{data_img}<!-- /update-logo:icon -->"
text = readme.read_text(encoding="utf-8")
pat = re.compile(
    r"<!-- update-logo:icon -->.*?<!-- /update-logo:icon -->"
    r"|!\[[^\]]*\]\(data:image/png;base64,[^)]+\)"
    r"|<img src=\"(?:data:image/png;base64,[^\"]+|controller/web/static/brand/virtualme-mark\.svg)\"[^>]*>"
    r"(?:\s*<img src=\"controller/web/static/brand/wordmark\.svg\"[^>]*>)?",
    re.S,
)
if not pat.search(text):
    raise SystemExit("update-logo: README logo anchor not found")
readme.write_text(pat.sub(block, text, count=1), encoding="utf-8")
print("update-logo: README icon embed replaced")
PY

# --- 5) optional GitHub social preview upload (no public API) ---------------

if [[ "$SKIP_GITHUB" -eq 0 ]]; then
  echo "update-logo: attempting GitHub social preview upload"
  if ! command -v gh >/dev/null 2>&1; then
    echo "update-logo: gh not found; skip social upload" >&2
  else
    # Undocumented uploads endpoint used by the settings UI. Best-effort only.
    token="$(gh auth token 2>/dev/null || true)"
    remote="$(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || true)"
    if [[ -n "$token" && -n "$remote" ]]; then
      code="$(
        curl -sS -o "$TMP/gh-social.json" -w "%{http_code}" \
          -X PUT \
          -H "Accept: application/vnd.github+json" \
          -H "Authorization: Bearer ${token}" \
          -H "Content-Type: image/png" \
          --data-binary @"$SOCIAL_OUT" \
          "https://uploads.github.com/repos/${remote}/open-graph-image" \
        || true
      )"
      if [[ "$code" == "200" || "$code" == "201" || "$code" == "204" ]]; then
        echo "update-logo: GitHub social preview updated (HTTP $code)"
      else
        # Alternate path some tenants use
        code2="$(
          curl -sS -o "$TMP/gh-social2.json" -w "%{http_code}" \
            -X POST \
            -H "Accept: application/vnd.github+json" \
            -H "Authorization: Bearer ${token}" \
            -H "Content-Type: image/png" \
            --data-binary @"$SOCIAL_OUT" \
            "https://uploads.github.com/repos/${remote}/social_preview" \
          || true
        )"
        if [[ "$code2" == "200" || "$code2" == "201" || "$code2" == "204" ]]; then
          echo "update-logo: GitHub social preview updated (HTTP $code2)"
        else
          echo "update-logo: GitHub has no public social-preview API (tried HTTP $code / $code2)." >&2
          echo "update-logo: asset ready at .github/social-preview.png — upload manually:" >&2
          echo "  https://github.com/${remote}/settings#social-preview" >&2
        fi
      fi
    else
      echo "update-logo: gh auth/repo unavailable; skip social upload" >&2
    fi
  fi
fi

# --- 6) inventory -----------------------------------------------------------

echo "update-logo: outputs"
ls -la \
  "$SRC" \
  "$BRAND"/favicon.svg \
  "$BRAND"/favicon.ico \
  "$BRAND"/favicon-16.png \
  "$BRAND"/favicon-32.png \
  "$BRAND"/favicon-48.png \
  "$BRAND"/virtualme-mark.svg \
  "$BRAND"/virtualme-mark.png \
  "$BRAND"/apple-touch-icon.png \
  "$BRAND"/android-chrome-192.png \
  "$BRAND"/android-chrome-512.png \
  "$BRAND"/hero.jpg \
  "$SOCIAL_OUT"

echo "update-logo: OK"
