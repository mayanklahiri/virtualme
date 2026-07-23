---
name: develop
description: Contribute to the virtualme repository — constitution rules, layout, quality gates, how to add CLI subcommands, Docker layers, s6 services, or controller endpoints.
---

# Developing virtualme

Read `AGENTS.md` first; it contains the constitution. Highlights:
zero runtime deps in the npm package; ESM only; append-only numbered Docker
layers; every artifact pinned by sha256; deterministic `scripts/check.sh`
gates everything.

## Setup after clone

    npm install
    git config core.hooksPath .githooks
    bash controller/tools/fetch-assets.sh   # once; downloads pinned fonts

## Quality gates

`npm run check` = shell syntax, eslint, tsc --checkJs, node --test, CLI dry
run, gofmt/go vet/go test. The pre-commit hook and CI run the same script.
Container tests: `bash test/smoke.sh`, `bash test/e2e.sh` (need Docker).

## Docker layers

| Layer | Capability |
|---|---|
| `001-base.sh` | Debian base tooling |
| `002-llama-runtime.sh` | llama.cpp `b10091` CPU runtime |
| `003-model.sh` | Gemma 4 E2B Q4_0 model |
| `004-xvfb-desktop.sh` | Xvfb, Openbox, x11vnc, noVNC |
| `005-chromium.sh` | Chromium and fonts |
| `006-valkey.sh` | Valkey |
| `007-node-playwright.sh` | Node.js and Playwright Core `1.61.1` |
| `008-s6-overlay.sh` | s6-overlay `v3.2.3.2` |

The s6 tree supervises `svc-xvfb`, `svc-openbox`, `svc-x11vnc`, `svc-novnc`,
`svc-valkey`, `svc-llama`, `svc-chromium`, and `svc-controller`.

## Controller

| Path | Responsibility |
|---|---|
| `controller/cmd/controller` | Route and subsystem wiring |
| `controller/internal/health` | Concurrent six-service health probes |
| `controller/internal/ws` | Minimal server-side RFC 6455 and connection hub |
| `controller/internal/state` | Two-second health/system snapshot collector |
| `controller/web/static` | Embedded same-origin vanilla-JS SPA |
| `controller/tools/fetch-assets.sh` | Pinned Inter variable-font fetch |

## How to add things

- **CLI subcommand**: new `src/commands/<name>.js` exporting `run(argv)`,
  register in `src/main.js` and the help text, add a test, update skills/README.
- **Controller endpoint**: register it in `newMux` in
  `controller/cmd/controller/main.go`, keep behavior in an `internal` package,
  and cover routing plus package behavior with hermetic Go tests.
- **Docker layer**: new `docker/layers/NNN-<slug>.sh` with the next number;
  `set -euo pipefail`; pin URLs+sha256; add a `COPY`+`RUN` pair at the END of
  the layer sequence in `docker/Dockerfile`. Never edit old layers without a
  spec amendment.
- **s6 service**: `docker/rootfs/etc/s6-overlay/s6-rc.d/svc-<name>/` with
  `type`, `run`, `dependencies.d/`, plus an entry in `user/contents.d/`.
- **Spec**: next number in `specs/`, follow the format of 001–003.

After any structural change run the `/master-update` skill.
