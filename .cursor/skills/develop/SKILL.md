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
    bash controller/tools/fetch-assets.sh   # once; downloads pinned web assets

## Quality gates

`npm run check` = shell syntax, deterministic LLM-locality/SPA-origin/
persistence-map enforcement, eslint, tsc --checkJs, node --test, CLI dry run,
web build (esbuild minify + sourcemaps into gitignored
`controller/web/dist/`), gofmt/go vet/go test. The pre-commit hook and CI run
the same script. New stateful components must be added to the canonical map in
`specs/007-persistence-locality.md` §1.
Container tests: `bash test/smoke.sh`, `bash test/e2e.sh` (need Docker; e2e
drives the real CLI and includes a restart cycle plus a chat probe).
Soak tests: `./cli.sh soak [--no-build]` (spec 012) rebuilds, restarts on a
fresh data dir, and runs live end-to-end flows from `test/soak.mjs` with
layered hard/soft assertions (initial flows drive the browser agent via chat;
the runner is feature-agnostic).

## Docker layers

| Layer | Capability |
|---|---|
| `001-base.sh` | Debian base tooling |
| `002-llama-runtime.sh` | llama.cpp `b10091` CPU runtime |
| `003-model.sh` | Gemma 4 E2B Q4_0 model |
| `004-xvfb-desktop.sh` | Xvfb, Openbox, x11vnc, noVNC |
| `005-chromium.sh` | Chromium and fonts |
| `006-valkey.sh` | Valkey |
| `008-s6-overlay.sh` | s6-overlay `v3.2.3.2` (layer 007 removed by spec 012; the numbering gap is permanent) |
| `009-user.sh` | Unprivileged `virtualme` user (uid/gid 1000) |
| `010-vision-projector.sh` | Pinned Gemma 4 E2B multimodal projector |
| `011-agent-tools.sh` | scrot and ImageMagick capture tooling |
| `012-manifest.sh` | Image-baked agent system manifest |
| `013-sherpa-onnx.sh` | sherpa-onnx `v1.13.4` offline TTS runtime |
| `014-tts-model.sh` | Pinned Piper `en_US-lessac-medium` voice |
| `015-mta.sh` | Debian dma outbound MTA |
| `016-tzdata.sh` | Host-local timezone data for scheduler wall clocks |

The s6 tree defines `svc-xvfb`, `svc-openbox`, `svc-x11vnc`, `svc-novnc`,
`svc-valkey`, `svc-llama`, `svc-chromium`, `svc-chromium-watchdog`,
`svc-controller`, `svc-tts`, and `svc-mailq`. Chromium's run script sources
`/usr/local/lib/virtualme/chromium-sandbox.sh`; its finish script gives the
profile time to flush before watchdog or container restarts. Its launch flag
rationales are canonical inline comments in `svc-chromium/run`; preserve them
with the flag order.

The documented future 2×2 browser path is to replace Openbox with i3, preset a
fixed splitv/splith grid through `i3-msg`, run one Chromium instance per cell
with separate profiles and CDP ports 9222–9225, and select one or four windows
with `VM_BROWSER_GRID=1|4`. i3 is preferred over matchbox or dwm because its
layout is runtime-scriptable over IPC. This is only a design seam: installing
i3, adding profile/port fan-out, or implementing the environment switch
requires a new spec.

## Controller

| Path | Responsibility |
|---|---|
| `controller/cmd/controller` | Route and subsystem wiring |
| `controller/internal/health` | Concurrent eight-service health probes |
| `controller/internal/procstat` | Per-core CPU and per-service CPU/RSS sampling from `/proc` |
| `controller/internal/ws` | Minimal server-side RFC 6455, hub, client-frame dispatch |
| `controller/internal/state` | Two-second live snapshot collector |
| `controller/internal/metrics` | Persistent four-tier metrics aggregation and querying |
| `controller/internal/chat` | Shared streaming chat, controls, stats, and LLM status |
| `controller/internal/valkey` | Shared dependency-free RESP2 client |
| `controller/internal/jobs` | Reliable priority queue, sequential worker, scheduler, and cancellation |
| `controller/internal/jobs/activity.go` | Persistent activity ledger and websocket replay/broadcast |
| `controller/internal/projects` | Persistent recurring tasks, scheduler source, executor, run summaries, and scratch directories |
| `controller/internal/agent` | Tool-call loop, read-only CDP/DOM observations, OS-level actions, bash, and artifacts |
| `controller/cmd/ttsd`, `controller/internal/tts` | Serialized sherpa subprocess synthesis, WAV parsing, NDJSON client, and streaming helpers |
| `controller/internal/mail` | Stdlib MIME/CID composition, DKIM signing, dma submission, and spool status |
| `controller/web/static` | Hand-written multi-page SPA, themes, charts, markdown, agent hooks |
| `controller/web/static/js/jobs.js` | Queue timeline, live activity, and type-specific job details |
| `controller/web/dist` | Gitignored minified SPA + generated icon sprite |
| `controller/tools/fetch-assets.sh` | Pinned fonts, selected Lucide SVGs, and hero image fetch |
| `scripts/build-icons.mjs` | Deterministic Lucide SVG sprite generation |

Console themes define two complete light/dark token blocks in `app.css`,
including `--brand-a`, `--brand-b`, `--font-scale`, and `--p1` through `--p8`.
Adding a theme also requires entries in the `theme.js` registry, the
`index.html` boot registry, and the CSS swatch registry. Pinned web fonts and
the Earthrise hero image are declared in `controller/tools/fetch-assets.sh`;
the fetched image is copied by `scripts/build-web.sh` and never committed.

The controller maps `tts-req`/`tts-stop` websocket requests to per-connection
`tts-*` streams, serves `POST /v1/audio/speech`, and broadcasts agent `speak`
tool audio with `origin:"chat"`.
It maps `mail-send`/`mail-status-req` requests to `mail-result`/`mail-status`
frames. Persistent dma configuration, queue files, and the DKIM key live under
`$VM_DATA_DIR/mail/`; `svc-mailq` retries deferred messages.
It maps `job-push`/`queue-peek` to `job-pushed`/`queue-state`; all LLM work
runs through the single `internal/jobs` worker, and disconnecting an initiating
websocket cancels that connection's work.
It maps `projects-req`, `project-create`, `project-update`, `project-delete`,
and `project-run` to `projects` snapshots or sender-only `project-error`
frames. Project records and summaries use Valkey; agent scratch space lives
under `$VM_DATA_DIR/projects/<id>/`.
It maps `activity-req` to sender-only `activity` frames and broadcasts
`activity-event` frames after durable writes. Activity feed points are agent
tool completion, chat/project LLM start and finish, each TTS synthesis, and
each mail submission.

## How to add things

- **CLI subcommand**: new `src/commands/<name>.js` exporting `run(argv)`,
  register in `src/main.js` and the help text, add a test, update skills/README.
- **Controller endpoint**: register it in `newMux` in
  `controller/cmd/controller/main.go`, keep behavior in an `internal` package,
  and cover routing plus package behavior with hermetic Go tests.
- **Agent tool**: add its OpenAI schema and executor in
  `controller/internal/agent/tools.go`, inject command execution through
  `Runner`, and add hermetic tests. Browser action tools must use `xdotool`
  OS input; CDP is observation-only and must never call `Input.*`,
  `Page.navigate`, or another state-changing method.
- **Job type**: register an `internal/jobs` executor in controller `main.go`;
  use an interactive envelope for client work or a `RegisterSource` provider
  for scheduled work, and cover retries/cancellation with the hermetic RESP
  fixture.
- **Web icon**: add its Lucide name to `controller/tools/fetch-assets.sh`,
  fetch pinned assets, and let `scripts/build-icons.mjs` rebuild the generated
  sprite during `npm run build:web`.
- **Docker layer**: new `docker/layers/NNN-<slug>.sh` with the next number;
  `set -euo pipefail`; pin URLs+sha256; add a `COPY`+`RUN` pair at the END of
  the layer sequence in `docker/Dockerfile`. Never edit old layers without a
  spec amendment.
- **s6 service**: `docker/rootfs/etc/s6-overlay/s6-rc.d/svc-<name>/` with
  `type`, `run`, `dependencies.d/`, plus an entry in
  `docker/rootfs/etc/s6-overlay/user-bundles.d/user/contents.d/`.
- **Spec**: next number in `specs/`, follow the format of 001–003.

After any structural change run the `/master-update` skill.
