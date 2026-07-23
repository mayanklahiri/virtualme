#!/usr/bin/env bash
# Layer 002: llama.cpp prebuilt CPU runtime, pinned release.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

LLAMA_TAG="b10091"
case "$(uname -m)" in
  x86_64)
    ASSET="llama-${LLAMA_TAG}-bin-ubuntu-x64.tar.gz"
    SHA256="d52fa1542d0aba5f2f7dbd86cf694f80db8d1c0bb1874b6d2ad15bebaa0efc6c"
    ;;
  aarch64)
    ASSET="llama-${LLAMA_TAG}-bin-ubuntu-arm64.tar.gz"
    SHA256="f4167a723abeee0c58e1ed5c0f6c58380cceece9e969878bb5833855a5e042d5"
    ;;
  *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
esac

apt-get update
CURL_PKG=libcurl4
if apt-cache show libcurl4t64 >/dev/null 2>&1; then CURL_PKG=libcurl4t64; fi
apt-get install -y --no-install-recommends libgomp1 "$CURL_PKG"
rm -rf /var/lib/apt/lists/*

cd /tmp
curl -fsSL --retry 3 -o "$ASSET" \
  "https://github.com/ggml-org/llama.cpp/releases/download/${LLAMA_TAG}/${ASSET}"
echo "${SHA256}  ${ASSET}" | sha256sum -c -
mkdir -p /opt/llama
tar -xzf "$ASSET" --strip-components=1 -C /opt/llama
rm -f "$ASSET"

# Sanity gate: prebuilt must run on this libc/arch. If this fails, STOP and
# use the source-build fallback documented in specs/002-container.md §3.1.
LD_LIBRARY_PATH=/opt/llama /opt/llama/llama-server --version
