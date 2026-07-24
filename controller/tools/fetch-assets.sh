#!/usr/bin/env bash
# Fetch pinned web fonts and selected Lucide SVGs (build-time; not committed).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

FONT_DEST="web/static/fonts"
ICON_DEST="web/static/icons"
IMAGE_DEST="web/static/img"
INTER_URL="https://github.com/rsms/inter/releases/download/v4.1/Inter-4.1.zip"
INTER_SHA256="9883fdd4a49d4fb66bd8177ba6625ef9a64aa45899767dde3d36aa425756b11e"
LUCIDE_URL="https://github.com/lucide-icons/lucide/releases/download/1.26.0/lucide-icons-1.26.0.zip"
LUCIDE_SHA256="7b3c98ebbd473db33057f75fd67076957ba59d7a9ccd2098d3754800fe533e84"
HERO_URL="https://upload.wikimedia.org/wikipedia/commons/thumb/a/a8/NASA-Apollo8-Dec24-Earthrise.jpg/1280px-NASA-Apollo8-Dec24-Earthrise.jpg"
HERO_SHA256="da22ac0b5fdbc1ebf1c080c8481d80e2b8b1ea22e2e7fee7215ab0c819e333e0"
ICONS=(house folder-kanban list-checks activity message-circle mail monitor menu x sun moon palette send square trash-2 copy check external-link triangle-alert bot terminal wrench brain clock-3 chevron-down chevron-right volume-2 play pause plus)
FONT_ROWS=(
  "SpaceGrotesk.woff2|https://fonts.gstatic.com/s/spacegrotesk/v22/V8mDoQDjQSkFtoMM3T6r8E7mPbF4Cw.woff2|0640890476fc1198ab4de571fb658de443c4d85b66466ec09534a8737ab1ce9d"
  "JetBrainsMono.woff2|https://fonts.gstatic.com/s/jetbrainsmono/v24/tDbV2o-flEEny0FZhsfKu5WU4xD7OwE.woff2|18be452724bfdc236c074ca94a249a7f41a86752c7d04ab258ce9ed5651f6a7e"
  "Fraunces.woff2|https://fonts.gstatic.com/s/fraunces/v38/6NU78FyLNQOQZAnv9bYEvDiIdE9Ea92uemAk_WBq8U_9v0c2Wa0KxC9TeA.woff2|7234ed860a9cc83045413c4faee63c960a8f2d1917adcf728119307d56e0d783"
  "SourceSerif4.woff2|https://fonts.gstatic.com/s/sourceserif4/v14/vEFI2_tTDB4M7-auWDN0ahZJW1gb8tc.woff2|f2ea9c12d2fe9bd3a9589b02ad2c0909da88f30938c91adc838c4f4098f9f9e0"
  "Nunito.woff2|https://fonts.gstatic.com/s/nunito/v32/XRXV3I6Li01BKofINeaB.woff2|ba344451eab25b217a165363b1982048a5e5830a0daf36577973955a04cac793"
  "AtkinsonHyperlegibleNext.woff2|https://fonts.gstatic.com/s/atkinsonhyperlegiblenext/v7/NaPNcYPdHfdVxJw0IfIP0lvYFqijb-UxCtm5_wdGseiJn3o.woff2|18b2a1a39a2fa298b0ba5390aca68462669826c90925656f1c1f6796e0e1bbaf"
  "AtkinsonHyperlegibleMono.woff2|https://fonts.gstatic.com/s/atkinsonhyperlegiblemono/v8/tss4AoFBci4C4gvhPXrt3wjT1MqSzhA4t7IIcncBiwKthFw.woff2|2706b1ee4f452e744ea91f7e4908cbde9c5d35521bf5ffffc71a382a2de89613"
)
complete=1
for file in InterVariable.woff2 InterVariable-Italic.woff2; do
  [[ -s "$FONT_DEST/$file" ]] || complete=0
done
for row in "${FONT_ROWS[@]}"; do
  IFS='|' read -r file _ _ <<<"$row"
  [[ -s "$FONT_DEST/$file" ]] || complete=0
done
for icon in "${ICONS[@]}"; do
  [[ -s "$ICON_DEST/$icon.svg" ]] || complete=0
done
[[ -s "$IMAGE_DEST/hero-earthrise.jpg" ]] || complete=0
if [[ "$complete" = 1 ]]; then
  echo "fetch-assets: assets present"
  exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$FONT_DEST" "$ICON_DEST" "$IMAGE_DEST"

if [[ ! -f "$FONT_DEST/InterVariable.woff2" || ! -f "$FONT_DEST/InterVariable-Italic.woff2" ]]; then
  curl -fsSL --retry 3 -o "$tmp/inter.zip" "$INTER_URL"
  echo "$INTER_SHA256  $tmp/inter.zip" | sha256sum -c -
  unzip -q -j -o "$tmp/inter.zip" 'web/InterVariable.woff2' 'web/InterVariable-Italic.woff2' -d "$FONT_DEST"
fi

for row in "${FONT_ROWS[@]}"; do
  IFS='|' read -r file url sha <<<"$row"
  if [[ ! -f "$FONT_DEST/$file" ]]; then
    curl -fsSL --retry 3 -o "$tmp/$file" "$url"
    echo "$sha  $tmp/$file" | sha256sum -c -
    mv "$tmp/$file" "$FONT_DEST/$file"
  fi
done

if [[ ! -f "$IMAGE_DEST/hero-earthrise.jpg" ]]; then
  curl -fsSL --retry 3 -o "$tmp/hero-earthrise.jpg" "$HERO_URL"
  echo "$HERO_SHA256  $tmp/hero-earthrise.jpg" | sha256sum -c -
  mv "$tmp/hero-earthrise.jpg" "$IMAGE_DEST/hero-earthrise.jpg"
fi

icons_complete=1
for icon in "${ICONS[@]}"; do
  [[ -s "$ICON_DEST/$icon.svg" ]] || icons_complete=0
done
if [[ "$icons_complete" = 0 ]]; then
  curl -fsSL --retry 3 -o "$tmp/lucide.zip" "$LUCIDE_URL"
  echo "$LUCIDE_SHA256  $tmp/lucide.zip" | sha256sum -c -
  for icon in "${ICONS[@]}"; do
    entry="$(unzip -Z1 "$tmp/lucide.zip" | awk -v name="$icon.svg" '$0 == name || $0 ~ ("/" name "$") { found=$0 } END { print found }')"
    [[ -n "$entry" ]] || { echo "fetch-assets: missing Lucide icon $icon" >&2; exit 1; }
    unzip -p "$tmp/lucide.zip" "$entry" > "$ICON_DEST/$icon.svg"
  done
fi

echo "fetch-assets: OK"
