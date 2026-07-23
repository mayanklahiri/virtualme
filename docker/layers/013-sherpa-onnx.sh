#!/usr/bin/env bash
# Layer 013: sherpa-onnx prebuilt CPU runtime (offline TTS CLI), pinned release.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

SHERPA_TAG="v1.13.4"
case "$(uname -m)" in
  x86_64)
    ASSET="sherpa-onnx-${SHERPA_TAG}-linux-x64-shared.tar.bz2"
    SHA256="18887dc13c7d313d0e0f6c164ed31715c27c1c2c4f71acd7c0147dc84cf02514"
    ;;
  aarch64)
    ASSET="sherpa-onnx-${SHERPA_TAG}-linux-aarch64-shared-cpu.tar.bz2"
    SHA256="36c5a3c942358ed635471488f50a28a96181331c935b0dce75a02b7f49913dc2"
    ;;
  *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
esac

apt-get update
apt-get install -y --no-install-recommends bzip2
rm -rf /var/lib/apt/lists/*

cd /tmp
curl -fsSL --retry 3 -o "$ASSET" \
  "https://github.com/k2-fsa/sherpa-onnx/releases/download/${SHERPA_TAG}/${ASSET}"
echo "${SHA256}  ${ASSET}" | sha256sum -c -
mkdir -p /opt/sherpa-onnx
tar -xjf "$ASSET" --strip-components=1 -C /opt/sherpa-onnx
rm -f "$ASSET"
find /opt/sherpa-onnx/bin -maxdepth 1 -type f ! -name 'sherpa-onnx-offline-tts' -delete
ldd /opt/sherpa-onnx/bin/sherpa-onnx-offline-tts | { ! grep 'not found'; }
