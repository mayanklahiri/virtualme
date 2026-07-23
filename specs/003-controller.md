# Spec 003: Go Master Controller and Control-Plane SPA

| | |
|---|---|
| Status | Approved for execution |
| Depends on | `specs/001-constitution.md` and `specs/002-container.md` executed (container builds, smoke test green) |
| Produces | Full Go controller (health API, websocket state + chat channel, noVNC reverse proxy, embedded minified SPA), vanilla-JS SPA with live per-process metrics charts and chat, `scripts/build-web.sh`, `controller/tools/fetch-assets.sh`, `test/e2e.sh`, `test/chat-probe.mjs` |
| Followed by | Future specs (agent loop, Playwright task runner, auth) |

## 0. Executor instructions

- The constitution (`specs/001-constitution.md` §1) binds this spec. Zero third-party Go dependencies: `go.mod` must never gain a `require` block. The websocket layer (§5), the Valkey RESP client, and the llama SSE client (§6) are implemented in-repo.
- The stub from spec 002 (`controller/cmd/controller/main.go`) is **refactored into packages**, not thrown away: its probe functions and the `/healthz` contract move to `internal/health` unchanged in semantics.
- Stop-on-red per section; finish with the Acceptance Checklist (§11). The E2E script is the final gate for specs 001–003 combined.
- Trust model (constitution rule 8): no auth, no TLS. The UI is server-driven but not view-only: the websocket accepts exactly one typed client message (`chat`, §6); all other client data frames are ignored. Anyone who can reach port 8080 can view state and chat with the local model.
- The SPA is served **minified with external sourcemaps** from a gitignored `controller/web/dist/` built by `scripts/build-web.sh` (§8). Hand-written sources stay in `controller/web/static/`. The build is deterministic and offline (esbuild is an exact-pinned devDependency; constitution rule 5).

## 1. Controller architecture

```
controller/
├── go.mod                        # module github.com/mayanklahiri/virtualme/controller, go 1.26, NO requires
├── embed.go                      # //go:embed all:web/dist (+ explicit font entries)
├── cmd/controller/main.go        # wiring only
├── internal/
│   ├── health/health.go          # probes + aggregate (from spec 002 stub)
│   ├── health/health_test.go
│   ├── procstat/procstat.go      # per-service CPU/RSS sampling from /proc
│   ├── procstat/procstat_test.go
│   ├── ws/ws.go                  # minimal RFC 6455 server + hub
│   ├── ws/ws_test.go
│   ├── state/state.go            # snapshot collector + ring buffer + broadcaster
│   ├── state/state_test.go
│   ├── chat/chat.go              # shared chat: llama streaming + valkey persistence
│   ├── chat/valkey.go            # minimal RESP client (RPUSH/LRANGE/LTRIM only)
│   └── chat/chat_test.go  chat/valkey_test.go
├── web/static/                   # hand-written SPA sources (committed)
│   ├── index.html
│   ├── css/app.css
│   ├── js/app.js  js/ws.js  js/render.js  js/chart.js  js/chat.js
│   └── fonts/                    # gitignored; filled by tools/fetch-assets.sh
├── web/dist/                     # gitignored; minified build output of scripts/build-web.sh
└── tools/fetch-assets.sh         # pinned font download (build-time)
```

**`cmd/controller/main.go`** responsibilities (exact route table):

| Route | Handler |
|---|---|
| `/` (and all static paths) | `http.FileServer` over `fs.Sub(webFS, "web/dist")` from `//go:embed all:web/dist` — serves minified assets and their `.map` files |
| `/healthz` | JSON aggregate from `health.Gather` — same contract as spec 002 §6 (200 + `"ok":true` iff all six probes pass, else 503) |
| `/ws` | `hub.HandleUpgrade` (§5); non-upgrade requests get 400 |
| `/desktop/` | `http.StripPrefix("/desktop/", httputil.NewSingleHostReverseProxy(http://127.0.0.1:6080))` — stdlib ReverseProxy passes the `Connection: Upgrade` websocket through to websockify, so noVNC works end-to-end through port 8080 |

Startup: build `health.Config` from env (same vars/defaults as spec 002 §4), create the ws hub, create `chat.Service` (§6) and load persisted history, start `state.Collector` (2 s period, §3) feeding `hub.Broadcast`, wire `hub.SetHandler(chat.HandleClientMessage)` and `hub.SetOnConnect` to replay the state ring buffer and chat history to each new connection, then `http.ListenAndServe(VM_HTTP_ADDR, mux)`. Log one line per subsystem to stdout.

For testability, route construction lives in `func newMux(cfg health.Config, hub *ws.Hub, desktopURL *url.URL) *http.ServeMux`; `main` only reads env and calls it. **`cmd/controller/main_test.go`** covers routing: (a) `/desktop/` proxying — `httptest.Server` backend that records the request path, passed as `desktopURL`; a request to `/desktop/vnc.html` must reach the backend as `/vnc.html`; (b) `/healthz` returns JSON with a `services` array; (c) `/` serves the embedded `index.html` (body contains `Virtual Me`).

## 2. `internal/health`

Move the stub's probe logic into a testable package:

```go
type Config struct {
    Display       string // ":99"
    X11SocketDir  string // "/tmp/.X11-unix"   (parametrized for tests)
    VNCAddr       string // "127.0.0.1:5900"
    NoVNCURL      string // "http://127.0.0.1:6080/vnc.html"
    ValkeyAddr    string // "127.0.0.1:6379"
    LlamaHealthURL string // "http://127.0.0.1:8081/health"
    Xdotool       string // "xdotool"          (parametrized for tests)
}
func FromEnv() Config
func Gather(cfg Config) Health   // runs the six probes CONCURRENTLY (sync.WaitGroup);
                                 // preserves fixed service order in the result:
                                 // xvfb, x11vnc, novnc, valkey, llama, chromium
```

`Service`, `Health`, probe timeout (2 s), and per-probe semantics are exactly those of spec 002 §6. Concurrent probing keeps worst-case `/healthz` latency at ~2 s instead of 12 s.

**`health_test.go`** (all hermetic, no network beyond loopback):

- `checkHTTP`: `httptest.Server` returning 200 → ok; returning 500 → fail with `status 500`; closed port → fail.
- `checkValkey`: fake server via `net.Listen` that reads a line and writes `+PONG\r\n` → ok; one that writes `-ERR` → fail.
- `checkX11Socket`: temp dir with a created `X99` file + `Display: ":99"` → ok; empty dir → fail.
- `checkChromium`: temp dir on `PATH`-free lookup — set `Config.Xdotool` to a stub script (`#!/bin/sh\nexit 0`) → ok; `exit 1` → fail.
- `Gather`: all-fake-green config → `OK:true`, six services in fixed order; one red → `OK:false`.

## 3. `internal/state`

```go
type System struct {
    Load1      float64 `json:"load1"`
    MemUsedMB  int     `json:"memUsedMB"`
    MemTotalMB int     `json:"memTotalMB"`
}
type Snapshot struct {
    Type      string           `json:"type"`      // always "state"
    Ts        int64            `json:"ts"`        // unix millis
    UptimeSec int64            `json:"uptimeSec"` // since controller start
    OK        bool             `json:"ok"`
    Services  []health.Service `json:"services"`
    System    System           `json:"system"`
    Processes []procstat.Proc  `json:"processes"` // §4; fixed order: xvfb, openbox,
                                                  // x11vnc, novnc, valkey, llama,
                                                  // chromium, controller
}
func ReadSystem(loadavg, meminfo string) System  // pure parser, tested on fixtures
func NewCollector(cfg health.Config, procRoot string, broadcast func([]byte)) *Collector
func (c *Collector) Run(ctx context.Context)     // every 2s: Gather + ReadSystem
                                                 // (/proc/loadavg, /proc/meminfo)
                                                 // + procstat.Sample, marshal
                                                 // Snapshot, append to ring buffer,
                                                 // broadcast
func (c *Collector) HistoryMessage() []byte      // {"type":"history","snapshots":[...]}
```

`ReadSystem` parses field 1 of `/proc/loadavg` and `MemTotal:`/`MemAvailable:` from `/proc/meminfo` (`used = total - available`), tolerating missing files (zeros) so the controller also runs on dev machines/macOS Docker hosts.

**Ring buffer**: the collector keeps the last **150 snapshots** (~5 min at 2 s) in a mutex-guarded ring. `HistoryMessage()` marshals them oldest-first as `{"type":"history","snapshots":[…]}`; the hub's on-connect hook sends it to every new websocket connection so a page refresh restores the chart window.

**`state_test.go`**: fixture strings for both proc files → exact `System` values; `Snapshot` marshals with `"type":"state"` and the fields above; ring buffer caps at 150 and `HistoryMessage` returns snapshots oldest-first.

## 4. `internal/procstat`

Per-service CPU and memory sampling from `/proc`, zero deps, parametrized on a proc root for tests.

```go
type Proc struct {
    Name   string  `json:"name"`   // service name (matches health/service naming + openbox, controller)
    CPUPct float64 `json:"cpuPct"` // % of one CPU over the sampling interval, >= 0
    MemMB  int     `json:"memMB"`  // resident set size, MiB
}
type Sampler struct{ /* procRoot, prev per-pid cpu ticks, prev sample time */ }
func NewSampler(procRoot string) *Sampler
func (s *Sampler) Sample() []Proc   // fixed order: xvfb, openbox, x11vnc, novnc,
                                    // valkey, llama, chromium, controller
```

- **Matching**: scan `/proc/[0-9]*/comm` and map to services: `Xvfb`→xvfb, `openbox`→openbox, `x11vnc`→x11vnc, `websockify`→novnc, `valkey-server`→valkey, `llama-server`→llama, `chromium` (prefix match — covers renderer/gpu children)→chromium, `controller`→controller. All PIDs matching a service are **aggregated** (summed).
- **CPU%**: per PID read `utime`+`stime` (fields 14/15 of `/proc/<pid>/stat`, parsed after the last `)` to survive spaces in comm). CPU% = Δticks / (Δwallclock × `sysconf(_SC_CLK_TCK)`=100 on Linux) × 100, summed per service, clamped ≥ 0. PIDs first seen this round contribute 0. First `Sample()` call returns 0 CPU% for everything.
- **MemMB**: RSS pages (field 2 of `/proc/<pid>/statm`) × page size (4096), summed per service, reported in MiB.
- Vanished PIDs mid-scan are skipped silently. Missing proc root (dev macOS) yields all-zero entries, never an error.

**`procstat_test.go`**: pure parsers (`parseStat` handles `(comm with) parens`, `parseStatm`) on fixture strings; `Sample` against a synthetic proc root in a temp dir — two calls with doctored tick counts produce the expected CPU% and MemMB, aggregation over multiple chromium PIDs verified, fixed output order asserted.

## 5. `internal/ws` — minimal RFC 6455 server

No third-party code. Scope: server-side accept, unfragmented text frames both directions, ping/pong, close. Everything else (fragmentation, extensions, compression, client role) is intentionally unsupported.

```go
type Conn struct{ /* net.Conn + bufio.ReadWriter + write mutex */ }
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error)
func (c *Conn) WriteText(p []byte) error
func (c *Conn) ReadLoop()   // service frames until close/error
func (c *Conn) Close() error

type Hub struct{ /* mutex + map[*Conn]struct{} + handler + onConnect */ }
func NewHub() *Hub
func (h *Hub) SetHandler(fn func(c *Conn, payload []byte))  // client text frames
func (h *Hub) SetOnConnect(fn func(c *Conn))                // called after register
func (h *Hub) HandleUpgrade(w http.ResponseWriter, r *http.Request)  // Upgrade, register,
                                                                     // onConnect, go ReadLoop,
                                                                     // unregister on exit
func (h *Hub) Broadcast(p []byte)  // WriteText to every conn; drop+close conns that error
func (h *Hub) Count() int
```

Implementation requirements (exact):

1. **Handshake** (`Upgrade`): require method GET; `Upgrade` header containing `websocket` (case-insensitive); `Connection` header containing `upgrade`; `Sec-WebSocket-Version: 13`; non-empty `Sec-WebSocket-Key`. On violation: `http.Error(w, ..., 400)` and error return. Compute accept key = `base64.StdEncoding(sha1(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))` (`crypto/sha1` is fine here; not used for security). Then `http.Hijacker.Hijack()` and write raw:
   `HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: <accept>\r\n\r\n`
2. **Write frames** (server→client, never masked): byte0 `0x81` (FIN + text) — or `0x8A` pong, `0x88` close; byte1 length: `<126` direct, `<=65535` `126` + uint16 BE, else `127` + uint64 BE; then payload. Serialize all writes with a mutex.
3. **Read loop**: parse byte0/byte1; client frames MUST have the mask bit set — if not, close with code 1002. Read 7/16/64-bit length (cap payload at 64 KiB, else close 1009), 4-byte mask key, payload; unmask with XOR. Dispatch: opcode `0x8` close → echo close (code 1000) and return; `0x9` ping → reply pong with same payload; `0xA` pong → ignore; `0x1` text → **deliver the unmasked payload to the hub handler** (discard if no handler is set); `0x2` binary → discard. Unknown opcode → close 1002.
4. **Close**: send close frame (code 1000, empty reason), then `net.Conn.Close()`. Idempotent via `sync.Once`.

**`ws_test.go`** (raw-socket client; no libraries):

- Accept-key vector from RFC 6455 §1.3: key `dGhlIHNhbXBsZSBub25jZQ==` → accept `s3pPLMBiTxaQ9kYGzzhZRbK+xOo=` (unit-test the key function directly).
- Handshake: `httptest.Server` whose handler upgrades and registers with a hub; test dials `net.Dial("tcp", ...)`, writes a raw GET with the four headers, reads the 101 response, asserts the accept header.
- Server push: after handshake, `hub.Broadcast([]byte("hi"))`; client reads raw bytes `0x81 0x02 'h' 'i'`.
- On-connect: hub with `SetOnConnect` that sends `hello`; client receives it immediately after the 101 without any broadcast.
- Client message: client sends a **masked** text frame `{"type":"chat","text":"x"}`; the handler set via `SetHandler` receives exactly that payload with the sending `*Conn`.
- Ping/pong: client sends a **masked** ping frame with payload `ab`; expects pong `0x8A 0x02 'a' 'b'`.
- Bad handshake: plain GET to `/ws` without upgrade headers → HTTP 400 (this exact behavior is also asserted by `test/e2e.sh`).

## 6. `internal/chat` — shared chat over the websocket

One shared conversation for the whole instance: every connected client sees the same history and the same streaming replies (constitution rule 8: single trusted user on a private network). Backed by llama-server's OpenAI-compatible streaming API and persisted in Valkey.

### Message protocol (JSON over `/ws` text frames)

Client → server (the only accepted client message):

| Message | Meaning |
|---|---|
| `{"type":"chat","text":"…"}` | Submit a user message. `text` is trimmed; must be 1–4096 chars. |

Anything unparseable, of unknown `type`, or oversized gets a **per-connection** `chat-error` and is otherwise ignored.

Server → clients:

| Message | Scope | Meaning |
|---|---|---|
| `{"type":"state",…}` | broadcast | Snapshot (§3), every 2 s |
| `{"type":"history","snapshots":[…]}` | per-connection, on connect | State ring buffer replay (§3) |
| `{"type":"chat-history","messages":[{"role","text","ts"},…]}` | per-connection, on connect | Persisted conversation (up to 200 messages, oldest first) |
| `{"type":"chat-message","role":"user","text":"…","ts":…}` | broadcast | A user message was accepted and appended |
| `{"type":"chat-delta","text":"…"}` | broadcast | Streamed token(s) of the in-flight assistant reply |
| `{"type":"chat-done","role":"assistant","text":"<full reply>","ts":…}` | broadcast | Reply complete; also appended to history |
| `{"type":"chat-error","error":"…"}` | per-connection for input errors; broadcast if an in-flight generation fails | Error report |

### Semantics

```go
type Service struct{ /* history []Message (mutex), valkey addr, llama URL, busy flag */ }
func New(valkeyAddr, llamaURL string, broadcast func([]byte)) *Service
func (s *Service) LoadHistory()                       // at startup: LRANGE; tolerate valkey down (empty + log)
func (s *Service) HistoryMessage() []byte             // {"type":"chat-history",...}
func (s *Service) HandleClientMessage(c *ws.Conn, p []byte)
```

- **Single in-flight completion**: a mutex/busy flag; a `chat` received while generating → per-connection `chat-error` `"busy: a reply is already streaming"`.
- **Flow**: validate → append user `Message{role:"user"}` to memory + Valkey → broadcast `chat-message` → POST `http://127.0.0.1:8081/v1/chat/completions` with `{"stream":true,"messages":[system + last 16 history messages]}` (system prompt: one sentence identifying the assistant as Virtual Me running locally) → parse SSE `data:` lines, extract `choices[0].delta.content`, broadcast each non-empty delta as `chat-delta` → on `data: [DONE]` append assistant message to memory + Valkey and broadcast `chat-done`. Request timeout 120 s; on HTTP/stream error broadcast `chat-error` and clear the busy flag.
- **Persistence** (`valkey.go`, minimal RESP client — no third-party code): dial `127.0.0.1:6379` per operation with 2 s deadlines; implement only what is used: `RPUSH virtualme:chat <json>`, `LTRIM virtualme:chat -200 -1`, `LRANGE virtualme:chat 0 -1` (and RESP parsing for simple strings, errors, integers, bulk strings, arrays). Valkey being down never crashes the controller: appends are best-effort (log once per failure), history loads as empty. Durability across container restarts comes from Valkey's append-only file on the data volume (spec 002 §5).

**Tests** (hermetic):

- `valkey_test.go`: scripted fake TCP server asserts exact RESP bytes for RPUSH/LTRIM/LRANGE and feeds back canned replies; error reply surfaces as Go error.
- `chat_test.go`: fake llama `httptest.Server` streaming three SSE deltas then `[DONE]` → broadcast sequence is `chat-message`, 3×`chat-delta`, `chat-done`, and history gains two messages; oversized/garbage client payload → per-connection `chat-error`, history unchanged; second concurrent `chat` → busy `chat-error`; SSE 500 → broadcast `chat-error`, busy flag cleared.

## 7. SPA (`controller/web/static/`, served from `controller/web/dist/`)

Dark/light auto, responsive, accessible, zero external requests (all assets same-origin, embedded in the binary). No framework; hand-written ESM modules bundled and minified by `scripts/build-web.sh` (§8). The UI is server-driven: it renders websocket messages (§3, §6) and sends only `chat` messages.

**`index.html`** — structure (executor writes real markup following this exactly):

- `<!doctype html><html lang="en">`, `<meta charset>`, `<meta name="viewport" content="width=device-width, initial-scale=1">`, `<title>Virtual Me</title>`, `<link rel="stylesheet" href="/css/app.css">`, `<script type="module" src="/js/app.js"></script>`.
- Inline SVG sprite (`<svg hidden><symbol id="i-display">…`) with six simple 24×24 stroke icons drawn from basic shapes only (rect/circle/line/path): `i-display` (xvfb), `i-eye` (x11vnc/novnc), `i-globe` (chromium), `i-db` (valkey), `i-chip` (llama), `i-gauge` (system).
- Landmarks: `<header>` (product name `<h1>Virtual Me</h1>` + connection pill), `<main>` (six sections below), `<footer>` (version + link to GitHub repo).
  - `#status` section: overall OK banner + uptime.
  - `#services` section: `<ul>` of cards, one per service — icon, name, green/red status dot (`aria-label="healthy"/"unhealthy"`), detail text when unhealthy.
  - `#metrics` section: two `<canvas>` streaming charts — **CPU (%)** and **Memory (MiB)** — grouped bars per process (§7a), plus one shared legend (`<ul>`) mapping the 8 process colors to names.
  - `#system` section: load and memory bars (`<meter>` or div bars with `role="meter"`, `aria-valuenow`).
  - `#chat` section: conversation log + input form (§7b).
  - `#desktop` section: prominent link (styled as button) to `/desktop/vnc.html?autoconnect=1&resize=scale&path=desktop/websockify` opening in a new tab — the remote desktop through the single exposed port.
- Connection pill has `aria-live="polite"` and cycles `connecting… / live / reconnecting…`.

### 7a. Metrics charts (`js/chart.js`)

Hand-written canvas 2D, no libraries:

- Data source: the `history` replay on connect plus live `state` snapshots; rolling window of the most recent 150 samples per chart.
- Rendering: x axis = time; each time bucket draws a **group of 8 bars** (one per process, consistent palette from CSS custom properties `--p1…--p8` read via `getComputedStyle` so both color schemes work); y axis auto-scales (CPU chart floors at 100%, memory chart at max observed); axis labels and gridlines in `--muted`.
- HiDPI: canvas backing store scaled by `devicePixelRatio`.
- **Hover**: `mousemove` maps the cursor to a time bucket, highlights the group, and shows a tooltip (positioned DOM element, `textContent` only) listing each process with its CPU% or MiB value plus the sample time; `mouseleave` hides it. Tooltip also appears for keyboard users via `focus` + arrow-key bucket navigation on the canvas (`tabindex="0"`, `role="img"`, `aria-label` summarizing the latest sample).
- `@media (prefers-reduced-motion: reduce)`: no animated transitions (bars just redraw).

### 7b. Chat panel (`js/chat.js`)

- Conversation log: `<ul id="chat-log" aria-live="polite">`; one `<li>` per message with a role class (`user`/`assistant`), rendered with `textContent` only (never innerHTML of remote strings). Streaming replies render into a single assistant `<li>` that grows with each `chat-delta` and finalizes on `chat-done`. Auto-scroll to bottom unless the user has scrolled up.
- Input: `<form>` with a `<textarea>` (Enter submits, Shift+Enter inserts newline) and a submit `<button>`. Both disabled while a reply is streaming or the socket is not live. Client-side limit 4096 chars with a small counter.
- `chat-error` renders as a dismissible inline notice in the log (never lost, never blocks further input once the busy state clears).
- On connect, the panel is populated from `chat-history` before any live messages.

**`css/app.css`** — requirements:

- `@font-face` for `InterVariable` (`/fonts/InterVariable.woff2`, `font-weight: 100 900`, `font-display: swap`) and italic variant; body font stack `InterVariable, system-ui, sans-serif`.
- Design tokens on `:root` + dark override:

```css
:root {
  color-scheme: light dark;
  --bg:#f7f7f8; --surface:#ffffff; --fg:#191a20; --muted:#565a66;
  --accent:#4055c8; --ok:#177a4c; --err:#b03636; --border:#e2e3e8;
  --radius:10px; --gap:1rem; --maxw:64rem;
  --p1:#4055c8; --p2:#177a4c; --p3:#b03636; --p4:#8a6d1a;
  --p5:#1a7f8a; --p6:#7a4bb0; --p7:#b0567a; --p8:#566036;
}
@media (prefers-color-scheme: dark) {
  :root { --bg:#101014; --surface:#191a21; --fg:#ececf1; --muted:#9a9daa;
          --accent:#8fa0ff; --ok:#3fc584; --err:#f07878; --border:#2a2b33;
          --p1:#8fa0ff; --p2:#3fc584; --p3:#f07878; --p4:#d9b856;
          --p5:#5ac8d8; --p6:#b78fe8; --p7:#e88fb0; --p8:#a8b878; }
}
```

- Responsive: services grid `grid-template-columns: repeat(auto-fill, minmax(15rem, 1fr))`; charts stack vertically below 40rem; header collapses gracefully at 30rem; chat log height capped with internal scroll.
- a11y: text/background contrast ≥ 4.5:1 in both schemes (the tokens above satisfy this; verify if changed), visible `:focus-visible` outlines, `@media (prefers-reduced-motion: reduce)` disables the pill pulse animation.
- Fast loading: no images except the inline SVG sprite; total CSS (source, pre-minify) < 12 KB.

**`js/ws.js`** — `connect(onMessage, onStatus)` returns `{ send(obj) }`: opens `new WebSocket((location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/ws")`; on `message` parse JSON, call `onMessage`; `send` serializes and transmits only when the socket is open (returns false otherwise); on `close`/`error` schedule reconnect with exponential backoff 1 s → 2 → 4 … capped 30 s, reset on successful open; report `connecting/live/reconnecting` through `onStatus`.

**`js/render.js`** — pure DOM update functions: `renderState(snapshot)` (idempotent: updates existing nodes, no innerHTML of untrusted strings — use `textContent`), `renderStatus(status)`.

**`js/app.js`** — entry: wires everything; dispatches inbound messages by `type` (`state`/`history` → render + charts, `chat-*` → chat panel). Nothing else.

## 8. Web build: `scripts/build-web.sh` (+ fonts: `controller/tools/fetch-assets.sh`)

The embedded SPA is minified with external sourcemaps. esbuild is an **exact-pinned devDependency** in `package.json` (constitution rule 1 — tooling only; the npm package itself still has zero runtime dependencies and the CLI ships unbundled). Output goes to `controller/web/dist/` which is **gitignored** (constitution rule 3: no build artifacts in git) and rebuilt deterministically and offline by:

```bash
#!/usr/bin/env bash
# Minify the SPA from controller/web/static into controller/web/dist with
# external sourcemaps (sourcesContent inlined). Deterministic; no network.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

SRC=controller/web/static
DIST=controller/web/dist
ESBUILD=node_modules/.bin/esbuild

[[ -x "$ESBUILD" ]] || { echo "build-web: esbuild missing; run: npm install" >&2; exit 1; }
[[ -f "$SRC/fonts/InterVariable.woff2" && -f "$SRC/fonts/InterVariable-Italic.woff2" ]] \
  || { echo "build-web: fonts missing; run: bash controller/tools/fetch-assets.sh" >&2; exit 1; }

rm -rf "$DIST"
mkdir -p "$DIST/js" "$DIST/css" "$DIST/fonts"
"$ESBUILD" "$SRC/js/app.js" --bundle --minify --format=esm \
  --sourcemap --sources-content=true --outfile="$DIST/js/app.js"
"$ESBUILD" "$SRC/css/app.css" --minify \
  --sourcemap --sources-content=true --outfile="$DIST/css/app.css"
cp "$SRC/index.html" "$DIST/index.html"
cp "$SRC/fonts/"*.woff2 "$DIST/fonts/"
echo "build-web: OK"
```

- `--bundle` folds `ws.js`/`render.js`/`chart.js`/`chat.js` into one minified `app.js` (tree-shaken); `index.html` keeps referencing `/js/app.js` and `/css/app.css` unchanged.
- Sourcemaps: `app.js.map` / `app.css.map` sit beside the minified files with `sourcesContent` inlined, are embedded, and are served by the controller — DevTools debugging works against original sources with nothing extra committed.
- `package.json` gains the script `"build:web": "bash scripts/build-web.sh"`.
- Runs in: dev loop (via `npm run check`), CI (same), and the Dockerfile controller-build stage (spec 002 §4: the stage runs `npm ci` from the committed lockfile — integrity-hash pinned — then `bash scripts/build-web.sh` before `go build`).
- `scripts/check.sh` (spec 001 §6) runs `build-web.sh` inside the Go-gates block before `go vet`/`go test`, so a stale or missing `dist/` can never pass gates, and `//go:embed all:web/dist` makes a distless Go build fail outright.

Fonts remain exactly as before — pinned build-time download into `controller/web/static/fonts/` (not committed), copied into `dist/fonts/` by the build:

```bash
#!/usr/bin/env bash
# Fetch pinned web fonts into web/static/fonts (build-time; not committed).
# Ships only the two variable-font files actually used ("tree-shaken" assets).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

DEST="web/static/fonts"
URL="https://github.com/rsms/inter/releases/download/v4.1/Inter-4.1.zip"
SHA256="9883fdd4a49d4fb66bd8177ba6625ef9a64aa45899767dde3d36aa425756b11e"

if [[ -f "$DEST/InterVariable.woff2" && -f "$DEST/InterVariable-Italic.woff2" ]]; then
  echo "fetch-assets: fonts present"
  exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fsSL --retry 3 -o "$tmp/inter.zip" "$URL"
echo "$SHA256  $tmp/inter.zip" | sha256sum -c -
mkdir -p "$DEST"
unzip -q -j -o "$tmp/inter.zip" 'web/InterVariable.woff2' 'web/InterVariable-Italic.woff2' -d "$DEST"
echo "fetch-assets: OK"
```

Guardrails: `scripts/check.sh` fails with an actionable message if `index.html` exists but fonts are missing; `build-web.sh` fails likewise; `go:embed` fails the Go build if `dist/` is incomplete. A fontless or unminified binary cannot ship.

## 9. E2E test

**`test/e2e.sh`** (mode 755) — the full acceptance gate for specs 001–003, and the regression gate for the two failure classes previously seen in the field: image-tag drift between `build` and `start`, and stale per-boot state (e.g. Chromium singleton locks) on a reused data dir. It therefore drives the **real CLI**, not raw `docker`, and includes a restart cycle on the same data dir. It takes over the local `virtualme` container and port 8080.

**`test/chat-probe.mjs`** — small Node script (global `WebSocket`, Node ≥ 22; no deps) used by e2e:

- `node test/chat-probe.mjs <ws-url>`: connect, wait for `chat-history`, send `{"type":"chat","text":"Reply with the single word: pong"}`, exit 0 on the first `chat-delta` (timeout `CHAT_PROBE_TIMEOUT`, default 240 s → exit 1).
- `node test/chat-probe.mjs --history-only <ws-url>`: connect, exit 0 iff the `chat-history` received on connect contains at least one message.

```bash
#!/usr/bin/env bash
# Full E2E: CLI-driven lifecycle; health, minified SPA + sourcemaps, ws, desktop
# proxy, visible browser, streaming chat, and a restart cycle on the same data
# dir (catches image-tag drift and stale-profile-lock regressions).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

NAME="virtualme"
PORT=8080
TIMEOUT="${E2E_TIMEOUT:-300}"
BASE="http://127.0.0.1:${PORT}"
DATA_DIR="$(mktemp -d)"
export VIRTUALME_DATA="$DATA_DIR"

fail() {
  echo "e2e: FAIL: $*" >&2
  echo "--- container logs (tail) ---" >&2
  docker logs "$NAME" 2>&1 | tail -200 >&2 || true
  ./cli.sh stop >/dev/null 2>&1 || true
  rm -rf "$DATA_DIR"
  exit 1
}

wait_healthy() {
  local deadline=$(( $(date +%s) + TIMEOUT ))
  until curl -fsS "$BASE/healthz" 2>/dev/null | grep -q '"ok":true'; do
    if [ "$(date +%s)" -ge "$deadline" ]; then
      fail "healthz not green within ${TIMEOUT}s: $(curl -s "$BASE/healthz" || echo unreachable)"
    fi
    sleep 5
  done
}

echo "e2e: [1/9] CLI build (tags :dev and the start tag)"
./cli.sh build >/dev/null || fail "cli build"

./cli.sh stop >/dev/null 2>&1 || true
echo "e2e: [2/9] CLI start on fresh data dir ${DATA_DIR}"
./cli.sh start >/dev/null || fail "cli start"

echo "e2e: [3/9] waiting for all-green /healthz (timeout ${TIMEOUT}s)"
wait_healthy

echo "e2e: [4/9] orchestrator serves the minified SPA and sourcemaps"
code=$(curl -s -o /tmp/e2e-index.html -w '%{http_code}' "$BASE/")
[ "$code" = 200 ] || fail "GET / returned $code"
grep -q "Virtual Me" /tmp/e2e-index.html || fail "SPA markup missing from /"
curl -fsS "$BASE/js/app.js" | grep -q "sourceMappingURL" || fail "app.js missing sourcemap pointer"
curl -fsS -o /dev/null "$BASE/js/app.js.map" || fail "app.js.map not served"
curl -fsS -o /dev/null "$BASE/css/app.css.map" || fail "app.css.map not served"

echo "e2e: [5/9] websocket endpoint rejects non-upgrade with 400"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/ws")
[ "$code" = 400 ] || fail "GET /ws returned $code (expected 400)"

echo "e2e: [6/9] remote desktop (noVNC via reverse proxy) serves 2xx"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/desktop/vnc.html")
[ "$code" = 200 ] || fail "GET /desktop/vnc.html returned $code"

echo "e2e: [7/9] a browser window is visible on the virtual display"
docker exec -e DISPLAY=:99 "$NAME" xdotool search --onlyvisible --class chromium >/dev/null \
  || fail "no visible chromium window on :99"

echo "e2e: [8/9] chat round-trip streams at least one delta"
node test/chat-probe.mjs "ws://127.0.0.1:${PORT}/ws" || fail "chat probe"

echo "e2e: [9/9] restart cycle on the same data dir stays healthy, chat history survives"
./cli.sh stop >/dev/null || fail "cli stop"
./cli.sh start >/dev/null || fail "cli start (restart)"
wait_healthy
node test/chat-probe.mjs --history-only "ws://127.0.0.1:${PORT}/ws" \
  || fail "chat history lost across restart"

./cli.sh stop >/dev/null
rm -rf "$DATA_DIR"
echo "e2e: OK"
```

Manual verification (human checklist, not CI): run `./cli.sh build && ./cli.sh start`, open `http://localhost:8080` — services turn green as they come up, uptime ticks every 2 s (live websocket), both metrics charts stream grouped bars and show hover tooltips with per-process values, sending a chat message streams a reply token by token and the conversation survives a page reload, OS light/dark theme switches automatically, page is usable at phone width; click the desktop button and **see the Chromium window** through noVNC; `./cli.sh stop`.

## 10. Docs refresh (constitution rule 9)

Run the `/master-update` skill procedure. Expected changes:

- README: User's Guide endpoints table lists `/`, `/healthz`, `/ws` (state + chat protocol), `/desktop/`; development setup includes `bash controller/tools/fetch-assets.sh` and notes that `npm run check` builds the minified SPA; CI section notes the E2E gate including the restart cycle and chat probe.
- `operate` skill: endpoints section matches; mention the chat panel and metrics charts.
- `develop` skill: controller package map (health, procstat, state, ws, chat), web build pipeline (`scripts/build-web.sh`, gitignored `dist/`), and "how to add a controller endpoint" pointer.
- `AGENTS.md`: commands table gains `bash test/e2e.sh` and `npm run build:web`.

## 11. Acceptance checklist (run every item)

| # | Command / action | Expected |
|---|---|---|
| 1 | `cat controller/go.mod` | no `require` lines (zero Go deps) |
| 2 | `bash controller/tools/fetch-assets.sh` twice | downloads once, second run `fonts present` |
| 3 | `npm run check` | `check: OK` — includes `build-web`, gofmt/vet, and all Go package tests |
| 4 | `cd controller && go test ./... -count=1` | all pass: health probes, procstat parsers + sampler, RFC 6455 accept-key vector, handshake, push frame, on-connect, client-message dispatch, ping/pong, RESP client, chat SSE streaming/busy/error, proxy, proc parsers |
| 5 | `rm -rf controller/web/static/fonts && npm run check` | FAILS with the fetch-assets hint (guardrail works); re-run fetch-assets after |
| 6 | `rm -rf controller/web/dist && npm run check` | passes — `dist/` is regenerated by the gate itself |
| 7 | `git check-ignore controller/web/dist` | ignored (no build artifacts in git) |
| 8 | `test -f controller/web/dist/js/app.js.map && grep -q sourcesContent controller/web/dist/js/app.js.map` | exit 0 |
| 9 | Size budgets | `dist/js/app.js` < 24 KB, `dist/css/app.css` < 12 KB |
| 10 | `bash test/smoke.sh` | still `smoke: OK` (002 gate unbroken) |
| 11 | `bash test/e2e.sh` | `e2e: OK` — all nine numbered steps pass, including chat streaming and the restart cycle |
| 12 | Manual browser pass (§9 closing paragraph) | SPA live, charts stream + hover, chat streams + survives reload, theme auto-switch, mobile responsive, desktop shows Chromium |
| 13 | SPA sources | no external URLs (`grep -RE "https?://" controller/web/static/js controller/web/static/css` → only comments, ideally nothing) |
| 14 | Keyboard-only walkthrough of the SPA | all interactive elements reachable including the chat form and chart focus/arrow navigation, `:focus-visible` outlines present |
| 15 | CI: push branch | `check` and `container` (smoke + e2e) jobs green |
| 16 | `/master-update` run | §10 changes present |

Commit as `spec 003: Go control plane (ws + healthz + noVNC proxy), metrics + chat, embedded minified SPA`.

When this checklist is green, specs 001–003 are complete: `npx virtualme start` boots the whole universe, `http://localhost:8080` is the control plane, and the release workflow can publish the first `v0.1.0` tag.
