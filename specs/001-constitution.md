# Spec 001: Constitution and Repository Scaffolding

| | |
|---|---|
| Status | Approved for execution |
| Depends on | Nothing (first spec; repo contains only `README.md`) |
| Produces | Repo constitution, zero-dependency npm CLI, `cli.sh`, quality gates + pre-commit hook, CI/CD workflows, AI skills, README User's Guide |
| Followed by | `specs/002-container.md`, `specs/003-controller.md` |

## 0. Executor instructions (read first)

- Execute sections in order. Each section lists files to create with exact contents or a precise contract.
- **Stop-on-red rule**: after each numbered section, run the verification command(s) given for it. If anything fails, fix it before moving on. Never proceed past a red step.
- Where exact file contents are given, reproduce them byte-for-byte (modulo trailing newline). Where a contract is given, implement the smallest thing that satisfies it.
- Do not add dependencies, files, options, or abstractions beyond what this spec names.
- When this spec says "pin", you must record an exact version/hash in the file at execution time — never a floating tag like `latest` inside committed files.
- Finish with the Acceptance Checklist (section 12). All items must pass.

## 1. Constitution (non-negotiable project rules)

These rules bind this spec, specs 002/003, and all future work. Copy this section verbatim into `AGENTS.md` (see section 10).

1. **Zero runtime dependencies.** The npm package `virtualme` must have an empty `dependencies` in `package.json`, forever. Only Node.js built-ins (`node:*`) may be imported by runtime code. devDependencies are allowed for tooling only (lint, typecheck, web asset minification) and must be exact-pinned.
2. **Pure modern ESM.** `"type": "module"`, no transpilers, no bundlers, no build step for CLI runtime code. Target Node >= 22 (current LTS lines: 22, 24). The controller's embedded SPA is the one exception: it is minified (with sourcemaps) by exact-pinned devDependency tooling into a gitignored output directory (spec 003 §8); its hand-written sources remain plain ESM.
3. **Distribution:** source lives only on GitHub (`github.com/mayanklahiri/virtualme`, public). Binaries are distributed as a Docker image on Docker Hub (`mayanklahiri/virtualme`) and a CLI on npm (`virtualme`). GitHub Actions builds everything; no artifacts are committed to git.
4. **Spec-driven workflow.** All non-trivial work is described first in a numbered spec under `specs/` (`NNN-slug.md`). Later specs must comply with this constitution. Amendments to executed specs are appended to the spec file under an `## Amendments` heading, never silently rewritten.
5. **Deterministic quality gates.** One canonical gate script (`scripts/check.sh`) is run identically by the pre-commit hook and by CI. Gates use no network and no wall-clock-dependent logic: same tree in, same verdict out.
6. **Docker image layering.** The image is built from numbered, append-only install scripts in `docker/layers/` (`001-*.sh`, `002-*.sh`, ...), slowest-moving at the bottom. New capability = new higher-numbered layer. Editing an existing layer requires a spec amendment.
7. **Pinned artifacts.** Every downloaded artifact (model, runtime, tarball, font) is pinned by exact URL + sha256 in the script that fetches it.
8. **Trust model (prototype).** Virtual Me runs on a trusted computer on a private network. There is no authentication or TLS in v1. All internal services bind to `127.0.0.1` inside the container; only port 8080 is exposed — anyone who can reach it can view state and use the chat. The container itself runs unprivileged (host-matched uid/gid) on a read-only root filesystem with a single rw data mount (spec 002). Do not add auth/TLS speculatively; that is a future spec.
9. **Docs never drift.** `README.md`, `AGENTS.md`, and the AI skills are kept in sync with the repo by the `/master-update` skill (section 9). Every executed spec ends by running its procedure.

## 2. Final repository layout

Target tree after specs 001–003 are all executed. Spec 001 creates everything **not** marked `(002)` or `(003)`.

```
virtualme/
├── README.md                      # rewritten in section 11
├── LICENSE                        # MIT
├── AGENTS.md                      # canonical agent guide (Codex reads this natively)
├── CLAUDE.md                      # one-line pointer to AGENTS.md
├── cli.sh                         # bash shortcut to the JS CLI
├── package.json
├── package-lock.json
├── jsconfig.json                  # JSDoc typechecking config
├── eslint.config.js
├── .editorconfig
├── .gitignore
├── .githooks/
│   └── pre-commit                 # runs scripts/check.sh
├── bin/
│   └── virtualme.js               # npm bin entry (shebang + import src/main.js)
├── src/
│   ├── main.js                    # arg dispatch
│   ├── ansi.js                    # ANSI colors, NO_COLOR/TTY aware
│   ├── config.js                  # image/container/port constants
│   ├── docker.js                  # child_process wrappers around the docker CLI
│   └── commands/
│       ├── help.js  version.js  doctor.js  keygen.js
│       ├── start.js stop.js status.js logs.js
│       └── build.js update.js
├── test/
│   ├── ansi.test.js
│   ├── cli.test.js
│   ├── keygen.test.js
│   ├── smoke.sh                   # (002) container smoke test
│   └── e2e.sh                     # (003) full E2E test
├── scripts/
│   ├── check.sh                   # THE canonical quality gate
│   └── build-web.sh               # (003) SPA minify → controller/web/dist
├── specs/
│   ├── 001-constitution.md        # this file
│   ├── 002-container.md
│   └── 003-controller.md
├── .github/workflows/
│   ├── ci.yml
│   └── release.yml
├── .cursor/skills/
│   ├── operate/SKILL.md
│   ├── develop/SKILL.md
│   └── master-update/SKILL.md
├── .claude/
│   └── skills -> ../.cursor/skills   # relative symlink, committed to git
├── docker/                        # (002)
│   ├── Dockerfile
│   ├── layers/001-base.sh ... 008-s6-overlay.sh
│   └── rootfs/                    # s6 service definitions, cont-init scripts
└── controller/                    # (002 stub, 003 full)
    ├── go.mod
    ├── cmd/controller/main.go
    ├── internal/...               # (003)
    ├── web/static/...             # (003) hand-written SPA sources
    ├── web/dist/...               # (003) gitignored minified build output
    └── tools/fetch-assets.sh      # (003)
```

## 3. Repo boilerplate

Initialize git if needed (`git init -b main`). Create:

**`LICENSE`** — standard MIT text, copyright line: `Copyright (c) 2026 Mayank Lahiri`.

**`.gitignore`**

```
node_modules/
*.tgz
controller/web/static/fonts/
controller/web/dist/
controller/controller
/.DS_Store
```

**`.editorconfig`**

```
root = true

[*]
charset = utf-8
end_of_line = lf
insert_final_newline = true
indent_style = space
indent_size = 2

[*.go]
indent_style = tab

[Makefile]
indent_style = tab
```

**`cli.sh`** (mode 755)

```bash
#!/usr/bin/env bash
# Shortcut to the Virtual Me CLI from a source checkout.
set -euo pipefail
exec node "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/bin/virtualme.js" "$@"
```

Verify: `bash -n cli.sh` exits 0.

## 4. npm package

**`package.json`** — exact contents, except: replace the devDependency versions with the exact latest versions at execution time (`npm view <pkg> version`), keeping them **exact** (no `^`/`~`). Do not add a `dependencies` key at all. (`@types/punycode` is required because current `@types/node` references it transitively under `strict` checkJs; `esbuild` minifies the SPA per spec 003 §8.)

```json
{
  "name": "virtualme",
  "version": "0.1.0",
  "description": "Virtual Me: private personal background agent. CLI for running the Docker container.",
  "type": "module",
  "bin": { "virtualme": "bin/virtualme.js" },
  "engines": { "node": ">=22" },
  "files": ["bin", "src", "README.md", "LICENSE"],
  "scripts": {
    "check": "bash scripts/check.sh",
    "test": "node --test test/*.test.js",
    "lint": "eslint .",
    "typecheck": "tsc -p jsconfig.json",
    "build:web": "bash scripts/build-web.sh"
  },
  "repository": { "type": "git", "url": "git+https://github.com/mayanklahiri/virtualme.git" },
  "homepage": "https://github.com/mayanklahiri/virtualme#readme",
  "keywords": ["agent", "docker", "local-llm", "playwright", "automation"],
  "author": "Mayank Lahiri",
  "license": "MIT",
  "devDependencies": {
    "@eslint/js": "<pin>",
    "@types/node": "<pin>",
    "@types/punycode": "<pin>",
    "esbuild": "<pin>",
    "eslint": "<pin>",
    "typescript": "<pin>"
  }
}
```

Run `npm install` to produce `package-lock.json` (committed).

**`jsconfig.json`**

```json
{
  "compilerOptions": {
    "checkJs": true,
    "noEmit": true,
    "strict": true,
    "module": "nodenext",
    "moduleResolution": "nodenext",
    "target": "es2023",
    "types": ["node"],
    "skipLibCheck": true
  },
  "include": ["bin", "src", "test"],
  "exclude": ["node_modules"]
}
```

**`eslint.config.js`**

```js
import js from "@eslint/js";

export default [
  { ignores: ["node_modules/", "controller/", "docker/"] },
  js.configs.recommended,
  {
    files: ["**/*.js"],
    languageOptions: {
      ecmaVersion: 2024,
      sourceType: "module",
      globals: { process: "readonly", console: "readonly", URL: "readonly", fetch: "readonly" },
    },
    rules: {
      "no-unused-vars": ["error", { argsIgnorePattern: "^_" }],
      eqeqeq: "error",
    },
  },
];
```

## 5. CLI implementation

General contract:

- `bin/virtualme.js` (mode 755): shebang `#!/usr/bin/env node`, then `import { main } from "../src/main.js"; process.exitCode = await main(process.argv.slice(2));`
- `src/main.js` exports `async function main(argv)` returning an exit code. Dispatch on `argv[0]`:
  - no args or `help` / `-h` / `--help` → help, exit 0
  - `version` / `-v` / `--version` → version, exit 0
  - known subcommand → run it, return its code
  - unknown → print `error: unknown command '<x>'` in red to stderr, print help to stderr, exit **2**
- Exit codes everywhere: `0` success, `1` operational failure (docker missing, container not running, command failed), `2` usage error.
- Every module is ESM with JSDoc type annotations that pass `tsc -p jsconfig.json` under `strict`.
- Use `node:util` `parseArgs` for per-command flags; in v1 only `logs` (`-f`/`--follow`) and `start` (`--data <dir>`) take flags.

**`src/ansi.js`** — exports:

```js
export const enabled =
  process.stdout.isTTY === true &&
  !("NO_COLOR" in process.env) &&
  process.env.TERM !== "dumb";
// wrap(code) returns (s) => enabled ? `\x1b[${code}m${s}\x1b[0m` : String(s)
export const bold, dim, red, green, yellow, cyan;  // codes 1, 2, 31, 32, 33, 36
```

**`src/config.js`**

```js
export const IMAGE = process.env.VIRTUALME_IMAGE ?? "mayanklahiri/virtualme";
export const TAG = process.env.VIRTUALME_TAG ?? "latest";
export const CONTAINER = "virtualme";
export const PORT = 8080;
export const DATA_MOUNT = "/home/virtualme/.virtualme";  // rw mountpoint inside the container
// defaultDataDir() → process.env.VIRTUALME_DATA ?? path.join(os.homedir(), ".virtualme")
```

**`src/docker.js`** — thin wrappers over `node:child_process`:

- `haveDocker()` → boolean, `spawnSync("docker", ["--version"])` succeeds.
- `daemonUp()` → boolean, `spawnSync("docker", ["info"])` exit 0.
- `run(args, { inherit = true } = {})` → spawns `docker <args>` with `stdio: "inherit"` (streams to user), returns exit code. A `capture(args)` variant returns `{ code, stdout }`.
- `containerState()` → `"running" | "exited" | "absent"` via `docker inspect -f {{.State.Status}} virtualme`.

**Subcommands** (one module each in `src/commands/`, each exporting `run(argv)`):

| Command | Behavior (exact docker invocations) |
|---|---|
| `help` | Print usage: header `Virtual Me — private personal background agent`, `Usage: virtualme <command>`, then a two-column colorized list of all commands below. Must include the word `Usage`. |
| `version` | Print the `version` field of the package's own `package.json` (read with `fs.readFileSync(new URL("../../package.json", import.meta.url))`). |
| `doctor` | Run checks, print one line per check with green `ok` / red `FAIL`, exit 1 if any fail: (1) Node >= 22 (`process.versions.node`); (2) `docker` on PATH; (3) docker daemon reachable (`docker info`); (4) *when run from a git checkout containing `scripts/check.sh`*: `git config core.hooksPath` equals `.githooks` (print hint `run: git config core.hooksPath .githooks` on FAIL); (5) informational (never fails): CPU arch and total RAM, with warning if RAM < 8 GiB (`Gemma 4 E2B needs ~4 GiB free`). |
| `start` | Fail (exit 1, red message) if docker/daemon missing or container already `running`. If container is `exited`, `docker rm virtualme` first. Resolve the host data dir: `--data <dir>` flag > `VIRTUALME_DATA` env > `~/.virtualme` (`os.homedir()`); create it with `mkdir -p` if missing. Then: `docker run -d --name virtualme --restart unless-stopped --shm-size=1g --read-only --user <uid>:<gid> --tmpfs /run:exec,mode=755,uid=<uid>,gid=<gid> --tmpfs /tmp:mode=1777 -p 8080:8080 -v <DATA_DIR>:/home/virtualme/.virtualme <IMAGE>:<TAG>` where `<uid>`/`<gid>` come from `process.getuid()`/`process.getgid()` (fall back to `1000` where unavailable). The container root filesystem is read-only; the data dir is the only rw mount and all files it gains are owned by the invoking host user (see spec 002 §1). On success print the UI URL `http://localhost:8080`, the data dir in use, and the `virtualme logs -f` hint. |
| `stop` | `docker stop virtualme` then `docker rm virtualme`. Exit 0 with note if container absent. |
| `status` | Print container state from `containerState()`. If running, also GET `http://127.0.0.1:8080/healthz` using built-in `fetch` (2s timeout via `AbortSignal.timeout(2000)`); pretty-print each service as green/red. If the fetch fails, print `controller: unreachable` in yellow (not an error before spec 003 is deployed). |
| `logs` | `docker logs virtualme`; flag `-f`/`--follow` appends `-f`. |
| `build` | Requires `docker/Dockerfile` relative to CWD; if missing, exit 1 with `build must run from a source checkout (see spec 002)`. Run `docker build -f docker/Dockerfile -t <IMAGE>:dev -t <IMAGE>:<TAG> .`; omit the duplicate second tag when `TAG=dev`. This makes `virtualme build && virtualme start` run the image just built. |
| `keygen` | Print one line: 32 random bytes as base64url — `crypto.randomBytes(32).toString("base64url")` (43 chars, `[A-Za-z0-9_-]`). Future auth primitive; no other side effects. |
| `update` | `docker pull <IMAGE>:<TAG>`. |

**Tests** (`node:test` + `node:assert/strict`):

- `test/ansi.test.js` — spawn `node -e` printing `red("x")` with `NO_COLOR=1` and assert output is exactly `x` (no escape codes); assert escape codes present with `FORCE_COLOR`-free TTY simulation skipped (only NO_COLOR path is deterministic — test that path plus the pure `wrap` logic by importing `ansi.js` with `enabled` false).
- `test/cli.test.js` — `spawnSync(process.execPath, ["bin/virtualme.js", ...])`: `help` → exit 0, stdout contains `Usage`; no args → same; `version` → stdout equals package.json version; `nope` → exit 2, stderr contains `unknown command`. Additionally, unit-test the docker argv construction by injecting a fake docker runner: `build` produces both the `:dev` tag and the configured start tag; `start` (with a temp data dir) produces exactly the flag set from the table above (`--read-only`, `--user`, both `--tmpfs` mounts, the data-dir bind mount) and creates the data dir when missing.
- `test/keygen.test.js` — run `keygen`, assert stdout trimmed matches `/^[A-Za-z0-9_-]{43}$/`; two runs differ.

Verify section 5: `npm test` green; `node bin/virtualme.js help` exit 0; `./cli.sh version` prints `0.1.0`.

## 6. Quality gates

**`scripts/check.sh`** (mode 755) — the single canonical gate. Deterministic: no network, no clock. CI and the pre-commit hook run exactly this.

```bash
#!/usr/bin/env bash
# Canonical deterministic quality gate. Run by .githooks/pre-commit and CI.
# Env: CHECK_SKIP_GO=1 skips Go gates (used by the npm publish job).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

fail() { echo "check: FAIL: $*" >&2; exit 1; }
step() { echo "check: $*"; }

step "shell syntax (bash -n)"
shopt -s nullglob
for f in cli.sh scripts/*.sh .githooks/* test/*.sh docker/layers/*.sh \
         docker/rootfs/etc/cont-init.d/* controller/tools/*.sh; do
  bash -n "$f" || fail "bash -n $f"
done
shopt -u nullglob

step "eslint"
node_modules/.bin/eslint . || fail eslint

step "typecheck (tsc --checkJs)"
node_modules/.bin/tsc -p jsconfig.json || fail typecheck

step "unit tests (node --test)"
node --test test/*.test.js || fail "node tests"

step "CLI dry run"
node bin/virtualme.js help >/dev/null || fail "cli help"
node bin/virtualme.js version >/dev/null || fail "cli version"

if [[ -d controller && "${CHECK_SKIP_GO:-0}" != "1" ]]; then
  step "go gates"
  if [[ -f controller/web/static/index.html ]] &&
     { [[ ! -f controller/web/static/fonts/InterVariable.woff2 ]] ||
       [[ ! -f controller/web/static/fonts/InterVariable-Italic.woff2 ]]; }; then
    fail "fonts missing; run: bash controller/tools/fetch-assets.sh (one-time, needs network)"
  fi
  if [[ -f scripts/build-web.sh ]]; then
    step "web build (esbuild minify + sourcemaps)"
    bash scripts/build-web.sh || fail "build-web"
  fi
  (cd controller && [[ -z "$(gofmt -l .)" ]]) || fail "gofmt -l"
  (cd controller && go vet ./...) || fail "go vet"
  (cd controller && go test ./...) || fail "go test"
fi

echo "check: OK"
```

**`.githooks/pre-commit`** (mode 755)

```bash
#!/usr/bin/env bash
set -euo pipefail
exec bash "$(git rev-parse --show-toplevel)/scripts/check.sh"
```

Activate: `git config core.hooksPath .githooks` (documented in README; verified by `doctor`).

Verify section 6: `npm run check` prints `check: OK`; making a commit runs the hook (test with an empty-ish commit, e.g. touch a scratch file, `git add`, `git commit` — then discard).

## 7. CI workflow

**`.github/workflows/ci.yml`** — exact contents (the `smoke` and `e2e` steps self-disable until specs 002/003 add their scripts):

```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

jobs:
  check:
    runs-on: ubuntu-24.04
    strategy:
      matrix:
        node: [22, 24]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: ${{ matrix.node }}
          cache: npm
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - run: npm ci
      - name: Fetch web assets (spec 003+)
        run: |
          if [ -f controller/tools/fetch-assets.sh ]; then bash controller/tools/fetch-assets.sh; fi
      - run: npm run check

  container:
    runs-on: ubuntu-24.04
    needs: check
    steps:
      - uses: actions/checkout@v4
      - name: Smoke test (spec 002+)
        run: |
          if [ -f test/smoke.sh ]; then bash test/smoke.sh; else echo "smoke: skipped (no test/smoke.sh yet)"; fi
      - name: E2E test (spec 003+)
        run: |
          if [ -f test/e2e.sh ]; then bash test/e2e.sh; else echo "e2e: skipped (no test/e2e.sh yet)"; fi
```

## 8. Release workflow

**`.github/workflows/release.yml`** — exact contents. Requires repo secrets `DOCKERHUB_USERNAME`, `DOCKERHUB_TOKEN`, `NPM_TOKEN`. Uses native arm64 runners (`ubuntu-24.04-arm`, free for public repos) — no QEMU.

```yaml
name: release

on:
  push:
    tags: ["v*"]

env:
  IMAGE: mayanklahiri/virtualme

jobs:
  verify:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4
      - name: Tag matches package.json version
        run: |
          PKG=$(node -p "require('./package.json').version")
          [ "v$PKG" = "$GITHUB_REF_NAME" ] || { echo "tag $GITHUB_REF_NAME != package.json v$PKG"; exit 1; }

  docker-arch:
    needs: verify
    strategy:
      matrix:
        include:
          - runner: ubuntu-24.04
            arch: amd64
          - runner: ubuntu-24.04-arm
            arch: arm64
    runs-on: ${{ matrix.runner }}
    steps:
      - uses: actions/checkout@v4
      - uses: docker/login-action@v3
        with:
          username: ${{ secrets.DOCKERHUB_USERNAME }}
          password: ${{ secrets.DOCKERHUB_TOKEN }}
      - name: Build and push per-arch image
        run: |
          VERSION=${GITHUB_REF_NAME#v}
          docker build -f docker/Dockerfile -t "$IMAGE:$VERSION-${{ matrix.arch }}" .
          docker push "$IMAGE:$VERSION-${{ matrix.arch }}"

  docker-manifest:
    needs: docker-arch
    runs-on: ubuntu-24.04
    steps:
      - uses: docker/login-action@v3
        with:
          username: ${{ secrets.DOCKERHUB_USERNAME }}
          password: ${{ secrets.DOCKERHUB_TOKEN }}
      - name: Create multi-arch manifests
        run: |
          VERSION=${GITHUB_REF_NAME#v}
          docker buildx imagetools create -t "$IMAGE:$VERSION" -t "$IMAGE:latest" \
            "$IMAGE:$VERSION-amd64" "$IMAGE:$VERSION-arm64"

  npm-publish:
    needs: verify
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 24
          registry-url: https://registry.npmjs.org
      - run: npm ci
      - run: CHECK_SKIP_GO=1 npm run check
      - run: npm publish --access public
        env:
          NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}
```

Note: `docker-arch` jobs will fail until spec 002 adds `docker/Dockerfile`; that is acceptable — do not tag a release before 002 is executed. State this in the README release runbook.

## 9. AI skills

Skills live in Cursor format at `.cursor/skills/<name>/SKILL.md`. Claude Code gets them through a committed relative symlink; Codex reads `AGENTS.md` natively.

Create symlink: `mkdir -p .claude && ln -s ../.cursor/skills .claude/skills` (commit the symlink).

**`.cursor/skills/operate/SKILL.md`**

```markdown
---
name: operate
description: Install, run, and troubleshoot Virtual Me via the CLI (npx virtualme or ./cli.sh). Use when asked to start, stop, update, check, or diagnose a Virtual Me container.
---

# Operating Virtual Me

Virtual Me ships as a Docker image (`mayanklahiri/virtualme`) driven by a
zero-dependency Node CLI (`npx virtualme`, or `./cli.sh` from a checkout).

## Commands

| Command | Effect |
|---|---|
| `npx virtualme doctor` | Verify node/docker/daemon (+ git hooks in a checkout) |
| `npx virtualme start [--data <dir>]` | Run the container unprivileged (host uid/gid) with a read-only root, tmpfs `/run`+`/tmp`, port 8080, and the host data dir (default `~/.virtualme`, created if missing) mounted rw at the container's `~/.virtualme` |
| `npx virtualme status` | Container state + `/healthz` per-service report |
| `npx virtualme logs -f` | Follow container logs |
| `npx virtualme stop` | Stop and remove the container (volume survives) |
| `npx virtualme update` | Pull the latest image |
| `npx virtualme build` | Build `:dev` and the configured start tag from a source checkout |
| `npx virtualme keygen` | Print a 256-bit base64url token |

Env overrides: `VIRTUALME_IMAGE`, `VIRTUALME_TAG`, `VIRTUALME_DATA`.

## Endpoints (container running)

- `http://localhost:8080/` — control-plane SPA (live metrics charts + chat)
- `http://localhost:8080/healthz` — aggregate JSON health
- `http://localhost:8080/desktop/vnc.html?autoconnect=1&resize=scale&path=desktop/websockify` — noVNC remote desktop into the Xvfb display

## Troubleshooting

1. `start` fails: run `doctor`; check `docker info`.
2. Unhealthy service: `virtualme logs` — s6 prefixes each line with the service name.
3. Slow first health: the ~3 GB Gemma model loads at startup; allow up to 5 minutes on a Raspberry Pi.
4. RAM: 8 GB minimum (Pi 5 or Pi 4 8GB). The LLM alone needs ~4 GB.
5. Trust model: prototype has NO auth/TLS — only run on a trusted private network.
```

**`.cursor/skills/develop/SKILL.md`**

```markdown
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

## How to add things

- **CLI subcommand**: new `src/commands/<name>.js` exporting `run(argv)`,
  register in `src/main.js` and the help text, add a test, update skills/README.
- **Docker layer**: new `docker/layers/NNN-<slug>.sh` with the next number;
  `set -euo pipefail`; pin URLs+sha256; add a `COPY`+`RUN` pair at the END of
  the layer sequence in `docker/Dockerfile`. Never edit old layers without a
  spec amendment.
- **s6 service**: `docker/rootfs/etc/s6-overlay/s6-rc.d/svc-<name>/` with
  `type`, `run`, `dependencies.d/`, plus an entry in `user/contents.d/`.
- **Spec**: next number in `specs/`, follow the format of 001–003.

After any structural change run the `/master-update` skill.
```

**`.cursor/skills/master-update/SKILL.md`**

```markdown
---
name: master-update
description: Meta-review gate. Audits and updates all other AI skills, README link tables, AGENTS.md, and CLAUDE.md against the actual repo state, then runs the quality gates. Run after any structural change or completed spec.
---

# /master-update — docs and skills meta-updater

Docs follow code, never the reverse. Procedure:

## 1. Enumerate ground truth (from the tree, not from memory)

- `package.json`: name, version, scripts, bin, engines, devDependencies
- `src/commands/*.js`: the real CLI surface
- `.github/workflows/*.yml`: job names, triggers, required secrets
- `docker/layers/*.sh`: layer numbers, artifacts, pinned versions/hashes
- `docker/rootfs/etc/s6-overlay/s6-rc.d/*`: service list and dependencies
- `controller/`: endpoints (grep `mux.Handle`/`HandleFunc`), ws message types
- `specs/*.md`: spec index
- `.cursor/skills/*/SKILL.md`: skill inventory

## 2. Diff against documentation

Check each of: `README.md` (User's Guide tables: artifacts, CI, skills,
specs, ports, commands), `AGENTS.md`, `CLAUDE.md`, every `SKILL.md`.
Flag: missing entries, stale versions/ports/flags, removed features still
documented, broken relative links, drift between skill tables and
`src/commands/`.

## 3. Apply updates

Edit the docs to match ground truth. Keep prose terse. Do not invent
features. Do not change code to match docs — if code looks wrong, stop and
report instead.

## 4. Validate

Run `npm run check`. If Docker is available and `test/smoke.sh` exists and
container-affecting docs changed, also run `bash test/smoke.sh`.

## 5. Report

Summarize: files changed, drift found, anything suspicious left alone.
```

## 10. AGENTS.md and CLAUDE.md

**`AGENTS.md`** — assemble from:

1. Title `# AGENTS.md — virtualme`, one-paragraph project description (from README overview).
2. The Constitution: section 1 of this spec, verbatim.
3. `## Layout` — condensed tree (top-level dirs, one comment each).
4. `## Commands` — table: `npm install`, `git config core.hooksPath .githooks`, `npm run check`, `npm test`, `./cli.sh <cmd>`, `bash test/smoke.sh`, `bash test/e2e.sh`, `bash controller/tools/fetch-assets.sh`.
5. `## Skills` — table of the three skills with one-line descriptions, path `.cursor/skills/`.
6. `## Specs` — table linking `specs/*.md`.

**`CLAUDE.md`**

```markdown
Read AGENTS.md — it is the canonical agent guide for this repository.
Skills are in .cursor/skills/ (symlinked at .claude/skills).
```

## 11. README.md rewrite

Keep the existing Overview and modes list at top, unchanged. Then add, in this order:

1. **Badges** (right under the title): CI badge `https://github.com/mayanklahiri/virtualme/actions/workflows/ci.yml/badge.svg` linked to the workflow; npm version badge `https://img.shields.io/npm/v/virtualme` linked to `https://www.npmjs.com/package/virtualme`; Docker badge `https://img.shields.io/docker/v/mayanklahiri/virtualme?label=docker` linked to `https://hub.docker.com/r/mayanklahiri/virtualme`.
2. **`## Quick start`**

   ```
   npx virtualme doctor   # check node >= 22 + docker
   npx virtualme start    # pull + run the container
   # open http://localhost:8080
   ```

   Plus the trust-model warning (constitution rule 8) in a blockquote.

3. **`## User's Guide`** — written for a developer returning after six months with zero memory of the project. Contains, as tables with links:
   - **Where everything lives**: npm package (`https://www.npmjs.com/package/virtualme`), Docker Hub image (`https://hub.docker.com/r/mayanklahiri/virtualme`), GitHub repo, GitHub Actions runs (`.../actions`), specs (`specs/`).
   - **CLI commands**: the full table from section 5.
   - **Ports**: 8080 exposed (SPA + `/healthz` + websocket + `/desktop/` noVNC); internal-only: 5900 x11vnc, 6080 noVNC/websockify, 6379 valkey, 8081 llama-server; Xvfb display `:99`.
   - **Data directory**: host dir `~/.virtualme` by default (override with `--data <dir>` or `VIRTUALME_DATA`), created on first `start`, mounted rw at the container's `~/.virtualme`; the container root filesystem is read-only and runs as the invoking host uid/gid, so all data files are host-owned.
   - **AI skills**: `operate`, `develop`, `master-update` with one-liners and paths; note `.claude/skills` symlink and `AGENTS.md` for Codex; explicit sentence: *"After changing anything structural, run the `/master-update` skill — it re-syncs this README, AGENTS.md, and all skills against the repo."*
   - **CI/CD**: table of the two workflows, their triggers, and required secrets (`DOCKERHUB_USERNAME`, `DOCKERHUB_TOKEN`, `NPM_TOKEN`).
   - **Development setup**: clone, `npm install`, `git config core.hooksPath .githooks`, `bash controller/tools/fetch-assets.sh`, `npm run check`.
   - **Release runbook**: bump `version` in `package.json` → commit → `git tag vX.Y.Z` → `git push --tags` → release workflow builds amd64+arm64 images natively, merges the manifest, publishes npm. Note: requires specs 002+ executed (Dockerfile must exist).
   - **Hardware**: Raspberry Pi 5 or Pi 4 (8 GB) minimum; 8 GB RAM floor (Gemma 4 E2B Q4_0 ≈ 3 GB on disk, ~4 GB resident).
4. **`## Architecture`** — three-sentence summary (container of s6-supervised services: Xvfb, openbox, x11vnc, noVNC, Chromium, Playwright, Valkey, llama.cpp + Gemma 4 E2B, Go controller on :8080) and a pointer to `specs/`.

Where 002/003 features are referenced before those specs run, mark them `*(available after spec 00N)*` — the executor of 002/003 removes the markers via `/master-update`.

## 12. Acceptance checklist (run every item)

| # | Command | Expected |
|---|---|---|
| 1 | `node --version` | >= 22 |
| 2 | `npm install && npm run check` | ends `check: OK`, exit 0 |
| 3 | `node bin/virtualme.js` | help text with `Usage`, exit 0 |
| 4 | `./cli.sh help` | same as above |
| 5 | `node bin/virtualme.js version` | `0.1.0` |
| 6 | `node bin/virtualme.js keygen \| grep -E '^[A-Za-z0-9_-]{43}$'` | match, exit 0 |
| 7 | `node bin/virtualme.js nope; echo $?` | stderr `unknown command`, prints `2` |
| 8 | `NO_COLOR=1 node bin/virtualme.js help \| grep -c $'\x1b'` | `0` (no escape codes) |
| 9 | `node bin/virtualme.js doctor` | all checks listed; hook check `ok` after `git config core.hooksPath .githooks` |
| 10 | `npm pack --dry-run` | contains only `bin/`, `src/`, `README.md`, `LICENSE`, `package.json` |
| 11 | `node -e "const p=require('./package.json'); if (p.dependencies) throw 1"` | exit 0 (zero runtime deps) |
| 12 | `git config core.hooksPath` | `.githooks` |
| 13 | Commit attempt with a deliberately broken JS file staged | pre-commit hook rejects |
| 14 | `ls -la .claude/skills` | symlink → `../.cursor/skills` |
| 15 | `test -f AGENTS.md && test -f CLAUDE.md && test -f LICENSE` | exit 0 |
| 16 | README contains sections `Quick start`, `User's Guide`, `Architecture`, badges | manual read |
| 17 | Both workflow files parse: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml')); yaml.safe_load(open('.github/workflows/release.yml'))"` (if PyYAML is unavailable, use any YAML validator or rely on GitHub's workflow parser after push) | exit 0 |

Finally: run the `/master-update` skill procedure once. Expected outcome on a fresh scaffold: the only "drift" it finds is forward references to spec 002/003 artifacts (`test/smoke.sh`, `test/e2e.sh`, `docker/`, `controller/`) in the skills and README — leave those, they document the target state and carry `*(available after spec 00N)*` markers in the README. Then commit everything as `spec 001: constitution, CLI, gates, CI/CD, skills`.
