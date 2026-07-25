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
    npm ci --prefix docs
    git config core.hooksPath .githooks
    bash controller/tools/fetch-assets.sh   # once; downloads pinned web assets

## Quality gates

`npm run check` = shell syntax, deterministic LLM-locality/SPA-origin/
persistence-map enforcement, eslint, tsc --checkJs, node --test, CLI dry run,
generated shared-theme drift, isolated offline Astro docs check/build, web
build (esbuild minify + sourcemaps into gitignored
`controller/web/dist/`), gofmt/go vet/go test, and generated configuration
reference drift. The pre-commit hook and CI run
the same script. New stateful components must be added to the canonical map in
`specs/007-persistence-locality.md` §1.
Container tests: `bash test/smoke.sh`, `bash test/e2e.sh` (need Docker; e2e
drives the real CLI and includes a restart cycle plus a chat probe).
Soak tests: `./cli.sh soak [--no-build]` (spec 012) builds once, runs the
full e2e suite on that image (`E2E_SKIP_BUILD=1`), then runs live flows from
`test/soak.mjs` on a fresh data dir with layered hard/soft assertions (flows
drive the browser agent via chat or invoke tools manually; the runner is
feature-agnostic).
The Node gate also discovers `test/fixtures/*.dom.json`, runs the production
`read_page` extractor, evaluates optional `*.props.mjs` properties, and checks
`*.digest.golden.yaml` snapshots (`REGEN_GOLDENS=1` regenerates them). Live
fixture capture is development-only: navigate a healthy `:8080` browser and
run `node test/capture-dom.mjs <fixture-name>`.

Documentation development is source-checkout-only:
`./cli.sh docs dev [--host <host>] [--port <port>]` and
`./cli.sh docs build`. Site tooling is exact-pinned under `docs/`; ordinary
builds never install or fetch. `common/themes/themes.json` is the single
controller/docs theme source. Regenerate with
`node scripts/generate-themes.mjs` and verify with `--check`.
The complete configuration reference is generated only from the embedded
schema: `(cd controller && go run ./cmd/configctl docs --output
../docs/src/generated/config-reference.json)`; use `docs --check` for drift.
`.github/workflows/docs.yml` installs both toolchains, runs local Playwright
tests, and publishes branch-root static output to the orphan `docs` branch.

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
| `016-tzdata.sh` | Host-local timezone data for scheduler wall clocks (layer 017, the second TTS voice, was removed with the voice; the numbering gap is permanent) |
| `018-llama-vulkan.sh` | llama.cpp `b10091` Vulkan runtime plus `libvulkan1`/`libegl1` in `/opt/llama-vulkan` (x86_64 only; the NVIDIA Vulkan ICD needs `libEGL.so.1` to initialize) |

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
| `controller/internal/gpu` | Cached NVIDIA/AMD/Intel detection and optional utilization sampling |
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
| `controller/internal/actuation` | Global lock serializing OS-level mouse/keyboard input |
| `controller/internal/jiggler` | Default-on humanlike mouse trajectories, Valkey state, and burst lifecycle |
| `controller/cmd/ttsd`, `controller/internal/tts` | Single-voice (Lessac) synthesis with unknown-voice fallback, sentence WAV cache, Valkey speech log, NDJSON client, and streaming helpers |
| `controller/internal/mail` | Stdlib MIME/CID composition, DKIM signing, dma submission, and defensive spool transparency |
| `controller/internal/datafs` | Read-only, GET-only, path-contained list/file/recursive-size HTTP view of `$VM_DATA_DIR`; directory sizes use five-minute Valkey caching (no write endpoints — adding one requires a new spec) |
| `controller/internal/config` | Embedded sole-source schema, typed model, strict YAML, precedence, secrets, validation, and atomic persistence |
| `controller/internal/configapi` | Redacted REST projections, optimistic save, notifier seam, and restart coordination |
| `controller/internal/notifications` | Durable notification model/validation, atomic Valkey scripts, websocket protocol, and crash/clean lifecycle marker |
| `controller/cmd/configctl` | Container preflight/service export and deterministic docs export/check |
| `controller/prompts` | Embedded plain-text agent and fallback-chat system prompts |
| `controller/web/static` | Hand-written multi-page SPA, themes, charts, markdown, agent hooks |
| `controller/web/static/js/jobs.js` | Queue, filtered live activity with durations, and type-specific job details |
| `controller/web/static/js/tools.js` | Server-manifest tool list, schema-generated forms, and shape-rendered manual results |
| `controller/web/static/js/data.js` | Sortable icon/list data explorer, `?path=` deep links, persisted pane/view preferences, and typed file viewers over `/api/data/*` (`innerHTML` forbidden) |
| `controller/web/static/js/tree.js` | Shared collapsible-tree widget for YAML/JSON values |
| DOM-free SPA modules | `markdown-table.js`, `chart-data.js`, `duration.js`, `tts-stream.js`, `tools-render.js`, `yaml-lite.js` carry pure logic covered by Node unit tests |
| `controller/web/dist` | Gitignored minified SPA + generated icon sprite |
| `controller/tools/fetch-assets.sh` | Pinned fonts and selected Lucide SVGs (hero comes from `update-logo.sh`) |
| `scripts/build-icons.mjs` | Deterministic Lucide SVG sprite generation |

System-prompt wording in `controller/prompts/` is runtime behavior; changes
require a spec amendment.

Console themes define two complete light/dark token blocks in `app.css`,
including `--brand-a`, `--brand-b`, `--font-scale`, and `--p1` through `--p8`.
Adding a theme also requires entries in the `theme.js` registry, the
`index.html` boot registry, and the CSS swatch registry. Pinned web fonts and
Lucide icons are declared in
`controller/tools/fetch-assets.sh` (build-time, not committed). Committed brand
assets live under `controller/web/static/brand/`. The raster logo source of
truth is repo-root `LOGO.png`; re-run `bash scripts/update-logo.sh` to
regenerate favicon (svg/ico/png), apple-touch / android-chrome icons,
`virtualme-mark.png` (+ SVG embed for the sprite), home `hero.jpg` (4:3 JPEG),
the README data-URL icon, and `.github/social-preview.png` (GitHub has no
public social-preview API; the script best-efforts an upload and always leaves
the file for manual Settings upload). Console README screenshots live under
`docs/src/screenshots/`; with a healthy container on `:8080`, run
`bash scripts/refresh-doc-screenshots.sh` then
`bash scripts/update-doc-images.sh` (also part of `/master-update`). Wordmark remains `brand/wordmark.svg`
(outlined Archivo Black small-caps `VIRTUAL` + larger Caveat `me` in fixed
brand red, regenerated by `scripts/gen-wordmark.mjs` from pinned TTFs with the
exact-pinned `opentype.js` devDependency). `scripts/build-icons.mjs` preserves
each brand SVG viewBox when creating the sprite; `build-web.sh` copies
favicon/apple-touch/brand PNGs and `hero.jpg` into `dist/`. SPA-visible copy must not contain em dashes or any of
these phrases: `so you don't have to`, `supercharge`, `seamless`, `effortless`,
`unleash`, `empower`, `delve`, `elevate`, `game-changing`, `AI-powered`.
The controller build version defaults to `dev`; Docker release builds pass
`VERSION` into `-ldflags "-X main.version=..."`, and the state snapshot is the
only source for the console footer version.
Charts use the reusable `makeChart` path in `chart.js`: supply sample values,
series names, units, and a maximum function while retaining the shared
lookback/tick/hover behavior. Every chart series and legend swatch must use the
theme's `--p1` through `--p8` ramp; never introduce chart-specific colors.
`chart-data.js` downsamples every drawn series to at most 36 bars (gauges
average, counter fields listed in `SUM_MODES` sum). LLM token/timing and
agent-action counters accumulate in `internal/metrics.Counters`, drain into
each two-second snapshot, and sum (never average) during tier roll-up.

The controller maps `tts-req`/`tts-stop` websocket requests to
per-connection `tts-*` streams, serves `POST /v1/audio/speech`, broadcasts
agent `speak` audio with `origin:"chat"`, and maps `speech-log-req` to the
bounded `virtualme:speech:log` history. The voice whitelist is
`internal/tts.Voices` (Lessac only; unknown names normalize to it); sentence
WAVs are cached under `$VM_DATA_DIR/tts-cache/`.
It maps `mail-send`/`mail-status-req`/`mail-clear` requests to
`mail-result`/`mail-status` frames. `internal/mail` defensively parses dma
`Q*` envelopes and RFC 5322
`M*` messages; captured dma 0.13 plain and multipart/quoted-printable pairs in
`internal/mail/testdata/` lock the real spool format. A bounded Valkey outbox
(`virtualme:mail:outbox`) tracks each submission's lifecycle; `mail-clear`
removes spool files and marks entries cleared. Persistent dma
configuration, queue files, bounded `flush.log`, `last-flush`, and the DKIM key
live under `$VM_DATA_DIR/mail/`; `svc-mailq` tees delivery errors, bounds the
log to 500 lines, writes the flush marker, and retries deferred messages.
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
each mail submission or jiggler burst.
It maps `jiggler-set` to the Valkey-backed ambient-motion setting (default on;
only an explicit `"0"` disables) exposed in each state snapshot. Jiggler
bursts yield only to the shared `internal/actuation` lock.
It maps `scheduler-set` to the Valkey-backed scheduler pause flag, broadcast
as `scheduler-state` and included in each snapshot's `scheduler.paused`;
pausing skips scheduled-job promotion only. In the Quick Options cockpit UI
the SCHED lamp shows the inverse (lit while running), so the client sends
`enabled: !ariaChecked` on press.
It maps `tools-list-req`/`tool-invoke` to sender-only `tools-list`/`tool-result`
frames. `localTools.Manifest()` serializes agent `Definitions()` plus
manual-only tools such as `dump_dom`; manual calls use `manual-tool` queue
envelopes and enter the activity ledger. Manual result text is capped at
64 KiB.

| Tools WS type | Direction | Purpose |
|---|---|---|
| `tools-list-req` | client → server | Request the authoritative manifest |
| `tools-list` | server → client | Full ordered definitions; also pushed on connect |
| `tool-invoke` | client → server | Enqueue one named tool with JSON arguments |
| `tool-result` | server → initiating client | Bounded text/image/error result and duration |

Agent CDP observation tools:

| Tool | Purpose |
|---|---|
| `read_page` | Structured YAML page digest capped at 24576 bytes for the default 32768-token context (scales from 4 to 64 KiB); layout tables collapse, numbered feeds gain explicit `title_link`/score/comments fields, and data tables preserve structured links |
| `dom_query` | Precise CSS extraction of bounded text and requested attributes |
| `dom_validate` | Full-batch structure/content assertions |
| `page_eval` | Bounded expression extraction with tripwires and Chromium side-effect rejection |
| `layout_debug` | Ref/selector geometry, visibility, occlusion, and scroll state |
| `dump_dom` | Manual-only DOM JSON fixture capture under `$VM_DATA_DIR/agent/dom-dumps/`; absent from agent definitions |

The CDP transport allowlists only `Runtime.evaluate` and
`DOMSnapshot.captureSnapshot`. Never weaken that boundary or use CDP for
browser input/navigation.

The reusable console switch is `.switch` with a child `.knob`; use this markup
for boolean controls and render `aria-checked` only from server state. Quick
Options uses `.qo-btn` cockpit buttons (a `.qo-lamp` lit via `aria-checked`,
a `.qo-label` that toggles the `.qo-tip` tooltip on tap) instead.
GPU detection uses three-second, failure-silent vendor probes and an injectable
sysfs root (default `/sys`) so AMD/Intel fixtures remain hermetic. The result is
cached at startup; absence is normal and never affects health.

Notification mutations must complete their atomic Valkey script before any
websocket broadcast. Add a generic type in the ordered Go `Registry()` and pin
its Lucide sprite; add a sender only by using the validated lowercase producer
seam. A custom renderer requires one allowed-renderer registry entry and one
DOM-only renderer map entry in `notifications.js` (never markup strings or
browser-local state). Stateful additions must also update spec 007's canonical
persistence map and known-root checks.

## How to add things

- **CLI subcommand**: new `src/commands/<name>.js` exporting `run(argv)`,
  register in `src/main.js` and the help text, add a test, update skills/README.
- **Controller endpoint**: register it in `newMux` in
  `controller/cmd/controller/main.go`, keep behavior in an `internal` package,
  and cover routing plus package behavior with hermetic Go tests.
- **Configuration setting**: add it first to
  `controller/internal/config/schema.json` with complete defaults/docs/UI/env/
  restart metadata, mirror only its type in `types.go`, migrate every consumer
  to injected typed configuration, regenerate the docs artifact, and add
  precedence/validation/UI tests. Never add a parallel default constant.
- **Agent tool**: add its OpenAI schema and executor in
  `controller/internal/agent/tools.go`, inject command execution through
  `Runner`, and add hermetic tests. Browser action tools must use `xdotool`
  OS input; CDP is observation-only and must never call `Input.*`,
  `Page.navigate`, or another state-changing method. Agent definitions appear
  on `/tools` through `Manifest()`; manual-only entries are appended there and
  handled in `ExecuteManual`, never exposed to the model. Do not duplicate
  definitions in the SPA. Manual screenshots skip the agent vision grid.
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
