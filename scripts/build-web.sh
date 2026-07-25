#!/usr/bin/env bash
# Minify the SPA from controller/web/static into controller/web/dist with
# external sourcemaps (sourcesContent inlined). Deterministic; no network.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

SRC=controller/web/static
DIST=controller/web/dist
ESBUILD=node_modules/.bin/esbuild

[[ -x "$ESBUILD" ]] || { echo "build-web: esbuild missing; run: npm install" >&2; exit 1; }
FONTS=(InterVariable.woff2 InterVariable-Italic.woff2 SpaceGrotesk.woff2 JetBrainsMono.woff2 Fraunces.woff2 SourceSerif4.woff2 Nunito.woff2 AtkinsonHyperlegibleNext.woff2 AtkinsonHyperlegibleMono.woff2)
for font in "${FONTS[@]}"; do
  [[ -s "$SRC/fonts/$font" ]] \
    || { echo "build-web: assets missing; run: bash controller/tools/fetch-assets.sh" >&2; exit 1; }
done
[[ -d "$SRC/icons" ]] && compgen -G "$SRC/icons/*.svg" >/dev/null \
  || { echo "build-web: assets missing; run: bash controller/tools/fetch-assets.sh" >&2; exit 1; }
[[ -s "$SRC/brand/hero.jpg" ]] \
  || { echo "build-web: brand/hero.jpg missing; run: bash scripts/update-logo.sh" >&2; exit 1; }

rm -rf "$DIST"
mkdir -p "$DIST/js" "$DIST/css" "$DIST/fonts" "$DIST/img" "$DIST/brand"
"$ESBUILD" "$SRC/js/app.js" --bundle --minify --format=esm \
  --sourcemap --sources-content=true --outfile="$DIST/js/app.js"
"$ESBUILD" "$SRC/css/app.css" --minify \
  --sourcemap --sources-content=true --outfile="$DIST/css/app.css"
cp "$SRC/index.html" "$DIST/index.html"
for font in "${FONTS[@]}"; do
  cp "$SRC/fonts/$font" "$DIST/fonts/"
done
# Optional legacy img/ assets (fonts/icons still come from fetch-assets).
if compgen -G "$SRC/img/*" >/dev/null; then
  cp "$SRC/img/"* "$DIST/img/"
fi
cp "$SRC/brand/favicon.svg" "$DIST/favicon.svg"
cp "$SRC/brand/favicon.ico" "$DIST/favicon.ico"
cp "$SRC/brand/apple-touch-icon.png" "$DIST/apple-touch-icon.png"
cp "$SRC/brand/"*.png "$DIST/brand/"
cp "$SRC/brand/hero.jpg" "$DIST/brand/hero.jpg"
node scripts/build-icons.mjs
echo "build-web: OK"
