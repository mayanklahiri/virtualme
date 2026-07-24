# Virtual Me

<img src="controller/web/static/brand/virtualme-mark.svg" alt="Virtual Me icon" width="44" height="44"> <img src="controller/web/static/brand/wordmark.svg" alt="Virtual Me wordmark" height="40">

[![CI](https://github.com/mayanklahiri/virtualme/actions/workflows/ci.yml/badge.svg)](https://github.com/mayanklahiri/virtualme/actions/workflows/ci.yml)
[![Release](https://github.com/mayanklahiri/virtualme/actions/workflows/release.yml/badge.svg)](https://github.com/mayanklahiri/virtualme/actions/workflows/release.yml)
[![npm](https://img.shields.io/npm/v/virtualme)](https://www.npmjs.com/package/virtualme)
[![Docker](https://img.shields.io/docker/v/mayanklahiri/virtualme?label=docker)](https://hub.docker.com/r/mayanklahiri/virtualme)

**Private Personal Background Agent**

## Overview

Virtual Me is a background AI agent that prioritizes privacy, reliability, and
cost-effectiveness. It runs on your computer, a Raspberry Pi, or a remote
server, and contains a virtual browser (Chromium), local LLM, management UI,
and built-in agent execution loop. Model inference stays local; browser
requests and mail leave the machine only when you use those features.

Virtual Me runs a **fully local** model
([llama.cpp](https://github.com/ggml-org/llama.cpp) +
[Gemma 4 E2B](https://huggingface.co/unsloth/gemma-4-E2B-it-GGUF)) by default:
prompts and model requests are not sent to an external provider, and there are
no AI API bills. Commercial-provider backends are a possible future direction,
not part of v1.

## Quick start

```console
npx virtualme doctor   # check node >= 22 + docker
npx virtualme start    # pull + run the container
# open http://localhost:8080
```

The first start loads a ~3 GB model; allow a few minutes for `/healthz` to become green.

> Virtual Me v1 has no authentication or TLS. Run it only on a trusted computer on a private network.

## User's Guide

### Where everything lives

| Artifact | Location |
|---|---|
| npm package | [`virtualme`](https://www.npmjs.com/package/virtualme) |
| Docker image | [`mayanklahiri/virtualme`](https://hub.docker.com/r/mayanklahiri/virtualme) |
| Source | [GitHub](https://github.com/mayanklahiri/virtualme) |
| Published releases | [GitHub Releases](https://github.com/mayanklahiri/virtualme/releases) |
| CI | [Workflow runs](https://github.com/mayanklahiri/virtualme/actions/workflows/ci.yml) · [source](.github/workflows/ci.yml) |
| Release automation | [Workflow runs](https://github.com/mayanklahiri/virtualme/actions/workflows/release.yml) · [source](.github/workflows/release.yml) |
| Design contracts | [`specs/`](specs/) |

### CLI commands

| Command | Effect |
|---|---|
| `virtualme help` | Show usage and every command |
| `virtualme version` | Print the package version |
| `virtualme doctor` | Check Node, Docker, daemon access, hooks, CPU, and RAM |
| `virtualme start [--data <dir>] [--no-browser-sandbox] [--gpus <spec>]` | Run unprivileged with port 8080 and the data dir mounted rw; optionally force Chromium's sandbox fallback or pass NVIDIA GPU access with observability |
| `virtualme stop` | Stop and remove the container; the data directory survives |
| `virtualme status` | Show container state and service health |
| `virtualme logs [-f\|--follow]` | Show or follow container logs |
| `virtualme build` | Build `:dev` and the configured start tag from a checkout |
| `virtualme keygen` | Generate a 256-bit base64url token |
| `virtualme update` | Pull the configured image tag |
| `virtualme soak [--no-build]` | Rebuild, restart on a fresh data dir, and run live soak flows against the running controller (source checkout only) |

Set `VIRTUALME_IMAGE` or `VIRTUALME_TAG` to override the default image reference,
`VIRTUALME_DATA` to override the default data directory, and `TZ` to override
the detected host timezone passed into the container. `start` also forwards
the `VM_MAIL_*` variables documented below plus `VM_TTS_CACHE_DIR` and
`VM_TTS_CACHE_MAX_MB`.

### Data directory

The host directory `~/.virtualme` (override with `--data <dir>` or
`VIRTUALME_DATA`) is created on first `start` and mounted read-write at the
container's `~/.virtualme`. It contains `valkey/` (chat and speech history,
stats, the reliable job queue, activity ledger, and project records),
`chromium/` (browser profile), `xdg/{config,cache,data}/`, `metrics/` (tiered
history), `agent/` (agent artifacts), `projects/` (per-project scratch space),
`tts-cache/` (recomputable synthesized audio), and `mail/` (dma config/spool,
bounded flush log, flush marker, and the DKIM private key). Chromium settings,
projects, and queued mail survive container and image replacement. The
container runs as the invoking host uid/gid, so
every data file is host-owned. Everything else is intentionally ephemeral or
baked into the image; the canonical persistence map is
[`specs/007-persistence-locality.md` §1](specs/007-persistence-locality.md#1-canonical-persistence-map),
as superseded for mail by [`spec 010 §7`](specs/010-outbound-mail.md#7-persistence--gate-updates-supersedes-spec-007-lists).

### Ports

| Address | Purpose |
|---|---|
| `8080` (exposed) | Controller SPA, health API, websocket state, and desktop proxy |
| `5900` (internal) | x11vnc |
| `6080` (internal) | noVNC/websockify |
| `6379` (internal) | Valkey |
| `8081` (internal) | llama-server |
| `8082` (internal, loopback only) | Local sherpa-onnx/Piper TTS daemon |
| `9222` (internal, loopback only) | Chromium DevTools observation endpoint |
| Xvfb display `:99` | Virtual desktop |

### Controller endpoints

| Route | Purpose |
|---|---|
| `/` | Console home: host and reachable/container addresses, live health/capacity, build version, model, and links |
| `/projects` | Recurring natural-language tasks, schedules, manual runs, and recent results |
| `/projects/<id>` | Project task, schedule, status, run history, and scratch-directory details |
| `/jobs` | Queue timeline, fine-grained machine activity, and type-specific details |
| `/tools` | Authoritative agent-tool list, schema-generated forms, and queue-backed manual invocation |
| `/status` | Service health, system/GPU meters, active time selectors, opt-in jiggler, and persistent per-core/process/GPU metrics |
| `/chat` | Markdown chat, generation controls, LLM progress, and conversation totals |
| `/speech` | Two-voice streaming local speech with seeds, persistent history/replay, and disk cache |
| `/mail` | Outbound-mail composer, message-level queue contents/errors/next-flush timing, activity, and DKIM DNS record |
| `/desktop-view` | Embedded private noVNC desktop |
| `/healthz` | Aggregate JSON health for all eight services |
| `/ws` | Websocket: live state, metrics, queued jobs, activity, tool manifests/invocation, chat, agent, TTS, and mail frames |
| `POST /v1/audio/speech` | OpenAI-compatible local speech API (`wav` or raw `pcm`) |
| `/desktop/` | Redirect to the noVNC client; child paths reverse-proxy noVNC and websockify |

History-API routes without file extensions fall back to the embedded SPA; missing asset paths still return 404. Status charts offer synchronized `15m` through `30d` lookbacks, boundary-aligned locale-aware time ticks, responsive titles and controls, and theme-consistent series colors; the page also shows the server-local scheduler clock and active selector tokens. The branded console has eight themes, each with light and dark variants plus automatic system-scheme selection; the collapsed theme button is in the sidebar footer. Its home page shows hostname, the browser-reachable address, up to two container-interface addresses, uptime, CPU/load, memory, disk capacity, build version, and a detected GPU beside a theme-tinted Earthrise image.

The sidebar connection watch shows the controller hostname and port. Its outer
arc maps server uptime onto the current 24-hour period, the hand advances once
per minute while linked, and the center pip is green when live, red while
reconnecting, and muted while connecting. The text below reports server uptime
and the current browser connection duration. Reduced-motion mode freezes the
hand and disables pulsing without removing the color-coded state.

The Status-page Jiggler switch is off by default. When enabled, the controller
moves the virtual desktop's OS cursor in occasional short humanlike bursts,
yields to queued jobs and agent actuation, and records each burst in Jobs
activity. The Valkey-backed setting survives reloads and container restarts.

GPU detection is best-effort and multi-vendor. The Status card reports the
first visible NVIDIA, AMD, or Intel GPU and available model/VRAM/driver
parameters. NVIDIA and supported AMD sysfs devices also expose utilization and
memory history; presence-only devices do not show an empty chart. GPU absence
is normal and never affects health. Use `--gpus all` for NVIDIA. AMD/Intel
`/dev/dri` passthrough is host-specific and outside the CLI's `--gpus` option.

Closing Chromium in `/desktop-view` automatically brings back one blank tab. Chromium uses its namespace sandbox when the host permits unprivileged user namespaces; otherwise it falls back to `--no-sandbox` with the warning infobar suppressed. Use `--no-browser-sandbox` to force that fallback when diagnosing host compatibility.

### Browser agent

Messages in `/chat` can ask Virtual Me to operate Chromium. The local model can
observe a gridded screenshot, compact rendered DOM, page URL/title/text, and
system information. Precise CSS extraction, multi-assertion validation,
side-effect-guarded page evaluation, and layout/occlusion debugging provide
additional read-only CDP observations; it acts through `xdotool` OS
mouse/keyboard input or a bounded bash tool. CDP is method-allowlisted and
never performs input or navigation.
The console shows each tool step and its screenshot; Stop cancels the active
model call or tool process.

Use `/tools` to inspect the exact definitions available to the local model and
invoke any tool manually. Forms are generated from the server schemas, calls
join the same sequential queue as chat/project work, and results are retained
in the Jobs activity ledger. This can invoke `bash` and browser actuation;
under the v1 trust model it has no additional authentication, so expose the
console only on a trusted private network.

All LLM work runs through a Valkey-backed sequential queue with interactive
priority, retries, visibility recovery, and a dead-letter list. Chat messages
wait instead of returning a busy error. Closing the initiating browser tab
cancels its running generation immediately and drops its queued jobs. The CLI
passes the host `TZ` into the container so time-bucket scheduling uses the
host's local wall clock.

### Projects

Use `/projects` to create recurring agent tasks in plain language. Each project
has a coarse day/time selector, an Enabled switch, a manual Run now action, and
five recent run summaries. Scheduled runs persist through Valkey and execute
sequentially with chat work. A manual run is tied to its initiating browser tab
and is cancelled if that tab closes; scheduled runs are not.

Project scratch files live under `~/.virtualme/projects/<id>/`. Deleting a
project removes its record and run summaries but deliberately preserves that
directory and its operator data.

### Jobs

Use `/jobs` to see what the machine is actually doing. The queue timeline reads
top-to-bottom from upcoming jobs through the single executing job to recently
finished jobs. The newest-first activity ledger records each LLM generation,
agent tool call, speech synthesis, and mail submission; selecting a row opens
its payload, result, timing, and type-specific details. Jiggler bursts also
appear as `jiggle` tool events. The bounded ledger
persists in Valkey across container restarts.

### Local speech

The `/speech` tab streams sentence-level audio from the fully local
[sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx) engine with baked Piper
Lessac (en-US) and Alba (en-GB) voices. Seed texts fill the editor, and the global
newest-first history persists in Valkey with cached replay. The browser starts
playing after the first sentence while later sentences synthesize. Agent chat
can use the `speak` tool when asked for an audible response; its audio bubble
supports replay.

OpenAI-compatible clients can call `POST /v1/audio/speech`; `wav` is the
default response format and `pcm` returns raw 16-bit mono PCM. Set `voice` to
`en_US-lessac-medium` or `en_GB-alba-medium`; unknown values fall back to
Lessac. Exact sentence/voice/speed renders are cached under
`~/.virtualme/tts-cache/`; `VM_TTS_CACHE_MAX_MB` sets the LRU cap (default
256 MiB), and deleting the cache is safe.

### Outbound mail

The `/mail` tab submits standards-compliant multipart mail through the
unprivileged [dma](https://github.com/corecode/dma) queue. Messages can include
a generated inline CID image. Delivery is direct to recipient MX hosts by
default, or through a configured STARTTLS smarthost. Optional controller-side
DKIM signing exposes the DNS TXT name and value to publish in the status panel.
Expandable queue rows show the envelope recipient, subject, age, plain-text
preview, attachment types/sizes, newest recorded delivery error, and a live
countdown to the next queue flush. The countdown is not a delivery guarantee:
dma may apply its own retry backoff. The session activity timeline is held only
in controller memory.

| Environment | Purpose |
|---|---|
| `VM_MAIL_MAILNAME` | HELO/default From domain; defaults to the container hostname |
| `VM_MAIL_FROM` | Envelope/header From; defaults to `virtualme@<mailname>` |
| `VM_MAIL_SMARTHOST` | Relay hostname; unset selects direct MX delivery |
| `VM_MAIL_SMARTHOST_PORT` | Relay port; defaults to `587` |
| `VM_MAIL_SMARTHOST_USER` | Optional relay username |
| `VM_MAIL_SMARTHOST_PASS` | Optional relay password |
| `VM_MAIL_DKIM_DOMAIN` | Enable DKIM signing for this domain |
| `VM_MAIL_DKIM_SELECTOR` | DKIM selector; defaults to `virtualme` |
| `VM_MAIL_FLUSH_SEC` | Queue flush cadence and countdown interval; defaults to `60` |

Set these variables before `virtualme start`; the CLI forwards them to the
container. Direct delivery needs SPF and/or DKIM alignment plus acceptable IP
and PTR reputation. Residential/dynamic IP mail is usually rejected, including
by Gmail, so use a reputable smarthost for reliable delivery.

Task screenshots and JSONL step logs are retained under
`~/.virtualme/agent/<taskId>/` for the most recent 20 tasks. CPU-only vision can
take tens of seconds per step. `--gpus all` forwards NVIDIA GPU access, enables
the GPU status/history display, and sets `VM_LLAMA_GPU=1`, but v1's pinned
llama.cpp runtime is still the CPU build.

### AI skills

| Skill | Path | Use |
|---|---|---|
| `operate` | [`.cursor/skills/operate/SKILL.md`](.cursor/skills/operate/SKILL.md) | Install, run, and troubleshoot |
| `develop` | [`.cursor/skills/develop/SKILL.md`](.cursor/skills/develop/SKILL.md) | Follow contribution rules and patterns |
| `do-release` | [`.cursor/skills/do-release/SKILL.md`](.cursor/skills/do-release/SKILL.md) | Execute the full npm and Docker release |
| `master-update` | [`.cursor/skills/master-update/SKILL.md`](.cursor/skills/master-update/SKILL.md) | Audit documentation against the tree |

Claude Code discovers these through the `.claude/skills` symlink; Codex reads [`AGENTS.md`](AGENTS.md).

After changing anything structural, run the `/master-update` skill — it re-syncs this README, AGENTS.md, and all skills against the repo.

### Specifications

| Spec | Purpose |
|---|---|
| [001](specs/001-constitution.md) | Constitution, CLI, gates, CI/CD, and docs |
| [002](specs/002-container.md) | Docker image and controller stub |
| [003](specs/003-controller.md) | Controller, UI, assets, and end-to-end tests |
| [004](specs/004-release-hardening.md) | Immutable release versions, registry pre-checks, and GitHub Releases |
| [005](specs/005-console-ui.md) | Multi-page console, tiered metrics, themes, markdown chat, and LLM status |
| [006](specs/006-desktop-reliability.md) | Reliable Chromium supervision, sandbox fallback, and profile persistence |
| [007](specs/007-persistence-locality.md) | Persistence grounding and deterministic LLM-locality gate |
| [008](specs/008-browser-agent.md) | OS-level browser-control agent |
| [009](specs/009-local-tts.md) | Local sherpa-onnx/Piper speech synthesis |
| [010](specs/010-outbound-mail.md) | Outbound dma queue, MIME/DKIM, and Mail console |
| [011](specs/011-ui-refresh.md) | Console brand, collapsed theme picker, eight themes, and live-capacity home page |
| [012](specs/012-agent-observation-soak.md) | Dense DOM observations, settled navigation, desktop coverage, Playwright-layer removal, and the live soak suite |
| [013](specs/013-job-queue-scheduler.md) | Valkey job queue, time-bucket scheduler, and initiator-bound cancellation |
| [014](specs/014-projects.md) | Projects: periodic natural-language tasks with schedules and scratch dirs |
| [015](specs/015-jobs-page.md) | Jobs page: activity ledger and queue peek with details pane |
| [016](specs/016-chromium-determinism.md) | Chromium determinism flags and single full-screen window |
| [017](specs/017-jiggler.md) | Jiggler: humanlike OS-level mouse motion with a Status switch |
| [018](specs/018-gpu-observability.md) | Multi-vendor GPU detection, status widget, and usage series |
| [019](specs/019-chart-overhaul.md) | Chart ticks, titles, lookback control, and uniform series color |
| [020](specs/020-speech-audio.md) | Speech seeds/history, TTS disk cache, second voice, and audio hygiene |
| [021](specs/021-agent-cdp-tools-console.md) | CDP observation tools and the Tools console page |
| [022](specs/022-system-prompt.md) | On-disk embedded system prompts and SLM-optimized rewrite |
| [023](specs/023-mail-transparency.md) | Mail queue transparency: contents, errors, and retry timing |
| [024](specs/024-brand-chrome-polish.md) | Brand wordmark, wristwatch live indicator, and console polish |
| [025](specs/025-release-presentation.md) | Marvin release notes, registry metadata, and the /do-release skill |

### CI/CD

| Workflow | Trigger | Purpose and secrets |
|---|---|---|
| [CI](https://github.com/mayanklahiri/virtualme/actions/workflows/ci.yml) ([source](.github/workflows/ci.yml)) | Push to `main`; pull request | Node 22/24 gates, container smoke test, and the CLI-driven E2E test (including the restart cycle and chat probe); no secrets |
| [Release](https://github.com/mayanklahiri/virtualme/actions/workflows/release.yml) ([source](.github/workflows/release.yml)) | Tag `v*` | Registry immutability and committed-notes pre-checks; native amd64/arm64 Docker publishing; Docker Hub overview refresh; npm publishing; curated GitHub Release notes plus generated commits; `DOCKERHUB_USERNAME`, `DOCKERHUB_TOKEN`, `NPM_TOKEN` |

### Development setup

```console
git clone https://github.com/mayanklahiri/virtualme.git
cd virtualme
npm install
git config core.hooksPath .githooks
bash controller/tools/fetch-assets.sh
npm run check
./cli.sh build
./cli.sh start
```

`npm run check` builds the minified SPA (`controller/web/dist/`, gitignored) before the Go gates; run `npm run build:web` to rebuild it alone.

### Release runbook

Requires the spec 002+ container scaffold, including `docker/Dockerfile`. Run
the `/do-release` skill for the full preflight, version, notes, commit, tag,
push, monitoring, and verification procedure.

Every tag requires a committed, non-empty `release-notes/vX.Y.Z.md`. The
release workflow verifies that the version is unused and the notes exist,
builds amd64 and arm64 images on native runners, merges the multi-arch
manifest, refreshes the Docker Hub overview from `docker/DOCKERHUB.md`,
publishes npm, and creates a GitHub Release with the curated notes followed by
the generated commit list.

`DOCKERHUB_TOKEN` must be a Docker Hub password or PAT with repository write
scope. A read-only token makes the overview PATCH fail with HTTP 403 and fails
the release job.

Version tags are write-once on Docker Hub (`X.Y.Z`, `X.Y.Z-amd64`, and `X.Y.Z-arm64`) and npm (`X.Y.Z`); only Docker's `latest` tag moves. If an attempt fails after publishing any versioned artifact, re-run the failed jobs in that same Actions run or fix the problem, bump the patch version, and create a new tag. Never delete and re-push a release tag: even an orphaned per-architecture Docker tag permanently retires that version number.

### Hardware

Use a Raspberry Pi 5 or Raspberry Pi 4 with 8 GB RAM at minimum. The RAM floor is 8 GB; Gemma 4 E2B Q4_0 is approximately 3 GB on disk and 4 GB resident.

## Architecture

The container has s6-supervised Xvfb, openbox, x11vnc, noVNC, Chromium, Valkey, vision-enabled llama.cpp with Gemma 4 E2B, local sherpa-onnx/Piper TTS, a dma outbound-mail queue, and a Go controller on `:8080`, running unprivileged (host uid/gid) with one rw data mount. The controller concurrently probes service health, detects and samples visible NVIDIA/AMD/Intel GPUs, samples and persists metrics, records machine activity, composes and DKIM-signs mail, schedules and sequentially executes reliable Valkey jobs and recurring projects, streams shared chat and speech, produces opt-in ambient OS-level mouse motion, and runs a bounded browser-agent loop combining screenshots, compact DOM/read-only CDP observations, OS-level `xdotool` actions, bash, and audible responses. It proxies noVNC and embeds the same-origin minified multi-page SPA. See [`specs/`](specs/) for the authoritative architecture and implementation contracts.

The model's system prompts are plain text in
[controller/prompts/](controller/prompts/) (spec 022).
