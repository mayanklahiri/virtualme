# Spec 005: Multi-Page Console UI, Persistent Metrics, Themes, and Chat Upgrades

| | |
|---|---|
| Status | Approved for execution |
| Depends on | `specs/001-constitution.md`–`specs/003-controller.md` executed (full controller + SPA live, e2e green) |
| Produces | Multi-page console SPA (Home / Status / Chat / Desktop, history-API routing, mobile offcanvas nav), five dynamic themes with pinned self-hosted fonts + Lucide icon sprite, persistent multi-resolution metrics store with selectable lookback, per-core CPU sampling, markdown chat with stop/clear/copy + conversation stats + fine-grained LLM status, agent-step UI hooks for spec 008, extended `fetch-assets.sh`/`build-web.sh`, e2e additions |
| Followed by | `specs/008-browser-agent.md` (produces the agent frames whose rendering is defined here) |

## 0. Executor instructions

- The constitution (`specs/001-constitution.md` §1) binds this spec. `controller/go.mod` must still have **zero `require` lines**; the SPA remains hand-written vanilla ESM with **no client framework and no runtime JS libraries** — the markdown renderer, router, theme engine, and charts are all in-repo.
- This spec **supersedes** (constitution rule 4 — superseding text here, prior specs untouched): spec 003 §7 (SPA structure: single page → multi-page console; the hand-drawn inline sprite → Lucide sprite), spec 003 §3's blanket `history` replay (→ per-lookback `metrics-req`/`metrics` exchange; the 150-snapshot ring is replaced by the tiered store in §3), spec 003 §7a chart rendering (grouped bars → area/line series; per-process CPU chart → per-core chart), and spec 003 §8 / §11 item 9 size budgets (new budgets in §10).
- It also supersedes one file defined by spec 002 §5: `svc-llama/run` gains `--slots` (§5d). `docker/rootfs/` is the fast-moving top of the image, not a numbered layer — no layer amendment needed.
- Trust model unchanged (constitution rule 8): no auth/TLS; the websocket accepts the client message types listed in §2 and nothing else.
- All pinned URLs and sha256 values in §9–§10 were fetched and hashed against the live sources on 2026-07-22. **Before use, re-verify each sha256** (`curl -fsSL <url> | sha256sum`). On mismatch STOP and report; never substitute silently.
- Stop-on-red per section; finish with the Acceptance Checklist (§13).

## 1. Page architecture and routing

Four pages under one embedded SPA, real URL paths (history API), server-side fallback:

| Path | Page | Content |
|---|---|---|
| `/` | Home | Friendly welcome: product name + one-paragraph description, overall health pill, uptime, quick links (cards) to Status / Chat / Desktop, current model name, version in footer |
| `/status` | Status | Service cards + stacked time-series charts (§7) with lookback selector + system meters |
| `/chat` | Chat | Full-page conversation (§5, §6, §8) with stats strip and LLM status subtext |
| `/desktop-view` | Desktop | noVNC embedded in an `<iframe src="/desktop/vnc.html?autoconnect=1&resize=scale&path=desktop/websockify">` filling the content area, plus a pop-out button opening the same URL in a new tab |

**Controller fallback** (`cmd/controller/main.go` / `newMux`): the embedded-static handler serves the requested file when it exists in `web/dist`; for any other **GET** path that does not begin with `/healthz`, `/ws`, `/desktop/` and has no file extension, it serves `dist/index.html` with status 200 (SPA fallback). Requests with a file extension that miss (e.g. `/js/nope.js`) still 404. Extend `main_test.go`: `/status` and `/desktop-view` return the index markup; `/js/nope.js` returns 404; existing route tests unchanged.

**Client router** (`js/router.js`): resolves `location.pathname` to a page, shows/hides the four `<section data-page>` elements (`hidden` attribute), sets `aria-current="page"` on the active nav link, intercepts same-origin `<a data-nav>` clicks via `pushState`, handles `popstate`. Unknown paths render Home. No hash routing.

**Navigation**: persistent left sidebar at ≥ 48rem viewport width (logo, four nav links with Lucide icons, theme picker in the footer slot, connection pill). Below 48rem the sidebar becomes an **offcanvas drawer**: hamburger button (`aria-expanded`, `aria-controls`) in a compact top bar; drawer slides in from the left (`transform: translateX`, 200ms ease-out) over a curtain overlay (`opacity` fade, click closes); body scroll locked while open; focus trapped inside the drawer (Tab cycles, Esc closes, focus returns to the hamburger); touch targets ≥ 44px. `@media (prefers-reduced-motion: reduce)` replaces the slide/fade with instant show/hide.

## 2. Websocket protocol (complete table after this spec)

Client → server (anything else: per-connection `chat-error`, ignored):

| Message | Meaning |
|---|---|
| `{"type":"chat","text":"…"}` | unchanged (spec 003 §6) |
| `{"type":"chat-stop"}` | cancel the in-flight generation (§5b) |
| `{"type":"chat-clear"}` | erase the shared conversation and stats (§5c) |
| `{"type":"metrics-req","lookback":"15m|1h|3h|12h|1d|3d|7d|30d"}` | request history for one lookback (§3); invalid lookback → `chat-error` |

Server → clients:

| Message | Scope | Meaning |
|---|---|---|
| `{"type":"state",…}` | broadcast, 2 s | as spec 003 §3, plus `"cores":[…]` (§4) |
| `{"type":"metrics","lookback":"…","resSec":N,"samples":[…]}` | per-connection reply | tiered history (§3); **replaces** spec 003's on-connect `history` replay — the client requests what it needs |
| `{"type":"chat-history",…}` / `chat-message` / `chat-delta` / `chat-done` / `chat-error` | as spec 003 §6 | `chat-done` gains optional `"stopped":true` (§5b) |
| `{"type":"chat-stats","queries":N,"promptTokens":N,"completionTokens":N,"genMs":N}` | per-connection on connect; broadcast after each `chat-done` and on `chat-clear` | conversation totals (§5e) |
| `{"type":"llm-status","phase":"idle\|sending\|queued\|processing\|generating","promptN":N,"promptTotal":N,"tokens":N,"tokPerSec":F,"elapsedMs":N}` | broadcast, ≤ 2/s while a reply is in flight, final `idle` after | fine-grained wait status (§5d) |
| `{"type":"agent-step",…}` / `{"type":"agent-status",…}` | broadcast | defined in §12; produced only by spec 008 |

## 3. `internal/metrics` — multi-resolution persistent store

New package replacing the state collector's flat 150-snapshot ring.

**Sample** (one point in every tier):

```go
type Sample struct {
    Ts         int64     `json:"ts"`         // unix millis (bucket end)
    Cores      []float64 `json:"cores"`      // per-core busy %, 0–100 each (§4)
    ProcMemMB  []int     `json:"procMemMB"`  // fixed order: xvfb, openbox, x11vnc,
                                             // novnc, valkey, llama, chromium, controller
    Load1      float64   `json:"load1"`
    MemUsedMB  int       `json:"memUsedMB"`
    MemTotalMB int       `json:"memTotalMB"`
}
```

**Tiers** — fixed, compile-time table. Every 2 s the state collector calls `Store.Add(sample)`; tier 0 appends directly; each coarser tier accumulates and emits the **arithmetic mean** of its window (element-wise over `Cores`/`ProcMemMB`) when the window closes:

| Tier | Resolution | Retention | Ring size | Serves lookbacks |
|---|---|---|---|---|
| 0 | 2 s | 1 h | 1800 | `15m`, `1h` |
| 1 | 30 s | 12 h | 1440 | `3h`, `12h` |
| 2 | 5 min | 7 d | 2016 | `1d`, `3d`, `7d` |
| 3 | 15 min | 30 d | 2880 | `30d` |

```go
func NewStore(dir string) *Store            // dir = $VM_DATA_DIR/metrics
func (s *Store) Add(sm Sample)              // mutex-guarded; feeds all tiers
func (s *Store) Query(lookback string) (resSec int, samples []Sample, ok bool)
                                             // picks the tier from the table, returns
                                             // only samples within the lookback window
func (s *Store) Load()                       // at startup: read tier files; missing or
                                             // corrupt file → empty tier, log once
func (s *Store) RunPersist(ctx context.Context)  // every 60s and on ctx cancel: write
                                             // each tier to dir/tierN.json atomically
                                             // (write tierN.json.tmp, rename)
```

- Persistence lives at **`$VM_DATA_DIR/metrics/tier{0,1,2,3}.json`** (JSON array of `Sample`, oldest first) — on the data mount, so history survives container replacement (this directory joins the spec 007 audit table). Worst case tier file ≈ 2880 samples ≈ low hundreds of KB; write amplification at 60 s cadence is negligible.
- `cont-init.d/10-data-dirs.sh` gains `"$VM_DATA_DIR/metrics"` in its `mkdir -p` list (rootfs change, same file spec 002 defined — superseded here).
- On boot after downtime, tiers simply have a gap: no backfill, no interpolation. The client renders gaps as breaks in the series (§7).
- The controller handles `metrics-req` by calling `Query` and replying per-connection with the `metrics` message (§2).

**`metrics_test.go`**: tier windowing (30 s tier emits the mean of 15 tier-0 samples; element-wise mean over `Cores` verified); ring caps at the stated sizes; `Query("15m")` returns only the trailing 15 minutes from tier 0 and `resSec` 2; round-trip `RunPersist` → new `Store` → `Load` reproduces samples; corrupt tier file → empty tier, no panic; unknown lookback → `ok == false`.

## 4. `internal/procstat` — per-core CPU sampling

Replace the aggregate per-process CPU chart source with per-core utilization (per-process **memory** sampling is unchanged and still charted):

```go
func (s *Sampler) Cores() []float64   // one entry per logical CPU, busy % of the
                                      // interval since the previous Cores() call
```

- Parse `/proc/stat` `cpu0…cpuN` lines (parametrized proc root, as before). Busy = Δ(total − idle − iowait) / Δtotal × 100, clamped to [0, 100]. First call returns zeros. Core count changes mid-run (never on real hardware) reset the baseline.
- `state.Snapshot` gains `Cores []float64 \`json:"cores"\`` and feeds `metrics.Sample`. `Processes` stays in the snapshot (service cards and the memory chart still use it).

**Tests**: fixture `/proc/stat` pairs with doctored counters → exact percentages; clamping; first-call zeros.

## 5. `internal/chat` — stop, clear, stats, LLM status

### 5a. Valkey client extension

`chat/valkey.go` gains a generic `do(args ...string) (reply, error)` used to implement `DEL`, `HGETALL`, and `HINCRBY` alongside the existing RPUSH/LTRIM/LRANGE (same RESP encoding, same per-op dial + 2 s deadline, same best-effort semantics). Keys: `virtualme:chat` (list, unchanged), `virtualme:chat-stats` (hash: `queries`, `promptTokens`, `completionTokens`, `genMs`).

### 5b. Stop generation

The in-flight llama request is issued with a cancellable `context.Context` held by the service. `chat-stop` while busy → cancel the context; the SSE loop exits; the partial assistant text collected so far is appended to memory + Valkey and broadcast as `chat-done` with `"stopped":true`; busy clears. `chat-stop` while idle → ignored silently. UI renders a stopped reply with a subtle "stopped" marker.

### 5c. Clear conversation

`chat-clear` (idle only; while busy → per-connection `chat-error` `"busy: stop generation first"`): clear the in-memory history, `DEL virtualme:chat`, `DEL virtualme:chat-stats`, then broadcast an empty `chat-history` and a zeroed `chat-stats` so every connected client resets.

### 5d. Fine-grained LLM status

- `docker/rootfs/etc/s6-overlay/s6-rc.d/svc-llama/run` gains `--slots` (exposes llama-server's `GET /slots`, localhost-only like everything else).
- While a completion is in flight the service runs a status loop (500 ms tick, stops at `chat-done`/error): phase transitions `sending` (request written) → `queued`/`processing` (from `/slots`: the slot's prompt-processing progress `promptN`/`promptTotal` while the prompt is being ingested; `queued` if no slot has picked the request up) → `generating` (first delta received; `tokens` = deltas counted controller-side, `tokPerSec` over a 5 s sliding window) — each tick broadcasts `llm-status`. After completion broadcast `phase:"idle"`. If `/slots` is unreachable, skip the queued/processing detail and go `sending` → `generating` (never fail the chat because status polling failed).
- The final SSE chunk from llama-server carries a `timings` object (`prompt_n`, `predicted_n`, `predicted_per_second`); parse it when present for §5e's authoritative token counts, falling back to controller-side counts.

### 5e. Conversation stats

After every `chat-done` (including stopped): `HINCRBY` the stats hash (`queries` +1, `promptTokens` + prompt_n, `completionTokens` + predicted_n, `genMs` + wall-clock generation time), read it back, broadcast `chat-stats`. On connect, send current stats after `chat-history`. Valkey down → stats are zeros, never an error.

**Tests** (extend `chat_test.go`, hermetic): stop mid-stream → partial persisted, `"stopped":true`, busy cleared; clear → empty history + zeroed stats broadcast, DELs issued (scripted fake Valkey asserts); status loop against a fake `/slots` server → phase sequence `sending, processing, generating, idle`; timings parsing feeds stats; `/slots` connection refused → chat still completes.

## 6. SPA structure (`controller/web/static/`)

```
web/static/
├── index.html            # shell: top bar, sidebar/offcanvas nav, four <section data-page>
├── css/app.css           # tokens for all themes (§9), layout, components
├── js/app.js             # entry: wiring + message dispatch only
├── js/router.js          # §1
├── js/nav.js             # offcanvas drawer behavior (§1)
├── js/theme.js           # §9: registry, apply, persist, picker
├── js/ws.js              # unchanged from spec 003 (+ metrics-req send helper)
├── js/render.js          # home/status/system rendering
├── js/chart.js           # §7
├── js/chat.js            # §5/§8 UI: log, form, stop/clear/copy, stats strip, llm status
├── js/markdown.js        # §8
├── js/agent.js           # §12: agent step timeline rendering
├── fonts/                # gitignored; filled by tools/fetch-assets.sh (8 families)
└── icons/                # gitignored; Lucide SVGs extracted by fetch-assets.sh
```

`index.html`: `<header>` compact top bar (hamburger < 48rem, page title, connection pill), `<nav>` sidebar/drawer, `<main>` with the four page sections, no inline hand-drawn sprite (replaced by the Lucide sprite file, §10). All rendering rules from spec 003 §7 that are not explicitly superseded (aria-live pill, `textContent` for remote strings outside the markdown path, `:focus-visible`, reduced-motion, contrast ≥ 4.5:1) still bind.

**Chat page controls**: send button (icon `send`), **stop** button (icon `square`, visible only while busy → sends `chat-stop`), **clear** button (icon `trash-2`, confirm via a two-tap pattern: first tap arms with "sure?", second within 3 s sends `chat-clear`), per-message **copy** button (icon `copy`, `navigator.clipboard.writeText` of the raw message text, check-icon feedback). **Stats strip** above the form: `N queries · N prompt + N completion tokens · N s thinking` from `chat-stats`. **LLM status subtext** under the form, driven by `llm-status`: `sending…`, `queued…`, `reading prompt (promptN/promptTotal)…`, `generating — N tokens · X tok/s · Ys`, empty when idle; `aria-live="polite"`.

## 7. Charts (`js/chart.js`)

Hand-written canvas 2D, no libraries. Two charts on the Status page, **stacked vertically, full content width**, in this order:

1. **CPU per core** — stacked area chart: one band per logical core (0–100 each, cumulative stack shows total machine utilization; y axis 0 → cores × 100 %). Legend `cpu0…cpuN`.
2. **Memory per process** — line chart, one series per process (8 series, MiB, auto-scaled y), same palette + legend behavior as before.

Shared behavior:

- **Lookback selector**: one segmented control above the charts — `15m 1h 3h 12h 1d 3d 7d 30d`, default `1h`, `role="radiogroup"` with arrow-key navigation. Selecting sends `metrics-req`; the reply replaces both charts' data (`resSec` drives the x scale). Persist the last selection to `localStorage` (`vm-lookback`) and re-request it on connect.
- **Live updates**: for `15m`/`1h` (tier 0) append live `state` snapshots directly; for coarser lookbacks re-send `metrics-req` every 30 s (cheap: single message, server-side slice).
- Rendering is area/line (grouped bars do not survive 1800+ points). Gaps in `ts` continuity (> 3 × `resSec`) render as breaks, not interpolated lines.
- Colors come from CSS custom properties (`--p1…--p8` for processes; core bands derive from `--accent` with stepped alpha) so all themes work (§9). HiDPI scaling, hover tooltip, and keyboard bucket navigation carry over from spec 003 §7a unchanged.

## 8. Markdown renderer (`js/markdown.js`)

Hand-rolled safe subset, applied to **assistant** messages only (user messages remain plain `textContent`). Output is built exclusively with `document.createElement` + `textContent` — there is no `innerHTML` sink anywhere, so the renderer is XSS-safe by construction.

Supported (nothing else; unrecognized syntax renders literally):

| Syntax | Output |
|---|---|
| `# ## ###` heading lines | `<h3>`/`<h4>`/`<h5>` (page headings stay above chat content) |
| `**bold**`, `*italic*` | `<strong>`, `<em>` |
| `` `inline code` `` | `<code>` |
| ```` ``` ```` fenced blocks | `<pre><code>`, content verbatim; copy button per block |
| `[text](url)` where url starts `http://` or `https://` | `<a rel="noopener noreferrer" target="_blank">`; any other scheme renders as literal text |
| `- ` / `* ` lists, `1. ` ordered lists | `<ul>`/`<ol>`/`<li>` (single level; nested markers render literally) |
| blank-line separated paragraphs | `<p>` |

Streaming: while deltas arrive, the in-flight assistant message renders as plain text (cheap); on `chat-done` the completed message re-renders through the markdown pipeline once. Contract test fixtures (documented in code comments; verified in the manual pass): `<img src=x onerror=…>` in model output appears as literal text; `[x](javascript:alert(1))` renders as literal text.

## 9. Themes

Five themes, each with light and dark variants. Selection state: `data-theme` (`modern|editorial|terminal|warm|contrast`) and `data-variant` (`light|dark`) attributes on `<html>`; a `variant` preference of `auto` (default) resolves via `prefers-color-scheme` and live-updates on scheme change. Persisted to `localStorage` (`vm-theme`, `vm-variant`); defaults `modern` + `auto`. Applied before first paint by a tiny inline script in `index.html` `<head>` (reads localStorage, sets the attributes) to prevent theme flash.

**Picker** (`js/theme.js`): in the sidebar/drawer footer — five swatch buttons (each shows the theme's accent + surface as a two-tone chip, `aria-pressed`, name label) plus a three-state variant toggle (auto / light / dark, icons `sun`/`moon`). Keyboard operable.

**Token sets** — all colors are CSS custom properties scoped by `[data-theme=…][data-variant=…]` selectors. Core tokens (executor verifies fg/bg contrast ≥ 4.5:1 — ≥ 7:1 for `contrast` — and may adjust any value minimally to pass; grounded against typography/palette guidance current 2026-07):

| Theme / variant | bg | surface | fg | muted | accent | ok | err | border |
|---|---|---|---|---|---|---|---|---|
| modern light | `#f7f7f8` | `#ffffff` | `#191a20` | `#565a66` | `#4055c8` | `#177a4c` | `#b03636` | `#e2e3e8` |
| modern dark | `#101014` | `#191a21` | `#ececf1` | `#9a9daa` | `#8fa0ff` | `#3fc584` | `#f07878` | `#2a2b33` |
| editorial light | `#faf6f0` | `#ffffff` | `#22201c` | `#5f594f` | `#8a3324` | `#3d6b35` | `#a03030` | `#e8e0d4` |
| editorial dark | `#17140f` | `#211d16` | `#ede7dc` | `#a89f90` | `#de9b6a` | `#8fbf7f` | `#e88f7f` | `#373126` |
| terminal light | `#f2f4f2` | `#ffffff` | `#1a201a` | `#4f5c4f` | `#146b3a` | `#146b3a` | `#a33333` | `#dce2dc` |
| terminal dark | `#0a0f0a` | `#101710` | `#c8e6c8` | `#7fa77f` | `#4fdf7f` | `#4fdf7f` | `#ff7f6f` | `#1f2f1f` |
| warm light | `#fdf8f3` | `#ffffff` | `#33302c` | `#6b6258` | `#b0500f` | `#2f855a` | `#c53030` | `#f0e6da` |
| warm dark | `#1c1713` | `#262019` | `#f2ebe3` | `#b3a698` | `#f6ad55` | `#68d391` | `#fc8181` | `#3d342a` |
| contrast light | `#ffffff` | `#ffffff` | `#000000` | `#333333` | `#0000d0` | `#006600` | `#cc0000` | `#000000` |
| contrast dark | `#000000` | `#000000` | `#ffffff` | `#cccccc` | `#ffff33` | `#33ff66` | `#ff5555` | `#ffffff` |

Chart palette `--p1…--p8`: `modern` keeps the spec 003 §7 values; for the other themes the executor picks 8 mutually distinguishable hues harmonized with the theme accent, each ≥ 3:1 contrast against `bg` in its variant.

Beyond colors, each theme also sets: `--radius` (modern 10px, editorial 4px, terminal 0, warm 16px, contrast 2px), `--motion` (transition duration token: terminal `0s`, contrast `0s`, warm `250ms`, others `150ms`; every CSS transition/animation uses it, and `prefers-reduced-motion: reduce` forces `0s` globally), and the font stacks below.

**Font pairings** (grounded 2026-07 against current typography-pairing guidance: Inter solo / Inter + JetBrains Mono as the modern-dashboard default; Fraunces display + Source Serif 4 body as the editorial pairing; JetBrains Mono solo for developer-terminal aesthetics; Nunito's rounded forms for warmth; Atkinson Hyperlegible designed for maximum legibility):

| Theme | Heading | Body | Mono |
|---|---|---|---|
| modern | InterVariable (600) | InterVariable (400) | JetBrains Mono |
| editorial | Fraunces | Source Serif 4 | JetBrains Mono |
| terminal | JetBrains Mono (700) | JetBrains Mono (400) | JetBrains Mono |
| warm | Nunito (700) | Nunito Sans (400) | JetBrains Mono |
| contrast | Atkinson Hyperlegible Next (700) | Atkinson Hyperlegible Next (400) | Atkinson Hyperlegible Mono |

All faces are declared as `@font-face` rules with `font-display: swap` in `app.css`; because browsers download only fonts referenced by rendered text, non-active theme fonts are **lazy-loaded by construction** — no JS font loader. Every stack ends in `system-ui, sans-serif` (or `monospace`).

## 10. Assets and build pipeline

### 10a. `controller/tools/fetch-assets.sh` — pinned additions

Keeps the existing Inter 4.1 zip block (URL + sha256 unchanged from spec 003 §8), and adds — every entry pinned by exact URL + sha256 (constitution rule 7), idempotent like the Inter block:

Latin-subset variable woff2 fonts (Google Fonts static CDN; content-addressed versioned URLs; hashed 2026-07-22):

| File (→ `web/static/fonts/`) | URL | sha256 | Size |
|---|---|---|---|
| `JetBrainsMono.woff2` | `https://fonts.gstatic.com/s/jetbrainsmono/v24/tDbV2o-flEEny0FZhsfKu5WU4xD7OwE.woff2` | `18be452724bfdc236c074ca94a249a7f41a86752c7d04ab258ce9ed5651f6a7e` | 40 KB |
| `Fraunces.woff2` | `https://fonts.gstatic.com/s/fraunces/v38/6NU78FyLNQOQZAnv9bYEvDiIdE9Ea92uemAk_WBq8U_9v0c2Wa0KxC9TeA.woff2` | `7234ed860a9cc83045413c4faee63c960a8f2d1917adcf728119307d56e0d783` | 67 KB |
| `SourceSerif4.woff2` | `https://fonts.gstatic.com/s/sourceserif4/v14/vEFI2_tTDB4M7-auWDN0ahZJW1gb8tc.woff2` | `f2ea9c12d2fe9bd3a9589b02ad2c0909da88f30938c91adc838c4f4098f9f9e0` | 122 KB |
| `Nunito.woff2` | `https://fonts.gstatic.com/s/nunito/v32/XRXV3I6Li01BKofINeaB.woff2` | `ba344451eab25b217a165363b1982048a5e5830a0daf36577973955a04cac793` | 39 KB |
| `NunitoSans.woff2` | `https://fonts.gstatic.com/s/nunitosans/v19/pe0AMImSLYBIv1o4X1M8ce2xCx3yop4tQpF_MeTm0lfUVwoNnq4CLz0_kJ3xzA.woff2` | `c9746ad68c8a1a94e9d8981ae5093fd4df05b6809e8b5afecd08df1a3bdb7e62` | 50 KB |
| `AtkinsonHyperlegibleNext.woff2` | `https://fonts.gstatic.com/s/atkinsonhyperlegiblenext/v7/NaPNcYPdHfdVxJw0IfIP0lvYFqijb-UxCtm5_wdGseiJn3o.woff2` | `18b2a1a39a2fa298b0ba5390aca68462669826c90925656f1c1f6796e0e1bbaf` | 34 KB |
| `AtkinsonHyperlegibleMono.woff2` | `https://fonts.gstatic.com/s/atkinsonhyperlegiblemono/v8/tss4AoFBci4C4gvhPXrt3wjT1MqSzhA4t7IIcncBiwKthFw.woff2` | `2706b1ee4f452e744ea91f7e4908cbde9c5d35521bf5ffffc71a382a2de89613` | 18 KB |

Icons (one zip, extracted selectively — "tree-shaken" assets like the Inter block):

| Artifact | URL | sha256 |
|---|---|---|
| Lucide `1.26.0` SVG zip | `https://github.com/lucide-icons/lucide/releases/download/1.26.0/lucide-icons-1.26.0.zip` | `7b3c98ebbd473db33057f75fd67076957ba59d7a9ccd2098d3754800fe533e84` |

Extract **only** this fixed icon list into `web/static/icons/` (the list is the single source of truth, defined once in `fetch-assets.sh`): `house`, `activity`, `message-circle`, `monitor`, `menu`, `x`, `sun`, `moon`, `palette`, `send`, `square`, `trash-2`, `copy`, `check`, `external-link`, `triangle-alert`, `github`, `bot`, `terminal`, `chevron-down`.

### 10b. `scripts/build-icons.mjs` + `scripts/build-web.sh` changes

- New `scripts/build-icons.mjs` (Node built-ins only): reads every SVG in `web/static/icons/`, converts each to `<symbol id="i-<name>" viewBox="0 0 24 24">…</symbol>` (strip the outer `<svg>` attributes, keep stroke geometry), concatenates into a single `<svg xmlns="…">` sprite, writes `controller/web/dist/icons.svg`. Deterministic (sorted by name), offline.
- `build-web.sh`: guard now requires all eight font files and a non-empty `icons/` dir (same actionable fetch-assets hint); copies all `fonts/*.woff2`; invokes `node scripts/build-icons.mjs`. Markup references icons as `<svg><use href="/icons.svg#i-house"/></svg>`; Lucide stroke styling (`stroke: currentColor`, `stroke-width` themable via a `--icon-stroke` token — terminal 2.5, warm 2, others 2) is applied in CSS.
- `embed.go` continues to embed `all:web/dist`; nothing else changes in the Go build.

### 10c. Size budgets (supersede spec 003 §11 item 9)

| Artifact | Budget |
|---|---|
| `dist/js/app.js` (minified) | < 48 KB |
| `dist/css/app.css` (minified) | < 24 KB |
| `dist/fonts/` total | < 1.2 MB (Inter pair ≈ 700 KB + 7 latin-subset files ≈ 370 KB) |
| `dist/icons.svg` | < 24 KB |

## 11. E2E additions (`test/e2e.sh` + probes)

Append numbered steps (renumber the `[n/N]` labels; existing steps unchanged):

- **SPA fallback**: `curl -s $BASE/status` and `curl -s $BASE/desktop-view` both return 200 with the index markup (`grep -q "Virtual Me"`); `curl -s -o /dev/null -w '%{http_code}' $BASE/js/nope.js` returns 404.
- **Metrics round-trip**: new `test/metrics-probe.mjs` (global `WebSocket`, no deps): connect, send `{"type":"metrics-req","lookback":"1h"}`, exit 0 on a `metrics` reply with `resSec === 2` and an array `samples` field (timeout 30 s). Also send lookback `"30d"` and require a `metrics` reply with `resSec === 900`.
- **Metrics persist across restart**: in the existing restart-cycle step, after the restart also assert `[ -f "$DATA_DIR/metrics/tier0.json" ]` and re-run `metrics-probe` expecting a non-empty `samples` array (history survived).
- **Chat stop**: extend `test/chat-probe.mjs` with a `--stop` mode: send a chat message, on first `chat-delta` send `{"type":"chat-stop"}`, exit 0 when `chat-done` with `"stopped":true` arrives within 30 s.

Manual verification (human checklist): phone-width offcanvas drawer slides with curtain, focus is trapped, Esc closes; all four pages navigate without reload and survive a hard refresh on `/status`; lookback switch repaints both charts and survives reload; charts are stacked vertically and the CPU chart shows one band per core; markdown renders in assistant replies (code block + copy button; hostile `<img onerror>` fixture stays literal); stop button truncates a long reply; clear empties the conversation on two browser tabs at once; stats strip counts up; each of the 10 theme variants applies (fonts, radius, motion, chart colors) and the selection survives reload; `contrast` theme passes a spot contrast check.

## 12. Agent-step UI hooks (consumed by spec 008)

Frame shapes and rendering are defined here; only spec 008's agent loop produces them. Until spec 008 executes, these frames simply never arrive.

| Message | Fields |
|---|---|
| `{"type":"agent-step",…}` | `taskId` (string), `n` (1-based step number), `tool` (string), `args` (object, pre-truncated by the server to ≤ 2 KB), `summary` (one-line human description), `screenshot` (optional data-URI JPEG thumbnail ≤ 32 KB), `ts` |
| `{"type":"agent-status","phase":"planning\|acting\|observing\|done\|failed\|stopped","taskId":"…","n":N}` | drives the chat status subtext while an agent task runs (same slot as `llm-status`) |

`js/agent.js` renders each `agent-step` into the chat log as a collapsible entry (`<details>`: summary line = step number + tool icon (`terminal` for bash, `monitor` for browser tools, `bot` otherwise) + `summary`; body = `args` pretty-printed via `textContent` + the thumbnail `<img>` when present). Steps of one `taskId` group under the triggering user message. The stop button (§6) also terminates agent tasks (spec 008 wires `chat-stop` to the agent loop).

## 13. Docs refresh (constitution rule 9)

Run the `/master-update` skill procedure. Expected changes:

- README: endpoints/pages table (four routes + fallback semantics); lookback + themes described in one sentence each; data-directory section gains `metrics/`.
- `operate` skill: endpoints section lists the four pages; troubleshooting note that metrics history lives in `~/.virtualme/metrics/`.
- `develop` skill: controller package map gains `metrics`; web build pipeline notes `build-icons.mjs` and the expanded `fetch-assets.sh`.
- `AGENTS.md`: no command changes expected.

## 14. Acceptance checklist (run every item)

| # | Command / action | Expected |
|---|---|---|
| 1 | `cat controller/go.mod` | still no `require` lines |
| 2 | `bash controller/tools/fetch-assets.sh` twice | first run downloads Inter zip + 7 woff2 + Lucide zip (each sha256-verified); second run says assets present |
| 3 | `npm run check` | `check: OK` — includes build-web with icons + all new Go package tests |
| 4 | `cd controller && go test ./... -count=1` | metrics tiering/persistence, per-core sampler, chat stop/clear/stats/status, SPA-fallback routing tests all pass |
| 5 | Size budgets (§10c) | all four met |
| 6 | `grep -RE "https?://" controller/web/static/js controller/web/static/css` | no external URLs (all assets same-origin) |
| 7 | `grep -R "innerHTML" controller/web/static/js` | no matches (markdown renderer is DOM-built) |
| 8 | `bash test/smoke.sh` | `smoke: OK` |
| 9 | `bash test/e2e.sh` | `e2e: OK` including the §11 steps |
| 10 | Manual pass (§11 closing paragraph) | all items |
| 11 | Keyboard-only walkthrough | drawer, nav, lookback radiogroup, theme picker, chat controls all operable; focus visible |
| 12 | `/master-update` run | §13 changes present |

Commit as `spec 005: multi-page console, persistent tiered metrics, themes, markdown chat, LLM status`.
