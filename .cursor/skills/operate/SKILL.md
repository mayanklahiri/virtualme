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
| `npx virtualme start [--data <dir>] [--no-browser-sandbox] [--gpus <spec>] [--no-gpu]` | Run unprivileged with tmpfs `/run`+`/tmp`, port 8080, and the host data dir mounted rw; a detected host NVIDIA stack is auto-passed through (`--no-gpu` opts out, `--gpus <spec>` overrides); optionally force Chromium's sandbox fallback |
| `npx virtualme status` | Container state + `/healthz` per-service report |
| `npx virtualme logs [-f\|--follow]` | Show or follow container logs |
| `npx virtualme stop` | Stop and remove the container (data dir survives) |
| `npx virtualme update` | Pull the configured image |
| `npx virtualme build` | Build `:dev` and the configured start tag from a source checkout |
| `npx virtualme keygen` | Print a 256-bit base64url token |
| `./cli.sh soak [--no-build]` | Rebuild, restart on a fresh data dir, and run live soak flows from a source checkout |

Env overrides: `VIRTUALME_IMAGE`, `VIRTUALME_TAG`, `VIRTUALME_DATA`, and `TZ`
(forwarded to the container; otherwise the detected host timezone is used). The CLI
also forwards configured `VM_MAIL_MAILNAME`, `VM_MAIL_FROM`,
`VM_MAIL_SMARTHOST`, `VM_MAIL_SMARTHOST_PORT`, `VM_MAIL_SMARTHOST_USER`,
`VM_MAIL_SMARTHOST_PASS`, `VM_MAIL_DKIM_DOMAIN`, and
`VM_MAIL_DKIM_SELECTOR`, `VM_MAIL_FLUSH_SEC`, plus `VM_TTS_CACHE_DIR` and
`VM_TTS_CACHE_MAX_MB`.

## Endpoints (container running)

- `http://localhost:8080/` — console home
- `http://localhost:8080/projects` — recurring projects and schedules
- `http://localhost:8080/projects/<id>` — project task, runs, and scratch details
- `http://localhost:8080/jobs` — queue, filterable machine activity, and details
- `http://localhost:8080/tools` — agent definitions, queue-backed manual invocation, and structured results
- `http://localhost:8080/status` — service/GPU status, LLM token and browser-action charts, active time selectors, Quick Options toggles, and tiered metrics
- `http://localhost:8080/chat` — shared local-model chat
- `http://localhost:8080/speech` — streaming local text-to-speech
- `http://localhost:8080/mail` — outbound-mail composer, queue, and DKIM status
- `http://localhost:8080/desktop-view` — embedded noVNC desktop
- `http://localhost:8080/healthz` — aggregate JSON health
- `http://localhost:8080/v1/audio/speech` — OpenAI-compatible local speech API
- `ws://localhost:8080/ws` — live state, metrics, queue, tools, chat, agent, TTS, and mail frames
- `http://localhost:8080/desktop/` — redirects to the noVNC client; child paths proxy noVNC and websockify

The desktop UI opens `/desktop/vnc.html` with autoconnect, scaling, and the
proxied `/desktop/websockify` websocket path.

## Console

The home page live-updates controller health, hostname, browser-reachable
address, up to two container-interface addresses, uptime, CPU cores/load,
memory, disk capacity, controller build version, and any detected GPU. The
browser address is the address used to open the console; `container:` values
come from the container network namespace and may not be reachable from the
LAN. The theme button in the sidebar footer opens the eight-theme picker and
its automatic/light/dark variant controls; selections persist in the browser.

The sidebar connection watch shows controller hostname and port beside a
status pip that is green when live, red while reconnecting, or muted while
connecting. The second line reports server uptime and the current browser-link
duration. Reduced-motion mode disables pulsing while preserving the state
colors.

Use `/jobs` to see what the machine is actually doing. The queue card groups
rows into Running now (at most one; only that row pulses), Up next, and
Recently finished, with short type-derived names, kind pills, and explicit
status icons; the newest-first activity list records finer-grained LLM, tool,
speech, and mail actions with run times. Tool calls and jiggler bursts are
hidden by default behind persisted filter toggles, and details open in a third
pane at desktop widths.

Use `/tools` to inspect every definition available to the local model and
invoke it with a schema-generated form. Manual calls wait in the same
sequential queue and their results appear in Jobs activity; page-shaped JSON
renders as a linked title plus text, env output as sorted tables, and manual
screenshots have no coordinate grid. The page can run
`bash` and browser-input tools; it has no additional authentication under the
v1 trust model, so use it only on a trusted private network.

The Quick Options panel on `/status` is a row of fixed-size cockpit-style lit
buttons with labels beneath; hover or focus a button (or tap its label) for
help text. The default-on JIGGLER button moves the virtual desktop cursor in
short humanlike bursts every 8 to 27 seconds, yields only while the agent
holds the input-actuation lock, and records each burst on `/jobs`. The SCHED
lamp is lit while scheduled-job promotion runs; pressing it pauses promotion
without touching interactive work. Both states persist across page reloads
and container restarts in Valkey. The banner at the top of `/status` shows
overall health, the scheduler clock with active time selectors, and uptime.

The GPU card on `/status` always reports presence, vendor, model, and available
parameters (VRAM in GB). Side-by-side utilization and memory charts appear only
when sampling is supported.
NVIDIA passthrough is automatic when `start` detects `nvidia-smi` or Docker's
`nvidia` runtime; `--no-gpu` opts out. AMD/Intel `/dev/dri` passthrough is
host-specific and must be configured directly with Docker; the CLI has no
`--device` option.

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
most recent 20 tasks. CPU-only vision steps can take tens of seconds. With
NVIDIA passthrough (automatic, or explicit `--gpus <spec>`), `svc-llama`
selects the baked Vulkan llama.cpp build with full GPU offload; otherwise the
CPU build runs. Check `virtualme logs` for the `svc-llama: runtime` line and
the device list beneath it: a `Vulkan0: ...` line confirms GPU inference,
while an empty `Available devices` list means the Vulkan driver failed to
initialize and inference is silently running on the CPU.

## Local speech

Use `/speech` to synthesize with the baked Lessac (en-US) voice, the only
voice in the image (any other requested voice name falls back to it). The seed
buttons (Sci-fi AI, Road notes, Night bridge) fill the editor; Clear resets it. Completed console, chat, and API
syntheses appear in the global Valkey-backed History list and can be replayed.
Playback starts after the first sentence while later sentences generate; Stop
cancels synthesis immediately. In chat, explicitly ask the agent to say
something aloud to invoke `speak`.

Exact sentence/voice/speed renders are cached under
`~/.virtualme/tts-cache/`. `VM_TTS_CACHE_MAX_MB` sets the LRU cap (default
256 MiB); `VM_TTS_CACHE_DIR` overrides the location. The cache is
recomputable and may be deleted while the container is stopped.

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
`~/.virtualme/mail/spool/` or the Mail status panel to read the queue. Queue
age is time since the dma spool pair was created; "retry" counts down to the
next queue flush, not a guaranteed delivery attempt, because dma may apply
backoff. The newest recorded attempt error comes from the envelope when
available, otherwise the bounded `~/.virtualme/mail/flush.log`. Text previews
are read directly from queued messages. The `/mail` Outbox is a durable
Valkey-backed list tracking each submission as queued, left queue, error, or
cleared; the confirmed Clear queue control removes all spooled mail. Keep the
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
12. Speech: check the `tts` entry in `/healthz`; `ttsd` listens only on container loopback port 8082. Its startup log lists the found voice directories (Lessac only); cache files are under `~/.virtualme/tts-cache/`.
13. Mail not arriving: check the `mail` health entry, queue row's last error and next-flush countdown, and `~/.virtualme/mail/flush.log`; confirm relay credentials/port or direct-path outbound port 25, publish the displayed DKIM TXT record and SPF, and verify sending-IP PTR/reputation. Residential/dynamic IPs should use a smarthost.
14. Job queue: `queue-peek` on `/ws` returns upcoming, running, and finished jobs; durable queue keys are in the Valkey AOF under `~/.virtualme/valkey/`.
15. Projects: records and run summaries are in the Valkey AOF; scratch files are under `~/.virtualme/projects/<id>/` and survive project deletion.
16. Activity ledger: `/jobs` replays the newest 100 entries from the bounded `virtualme:activity` Valkey list; queue rows are separate envelope state.
17. GPU absent: this is normal. NVIDIA is auto-passed through when detected (or use `--gpus <spec>`); if `docker run` fails on a host without the NVIDIA Container Toolkit, restart with `--no-gpu`. AMD/Intel require host-specific `/dev/dri` device passthrough. GPU absence never changes `/healthz`.
