# Virtual Me

[![CI](https://github.com/mayanklahiri/virtualme/actions/workflows/ci.yml/badge.svg)](https://github.com/mayanklahiri/virtualme/actions/workflows/ci.yml)
[![npm](https://img.shields.io/npm/v/virtualme)](https://www.npmjs.com/package/virtualme)
[![Docker](https://img.shields.io/docker/v/mayanklahiri/virtualme?label=docker)](https://hub.docker.com/r/mayanklahiri/virtualme)

**Private Personal Background Agent**

## Overview

Virtual Me is a background AI agent (like OpenClaw or Hermes) that prioritizes privacy, reliability, and cost-effectiveness. It runs completely locally on your computer, a Raspberry Pi, or a remote server, and contains a full virtual browser (Chromium), local LLM, management UI, and built-in agent execution loop. It can be used to automate web-based tasks and automations without any data leaving your computer.

Virtual Me runs a **fully local** model (llama.cpp + Gemma 4 E2B) by default:
no data leaves your machine and there are no AI bills. Optional
commercial-provider backends are a possible future direction, not part of v1.

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
| Releases | [GitHub Releases](https://github.com/mayanklahiri/virtualme/releases) |
| CI/CD runs | [GitHub Actions](https://github.com/mayanklahiri/virtualme/actions) |
| Design contracts | [`specs/`](specs/) |

### CLI commands

| Command | Effect |
|---|---|
| `virtualme help` | Show usage and every command |
| `virtualme version` | Print the package version |
| `virtualme doctor` | Check Node, Docker, daemon access, hooks, CPU, and RAM |
| `virtualme start [--data <dir>] [--no-browser-sandbox] [--gpus <spec>]` | Run unprivileged with port 8080 and the data dir mounted rw; optionally force Chromium's sandbox fallback or pass a Docker GPU specification |
| `virtualme stop` | Stop and remove the container; the data directory survives |
| `virtualme status` | Show container state and service health |
| `virtualme logs [-f\|--follow]` | Show or follow container logs |
| `virtualme build` | Build `:dev` and the configured start tag from a checkout |
| `virtualme keygen` | Generate a 256-bit base64url token |
| `virtualme update` | Pull the configured image tag |

Set `VIRTUALME_IMAGE` or `VIRTUALME_TAG` to override the default image reference, and `VIRTUALME_DATA` to override the default data directory.

### Data directory

The host directory `~/.virtualme` (override with `--data <dir>` or
`VIRTUALME_DATA`) is created on first `start` and mounted read-write at the
container's `~/.virtualme`. It contains `valkey/` (chat history and stats),
`chromium/` (browser profile), `xdg/{config,cache,data}/`, `metrics/` (tiered
history), and `agent/` (agent artifacts). Chromium settings survive container
and image replacement. The container runs as the invoking host uid/gid, so
every data file is host-owned. Everything else is intentionally ephemeral or
baked into the image; the canonical persistence map is
[`specs/007-persistence-locality.md` §1](specs/007-persistence-locality.md#1-canonical-persistence-map).

### Ports

| Address | Purpose |
|---|---|
| `8080` (exposed) | Controller SPA, health API, websocket state, and desktop proxy |
| `5900` (internal) | x11vnc |
| `6080` (internal) | noVNC/websockify |
| `6379` (internal) | Valkey |
| `8081` (internal) | llama-server |
| `9222` (internal, loopback only) | Chromium DevTools observation endpoint |
| Xvfb display `:99` | Virtual desktop |

### Controller endpoints

| Route | Purpose |
|---|---|
| `/` | Console home: health, uptime, model, and links |
| `/status` | Service health, system meters, and persistent per-core/process metrics |
| `/chat` | Markdown chat, generation controls, LLM progress, and conversation totals |
| `/desktop-view` | Embedded private noVNC desktop |
| `/healthz` | Aggregate JSON health for all six services |
| `/ws` | Websocket: live state, requested metrics history, shared chat, and agent UI frames |
| `/desktop/` | Reverse proxy to noVNC and websockify |

History-API routes without file extensions fall back to the embedded SPA; missing asset paths still return 404. Status history offers `15m` through `30d` lookbacks. The console has five themes, each with light and dark variants plus automatic system-scheme selection.

Closing Chromium in `/desktop-view` automatically brings back one blank tab. Chromium uses its namespace sandbox when the host permits unprivileged user namespaces; otherwise it falls back to `--no-sandbox` with the warning infobar suppressed. Use `--no-browser-sandbox` to force that fallback when diagnosing host compatibility.

### Browser agent

Messages in `/chat` can ask Virtual Me to operate Chromium. The local model can
observe a gridded screenshot, compact rendered DOM, page URL/title/text, and
system information; it acts through `xdotool` OS mouse/keyboard input or a
bounded bash tool. CDP is read-only and never performs input or navigation.
The console shows each tool step and its screenshot; Stop cancels the active
model call or tool process.

Task screenshots and JSONL step logs are retained under
`~/.virtualme/agent/<taskId>/` for the most recent 20 tasks. CPU-only vision can
take tens of seconds per step. `--gpus <spec>` forwards Docker GPU access and
sets `VM_LLAMA_GPU=1`, but v1's pinned llama.cpp runtime is still the CPU build;
the flag only establishes passthrough plumbing for a future GPU runtime.

### AI skills

| Skill | Path | Use |
|---|---|---|
| `operate` | [`.cursor/skills/operate/SKILL.md`](.cursor/skills/operate/SKILL.md) | Install, run, and troubleshoot |
| `develop` | [`.cursor/skills/develop/SKILL.md`](.cursor/skills/develop/SKILL.md) | Follow contribution rules and patterns |
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

### CI/CD

| Workflow | Trigger | Purpose and secrets |
|---|---|---|
| [`ci.yml`](.github/workflows/ci.yml) | Push to `main`; pull request | Node 22/24 gates, container smoke test, and the CLI-driven E2E test (including the restart cycle and chat probe); no secrets |
| [`release.yml`](.github/workflows/release.yml) | Tag `v*` | Registry immutability pre-checks; native amd64/arm64 Docker publishing; npm publishing; `github-release` job with generated notes; `DOCKERHUB_USERNAME`, `DOCKERHUB_TOKEN`, `NPM_TOKEN` |

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

Requires the spec 002+ container scaffold, including `docker/Dockerfile`.

1. Bump `version` in `package.json` and commit it.
2. Run `git tag vX.Y.Z`.
3. Run `git push --tags`.
4. The release workflow verifies that the version is unused, builds amd64 and arm64 images on native runners, merges the multi-arch manifest, publishes npm, and creates a GitHub Release with autogenerated notes.

Version tags are write-once on Docker Hub (`X.Y.Z`, `X.Y.Z-amd64`, and `X.Y.Z-arm64`) and npm (`X.Y.Z`); only Docker's `latest` tag moves. If an attempt fails after publishing any versioned artifact, re-run the failed jobs in that same Actions run or fix the problem, bump the patch version, and create a new tag. Never delete and re-push a release tag: even an orphaned per-architecture Docker tag permanently retires that version number.

### Hardware

Use a Raspberry Pi 5 or Raspberry Pi 4 with 8 GB RAM at minimum. The RAM floor is 8 GB; Gemma 4 E2B Q4_0 is approximately 3 GB on disk and 4 GB resident.

## Architecture

The container has s6-supervised Xvfb, openbox, x11vnc, noVNC, Chromium, Playwright, Valkey, vision-enabled llama.cpp with Gemma 4 E2B, and a Go controller on `:8080`, running unprivileged (host uid/gid) with one rw data mount. The controller concurrently probes service health, samples and persists metrics, streams shared chat, and runs a bounded browser-agent loop combining screenshots, compact DOM/read-only CDP observations, OS-level `xdotool` actions, and bash. It proxies noVNC and embeds the same-origin minified multi-page SPA. See [`specs/`](specs/) for the authoritative architecture and implementation contracts.
