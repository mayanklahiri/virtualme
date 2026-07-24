#!/usr/bin/env bash
# Layer 017: Piper VITS en_US ryan (medium) voice for sherpa-onnx, baked in.
set -euo pipefail

MODEL_URL="https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/vits-piper-en_US-ryan-medium.tar.bz2"
MODEL_SHA256="c546af78b6395b4e7c4ce1ed899438b64426a362f5d4ec5fecd090ded9ad7505"
MODEL_DIR="/opt/models/tts"

mkdir -p "$MODEL_DIR"
cd /tmp
curl -fSL --retry 3 -o voice-ryan.tar.bz2 "$MODEL_URL"
echo "${MODEL_SHA256}  voice-ryan.tar.bz2" | sha256sum -c -
tar -xjf voice-ryan.tar.bz2 -C "$MODEL_DIR"
rm -f voice-ryan.tar.bz2
find "$MODEL_DIR/vits-piper-en_US-ryan-medium" -type f -exec chmod 0444 {} +
test -f "$MODEL_DIR/vits-piper-en_US-ryan-medium/en_US-ryan-medium.onnx"
test -f "$MODEL_DIR/vits-piper-en_US-ryan-medium/tokens.txt"
test -d "$MODEL_DIR/vits-piper-en_US-ryan-medium/espeak-ng-data"
