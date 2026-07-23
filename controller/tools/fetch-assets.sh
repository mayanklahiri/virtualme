#!/usr/bin/env bash
# Fetch pinned web fonts into web/static/fonts (build-time; not committed).
# Ships only the two variable-font files actually used ("tree-shaken" assets).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

DEST="web/static/fonts"
URL="https://github.com/rsms/inter/releases/download/v4.1/Inter-4.1.zip"
SHA256="9883fdd4a49d4fb66bd8177ba6625ef9a64aa45899767dde3d36aa425756b11e"

if [[ -f "$DEST/InterVariable.woff2" && -f "$DEST/InterVariable-Italic.woff2" ]]; then
  echo "fetch-assets: fonts present"
  exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fsSL --retry 3 -o "$tmp/inter.zip" "$URL"
echo "$SHA256  $tmp/inter.zip" | sha256sum -c -
mkdir -p "$DEST"
unzip -q -j -o "$tmp/inter.zip" 'web/InterVariable.woff2' 'web/InterVariable-Italic.woff2' -d "$DEST"
echo "fetch-assets: OK"
