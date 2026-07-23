#!/usr/bin/env bash
# Layer 008: s6-overlay (PID 1 process supervisor), pinned.
set -euo pipefail

S6_VERSION="v3.2.3.2"
BASE_URL="https://github.com/just-containers/s6-overlay/releases/download/${S6_VERSION}"
NOARCH_SHA256="5379750ed30a84bbd2e2dd74847ba6b5bd29cd0b2e3ea2ec58049b57eb2eda12"
case "$(uname -m)" in
  x86_64)
    ARCH_ASSET="s6-overlay-x86_64.tar.xz"
    ARCH_SHA256="e6befcc96a437a3831386ecfc51808c5d3e939dc5fe3c02ae9284599e8aa2408"
    ;;
  aarch64)
    ARCH_ASSET="s6-overlay-aarch64.tar.xz"
    ARCH_SHA256="b17f17a82e7a515c682a91edaf2ffdabb73f891981b6c1fd712115693a2f8b4c"
    ;;
  *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
esac

cd /tmp
curl -fsSL --retry 3 -O "${BASE_URL}/s6-overlay-noarch.tar.xz"
curl -fsSL --retry 3 -O "${BASE_URL}/${ARCH_ASSET}"
echo "${NOARCH_SHA256}  s6-overlay-noarch.tar.xz" | sha256sum -c -
echo "${ARCH_SHA256}  ${ARCH_ASSET}" | sha256sum -c -
tar -C / -Jxpf s6-overlay-noarch.tar.xz
tar -C / -Jxpf "${ARCH_ASSET}"
rm -f s6-overlay-noarch.tar.xz "${ARCH_ASSET}"
