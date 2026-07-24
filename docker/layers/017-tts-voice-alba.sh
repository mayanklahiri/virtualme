#!/usr/bin/env bash
# Layer 017: Piper VITS en_GB alba (medium) voice for sherpa-onnx, baked in.
# Replaces the earlier en_US ryan voice (spec 020 amendment 2026-07-24).
set -euo pipefail

MODEL_URL="https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/vits-piper-en_GB-alba-medium.tar.bz2"
MODEL_SHA256="fcd45962906933eec4431d3688f7d74aaac8713c87c6717f91fd3b23463aa1a1"
MODEL_DIR="/opt/models/tts"

mkdir -p "$MODEL_DIR"
cd /tmp
curl -fSL --retry 3 -o voice-alba.tar.bz2 "$MODEL_URL"
echo "${MODEL_SHA256}  voice-alba.tar.bz2" | sha256sum -c -
tar -xjf voice-alba.tar.bz2 -C "$MODEL_DIR"
rm -f voice-alba.tar.bz2
find "$MODEL_DIR/vits-piper-en_GB-alba-medium" -type f -exec chmod 0444 {} +
test -f "$MODEL_DIR/vits-piper-en_GB-alba-medium/en_GB-alba-medium.onnx"
test -f "$MODEL_DIR/vits-piper-en_GB-alba-medium/tokens.txt"
test -d "$MODEL_DIR/vits-piper-en_GB-alba-medium/espeak-ng-data"
