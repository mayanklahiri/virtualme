# syntax=docker/dockerfile:1
FROM golang:1.26-trixie AS controller-build
WORKDIR /src
COPY controller/ ./
RUN if [ -f tools/fetch-assets.sh ]; then \
      apt-get update && apt-get install -y --no-install-recommends unzip \
      && bash tools/fetch-assets.sh; \
    fi
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/controller ./cmd/controller

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

# Fast-moving top: service tree + controller binary.
COPY docker/rootfs/ /
COPY --from=controller-build /out/controller /usr/local/bin/controller

ENV LANG=C.UTF-8 \
    VM_DISPLAY=:99 \
    VM_RESOLUTION=1600x900x24 \
    VM_MODEL_PATH=/opt/models/gemma-4-E2B-it-Q4_0.gguf \
    VM_LLAMA_PORT=8081 \
    VM_LLAMA_CTX=4096 \
    VM_HTTP_ADDR=:8080 \
    S6_BEHAVIOUR_IF_STAGE2_FAILS=2 \
    S6_CMD_WAIT_FOR_SERVICES_MAXTIME=0

VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/init"]
