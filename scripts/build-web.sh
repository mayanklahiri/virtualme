#!/usr/bin/env bash
# Minify the SPA from controller/web/static into controller/web/dist with
# external sourcemaps (sourcesContent inlined). Deterministic; no network.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

SRC=controller/web/static
DIST=controller/web/dist
ESBUILD=node_modules/.bin/esbuild

[[ -x "$ESBUILD" ]] || { echo "build-web: esbuild missing; run: npm install" >&2; exit 1; }
[[ -f "$SRC/fonts/InterVariable.woff2" && -f "$SRC/fonts/InterVariable-Italic.woff2" ]] \
  || { echo "build-web: fonts missing; run: bash controller/tools/fetch-assets.sh" >&2; exit 1; }

rm -rf "$DIST"
mkdir -p "$DIST/js" "$DIST/css" "$DIST/fonts"
"$ESBUILD" "$SRC/js/app.js" --bundle --minify --format=esm \
  --sourcemap --sources-content=true --outfile="$DIST/js/app.js"
"$ESBUILD" "$SRC/css/app.css" --minify \
  --sourcemap --sources-content=true --outfile="$DIST/css/app.css"
cp "$SRC/index.html" "$DIST/index.html"
cp "$SRC/fonts/"*.woff2 "$DIST/fonts/"
echo "build-web: OK"
