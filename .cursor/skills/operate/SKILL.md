---
name: operate
description: Install, run, and troubleshoot Virtual Me via the CLI (npx virtualme or ./cli.sh). Use when asked to start, stop, update, check, or diagnose a Virtual Me container.
---

# Operating Virtual Me

Virtual Me ships as a Docker image (`mayanklahiri/virtualme`) driven by a
zero-dependency Node CLI (`npx virtualme`, or `./cli.sh` from a checkout).
All v1 inference uses the container's loopback-only llama.cpp server; no
prompts or model requests are sent to external providers.

## Commands

| Command | Effect |
|---|---|
| `npx virtualme help` | Show usage and every command |
| `npx virtualme version` | Print the package version |
| `npx virtualme doctor` | Verify node/docker/daemon (+ git hooks in a checkout) |
| `npx virtualme start [--data <dir>] [--no-browser-sandbox] [--gpus <spec>]` | Run unprivileged with tmpfs `/run`+`/tmp`, port 8080, and the host data dir mounted rw; optionally force Chromium's sandbox fallback or pass Docker GPU access |
| `npx virtualme status` | Container state + `/healthz` per-service report |
| `npx virtualme logs [-f\|--follow]` | Show or follow container logs |
| `npx virtualme stop` | Stop and remove the container (data dir survives) |
| `npx virtualme update` | Pull the configured image |
| `npx virtualme build` | Build `:dev` and the configured start tag from a source checkout |
| `npx virtualme keygen` | Print a 256-bit base64url token |

Env overrides: `VIRTUALME_IMAGE`, `VIRTUALME_TAG`, `VIRTUALME_DATA`, and `TZ`
(forwarded to the container; otherwise the detected host timezone is used). The CLI
also forwards configured `VM_MAIL_MAILNAME`, `VM_MAIL_FROM`,
`VM_MAIL_SMARTHOST`, `VM_MAIL_SMARTHOST_PORT`, `VM_MAIL_SMARTHOST_USER`,
`VM_MAIL_SMARTHOST_PASS`, `VM_MAIL_DKIM_DOMAIN`, and
`VM_MAIL_DKIM_SELECTOR`.

## Endpoints (container running)

- `http://localhost:8080/` — console home
- `http://localhost:8080/projects` — recurring projects and schedules
- `http://localhost:8080/projects/<id>` — project task, runs, and scratch details
- `http://localhost:8080/jobs` — queue timeline, machine activity, and details
- `http://localhost:8080/status` — service status, active time selectors, opt-in jiggler, and tiered metrics
- `http://localhost:8080/chat` — shared local-model chat
- `http://localhost:8080/speech` — streaming local text-to-speech
- `http://localhost:8080/mail` — outbound-mail composer, queue, and DKIM status
- `http://localhost:8080/desktop-view` — embedded noVNC desktop
- `http://localhost:8080/healthz` — aggregate JSON health
- `http://localhost:8080/v1/audio/speech` — OpenAI-compatible local speech API
- `ws://localhost:8080/ws` — live state, metrics, queue, chat, agent, TTS, and mail frames
- `http://localhost:8080/desktop/` — proxied noVNC and websockify

The desktop UI opens `/desktop/vnc.html` with autoconnect, scaling, and the
proxied `/desktop/websockify` websocket path.

## Console

The home page live-updates controller health, hostname, uptime, CPU cores/load,
memory, and disk capacity. The theme button in the sidebar footer opens the
eight-theme picker and its automatic/light/dark variant controls; selections
persist in the browser.

Use `/jobs` to see what the machine is actually doing. Queue time flows from
upcoming at the top through the single executing job to recently finished
jobs; the activity list below is newest-first and records finer-grained LLM,
tool, speech, and mail actions.

The Jiggler switch on `/status` is off by default. When enabled it moves the
virtual desktop cursor in occasional short, humanlike bursts, yields whenever
an agent or queued job may act, and records each burst on `/jobs`. The setting
persists across page reloads and container restarts in Valkey.

## Browser-agent tasks

Give an operating task in `/chat`, for example: “Open example.com and tell me
the page title.” The agent observes screenshots, rendered DOM, and read-only
CDP state; all browser actions use `xdotool` mouse/keyboard input on `:99`.
The chat timeline shows each tool step. Use the Stop button to cancel an
in-flight model request, shell command, or runaway task.

Chat and agent work runs sequentially through the reliable Valkey queue;
additional messages wait instead of returning a busy error. Closing the browser
tab that initiated a queued or running request drops its queued jobs and
immediately cancels its active generation.

## Projects

Create periodic natural-language agent tasks from `/projects`, choose day/time
chips, and enable scheduling. Run now is manual and tied to the current browser
connection: keep that page open until it finishes, or enable a schedule for
connection-independent execution. Project scratch directories are
`~/.virtualme/projects/<id>/`.

Deleting a project removes its Valkey record and run summaries but intentionally
keeps its scratch directory. Operator data is never deleted implicitly; remove
an obsolete project directory manually after inspecting it.

Full screenshots and `steps.jsonl` (tool arguments, summaries, and capped
observation text) are retained under `~/.virtualme/agent/<taskId>/` for the
most recent 20 tasks. CPU-only vision steps can take tens of seconds. `--gpus all` passes GPU devices and marks
`VM_LLAMA_GPU=1`, but v1 still ships the pinned CPU llama.cpp build.

## Local speech

Use `/speech` to synthesize with the single baked Lessac en-US voice. Playback
starts after the first sentence while later sentences generate; Raspberry Pi
latency is therefore sentence-dependent. Stop cancels synthesis immediately.
In chat, explicitly ask the agent to say something aloud to invoke `speak`;
audio is session-only and replayable from its chat bubble.

## Outbound mail

Direct MX delivery is the default. For reliable delivery, set a relay before
starting:

    VM_MAIL_SMARTHOST=smtp.example.com \
    VM_MAIL_SMARTHOST_PORT=587 \
    VM_MAIL_SMARTHOST_USER=user \
    VM_MAIL_SMARTHOST_PASS=secret \
    VM_MAIL_DKIM_DOMAIN=example.com \
    npx virtualme start

With `VM_MAIL_DKIM_DOMAIN` set, copy the TXT owner/value shown in `/mail` into
DNS. The selector defaults to `virtualme`. Persistent configuration, queued
messages, and `dkim.key` are under `~/.virtualme/mail/`; inspect
`~/.virtualme/mail/spool/` or the Mail status panel to read the queue. Keep the
private key mode at 0600.

## Troubleshooting

1. `start` fails: run `doctor`; check `docker info`.
2. Unhealthy service: `virtualme logs` — s6 prefixes each line with the service name.
3. Slow first health: the ~3 GB Gemma model loads at startup; allow up to 5 minutes on a Raspberry Pi.
4. RAM: 8 GB minimum (Pi 5 or Pi 4 8GB). The LLM alone needs ~4 GB.
5. Trust model: prototype has NO auth/TLS — only run on a trusted private network.
6. Metrics history: persisted multi-resolution files live under `~/.virtualme/metrics/`.
7. Browser window: Chromium is undecorated and full-screen; closing it auto-restarts one blank tab, and the watchdog restores exact screen geometry. Popups cover the browser full-screen instead of tiling or overlapping it; close the popup to return to the main window.
8. Browser profile: settings persist under `~/.virtualme/chromium/`.
9. Browser sandbox: namespace sandboxing is automatic when supported; use `--no-browser-sandbox` to force the warning-suppressed fallback.
10. Data location: all persistent state is under `~/.virtualme/`; see `specs/007-persistence-locality.md` §1a plus its amendments.
11. Agent artifacts: inspect `~/.virtualme/agent/<taskId>/steps.jsonl` and `step-*.jpg`; Stop cancels the current task.
12. Speech: check the `tts` entry in `/healthz`; `ttsd` listens only on container loopback port 8082.
13. Mail not arriving: check the `mail` health entry, last result, and queue; confirm relay credentials/port or direct-path outbound port 25, publish the displayed DKIM TXT record and SPF, and verify sending-IP PTR/reputation. Residential/dynamic IPs should use a smarthost.
14. Job queue: `queue-peek` on `/ws` returns upcoming, running, and finished jobs; durable queue keys are in the Valkey AOF under `~/.virtualme/valkey/`.
15. Projects: records and run summaries are in the Valkey AOF; scratch files are under `~/.virtualme/projects/<id>/` and survive project deletion.
16. Activity ledger: `/jobs` replays the newest 100 entries from the bounded `virtualme:activity` Valkey list; queue rows are separate envelope state.
