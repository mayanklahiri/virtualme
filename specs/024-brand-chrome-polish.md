# Spec 024: Brand, Window Chrome, and Console Polish

| | |
|---|---|
| Status | Executed (2026-07-24) |
| Depends on | `specs/011-ui-refresh.md` (brand, themes, home page), `specs/018-gpu-observability.md` (GPU data hooks), `specs/019-chart-overhaul.md` (chart polish is there, not here) |
| Produces | Home-page layout/alignment fixes, server IP:port facts, GPU line, a more assertive quick-link bar, copy scrubbed of em-dashes and AI cliches; a redesigned Virtual Me wordmark grounded in typographic practice; a wristwatch-style live-connection infographic replacing the plain "live" pill; per-tab visual fixes from the 2026-07-23 live inspection |
| Followed by | Future specs |

## 0. Executor instructions

- Constitution binds. All fonts must come from the already-pinned set in `controller/tools/fetch-assets.sh` (Inter, Space Grotesk, JetBrains Mono, Fraunces, Source Serif 4, Nunito, Atkinson Hyperlegible Next/Mono). Adding a different font requires extending that script with a pinned sha256 — allowed, but prefer the existing set.
- Copy blocks in §3 are verbatim. The scrub rule is absolute: **no em-dash (—) anywhere in SPA-visible copy** (code comments exempt), and none of the banned phrases in §3.3.
- Verify every change across eight themes × light/dark and at 1600/768/375 px widths.
- Stop-on-red; finish with §8 Acceptance.

## 1. Home page structure

Current `index.html` hero packs the health pill and all stats into one wrapping `.home-facts` row inside the hero copy; the live inspection showed misaligned columns. Restructure `[data-page="home"]`:

1. **Order**: hero (copy + image) → **status band** → quick links → footer.
2. **Status band** (new, replaces `.home-facts`): a full-width strip below the hero. First row: the health pill (`#home-health`, unchanged semantics) alone, left-aligned. Second row: a definition grid, all cells top-aligned on one baseline (`display:grid; grid-template-columns:repeat(auto-fit,minmax(9rem,1fr)); gap:var(--gap)`), each cell `<div class="fact"><dt>Host</dt><dd id="home-host">…</dd></div>` style — label 0.7 rem uppercase `--muted`, value 0.95 rem `--fg` `--font-mono` where numeric. Cells, in order: Host, **Address** (§1.3), Uptime, CPU, Memory, Disk, **GPU** (hidden unless present, hook from spec 018), Model. This is the "stats below all-systems-operational" alignment fix: pill on its own line, stats in a true grid beneath it.
3. **Address cell (IP and port)**: server-side, extend the `state` snapshot (`controller/internal/state`) with `"net": {"port": 8080, "addrs": ["192.168.1.20"]}` — port parsed from `VM_HTTP_ADDR`, addrs from `net.Interfaces()` filtering loopback/link-local, IPv4 first, cap 2. `render.js` renders `host:port` for the first addr with a `title` listing the rest. (These are container-namespace addresses when Docker uses bridge networking; additionally show `location.host` as the reachable address the browser actually used: value = `location.host`, sub-line = container addrs, labelled `container:`. Both matter for a LAN operator.)
4. **Quick-link bar pizazz** (restrained): icons grow to 28 px with `stroke-width: var(--icon-stroke)`; each card gets an `--accent`-tinted icon chip (36 px rounded square, `color-mix(in srgb, var(--accent) 12%, transparent)` background); hover lifts the card 2 px with a soft shadow and tints the border `--accent` (all transitions gated on `--motion` and `prefers-reduced-motion: reduce`). Cards for Projects/Jobs/Tools added by specs 014/015/021 inherit this automatically.
5. **Mobile fix** (live inspection): quick-link cards render too narrow at 375 px; make the container `grid-template-columns: 1fr` under 48 rem with cards full-width and comfortable padding (min 44 px tap targets).

## 2. Footer version truth

`index.html` footer hardcodes `Virtual Me v0.1.0`. Render from data: add `"version"` to the `state` snapshot (from a `controller` build constant set via `-ldflags` in `docker/Dockerfile`'s build stage, default `dev`); `render.js` fills `#home-version`. Fallback text while disconnected: `Virtual Me`.

## 3. Copy scrub

1. **Hero** replacement (removes the em-dash and the "watching the desk so you don't have to" cliche):
   - eyebrow: `Private background agent`
   - h1: `Good to see you.` (kept: short, human, not a cliche)
   - body: `<strong>Virtual Me</strong> runs a private browser, a local model, and an agent loop on this machine. Data stored on your hard drive. Nothing leaves the box.`
2. **Status page stale count** (confirmed live): `render.js` builds the healthy-state detail string; replace `All six supervised services are healthy.` with a computed `All ${services.length} supervised services are healthy.` so it can never go stale again.
3. **Banned phrases** anywhere in SPA copy (grep and replace with plain statements): `so you don't have to`, `supercharge`, `seamless`, `effortless`, `unleash`, `empower`, `delve`, `elevate`, `game-changing`, `AI-powered`. Quick-link card sub-lines are already plain; keep them.
4. Em-dash sweep: `grep -n "—" controller/web/static/index.html controller/web/static/js/*.js` — replace each visible instance with a period, comma, or colon per sentence sense. The `—` placeholder glyphs for unset values (`<strong id="home-host">—</strong>`) are **data placeholders, not copy**: replace them with `…` to satisfy the no-em-dash rule without inventing values.

## 4. Wordmark redesign

Grounding (web research, 2026-07): tech wordmarks succeed on custom-tuned letterforms, deliberate optical kerning (defaults read amateur at logo scale), restrained weight contrast, one distinctive letterform modification for ownability, and mandatory monochrome variants; test at favicon (16–24 px), UI (24–120 px), and display sizes; avoid trendy fonts and effects.

Deliverables (all committed under `controller/web/static/brand/`):

1. **`wordmark.svg`** — the words `Virtual Me` set as **outlined paths** (convert text to paths so no font loading is involved in the logo itself). Construction recipe for the executor:
   - Base face: Space Grotesk 600 (the existing brand font, so the mark and UI stay related), tracking tightened to approximately −2% em overall, then optically kerned: close `V‑i` (the V's open arm needs the i tucked in), open `l‑M` slightly (stem collision risk), keep the word gap at 0.55 em (tighter than a space; the two words read as one mark).
   - Weight play: `Virtual` at 600, `Me` at 700 (a half-step heavier, not a different size). Single distinctive modification: slice the apex of the `M` with a horizontal cut mirroring the V's angle (the V and M become a call-and-response pair; this is the ownable detail).
   - Color: fill `currentColor` so the existing `--brand-a`→`--brand-b` gradient text treatment (CSS `background-clip`) can be replaced by an SVG `<linearGradient>` using `var(--brand-a)`/`var(--brand-b)` via CSS custom properties on the `<svg>`; provide `class="wordmark-svg"` and style both gradient and monochrome (`fill: var(--fg)`) variants in `app.css` (`.wordmark-mono`).
   - Produce it with any vector tool; commit only the final optimized SVG (run through `svgo`-equivalent manual cleanup: no editor metadata, 1 decimal place).
2. **`virtualme-mark.svg` refresh** (the square monogram): keep the geometry family but apply the same sliced-apex detail to its V; regenerate `favicon.svg` from it. Verify legibility at 16 px (pinch test) and 48 px.
3. **Sidebar brand row** (`index.html` `.brand`): mark + the new `wordmark.svg` inline (`<svg>` referencing the sprite or inlined paths) replacing the styled `<strong class="wordmark">` text. Height 20 px in the sidebar; the gradient variant in color themes, mono variant in the `contrast` theme (its token contract favors flat ink; wire via `[data-theme="contrast"] .wordmark-svg { … }`).
4. Update `scripts/build-icons.mjs`/sprite wiring if the mark is sprite-sourced. Every theme × variant must pass a squint test: even gray-blob rhythm, no letter pair visually darker than the rest.

## 5. Live-connection "wristwatch" indicator

Replace the plain `live` pill (both instances of `[data-connection]`: topbar mobile + sidebar footer) with a compact horological infographic. Design: a 44 px circular dial + a two-line text stack to its right (sidebar); topbar shows the dial alone.

1. **Markup** (`index.html`, both locations; sidebar version):

```html
<div class="conn-watch" data-connection role="status" aria-live="polite">
  <svg class="conn-dial" viewBox="0 0 44 44" aria-hidden="true">
    <circle class="dial-face" cx="22" cy="22" r="20"/>
    <g class="dial-ticks"></g>                      <!-- 12 minute ticks, JS-generated once -->
    <circle class="dial-uptime" cx="22" cy="22" r="16"/>  <!-- uptime ring, stroke-dasharray driven -->
    <line class="dial-hand" x1="22" y1="22" x2="22" y2="8"/> <!-- sweeps once per minute -->
    <circle class="dial-pip" cx="22" cy="22" r="2.5"/>
  </svg>
  <div class="conn-text">
    <strong class="conn-host">…</strong>
    <span class="conn-meta">…</span>
  </div>
</div>
```

2. **Behavior** (`render.js` + a small `conn.js` module):
   - `dial-pip` color: `--ok` when live, `--err` when reconnecting, `--muted` when connecting; the pip gently pulses only when NOT live.
   - `dial-hand`: rotates continuously, one revolution per minute (`transform: rotate(…)` updated by `requestAnimationFrame` throttled to 1 s steps; frozen when disconnected). This is the "it is alive" heartbeat.
   - `dial-uptime` ring: arc fill = server uptime within the current 24 h (`uptimeSec % 86400`), via `stroke-dasharray`; full ring = a day; `title` shows exact uptime.
   - `conn-host` line: `<hostname>:<port>` from the `state` snapshot (§1.3).
   - `conn-meta` line, live: `up 3d 4h · linked 12m` (server uptime, humanized; connected duration = time since this WS session opened, tracked in `ws.js` by exposing `connectedSince`). Not live: `reconnecting…` / `connecting…`.
   - All figures monospace (`--font-mono`, 0.7 rem); host line 0.75 rem.
3. **Styling**: dial face `--surface` fill + `--border` stroke; ticks `--border`; hand `--accent`; uptime ring `--brand-a` at 60% opacity. Motion gated on `--motion`/reduced-motion (reduced: hand does not animate; pip does not pulse; state is still color-coded).
4. Keep the DOM contract: anything else reading `[data-connection]` (`renderStatus`) is updated to the new structure in the same change.

## 6. Per-tab fixes from the live inspection (2026-07-23)

Items not owned by other specs (019 owns chart defects, 023 owns mail, 020 owns speech):

1. **`/desktop/` directory listing**: the noVNC proxy root serves websockify's file index. In `controller/cmd/controller/main.go`, the `/desktop/` reverse-proxy handler special-cases an exact `GET /desktop/` (and `/desktop`) with a 302 to `/desktop/vnc.html?autoconnect=1&resize=scale&path=desktop/websockify`. The `/desktop-view` SPA page is unaffected.
2. **Status page mobile lookback wrap**: fixed by spec 019 §3.4; verify here as part of the tab sweep, do not re-implement.
3. **Tab sweep**: after all sections land, walk every page at 1600/375 px in `modern` light + `terminal` dark, screenshot each, and fix any spacing/contrast paper cuts found (≤ 4 px adjustments and token swaps only; anything structural becomes a spec amendment). Record the sweep results in the commit message.

## 7. Docs

`/master-update` — operate skill (new home facts incl. address cell semantics, watch indicator reading guide), develop skill (brand asset inventory, copy rules: no em-dash, banned phrase list, version ldflags), README (new wordmark appears wherever the old one was referenced).

## 8. Acceptance checklist

- [ ] `npm run check` green.
- [ ] Home: pill on its own line; stats in an aligned grid; Address cell shows `location.host` plus container addrs; GPU cell appears only with a GPU; footer version comes from the build, not hardcoded text.
- [ ] `grep -rn "—" controller/web/static/index.html controller/web/static/js` → no visible-copy hits; banned-phrase grep clean; Status detail says `All 8 supervised services…` (computed).
- [ ] Wordmark: new SVG renders in sidebar at 20 px, favicon legible at 16 px, mono variant used in `contrast`, gradient elsewhere; squint test passes in all eight themes.
- [ ] Watch indicator: hand sweeps when live and freezes when the container stops; pip color tracks connection state; meta line shows uptime + connected duration; reduced-motion honored.
- [ ] `GET /desktop/` redirects to the noVNC client; no directory listing reachable.
- [ ] 375 px: home quick links full-width; nothing overlaps on any page in the tab sweep.

## Amendments

### 2026-07-24 — Two-face wordmark ("Virtual" block + red "me" script)

Supersedes §4's single-face Space Grotesk recipe. The wordmark is now two words
in two faces, still committed as outlined paths with no runtime font loading:

- **`Virtual`** — Archivo Black (decorative block face), cap height 20 units,
  slight logo tracking (−0.012 em), filled `var(--wordmark-fill, #7d8590)`.
  The console sets `--wordmark-fill: var(--fg)`; the `#7d8590` fallback keeps
  the standalone file legible on both GitHub light and dark backgrounds.
- **`me`** — Caveat (casual handwritten face; no italic cut exists, so a
  `skewX(-7)` transform supplies the lean, baseline-compensated), oversized to
  34 units on the shared baseline, filled the fixed brand red `#d63b2f` in
  every theme (including `contrast`). The red is part of the mark, not a theme
  token.
- Generation is reproducible: `scripts/gen-wordmark.mjs` (run manually, never
  by the gate) fetches Archivo Black and Caveat TTFs from pinned
  `google/fonts` commits (`94a7d813…` and `5571d84c…`) with sha256
  verification, outlines the text with the exact-pinned `opentype.js@1.3.4`
  devDependency, and rewrites `controller/web/static/brand/wordmark.svg`
  (committed). Font downloads cache under gitignored `scripts/.cache/`.
- The old `--brand-a`→`--brand-b` gradient fill and the sliced-M mask are
  retired. `.wordmark-svg` is 110×19 px in the sidebar (new aspect ≈ 5.8:1);
  the outer `<svg>` carries no viewBox and scales the sprite symbol.
- `virtualme-mark.svg` (the checkbox/V project icon) is retained everywhere
  and now paints via `var(--mark-fill, #7d8590)` / `var(--mark-dot, #d63b2f)`
  so the standalone file renders on GitHub; the console maps `--mark-fill` to
  `var(--brand-a)`. README shows the mark inline beside the wordmark image.
- The sidebar brand row became `<a class="brand" href="/" data-nav>` with a
  subtle resting background and hover state; clicking it routes to the home
  page like any nav link.

### 2026-07-24 — Home layout: uniform top alignment, fact tiles, hero health pill, pinned footer

Feedback pass over the executed home page (§2):

- **Uniform top alignment.** `.hero-copy` stretches as a flex column
  (was `align-self:center`) and `.quick-links a` uses `align-content:start`
  (was `center`); fact tiles were already top-aligned. No card on the page
  centers vertically anymore.
- **Modular fact tiles.** Each `.fact` becomes a bordered tile
  (`--bg` fill, radius, compact padding) in an equal-height grid. Values are
  shortened for scannability: Uptime shows the two most significant units
  (`formatUptimeShort`), CPU is `N cores` with `load X.XX` demoted to the
  small line, Disk is `X GB free` with `of Y GB` demoted. Address and
  container addresses are unchanged. The Status page keeps full-precision
  uptime.
- **Health pill relocation.** `#home-health` moves from the status band into
  the bottom of the hero's left column (`.hero-health`, `margin-top:auto`);
  the status band is now facts only.
- **Viewport-pinned footer.** `[data-page=home]` is a flex column with
  `min-height` derived from the main padding (desktop and ≤64rem variants);
  the footer carries `margin-top:auto` so it sits at the viewport bottom even
  on short content. The footer adds an "MIT license" link (to `LICENSE` on
  GitHub) beside the existing GitHub link.

### 2026-07-24 — Wider desktop layout and clickable brand

Console feedback pass (specs 012–025 amendments). Files:
`controller/web/static/css/app.css`, `index.html`.

1. **Content cap raised.** `main > section` grows from `min(100%, 72rem)` to
   `min(100%, 100rem)`, and the `main` gutter clamp tightens from
   `clamp(1.25rem, 4vw, 3rem)` to `clamp(1.25rem, 3vw, 2.25rem)` so wide
   desktop viewports spend the reclaimed space on content, not padding. The
   home page's flex `min-height` calc uses the same clamp.
2. **Grids scale with the new width.** `.jobs-grid` becomes
   `minmax(0, 2.2fr) minmax(24rem, 1fr)` (detail pane grows past its old
   fixed 24rem), and `.tools-grid` becomes
   `minmax(16rem, 22rem) minmax(0, 1.4fr) minmax(24rem, 1fr)` so the form
   and output columns absorb extra width. Mobile breakpoints are unchanged.
3. **Clickable brand.** The sidebar brand block is an `<a href="/" data-nav>`
   with a subtle hover background (documented here; implemented with the
   wordmark amendment above).
