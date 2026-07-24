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
| `docker/` | Container image and supervised services (spec 002) |
| `controller/` | Go control plane, reliable job queue/scheduler, activity ledger, recurring projects, browser-agent loop, local TTS, outbound mail, and multi-page console (specs 002–015) |

## Commands

| Command | Purpose |
|---|---|
| `npm install` | Install exact-pinned development tools |
| `git config core.hooksPath .githooks` | Activate repository hooks |
| `npm run check` | Run the canonical deterministic quality gate |
| `npm run build:web` | Minify the SPA into `controller/web/dist/` (also done by `check`) |
| `npm test` | Run Node unit tests |
| `./cli.sh <cmd>` | Run the CLI from a checkout |
| `bash test/smoke.sh` | Run the container smoke test (spec 002) |
| `bash test/e2e.sh` | Run full end-to-end tests (spec 003) |
| `E2E_AGENT=1 bash test/e2e.sh` | Include the slow real vision/browser-agent probe |
| `./cli.sh soak [--no-build]` | Rebuild, restart on a fresh data dir, and run live soak flows (spec 012) |
| `bash controller/tools/fetch-assets.sh` | Fetch pinned fonts, icons, and hero image (specs 003, 005, 011) |

The controller's browser agent combines vision screenshots, dense rendered
DOM and read-only CDP observations, OS-level `xdotool` mouse/keyboard
actuation, and bounded bash execution. DOM observations carry the page URL
and title, omit layout-only noise, and always fit the model's context;
`navigate` waits for the page to settle. CDP never performs input or
navigation; agent screenshots and step logs (including observation text)
persist under `$VM_DATA_DIR/agent/`. Chromium uses documented deterministic
automation flags and one undecorated full-screen virtual-desktop surface.
The loopback-only `ttsd` service wraps pinned sherpa-onnx and Piper Lessac
artifacts; the controller streams its audio to the Speech tab, OpenAI-compatible
speech clients, and the agent's `speak` tool without persistent TTS state.
The controller composes stdlib MIME/CID mail, signs it with a persistent DKIM
key, and submits it to the supervised dma queue for direct-MX or smarthost
delivery; mail configuration and spool state live under `$VM_DATA_DIR/mail/`.
All LLM work runs sequentially through a Valkey-backed interactive/scheduled
job queue with visibility recovery, retries, dead-lettering, time-bucket
scheduling, and initiator-disconnect cancellation.
Recurring projects persist natural-language tasks and selectors in Valkey,
run through that queue without modifying chat history, and own scratch
directories under `$VM_DATA_DIR/projects/`.
The Jobs console combines queue envelopes with a Valkey-backed activity ledger
fed by LLM, agent-tool, speech, and mail lifecycle events.

## Skills

| Skill | Path | Purpose |
|---|---|---|
| `operate` | `.cursor/skills/operate/SKILL.md` | Run and troubleshoot Virtual Me |
| `develop` | `.cursor/skills/develop/SKILL.md` | Contribute within repository rules |
| `master-update` | `.cursor/skills/master-update/SKILL.md` | Reconcile docs and skills with the tree |

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
| [017](specs/017-jiggler.md) | Jiggler: humanlike OS-level mouse motion with a Status switch (draft) |
| [018](specs/018-gpu-observability.md) | Multi-vendor GPU detection, status widget, and usage series (draft) |
| [019](specs/019-chart-overhaul.md) | Chart ticks, titles, lookback control, and uniform series color (draft) |
| [020](specs/020-speech-audio.md) | Speech seeds/history, TTS disk cache, second voice, and audio hygiene (draft) |
| [021](specs/021-agent-cdp-tools-console.md) | CDP observation tools and the Tools console page (draft) |
| [022](specs/022-system-prompt.md) | On-disk embedded system prompts, SLM-optimized rewrite (draft) |
| [023](specs/023-mail-transparency.md) | Mail queue transparency: contents, errors, and retry timing (draft) |
| [024](specs/024-brand-chrome-polish.md) | Brand wordmark, wristwatch live indicator, and console polish (draft) |
| [025](specs/025-release-presentation.md) | Marvin release notes, registry metadata, and the /do-release skill (draft) |
