# AGENTS.md — virtualme

Virtual Me is a background AI agent that prioritizes privacy, reliability, and cost-effectiveness. It runs locally and combines a virtual browser, local LLM, management UI, and built-in agent execution loop for private web automation.

## 1. Constitution (non-negotiable project rules)

These rules bind this spec, specs 002/003, and all future work. Copy this section verbatim into `AGENTS.md` (see section 10).

1. **Zero runtime dependencies.** The npm package `virtualme` must have an empty `dependencies` in `package.json`, forever. Only Node.js built-ins (`node:*`) may be imported by runtime code. devDependencies are allowed for tooling only (lint, typecheck) and must be exact-pinned.
2. **Pure modern ESM.** `"type": "module"`, no transpilers, no bundlers, no build step for CLI runtime code. Target Node >= 22 (current LTS lines: 22, 24).
3. **Distribution:** source lives only on GitHub (`github.com/mayanklahiri/virtualme`, public). Binaries are distributed as a Docker image on Docker Hub (`mayanklahiri/virtualme`) and a CLI on npm (`virtualme`). GitHub Actions builds everything; no artifacts are committed to git.
4. **Spec-driven workflow.** All non-trivial work is described first in a numbered spec under `specs/` (`NNN-slug.md`). Later specs must comply with this constitution. Amendments to executed specs are appended to the spec file under an `## Amendments` heading, never silently rewritten.
5. **Deterministic quality gates.** One canonical gate script (`scripts/check.sh`) is run identically by the pre-commit hook and by CI. Gates use no network and no wall-clock-dependent logic: same tree in, same verdict out.
6. **Docker image layering.** The image is built from numbered, append-only install scripts in `docker/layers/` (`001-*.sh`, `002-*.sh`, ...), slowest-moving at the bottom. New capability = new higher-numbered layer. Editing an existing layer requires a spec amendment.
7. **Pinned artifacts.** Every downloaded artifact (model, runtime, tarball, font) is pinned by exact URL + sha256 in the script that fetches it.
8. **Trust model (prototype).** Virtual Me runs on a trusted computer on a private network. There is no authentication or TLS in v1. All internal services bind to `127.0.0.1` inside the container; only port 8080 is exposed. Do not add auth/TLS speculatively; that is a future spec.
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
| `controller/` | Go control plane (specs 002–003) |

## Commands

| Command | Purpose |
|---|---|
| `npm install` | Install exact-pinned development tools |
| `git config core.hooksPath .githooks` | Activate repository hooks |
| `npm run check` | Run the canonical deterministic quality gate |
| `npm test` | Run Node unit tests |
| `./cli.sh <cmd>` | Run the CLI from a checkout |
| `bash test/smoke.sh` | Run the container smoke test (spec 002) |
| `bash test/e2e.sh` | Run full end-to-end tests (spec 003) |
| `bash controller/tools/fetch-assets.sh` | Fetch pinned web assets (spec 003) |

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
