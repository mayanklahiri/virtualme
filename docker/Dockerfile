# syntax=docker/dockerfile:1
FROM golang:1.26-trixie AS controller-build
WORKDIR /src
COPY package.json package-lock.json ./
COPY scripts/build-web.sh scripts/build-web.sh
COPY controller/ controller/
RUN if [ -f controller/tools/fetch-assets.sh ]; then \
      apt-get update && apt-get install -y --no-install-recommends unzip nodejs npm \
      && bash controller/tools/fetch-assets.sh; \
    fi
RUN if [ -f scripts/build-web.sh ]; then npm ci && bash scripts/build-web.sh; fi
RUN cd controller && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/controller ./cmd/controller

FROM debian:stable-slim

LABEL org.opencontainers.image.title="Virtual Me" \
      org.opencontainers.image.description="Private personal background agent: Xvfb, Chromium, Playwright, Valkey, llama.cpp + Gemma, Go control plane" \
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
COPY docker/layers/007-node-playwright.sh /tmp/layers/007-node-playwright.sh
RUN bash /tmp/layers/007-node-playwright.sh
COPY docker/layers/008-s6-overlay.sh /tmp/layers/008-s6-overlay.sh
RUN bash /tmp/layers/008-s6-overlay.sh
COPY docker/layers/009-user.sh /tmp/layers/009-user.sh
RUN bash /tmp/layers/009-user.sh

# Fast-moving top: service tree + controller binary.
COPY docker/rootfs/ /
COPY --from=controller-build /out/controller /usr/local/bin/controller

ENV LANG=C.UTF-8 \
    HOME=/home/virtualme \
    VM_DATA_DIR=/home/virtualme/.virtualme \
    XDG_CONFIG_HOME=/home/virtualme/.virtualme/xdg/config \
    XDG_CACHE_HOME=/home/virtualme/.virtualme/xdg/cache \
    XDG_DATA_HOME=/home/virtualme/.virtualme/xdg/data \
    VM_DISPLAY=:99 \
    VM_RESOLUTION=1600x900x24 \
    VM_MODEL_PATH=/opt/models/gemma-4-E2B-it-Q4_0.gguf \
    VM_LLAMA_PORT=8081 \
    VM_LLAMA_CTX=4096 \
    VM_HTTP_ADDR=:8080 \
    S6_READ_ONLY_ROOT=1 \
    S6_BEHAVIOUR_IF_STAGE2_FAILS=2 \
    S6_CMD_WAIT_FOR_SERVICES_MAXTIME=0

USER virtualme
EXPOSE 8080
ENTRYPOINT ["/init"]
