# syntax=docker/dockerfile:1
FROM golang:1.26-trixie AS controller-build
ARG VERSION=dev
WORKDIR /src
COPY package.json package-lock.json ./
COPY scripts/build-web.sh scripts/build-web.sh
COPY scripts/build-icons.mjs scripts/build-icons.mjs
COPY controller/ controller/
RUN if [ -f controller/tools/fetch-assets.sh ]; then \
      apt-get update && apt-get install -y --no-install-recommends unzip nodejs npm \
      && bash controller/tools/fetch-assets.sh; \
    fi
RUN if [ -f scripts/build-web.sh ]; then npm ci && bash scripts/build-web.sh; fi
RUN cd controller && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/controller ./cmd/controller
RUN cd controller && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ttsd ./cmd/ttsd

FROM debian:stable-slim

LABEL org.opencontainers.image.title="Virtual Me" \
      org.opencontainers.image.description="Private personal background agent: Xvfb, Chromium, Valkey, llama.cpp + Gemma, Go control plane" \
      org.opencontainers.image.source="https://github.com/mayanklahiri/virtualme" \
      org.opencontainers.image.licenses="MIT"

# Append-only layer sequence (constitution rule 6): slowest-moving first.
# Each script is COPY'd individually so editing layer N only rebuilds N and above.
COPY docker/layers/001-base.sh /tmp/layers/001-base.sh
RUN bash /tmp/layers/001-base.sh
COPY docker/layers/002-llama-runtime.sh /tmp/layers/002-llama-runtime.sh
RUN bash /tmp/layers/002-llama-runtime.sh
COPY docker/layers/003-model.sh /tmp/layers/003-model.sh
RUN bash /tmp/layers/003-model.sh
COPY docker/layers/004-xvfb-desktop.sh /tmp/layers/004-xvfb-desktop.sh
RUN bash /tmp/layers/004-xvfb-desktop.sh
COPY docker/layers/005-chromium.sh /tmp/layers/005-chromium.sh
RUN bash /tmp/layers/005-chromium.sh
COPY docker/layers/006-valkey.sh /tmp/layers/006-valkey.sh
RUN bash /tmp/layers/006-valkey.sh
# Layer 007 (Node.js + playwright-core) removed by spec 012; the numbering
# gap is permanent (layers are append-only).
COPY docker/layers/008-s6-overlay.sh /tmp/layers/008-s6-overlay.sh
RUN bash /tmp/layers/008-s6-overlay.sh
COPY docker/layers/009-user.sh /tmp/layers/009-user.sh
RUN bash /tmp/layers/009-user.sh
COPY docker/layers/010-vision-projector.sh /tmp/layers/010-vision-projector.sh
RUN bash /tmp/layers/010-vision-projector.sh
COPY docker/layers/011-agent-tools.sh /tmp/layers/011-agent-tools.sh
RUN bash /tmp/layers/011-agent-tools.sh
COPY --chmod=0755 docker/rootfs/usr/local/lib/virtualme/system-manifest.sh /usr/local/lib/virtualme/system-manifest.sh
COPY docker/layers/012-manifest.sh /tmp/layers/012-manifest.sh
RUN bash /tmp/layers/012-manifest.sh
COPY docker/layers/013-sherpa-onnx.sh /tmp/layers/013-sherpa-onnx.sh
RUN bash /tmp/layers/013-sherpa-onnx.sh
COPY docker/layers/014-tts-model.sh /tmp/layers/014-tts-model.sh
RUN bash /tmp/layers/014-tts-model.sh
COPY docker/layers/015-mta.sh /tmp/layers/015-mta.sh
RUN bash /tmp/layers/015-mta.sh
COPY docker/layers/016-tzdata.sh /tmp/layers/016-tzdata.sh
RUN bash /tmp/layers/016-tzdata.sh
COPY docker/layers/017-tts-voice-alba.sh /tmp/layers/017-tts-voice-alba.sh
RUN bash /tmp/layers/017-tts-voice-alba.sh
COPY docker/layers/018-llama-vulkan.sh /tmp/layers/018-llama-vulkan.sh
RUN bash /tmp/layers/018-llama-vulkan.sh

# Fast-moving top: service tree + controller binary.
COPY docker/rootfs/ /
COPY --from=controller-build /out/controller /usr/local/bin/controller
COPY --from=controller-build /out/ttsd /usr/local/bin/ttsd

ENV LANG=C.UTF-8 \
    HOME=/home/virtualme \
    VM_DATA_DIR=/home/virtualme/.virtualme \
    XDG_CONFIG_HOME=/home/virtualme/.virtualme/xdg/config \
    XDG_CACHE_HOME=/home/virtualme/.virtualme/xdg/cache \
    XDG_DATA_HOME=/home/virtualme/.virtualme/xdg/data \
    VM_DISPLAY=:99 \
    VM_RESOLUTION=1600x900x24 \
    VM_MODEL_PATH=/opt/models/gemma-4-E2B-it-Q4_0.gguf \
    VM_MMPROJ_PATH=/opt/models/mmproj-gemma-4-E2B-F16.gguf \
    VM_LLAMA_PORT=8081 \
    VM_LLAMA_CTX=16384 \
    VM_TTS_PORT=8082 \
    VM_SHERPA_DIR=/opt/sherpa-onnx \
    VM_TTS_MODEL_DIR=/opt/models/tts \
    VM_TTS_MAX_CHARS=4096 \
    VM_TTS_CACHE_MAX_MB=256 \
    VM_MAIL_FLUSH_SEC=60 \
    VM_AGENT_MAX_STEPS=25 \
    VM_AGENT_KEEP_TASKS=20 \
    VM_HTTP_ADDR=:8080 \
    S6_READ_ONLY_ROOT=1 \
    S6_BEHAVIOUR_IF_STAGE2_FAILS=2 \
    S6_CMD_WAIT_FOR_SERVICES_MAXTIME=0

USER virtualme
EXPOSE 8080
ENTRYPOINT ["/init"]
