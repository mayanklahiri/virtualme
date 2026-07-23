#!/usr/bin/env bash
# Layer 014: Piper VITS en_US lessac (medium) voice for sherpa-onnx, baked in.
set -euo pipefail

MODEL_URL="https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/vits-piper-en_US-lessac-medium.tar.bz2"
MODEL_SHA256="9e3febfacf0abf4270172d2958bcec246032b7e88efc2720840cc80c93de334e"
MODEL_DIR="/opt/models/tts"

mkdir -p "$MODEL_DIR"
cd /tmp
curl -fSL --retry 3 -o voice.tar.bz2 "$MODEL_URL"
echo "${MODEL_SHA256}  voice.tar.bz2" | sha256sum -c -
tar -xjf voice.tar.bz2 -C "$MODEL_DIR"
rm -f voice.tar.bz2
find "$MODEL_DIR" -type f -exec chmod 0444 {} +
test -f "$MODEL_DIR/vits-piper-en_US-lessac-medium/en_US-lessac-medium.onnx"
test -f "$MODEL_DIR/vits-piper-en_US-lessac-medium/tokens.txt"
test -d "$MODEL_DIR/vits-piper-en_US-lessac-medium/espeak-ng-data"
