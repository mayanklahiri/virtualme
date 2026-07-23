#!/usr/bin/env bash
# Layer 010: pinned multimodal projector for Gemma 4 E2B vision.
set -euo pipefail

MMPROJ_URL="https://huggingface.co/unsloth/gemma-4-E2B-it-GGUF/resolve/main/mmproj-F16.gguf"
MMPROJ_SHA256="140be8d7849741f88c50757d529b84373ee8e27052cc2236855b537f4a8215fa"
MMPROJ_PATH="/opt/models/mmproj-gemma-4-E2B-F16.gguf"

mkdir -p /opt/models
curl -fSL --retry 3 -o "$MMPROJ_PATH" "$MMPROJ_URL"
echo "${MMPROJ_SHA256}  ${MMPROJ_PATH}" | sha256sum -c -
chmod 0444 "$MMPROJ_PATH"
