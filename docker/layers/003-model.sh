#!/usr/bin/env bash
# Layer 003: Gemma 4 E2B instruct GGUF (Q4_0), baked into the image.
# Grounded 2026-07: current Google open-model family, edge variant, ungated mirror.
set -euo pipefail

MODEL_URL="https://huggingface.co/unsloth/gemma-4-E2B-it-GGUF/resolve/main/gemma-4-E2B-it-Q4_0.gguf"
MODEL_SHA256="31d3a3c630d4e71a7416498c42660dd3805066948acaec76a47e1ffac7010132"
MODEL_PATH="/opt/models/gemma-4-E2B-it-Q4_0.gguf"

mkdir -p /opt/models
curl -fSL --retry 3 -o "$MODEL_PATH" "$MODEL_URL"
echo "${MODEL_SHA256}  ${MODEL_PATH}" | sha256sum -c -
chmod 0444 "$MODEL_PATH"
