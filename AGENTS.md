# AGENTS.md — virtualme

Virtual Me is a background AI agent that prioritizes privacy, reliability, and cost-effectiveness. It runs locally and combines a virtual browser, local LLM, management UI, and built-in agent execution loop for private web automation.

## 1. Constitution (non-negotiable project rules)

These rules bind this spec, specs 002/003, and all future work. Copy this section verbatim into `AGENTS.md` (see section 10).

1. **Zero runtime dependencies.** The npm package `virtualme` must have an empty `dependencies` in `package.json`, forever. Only Node.js built-ins (`node:*`) may be imported by runtime code. devDependencies are allowed for tooling only (lint, typecheck, web asset minification) and must be exact-pinned.
2. **Pure modern ESM.** `"type": "module"`, no transpilers, no bundlers, no build step for CLI runtime code. Target Node >= 22 (current LTS lines: 22, 24). The controller's embedded SPA is the one exception: it is minified (with sourcemaps) by exact-pinned devDependency tooling into a gitignored output directory (spec 003 §8); its hand-written sources remain plain ESM.
3. **Distribution:** source lives only on GitHub (`github.com/mayanklahiri/virtualme`, public). Binaries are distributed as a Docker image on Docker Hub (`mayanklahiri/virtualme`) and a CLI on npm (`virtualme`). GitHub Actions builds everything; no artifacts are committed to git.
4. **Spec-driven workflow.** All non-trivial work is described first in a numbered spec under `specs/` (`NNN-slug.md`). Later specs must comply with this constitution. Amendments to executed specs are appended to the spec file under an `## Amendments` heading, never silently rewritten.
5. **Deterministic quality gates.** One canonical gate script (`scripts/check.sh`) is run identically by the pre-commit hook and by CI. Gates use no network and no wall-clock-dependent logic: same tree in, same verdict out.
6. **Docker image layering.** The image is built from numbered, append-only install scripts in `docker/layers/` (`001-*.sh`, `002-*.sh`, ...), slowest-moving at the bottom. New capability = new higher-numbered layer. Editing an existing layer requires a spec amendment.
7. **Pinned artifacts.** Every downloaded artifact (model, runtime, tarball, font) is pinned by exact URL + sha256 in the script that fetches it.
8. **Trust model (prototype).** Virtual Me runs on a trusted computer on a private network. There is no authentication or TLS in v1. All internal services bind to `127.0.0.1` inside the container; only port 8080 is exposed — anyone who can reach it can view state and use the chat. The container itself runs unprivileged (host-matched uid/gid) with a single rw data mount; the root filesystem mounts read-write (a `--read-only` posture was reverted as too restrictive — s6 creates rc.d files at runtime), but root-owned paths are not writable by the runtime uid (spec 002). Do not add auth/TLS speculatively; that is a future spec.
9. **Docs never drift.** `README.md`, `AGENTS.md`, and the AI skills are kept in sync with the repo by the `/master-update` skill (section 9). Every executed spec ends by running its procedure.

## Layout

| Path | Purpose |
|---|---|
| `bin/`, `src/`, `test/` | Zero-dependency npm CLI and unit tests |
| `scripts/`, `.githooks/` | Canonical quality gate and pre-commit hook |
| `.github/workflows/` | CI and release automation |
| `.cursor/skills/` | Shared AI operating and development procedures |
| `specs/` | Numbered, authoritative implementation specs |
| `docs/` | Isolated static Astro documentation/marketing site, authored content/assets, committed generated inputs, and browser tests |
| `common/themes/` | Canonical validated theme-token source shared by controller and docs |
| `docker/` | Container image and supervised services (spec 002) |
| `controller/` | Go control plane, durable notifications/lifecycle marker, master configuration, GPU observability, reliable job queue/scheduler, activity ledger, recurring projects, browser-agent loop, manual Tools console, ambient jiggler, cached local TTS, outbound mail, read-only data explorer, and multi-page console |
| `controller/internal/config`, `controller/cmd/configctl` | Embedded schema, strict YAML loader, atomic persistence, preflight exports, and deterministic docs reference |
| `controller/prompts/` | Embedded plain-text agent and fallback-chat system prompts |

## Commands

| Command | Purpose |
|---|---|
| `npm install` | Install exact-pinned development tools |
| `npm ci --prefix docs` | Install the isolated exact-pinned documentation toolchain |
| `git config core.hooksPath .githooks` | Activate repository hooks |
| `npm run check` | Run the canonical deterministic quality gate |
| `npm run build:web` | Minify the SPA into `controller/web/dist/` (also done by `check`) |
| `npm test` | Run Node unit tests |
| `./cli.sh <cmd>` | Run the CLI from a checkout |
| `bash test/smoke.sh` | Run the container smoke test (spec 002) |
| `bash test/e2e.sh` | Run full end-to-end tests (spec 003) |
| `E2E_AGENT=1 bash test/e2e.sh` | Include the slow real vision/browser-agent probe |
| `./cli.sh soak [--no-build]` | Rebuild once, run the full e2e suite, then run live soak flows on a fresh data dir (spec 012) |
| `bash controller/tools/fetch-assets.sh` | Fetch pinned fonts and icons (specs 003, 005, 011) |
| `bash scripts/update-logo.sh` | Regenerate brand icons + home hero from repo-root `LOGO.png` |
| `bash scripts/refresh-doc-screenshots.sh` | Capture console routes from a live `:8080` into `docs/src/screenshots/` (480/960/1280 JPEG widths) |
| `bash scripts/update-doc-images.sh` | Rewire README image markers to `docs/src/screenshots/` paths |
| `./cli.sh docs dev [--host <host>] [--port <port>]` | Serve the documentation site from a source checkout |
| `./cli.sh docs build` | Build and verify the static documentation site offline |
| `node scripts/generate-themes.mjs --check` | Verify controller/docs theme outputs match `common/themes/themes.json` |
| `(cd controller && go run ./cmd/configctl docs --check --output ../docs/src/generated/config-reference.json)` | Verify generated configuration reference |

The controller's browser agent combines vision screenshots, dense rendered
DOM and read-only CDP observations, OS-level `xdotool` mouse/keyboard
actuation, and bounded bash execution. Browser interactions use power-law
pauses before and after each action plus humanized typing and scrolling
cadence. DOM observations carry the page URL and title, omit layout-only
noise, and fit the default 32768-token context;
`read_page` emits a structured YAML digest capped at 24576 bytes at the default
context (scaling from 4 to 64 KiB with configured context), collapsing
layout tables while preserving links, grouping numbered feed rows into
explicit article fields (including ready-to-copy `title_link`, score, comments,
and comment URL), and retaining structured links in data tables,
and `navigate` waits for the page to settle. CDP never performs input or
navigation; agent screenshots and step logs (including observation text)
persist under `$VM_DATA_DIR/agent/`. The vision coordinate grid is drawn only
on agent observations; manual Tools-console screenshots are pure captures.
Chromium uses documented deterministic
automation flags and one undecorated full-screen virtual-desktop surface.
Its agent and fallback-chat system prompts are embedded from reviewable plain
text in `controller/prompts/`; wording changes require a spec amendment.
The server-driven `/tools` console lists every agent definition plus
manual-only development tools such as `dump_dom`, generates forms from JSON
schemas, and serializes manual calls through the job queue;
manual results and timings enter the persistent activity ledger. Tool results
render by shape: page-shaped JSON becomes a linked title plus plain text,
KEY=value runs become sorted tables, and `read_page` YAML digests become
collapsible trees. Manual result text is capped at 64 KiB. Before every model
call, a calibrated conservative estimator budgets messages and tool schemas,
supersedes stale observations, compacts older tool results, and adaptively
allocates up to one quarter of configured context for completion. Server-side
context rejection triggers hard compaction and then observation/image
reduction before failure. A token-limit stop gains an explicit
`…[response truncated at token limit]` marker.
The read-only `/data` console tab provides icon/list single-directory browsing,
sortable columns and recursive size bars, `?path=` deep links, a remembered
drag-resizable 66/34 desktop split, and a mobile preview slide-over. GET-only
`/api/data/list`, `/api/data/file`, and five-minute Valkey-cached
`/api/data/du` endpoints enforce strict path containment; typed viewers render
images (lightbox), JSON/YAML trees, JSONL rows, WAV audio, and 256 KiB-capped
text, with raw downloads for everything. The root UI omits `chromium`, `mail`,
`metrics`, `valkey`, and `xdg`, but direct links and the API expose the whole
volume by design under the v1 trust model.
The mode-0600 `$VM_DATA_DIR/virtualme.config.yaml` is the sole runtime master
configuration. Its embedded JSON Schema owns defaults, docs, UI metadata,
legacy environment mappings, restart ownership, and secret policy. The strict
stdlib-only loader seeds canonical commented YAML, applies legacy-env > YAML >
default precedence, resolves whole-scalar environment and secret references,
and rejects invalid present files before services start. `configctl preflight`
exports non-secret supervisor settings and dma files before longruns.
`/config` provides schema-driven read/edit views, optimistic atomic saves,
redacted state, explicit secret refresh, and deliberate service restart.
The notification service retains 500 immutable messages plus global read state
in Valkey through atomic Lua scripts, broadcasts authoritative websocket
snapshots/pages/details, and exposes a bell popover plus `/notifications`.
Lifecycle evidence in mode-0600 `$VM_DATA_DIR/controller-lifecycle.json`
distinguishes clean stops, crashes, and planned configuration restarts.
Configuration saves and the agent's `notify` tool use narrow typed producer
seams; notification creation itself does not duplicate activity-ledger events.
Chat renders GFM pipe tables, buffers the latest task's agent steps
server-side for websocket-reconnect replay, and declares no live regions.
The loopback-only `ttsd` service wraps pinned sherpa-onnx and Piper Lessac
artifacts (the sole voice; unknown voice names fall back to it); the
controller streams its audio to the Speech tab, OpenAI-compatible speech
clients, and the agent's `speak` tool. Bounded speech history persists in
Valkey and exact sentence renders use a disposable on-disk LRU cache.
The controller composes stdlib MIME/CID mail, signs it with a persistent DKIM
key, and submits it to the supervised dma queue for direct-MX or smarthost
delivery. The Mail console defensively reads dma envelope/message pairs to show
recipients, text previews, errors, and next-flush timing; a durable Valkey
outbox tracks each submission (queued, left queue, error, cleared) and a
confirmed Clear queue control removes spooled mail. Mail state lives under
`$VM_DATA_DIR/mail/`.
All LLM work runs sequentially through a Valkey-backed interactive/scheduled
job queue with visibility recovery, retries, dead-lettering, time-bucket
scheduling, and initiator-disconnect cancellation.
Recurring projects persist natural-language tasks and selectors in Valkey,
run through that queue without modifying chat history, and own scratch
directories under `$VM_DATA_DIR/projects/`.
The Jobs console combines queue envelopes with a Valkey-backed activity ledger
fed by LLM, agent-tool, speech, and mail lifecycle events in a responsive
three-pane layout with explicit status icons, per-event durations, and
persisted tool-call/jiggler visibility filters.
The console home separates health from an aligned host/address/capacity grid,
shows browser-reachable and container-network addresses plus the controller
build version, and uses committed wordmark plus raster logo assets regenerated
from repo-root `LOGO.png` via `scripts/update-logo.sh`. Its
connection watch combines server uptime, current websocket duration, and
motion/reduced-motion-aware live state.
The default-on jiggler produces bursty humanlike mouse trajectories through
`xdotool` every 8 to 27 seconds, yields only to the agent's actuation lock,
persists its state in Valkey, and records each completed burst in the
activity ledger. The Status page groups it with the scheduler in a Quick
Options panel of fixed-size cockpit-style lit buttons with tooltips; the
SCHED lamp is lit while promotion runs, and the Valkey-backed pause flag
stops scheduled-job promotion without touching interactive work.
The controller detects the first visible NVIDIA, AMD, or Intel GPU once at
startup without affecting health. It includes static GPU identity in state and
persists utilization/memory metrics when NVIDIA or AMD sysfs sampling is
available; the console hides the GPU chart for presence-only devices. The CLI
auto-passes a detected host NVIDIA stack (`--gpus all` plus `VM_LLAMA_GPU=1`
and `NVIDIA_DRIVER_CAPABILITIES=all`; `--no-gpu` opts out), and `svc-llama`
selects the baked Vulkan llama.cpp runtime with full offload when a GPU device
is injected (logging the enumerated devices so a silent CPU fallback is
visible), else the CPU build. The Vulkan layer ships `libegl1`, which the
NVIDIA Vulkan ICD requires to initialize. GPU VRAM renders in binary GB.
Status charts share a persistent lookback, bounded boundary-aligned locale
ticks, responsive title/control headers, timestamp-true hover selection, and
the theme-defined `--p1` through `--p8` series ramp. Every chart downsamples
client-side to at most 36 bars (gauges average, counters sum); GPU
utilization and memory (in GB) draw side by side, and dedicated charts track
LLM tokens, effective token throughput, and browser actions by category from
per-sample counters drained by the state collector. The Status top banner
carries health, the scheduler clock with active selector tokens, and uptime.
Durations render through one graded component and shared short formatting.
The static documentation site builds beneath `/virtualme/`, is published from
branch-root output on the orphan `docs` branch by `.github/workflows/docs.yml`,
and keeps analytics disabled unless `docs/src/config/site.ts` or
`PUBLIC_GA_MEASUREMENT_ID` supplies a valid public ID. All eight controller and
docs theme variants derive from `common/themes/themes.json`; generated CSS and
registries are committed and drift-checked. All authored documentation-site
content and site-owned assets must remain below `docs/`; Astro sources must not
import outside that tree; shared themes and config docs enter only through
checked generated files under `docs/src/generated/` or `docs/src/styles/`.

## Skills

| Skill | Path | Purpose |
|---|---|---|
| `operate` | `.cursor/skills/operate/SKILL.md` | Run and troubleshoot Virtual Me |
| `develop` | `.cursor/skills/develop/SKILL.md` | Contribute within repository rules |
| `do-release` | `.cursor/skills/do-release/SKILL.md` | Execute and verify a full npm and Docker release |
| `master-update` | `.cursor/skills/master-update/SKILL.md` | Reconcile docs/skills with the tree; refresh `docs/src/screenshots/` from live `:8080` and rewire README markers |

## Specs

| Spec | Purpose |
|---|---|
| [001](specs/001-constitution.md) | Constitution, CLI, gates, CI/CD, and docs |
| [002](specs/002-container.md) | Docker image and controller stub |
| [003](specs/003-controller.md) | Controller, UI, assets, and end-to-end tests |
| [004](specs/004-release-hardening.md) | Immutable release versions, registry pre-checks, GitHub Releases |
| [005](specs/005-console-ui.md) | Multi-page console, tiered metrics, themes, markdown chat, LLM status |
| [006](specs/006-desktop-reliability.md) | Reliable Chromium supervision, sandbox fallback, profile persistence |
| [007](specs/007-persistence-locality.md) | Persistence grounding and deterministic LLM-locality gate |
| [008](specs/008-browser-agent.md) | OS-level browser-control agent (vision + xdotool + DOM + bash) |
| [009](specs/009-local-tts.md) | Local sherpa-onnx/Piper speech synthesis, API, console, and agent tool |
| [010](specs/010-outbound-mail.md) | dma outbound queue, MIME/DKIM controller, and Mail console |
| [011](specs/011-ui-refresh.md) | Console brand, collapsed theme picker, eight themes, and live-capacity home page |
| [012](specs/012-agent-observation-soak.md) | Dense DOM observations, settled navigation, desktop coverage, Playwright-layer removal, and the live soak suite |
| [013](specs/013-job-queue-scheduler.md) | Valkey job queue, time-bucket scheduler, initiator-bound cancellation |
| [014](specs/014-projects.md) | Projects: periodic natural-language tasks with schedules and scratch dirs |
| [015](specs/015-jobs-page.md) | Jobs page: activity ledger and queue peek with details pane |
| [016](specs/016-chromium-determinism.md) | Chromium determinism flags and single full-screen window |
| [017](specs/017-jiggler.md) | Jiggler: humanlike OS-level mouse motion with a Status switch |
| [018](specs/018-gpu-observability.md) | Multi-vendor GPU detection, status widget, and usage series |
| [019](specs/019-chart-overhaul.md) | Chart ticks, titles, lookback control, and uniform series color |
| [020](specs/020-speech-audio.md) | Speech seeds/history, TTS disk cache, and audio hygiene |
| [021](specs/021-agent-cdp-tools-console.md) | CDP observation tools and the Tools console page |
| [022](specs/022-system-prompt.md) | On-disk embedded system prompts and SLM-optimized rewrite |
| [023](specs/023-mail-transparency.md) | Mail queue transparency: contents, errors, and retry timing |
| [024](specs/024-brand-chrome-polish.md) | Brand wordmark, wristwatch live indicator, and console polish |
| [025](specs/025-release-presentation.md) | Marvin release notes, registry metadata, and the /do-release skill |
| [026](specs/026-console-fixes.md) | Console bugfix sweep: chat, speech, charts, jobs, mail, tools, screenshots |
| [027](specs/027-structured-read-page.md) | Structured YAML `read_page` digest, tree UI, and tool-testing soak |
| [028](specs/028-data-explorer.md) | Read-only Data explorer tab and `/api/data/*` volume API |
| [029](specs/029-readpage-goldens.md) | `read_page` DOM goldens, layout-table fidelity, 32K context, and proportionate caps |
| [030](specs/030-docs-site.md) | Static Astro docs/marketing site, shared themes, source-checkout CLI, and Pages publication |
| [031](specs/031-master-config.md) | Master configuration schema, strict loader, preflight/runtime migration, console, restart flow, and docs export |
| [032](specs/032-assistant-notifications.md) | Durable notifications, global read state, lifecycle markers, agent tool, and console UI |
| [033](specs/033-telegram.md) | Accepted authorized Telegram integration follow-up; not yet implemented |
| [034](specs/034-agent-context-budget.md) | Preflight agent context budgeting, scaled observations, adaptive completions, and graduated recovery |
