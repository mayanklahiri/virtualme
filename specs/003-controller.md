# Spec 003: Go Master Controller and Control-Plane SPA

| | |
|---|---|
| Status | Approved for execution |
| Depends on | `specs/001-constitution.md` and `specs/002-container.md` executed (container builds, smoke test green) |
| Produces | Full Go controller (health API, websocket state channel, noVNC reverse proxy, embedded SPA), vanilla-JS view-only SPA, `controller/tools/fetch-assets.sh`, `test/e2e.sh` |
| Followed by | Future specs (agent loop, Playwright task runner, auth) |

## 0. Executor instructions

- The constitution (`specs/001-constitution.md` §1) binds this spec. Zero third-party Go dependencies: `go.mod` must never gain a `require` block. The websocket layer is implemented in-repo (§4).
- The stub from spec 002 (`controller/cmd/controller/main.go`) is **refactored into packages**, not thrown away: its probe functions and the `/healthz` contract move to `internal/health` unchanged in semantics.
- Stop-on-red per section; finish with the Acceptance Checklist (§9). The E2E script is the final gate for specs 001–003 combined.
- Trust model (constitution rule 8): no auth, no TLS, view-only UI. The websocket is server-push only; ignore all client data frames.

## 1. Controller architecture

```
controller/
├── go.mod                        # module github.com/mayanklahiri/virtualme/controller, go 1.26, NO requires
├── cmd/controller/main.go        # wiring only (~60 lines)
├── internal/
│   ├── health/health.go          # probes + aggregate (from spec 002 stub)
│   ├── health/health_test.go
│   ├── ws/ws.go                  # minimal RFC 6455 server + hub
│   ├── ws/ws_test.go
│   ├── state/state.go            # periodic snapshot collector + broadcaster
│   └── state/state_test.go
├── web/static/                   # SPA, embedded via go:embed
│   ├── index.html
│   ├── css/app.css
│   ├── js/app.js  js/ws.js  js/render.js
│   └── fonts/                    # gitignored; filled by tools/fetch-assets.sh
└── tools/fetch-assets.sh         # pinned font download (build-time)
```

**`cmd/controller/main.go`** responsibilities (exact route table):

| Route | Handler |
|---|---|
| `/` (and all static paths) | `http.FileServer` over `fs.Sub(webFS, "web/static")` from `//go:embed all:web/static` |
| `/healthz` | JSON aggregate from `health.Gather` — same contract as spec 002 §6 (200 + `"ok":true` iff all six probes pass, else 503) |
| `/ws` | `hub.HandleUpgrade` (§4); non-upgrade requests get 400 |
| `/desktop/` | `http.StripPrefix("/desktop/", httputil.NewSingleHostReverseProxy(http://127.0.0.1:6080))` — stdlib ReverseProxy passes the `Connection: Upgrade` websocket through to websockify, so noVNC works end-to-end through port 8080 |

Startup: build `health.Config` from env (same vars/defaults as spec 002 §4), start `state.Collector` (2 s period) feeding the ws hub, `http.ListenAndServe(VM_HTTP_ADDR, mux)`. Log one line per subsystem to stdout.

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
}
func ReadSystem(loadavg, meminfo string) System  // pure parser, tested on fixtures
func NewCollector(cfg health.Config, broadcast func([]byte)) *Collector
func (c *Collector) Run(ctx context.Context)     // every 2s: Gather + ReadSystem
                                                 // (/proc/loadavg, /proc/meminfo),
                                                 // marshal Snapshot, broadcast
```

`ReadSystem` parses field 1 of `/proc/loadavg` and `MemTotal:`/`MemAvailable:` from `/proc/meminfo` (`used = total - available`), tolerating missing files (zeros) so the controller also runs on dev machines/macOS Docker hosts.

**`state_test.go`**: fixture strings for both proc files → exact `System` values; `Snapshot` marshals with `"type":"state"` first-class fields as above.

## 4. `internal/ws` — minimal RFC 6455 server

No third-party code. Scope: server-side accept, unfragmented text frames server→client, ping/pong, close. Everything else (fragmentation, extensions, compression, client role) is intentionally unsupported.

```go
type Conn struct{ /* net.Conn + bufio.ReadWriter + write mutex */ }
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error)
func (c *Conn) WriteText(p []byte) error
func (c *Conn) ReadLoop()   // service control frames until close/error
func (c *Conn) Close() error

type Hub struct{ /* mutex + map[*Conn]struct{} */ }
func NewHub() *Hub
func (h *Hub) HandleUpgrade(w http.ResponseWriter, r *http.Request)  // Upgrade, register,
                                                                     // go ReadLoop, unregister on exit
func (h *Hub) Broadcast(p []byte)  // WriteText to every conn; drop+close conns that error
func (h *Hub) Count() int
```

Implementation requirements (exact):

1. **Handshake** (`Upgrade`): require method GET; `Upgrade` header containing `websocket` (case-insensitive); `Connection` header containing `upgrade`; `Sec-WebSocket-Version: 13`; non-empty `Sec-WebSocket-Key`. On violation: `http.Error(w, ..., 400)` and error return. Compute accept key = `base64.StdEncoding(sha1(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))` (`crypto/sha1` is fine here; not used for security). Then `http.Hijacker.Hijack()` and write raw:
   `HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: <accept>\r\n\r\n`
2. **Write frames** (server→client, never masked): byte0 `0x81` (FIN + text) — or `0x8A` pong, `0x88` close; byte1 length: `<126` direct, `<=65535` `126` + uint16 BE, else `127` + uint64 BE; then payload. Serialize all writes with a mutex.
3. **Read loop**: parse byte0/byte1; client frames MUST have the mask bit set — if not, close with code 1002. Read 7/16/64-bit length (cap payload at 64 KiB, else close 1009), 4-byte mask key, payload; unmask with XOR. Dispatch: opcode `0x8` close → echo close (code 1000) and return; `0x9` ping → reply pong with same payload; `0xA` pong → ignore; `0x1`/`0x2` data → **discard** (view-only UI; do not process client data). Unknown opcode → close 1002.
4. **Close**: send close frame (code 1000, empty reason), then `net.Conn.Close()`. Idempotent via `sync.Once`.

**`ws_test.go`** (raw-socket client; no libraries):

- Accept-key vector from RFC 6455 §1.3: key `dGhlIHNhbXBsZSBub25jZQ==` → accept `s3pPLMBiTxaQ9kYGzzhZRbK+xOo=` (unit-test the key function directly).
- Handshake: `httptest.Server` whose handler upgrades and registers with a hub; test dials `net.Dial("tcp", ...)`, writes a raw GET with the four headers, reads the 101 response, asserts the accept header.
- Server push: after handshake, `hub.Broadcast([]byte("hi"))`; client reads raw bytes `0x81 0x02 'h' 'i'`.
- Ping/pong: client sends a **masked** ping frame with payload `ab`; expects pong `0x8A 0x02 'a' 'b'`.
- Bad handshake: plain GET to `/ws` without upgrade headers → HTTP 400 (this exact behavior is also asserted by `test/e2e.sh`).

## 5. SPA (`controller/web/static/`)

View-only, dark/light auto, responsive, accessible, zero external requests (all assets same-origin, embedded in the binary). No framework, no bundler; "tree-shaken" by construction — only hand-written modules ship.

**`index.html`** — structure (executor writes real markup following this exactly):

- `<!doctype html><html lang="en">`, `<meta charset>`, `<meta name="viewport" content="width=device-width, initial-scale=1">`, `<title>Virtual Me</title>`, `<link rel="stylesheet" href="/css/app.css">`, `<script type="module" src="/js/app.js"></script>`.
- Inline SVG sprite (`<svg hidden><symbol id="i-display">…`) with six simple 24×24 stroke icons drawn from basic shapes only (rect/circle/line/path): `i-display` (xvfb), `i-eye` (x11vnc/novnc), `i-globe` (chromium), `i-db` (valkey), `i-chip` (llama), `i-gauge` (system).
- Landmarks: `<header>` (product name `<h1>Virtual Me</h1>` + connection pill), `<main>` (three sections below), `<footer>` (version + link to GitHub repo).
  - `#status` section: overall OK banner + uptime.
  - `#services` section: `<ul>` of cards, one per service — icon, name, green/red status dot (`aria-label="healthy"/"unhealthy"`), detail text when unhealthy.
  - `#system` section: load and memory bars (`<meter>` or div bars with `role="meter"`, `aria-valuenow`).
  - `#desktop` section: prominent link (styled as button) to `/desktop/vnc.html?autoconnect=1&resize=scale&path=desktop/websockify` opening in a new tab — the remote desktop through the single exposed port.
- Connection pill has `aria-live="polite"` and cycles `connecting… / live / reconnecting…`.

**`css/app.css`** — requirements:

- `@font-face` for `InterVariable` (`/fonts/InterVariable.woff2`, `font-weight: 100 900`, `font-display: swap`) and italic variant; body font stack `InterVariable, system-ui, sans-serif`.
- Design tokens on `:root` + dark override:

```css
:root {
  color-scheme: light dark;
  --bg:#f7f7f8; --surface:#ffffff; --fg:#191a20; --muted:#565a66;
  --accent:#4055c8; --ok:#177a4c; --err:#b03636; --border:#e2e3e8;
  --radius:10px; --gap:1rem; --maxw:64rem;
}
@media (prefers-color-scheme: dark) {
  :root { --bg:#101014; --surface:#191a21; --fg:#ececf1; --muted:#9a9daa;
          --accent:#8fa0ff; --ok:#3fc584; --err:#f07878; --border:#2a2b33; }
}
```

- Responsive: services grid `grid-template-columns: repeat(auto-fill, minmax(15rem, 1fr))`; header collapses gracefully at 30rem.
- a11y: text/background contrast ≥ 4.5:1 in both schemes (the tokens above satisfy this; verify if changed), visible `:focus-visible` outlines, `@media (prefers-reduced-motion: reduce)` disables the pill pulse animation.
- Fast loading: no images except the inline SVG sprite; total CSS < 10 KB.

**`js/ws.js`** — `connect(onMessage, onStatus)`: opens `new WebSocket((location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/ws")`; on `message` parse JSON, call `onMessage`; on `close`/`error` schedule reconnect with exponential backoff 1 s → 2 → 4 … capped 30 s, reset on successful open; report `connecting/live/reconnecting` through `onStatus`.

**`js/render.js`** — pure DOM update functions: `renderState(snapshot)` (idempotent: updates existing nodes, no innerHTML of untrusted strings — use `textContent`), `renderStatus(status)`.

**`js/app.js`** — entry: wires `connect(renderState, renderStatus)`. Nothing else.

Server-side controlled: the SPA renders exclusively what arrives on the websocket (`Snapshot` §3); it holds no state of its own beyond the last snapshot.

## 6. Fonts: `controller/tools/fetch-assets.sh`

Pinned build-time download (constitution rule 7); fonts are **not** committed (`.gitignore` already covers `controller/web/static/fonts/`). Runs in: dev setup (once), CI `check` job, and the Dockerfile controller-build stage (spec 002 already has the hook: `if [ -f tools/fetch-assets.sh ]`).

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

Guardrails already in place from spec 001: `scripts/check.sh` fails with an actionable message if `index.html` exists but fonts are missing; `go:embed` makes the Go build itself fail if the directory is incomplete, so a fontless binary cannot ship.

## 7. E2E test

**`test/e2e.sh`** (mode 755) — the full acceptance gate for specs 001–003. The CI `container` job step from spec 001 activates automatically.

```bash
#!/usr/bin/env bash
# Full E2E: container builds and goes healthy; master orchestrator answers 2xx;
# remote desktop is reachable through port 8080; a browser is visible on the
# virtual display.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

IMAGE_TAG="virtualme:e2e"
NAME="virtualme-e2e"
PORT="${E2E_PORT:-18081}"
TIMEOUT="${E2E_TIMEOUT:-300}"
BASE="http://127.0.0.1:${PORT}"

fail() {
  echo "e2e: FAIL: $*" >&2
  echo "--- container logs (tail) ---" >&2
  docker logs "$NAME" 2>&1 | tail -200 >&2 || true
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  exit 1
}

echo "e2e: building image"
docker build -f docker/Dockerfile -t "$IMAGE_TAG" . || { echo "e2e: FAIL: build" >&2; exit 1; }

docker rm -f "$NAME" >/dev/null 2>&1 || true
echo "e2e: starting container"
docker run -d --name "$NAME" --shm-size=1g -p "${PORT}:8080" "$IMAGE_TAG" >/dev/null

echo "e2e: [1/5] waiting for all-green /healthz (timeout ${TIMEOUT}s)"
deadline=$(( $(date +%s) + TIMEOUT ))
until curl -fsS "$BASE/healthz" 2>/dev/null | grep -q '"ok":true'; do
  if [ "$(date +%s)" -ge "$deadline" ]; then
    fail "healthz not green within ${TIMEOUT}s: $(curl -s "$BASE/healthz" || echo unreachable)"
  fi
  sleep 5
done

echo "e2e: [2/5] orchestrator serves the SPA with 2xx"
code=$(curl -s -o /tmp/e2e-index.html -w '%{http_code}' "$BASE/")
[ "$code" = 200 ] || fail "GET / returned $code"
grep -q "Virtual Me" /tmp/e2e-index.html || fail "SPA markup missing from /"

echo "e2e: [3/5] websocket endpoint rejects non-upgrade with 400"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/ws")
[ "$code" = 400 ] || fail "GET /ws returned $code (expected 400)"

echo "e2e: [4/5] remote desktop (noVNC via reverse proxy) serves 2xx"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/desktop/vnc.html")
[ "$code" = 200 ] || fail "GET /desktop/vnc.html returned $code"

echo "e2e: [5/5] a browser window is visible on the virtual display"
docker exec -e DISPLAY=:99 "$NAME" xdotool search --onlyvisible --class chromium >/dev/null \
  || fail "no visible chromium window on :99"

docker rm -f "$NAME" >/dev/null
echo "e2e: OK"
```

Manual verification (human checklist, not CI): run `./cli.sh build && VIRTUALME_IMAGE=virtualme VIRTUALME_TAG=dev ./cli.sh start`, open `http://localhost:8080` — services turn green as they come up, uptime ticks every 2 s (live websocket), OS light/dark theme switches automatically, page is usable at phone width; click the desktop button and **see the Chromium window** through noVNC; `./cli.sh stop`.

## 8. Docs refresh (constitution rule 9)

Run the `/master-update` skill procedure. Expected changes:

- README: remove remaining `*(available after spec 003)*` markers; User's Guide endpoints table lists `/`, `/healthz`, `/ws`, `/desktop/`; development setup includes `bash controller/tools/fetch-assets.sh`; CI section notes the E2E gate.
- `operate` skill: endpoints section already matches — verify.
- `develop` skill: controller package map and "how to add a controller endpoint" pointer.
- `AGENTS.md`: commands table gains `bash test/e2e.sh`.

## 9. Acceptance checklist (run every item)

| # | Command / action | Expected |
|---|---|---|
| 1 | `cat controller/go.mod` | no `require` lines (zero Go deps) |
| 2 | `bash controller/tools/fetch-assets.sh` twice | downloads once, second run `fonts present` |
| 3 | `npm run check` | `check: OK` — includes gofmt/vet and all Go package tests |
| 4 | `cd controller && go test ./... -count=1` | all pass: health probes, RFC 6455 accept-key vector, handshake, push frame, ping/pong, proxy, proc parsers |
| 5 | `rm -rf controller/web/static/fonts && npm run check` | FAILS with the fetch-assets hint (guardrail works); re-run fetch-assets after |
| 6 | `bash test/smoke.sh` | still `smoke: OK` (002 gate unbroken) |
| 7 | `bash test/e2e.sh` | `e2e: OK` — all five numbered steps pass |
| 8 | Manual browser pass (§7 closing paragraph) | SPA live, theme auto-switch, mobile responsive, desktop shows Chromium |
| 9 | SPA sources | no external URLs (`grep -RE "https?://" controller/web/static/js controller/web/static/css` → only comments, ideally nothing) |
| 10 | Keyboard-only walkthrough of the SPA | all interactive elements reachable, `:focus-visible` outlines present |
| 11 | CI: push branch | `check` and `container` (smoke + e2e) jobs green |
| 12 | `/master-update` run | §8 changes present; no residual "available after spec" markers anywhere |

Commit as `spec 003: Go control plane (ws + healthz + noVNC proxy) and embedded view-only SPA`.

When this checklist is green, specs 001–003 are complete: `npx virtualme start` boots the whole universe, `http://localhost:8080` is the control plane, and the release workflow can publish the first `v0.1.0` tag.
