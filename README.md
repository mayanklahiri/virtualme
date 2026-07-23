# Virtual Me

[![CI](https://github.com/mayanklahiri/virtualme/actions/workflows/ci.yml/badge.svg)](https://github.com/mayanklahiri/virtualme/actions/workflows/ci.yml)
[![npm](https://img.shields.io/npm/v/virtualme)](https://www.npmjs.com/package/virtualme)
[![Docker](https://img.shields.io/docker/v/mayanklahiri/virtualme?label=docker)](https://hub.docker.com/r/mayanklahiri/virtualme)

**Private Personal Background Agent**

## Overview

Virtual Me is a background AI agent (like OpenClaw or Hermes) that prioritizes privacy, reliability, and cost-effectiveness. It runs completely locally on your computer, a Raspberry Pi, or a remote server, and contains a full virtual browser (Chromium), local LLM, management UI, and built-in agent execution loop. It can be used to automate web-based tasks and automations without any data leaving your computer.

It can be run in several modes:

* **Free**: local models running on local hardware, no AI bills
* **Low-Cost**: commercial low-cost LLM models via OpenRouter, some AI bills
* **Balanced**: commercial balanced-cost LLM models via OpenRouter, moderate AI bills
* **Best**: frontier models on their native platform, high AI bills

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
| CI/CD runs | [GitHub Actions](https://github.com/mayanklahiri/virtualme/actions) |
| Design contracts | [`specs/`](specs/) |

### CLI commands

| Command | Effect |
|---|---|
| `virtualme help` | Show usage and every command |
| `virtualme version` | Print the package version |
| `virtualme doctor` | Check Node, Docker, daemon access, hooks, CPU, and RAM |
| `virtualme start` | Start the persistent container on port 8080 |
| `virtualme stop` | Stop and remove the container; preserve its data volume |
| `virtualme status` | Show container state and service health |
| `virtualme logs [-f\|--follow]` | Show or follow container logs |
| `virtualme build` | Build the `:dev` image from a checkout |
| `virtualme keygen` | Generate a 256-bit base64url token |
| `virtualme update` | Pull the configured image tag |

Set `VIRTUALME_IMAGE` or `VIRTUALME_TAG` to override the default image reference.

### Ports

| Address | Purpose |
|---|---|
| `8080` (exposed) | Controller SPA, health API, websocket state, and desktop proxy |
| `5900` (internal) | x11vnc |
| `6080` (internal) | noVNC/websockify |
| `6379` (internal) | Valkey |
| `8081` (internal) | llama-server |
| Xvfb display `:99` | Virtual desktop |

### Controller endpoints

| Route | Purpose |
|---|---|
| `/` | Embedded responsive control-plane SPA |
| `/healthz` | Aggregate JSON health for all six services |
| `/ws` | Server-push websocket state snapshots |
| `/desktop/` | Reverse proxy to noVNC and websockify |

### AI skills

| Skill | Path | Use |
|---|---|---|
| `operate` | [`.cursor/skills/operate/SKILL.md`](.cursor/skills/operate/SKILL.md) | Install, run, and troubleshoot |
| `develop` | [`.cursor/skills/develop/SKILL.md`](.cursor/skills/develop/SKILL.md) | Follow contribution rules and patterns |
| `master-update` | [`.cursor/skills/master-update/SKILL.md`](.cursor/skills/master-update/SKILL.md) | Audit documentation against the tree |

Claude Code discovers these through the `.claude/skills` symlink; Codex reads [`AGENTS.md`](AGENTS.md).

After changing anything structural, run the `/master-update` skill — it re-syncs this README, AGENTS.md, and all skills against the repo.

### CI/CD

| Workflow | Trigger | Purpose and secrets |
|---|---|---|
| [`ci.yml`](.github/workflows/ci.yml) | Push to `main`; pull request | Node 22/24 gates, container smoke test, and full E2E test; no secrets |
| [`release.yml`](.github/workflows/release.yml) | Tag `v*` | Native amd64/arm64 Docker publishing and npm publishing; `DOCKERHUB_USERNAME`, `DOCKERHUB_TOKEN`, `NPM_TOKEN` |

### Development setup

```console
git clone https://github.com/mayanklahiri/virtualme.git
cd virtualme
npm install
git config core.hooksPath .githooks
bash controller/tools/fetch-assets.sh
npm run check
```

### Release runbook

Requires the spec 002+ container scaffold, including `docker/Dockerfile`.

1. Bump `version` in `package.json` and commit it.
2. Run `git tag vX.Y.Z`.
3. Run `git push --tags`.
4. The release workflow builds amd64 and arm64 images on native runners, merges the multi-arch manifest, and publishes npm.

### Hardware

Use a Raspberry Pi 5 or Raspberry Pi 4 with 8 GB RAM at minimum. The RAM floor is 8 GB; Gemma 4 E2B Q4_0 is approximately 3 GB on disk and 4 GB resident.

## Architecture

The container has s6-supervised Xvfb, openbox, x11vnc, noVNC, Chromium, Playwright, Valkey, llama.cpp with Gemma 4 E2B, and a Go controller on `:8080`. The controller concurrently probes service health, broadcasts state over a minimal RFC 6455 implementation, proxies noVNC, and embeds the same-origin SPA. See [`specs/`](specs/) for the authoritative architecture and implementation contracts.
