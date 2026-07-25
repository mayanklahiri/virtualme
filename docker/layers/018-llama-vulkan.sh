#!/usr/bin/env bash
# Layer 018: llama.cpp prebuilt Vulkan runtime (x86_64 only), pinned release.
# Installed alongside the CPU build in /opt/llama; svc-llama selects
# /opt/llama-vulkan at runtime when VM_LLAMA_GPU=1 and a GPU device node is
# injected (spec 018 amendment 2026-07-24). The NVIDIA Vulkan ICD arrives via
# the NVIDIA Container Toolkit with NVIDIA_DRIVER_CAPABILITIES=all.
# libegl1 (GLVND EGL dispatcher) is required by the NVIDIA ICD: without
# libEGL.so.1 the driver's vk_icdGetInstanceProcAddr fails to initialize,
# llama.cpp enumerates zero Vulkan devices, and inference silently falls
# back to the CPU (diagnosed live 2026-07-24, RTX 3060).
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

# No Vulkan prebuilt exists for arm64; those hosts keep the CPU runtime.
if [ "$(uname -m)" != x86_64 ]; then
  echo "018-llama-vulkan: skipping on $(uname -m) (no upstream Vulkan prebuilt)"
  exit 0
fi

LLAMA_TAG="b10091"
ASSET="llama-${LLAMA_TAG}-bin-ubuntu-vulkan-x64.tar.gz"
SHA256="8636767e0fdf440247913e4ba46a33fe02b8f13181bb11756ab890d73fdecdb4"

apt-get update
apt-get install -y --no-install-recommends libvulkan1 libegl1
rm -rf /var/lib/apt/lists/*

cd /tmp
curl -fsSL --retry 3 -o "$ASSET" \
  "https://github.com/ggml-org/llama.cpp/releases/download/${LLAMA_TAG}/${ASSET}"
echo "${SHA256}  ${ASSET}" | sha256sum -c -
mkdir -p /opt/llama-vulkan
tar -xzf "$ASSET" --strip-components=1 -C /opt/llama-vulkan
rm -f "$ASSET"

# Sanity gate: binary must load on this libc (no GPU needed for --version).
LD_LIBRARY_PATH=/opt/llama-vulkan /opt/llama-vulkan/llama-server --version
