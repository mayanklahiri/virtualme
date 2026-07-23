# Spec 011: Console UI Refresh — Brand, Themes, Homepage

| | |
|---|---|
| Status | Proposed |
| Depends on | `specs/003-controller.md` (SPA, asset pipeline), `specs/005-console-ui.md` (themes, theme picker, homepage) executed; `specs/007-persistence-locality.md` gates (SPA same-origin, spec 007 §2b) bind all assets; independent of 009/010 (if those land first, the Speech/Mail nav entries simply inherit the refresh) |
| Produces | A collapsed theme selector (button + popover); a "Virtual Me" brand identity (pinned display font, in-repo logo mark, per-theme accent gradient, favicon); a friendlier homepage with a pinned public-domain hero photograph (theme-tinted), hostname, and live capacity stats; revised `editorial`/`terminal`/`warm` themes; three new themes (`arctic`, `solar`, `studio`); a per-theme type-scale token |
| Followed by | Future specs |

## 0. Executor instructions

- The constitution (`specs/001-constitution.md` §1) binds this spec. No container/layer changes; the work is SPA sources, `controller/tools/fetch-assets.sh`, `scripts/build-web.sh` (copying one new asset directory), and a small `internal/state` extension. `controller/go.mod` stays at zero `require` lines (`os.Hostname`, `syscall.Statfs` are stdlib).
- All pins in §1/§4 were fetched and hashed on **2026-07-23**. **Re-verify each sha256 before build**; STOP on mismatch.
- The SPA remains same-origin (spec 007 §2b gate): the brand font and hero image are **downloaded at build time by `fetch-assets.sh` and self-hosted**, never hotlinked. No new URL may appear in `web/static/js`/`css` outside comments.
- Licenses, recorded here per constitution rule 7 (pinned artifacts): Space Grotesk — SIL Open Font License 1.1 (same regime as the fonts pinned by spec 005). Hero photograph — *Earthrise* (NASA Apollo 8, AS08-14-2383, Dec 24 1968), **public domain** (NASA-created imagery is not subject to copyright), served from Wikimedia Commons.
- Keep every theme AA-contrast for body text on `--bg`/`--surface` (spec 005 posture); the `contrast` theme is not modified.
- Stop-on-red per section; finish with the Acceptance Checklist (§8).

## 1. Brand identity for "Virtual Me"

### 1a. Pinned brand font

Add to `FONT_ROWS` in `controller/tools/fetch-assets.sh` (exact pattern of the existing rows):

```
"SpaceGrotesk.woff2|https://fonts.gstatic.com/s/spacegrotesk/v22/V8mDoQDjQSkFtoMM3T6r8E7mPbF4Cw.woff2|0640890476fc1198ab4de571fb658de443c4d85b66466ec09534a8737ab1ce9d"
```

(Space Grotesk variable, wght 500–700, latin subset — the wordmark and headings never need more.) `@font-face` for `"Space Grotesk"` added to `app.css`; exposed as `--font-brand` on `:root` (all themes share the wordmark font — that is what makes it a brand).

### 1b. Logo mark + wordmark

- New **in-repo, hand-written** `controller/web/static/brand/virtualme-mark.svg`. It cannot live in `web/static/icons/` — that directory is gitignored (fetched Lucide assets only) — so `brand/` is a new **committed** source directory; extend `scripts/build-icons.mjs` to read `brand/*.svg` in addition to `icons/*.svg` when building the sprite (symbol id `#i-virtualme-mark`). The mark: a rounded square containing a stylized "V" whose right stroke ends in a filled orbit dot — 24×24 viewBox, `stroke="currentColor"` + one `fill="currentColor"` circle so it inherits theme color. Keep it ≤ 15 simple path commands; no embedded raster, no gradients inside the SVG (theme gradients come from CSS).
- Sidebar brand block (`index.html` `.brand`): replace the generic `bot` icon with the mark; wordmark becomes `<strong class="wordmark">Virtual&nbsp;Me</strong>`.
- CSS: `.wordmark{font-family:var(--font-brand);font-weight:700;letter-spacing:-.02em;font-size:1.25rem;background:linear-gradient(100deg,var(--brand-a),var(--brand-b));-webkit-background-clip:text;background-clip:text;color:transparent}` with the mark colored `var(--brand-a)`. New per-theme tokens `--brand-a`/`--brand-b` (a two-stop accent pair; each theme block in §3/§4 defines them — e.g. modern light `#4055c8 → #08717a`). Fallback: `@supports not (background-clip:text)` → solid `var(--accent)`.
- **Favicon**: new `controller/web/static/brand/favicon.svg` (the mark on a neutral tile, hand-written, committed) + `<link rel="icon" href="/favicon.svg" type="image/svg+xml">`; `build-web.sh` copies it to `dist/favicon.svg`.
- The topbar keeps the page title; the brand lives in the sidebar (visible on desktop always, in the drawer on mobile) — no duplicate wordmark.

## 2. Collapsed theme selector

Replace the always-open `fieldset.theme-picker` (index.html + `theme.js`) with:

- A single **theme button** in the sidebar footer: palette icon + current theme name + `chevron-down` icon (both already fetched), `aria-expanded`, `aria-controls="theme-popover"`.
- A **popover panel** (`#theme-popover`, absolutely positioned above the footer, `role="dialog"`, `aria-label="Theme"`) containing exactly the existing contents: the theme swatch grid (`#theme-options`) and the variant row (`#variant-options`). `theme.js` keeps building both exactly as today; only the open/close shell is new.
- Behavior: button toggles; `Escape`, outside click, and selecting a theme close it (selecting a *variant* keeps it open — variant toggling is a common quick follow-up); focus returns to the button on close. Persistence (`vm-theme`/`vm-variant` in localStorage) and the boot-time inline script are unchanged except for the extended theme list (§4).
- CSS: popover uses `--surface`, `--border`, `--radius`, subtle shadow; swatch grid becomes 2-column to fit the panel.

## 3. Theme revisions (existing themes)

### 3a. Per-theme type scale (root cause of "terminal is comically large")

New token `--font-scale` in each `[data-theme=…]` block; `body{font-size:calc(1rem*var(--font-scale,1))}` so every rem-based size inherits it. JetBrains Mono has a large x-height, so the terminal theme reads oversized at the shared scale:

| Theme | `--font-scale` |
|---|---|
| modern, contrast, arctic, solar, studio | `1` |
| editorial | `1` |
| terminal | `0.9` |
| warm | `0.975` |

Additionally for terminal: `h1` participates via the calc automatically; drop terminal's implicit heading bulk further with `[data-theme=terminal] h1{font-size:clamp(1.4rem,3vw,1.9rem);text-transform:uppercase;letter-spacing:.04em}` — terminal keeps its character (mono, square, instant motion) at a sane size.

### 3b. `editorial` — livelier palette (colors currently read stuffy)

Same fonts (Fraunces/Source Serif 4 are the theme's identity); the palette moves from brown-on-beige toward inky text with a vivid editorial red and cooler paper:

- light: `--bg:#faf7f2 --surface:#fffdf9 --fg:#1c1b18 --muted:#57534a --accent:#c73e1d --accent-fg:#fff --border:#e7e0d2 --brand-a:#c73e1d --brand-b:#1f6f8b` and a re-pitched chart ramp led by `--p1:#c73e1d --p2:#1f6f8b --p3:#3d6b35 …` (keep 8 stops, all AA on `--bg`).
- dark: `--bg:#141311 --surface:#1e1c19 --fg:#efeae1 --muted:#a8a094 --accent:#ff7a59 --accent-fg:#141311 --border:#33302a --brand-a:#ff7a59 --brand-b:#6fc3d9` + matching ramp.
- radius stays 4px; motion stays 150ms.

### 3c. `warm` — fix "rounded and weird font"

- Fonts: body switches from Nunito Sans to **InterVariable** (already shipped); headings keep Nunito (the warmth) at `font-weight:800`. Nunito Sans is dropped from `FONT_ROWS` **only if** no other rule references it after this change (it will not be); remove the row and its `@font-face`.
- `--radius:16px → 12px`; motion `250ms → 200ms`. Palette untouched (the complaint was shape+font, not color).

## 4. Homepage refresh

### 4a. Pinned hero photograph

Add an image fetch block to `controller/tools/fetch-assets.sh` (same URL|sha256 row pattern; new dest `web/static/img/`):

```
HERO_URL="https://upload.wikimedia.org/wikipedia/commons/thumb/a/a8/NASA-Apollo8-Dec24-Earthrise.jpg/1280px-NASA-Apollo8-Dec24-Earthrise.jpg"
HERO_SHA256="da22ac0b5fdbc1ebf1c080c8481d80e2b8b1ea22e2e7fee7215ab0c819e333e0"
# → web/static/img/hero-earthrise.jpg   (1280×1280 JPEG, ~105 KB, public domain: NASA AS08-14-2383)
```

Extend the script's completeness check and `scripts/build-web.sh` to copy `img/` into `dist/img/`; add `controller/web/static/img/` to `.gitignore` (fetched, never committed, like `fonts/` and `icons/`). (Vintage-stock aesthetic per the request — a 1968 photograph — with an unambiguous public-domain license, which curated "no known restrictions" pools cannot guarantee.)

### 4b. Theme-tinted presentation

Hero becomes a two-column card (stacks under 720px): copy left, media right.

```html
<figure class="hero-media" aria-hidden="true"><img src="/img/hero-earthrise.jpg" alt=""></figure>
```

`.hero-media{background:linear-gradient(135deg,var(--brand-a),var(--brand-b));border-radius:var(--radius);overflow:hidden}` and `.hero-media img{display:block;width:100%;aspect-ratio:4/3;object-fit:cover;filter:grayscale(1) contrast(1.05);mix-blend-mode:overlay}` — a CSS duotone: the grayscale photo blended over the theme's brand gradient, so the image re-colors itself for every theme/variant (terminal → green, editorial → red/teal, contrast → effectively monochrome). No per-theme image variants are shipped.

### 4c. Friendlier copy + host identity + capacity

- Copy: eyebrow stays; `h1` → "Good to see you." with a subline "**Virtual Me** is watching the desk so you don't have to — a private browser, local model, and agent loop, all on this machine."; drop the current spec-sheet sentence.
- **`internal/state` extension**: `Snapshot` gains `Hostname string` (`os.Hostname`, cached at collector construction) and `System` gains `DiskFreeMB, DiskTotalMB int` from a new `ReadDisk(path string)` using `syscall.Statfs` on `$VM_DATA_DIR` (0/0 on error; unit-tested via a tempdir). Cores are already present (`len(cores)`).
- **Facts grid** (replaces the current three-pill row; all populated from `state` frames by `render.js`): health pill; `Host <hostname>`; `Uptime`; `CPU N cores · load L`; `Memory used/total GB`; `Disk free GB free of total`; `Model Gemma 4 E2B Q4_0` (static). Values format client-side (MB→GB one decimal).
- Quick-link cards stay (and pick up Speech/Mail cards if specs 009/010 have landed); footer stays.

## 5. Three new themes

Registered everywhere a theme name appears: `theme.js` `themes` array, the `index.html` boot-script array, swatch CSS, and the two token blocks each in `app.css`. All define the full token set including `--brand-a/b`, `--font-scale`, and the 8-stop chart ramp; all AA for `--fg` on `--bg`/`--surface`.

| Theme | Identity | Fonts / shape | Light (key tokens) | Dark (key tokens) |
|---|---|---|---|---|
| `arctic` | Nordic cool — calm blue-grays, ice accent | Inter / radius 8px / motion 150ms | `--bg:#eceff4 --surface:#fff --fg:#2e3440 --muted:#5b657a --accent:#5e81ac --brand-a:#5e81ac --brand-b:#88c0d0` | `--bg:#242933 --surface:#2e3440 --fg:#e5e9f0 --muted:#9aa5b8 --accent:#88c0d0 --brand-a:#88c0d0 --brand-b:#81a1c1` |
| `solar` | Solarized-style — warm paper / deep teal night | headings Fraunces, body Inter / radius 6px / motion 150ms | `--bg:#fdf6e3 --surface:#fffcf0 --fg:#073642 --muted:#657b83 --accent:#b58900 --brand-a:#b58900 --brand-b:#268bd2` | `--bg:#002b36 --surface:#073642 --fg:#e6ddc4 --muted:#93a1a1 --accent:#d3a625 --brand-a:#d3a625 --brand-b:#5fb3e8` |
| `studio` | Polished neutral — gallery grays, single teal accent | Inter / radius 4px / motion 100ms | `--bg:#fafafa --surface:#fff --fg:#18181b --muted:#52525b --accent:#0d9488 --brand-a:#0d9488 --brand-b:#155e75` | `--bg:#131316 --surface:#1c1c21 --fg:#f0f0f2 --muted:#a1a1ab --accent:#2dd4bf --brand-a:#2dd4bf --brand-b:#67b7d1` |

Chart ramps (`--p1…--p8`): derive per theme from the accent hue outward (analogous → complementary), keeping ≥ 3:1 against `--bg`; the executor tunes exact hexes at implementation with the same discipline as the existing ramps. Swatch gradients follow the existing `.theme-<name>` pattern using each theme's `--brand-a`/dark-bg pair.

Existing themes gain their `--brand-a/b` pairs too: modern `#4055c8/#08717a` (light) `#8fa0ff/#5ac8d8` (dark); terminal `#146b3a/#315c9b` · `#4fdf7f/#70aaff`; warm `#b0500f/#7744a0` · `#f6ad55/#c79aef`; contrast `#0000d0/#0000d0` · `#ffff33/#ffff33` (gradients collapse to solid — deliberate); editorial per §3b.

## 6. Tests + gates

- **Go**: `state` tests — `Snapshot` marshals `hostname`; `ReadDisk` returns nonzero totals on a tempdir and zeros on a bogus path; existing `ReadSystem` tests untouched.
- **e2e** (extend the existing SPA assertions in `test/e2e.sh`): served `index.html` boot array lists all **8** themes; `/img/hero-earthrise.jpg` returns 200 with `image/jpeg`; `/favicon.svg` returns 200; a `state` frame contains `hostname` and `diskTotalMB > 0`; `dist/icons.svg` contains `i-virtualme-mark`.
- **Gates**: `npm run check` green — notably spec 007 §2b (no external origins in SPA sources: font + image are self-hosted) and the SPA build. `fetch-assets.sh` early-exit check covers the new font, image, and any new icons so CI stays deterministic after first fetch.
- **Manual sweep** (checklist §8): every theme × variant on every page — wordmark gradient legible, popover usable, hero duotone not muddy, charts readable with new ramps.

## 7. Docs refresh (constitution rule 9)

Run the `/master-update` skill procedure. Expected changes: README — updated console screenshot/description (8 themes, collapsed picker, homepage stats); `operate` skill — theme button location, homepage capacity readout; `develop` skill — `--brand-a/b`/`--font-scale` token conventions, how to add a theme (now: two token blocks + three registries + swatch), hero-image pin location; `AGENTS.md` unchanged unless the console description sentence needs the new tab/theme count.

## 8. Acceptance checklist (run every item)

| # | Command / action | Expected |
|---|---|---|
| 1 | Re-verify §1a font + §4a image sha256 pins | match; STOP on mismatch |
| 2 | `bash controller/tools/fetch-assets.sh` (clean tree) | fetches Space Grotesk + hero image + icons; second run prints "assets present" |
| 3 | `npm run check` | `check: OK` (SPA same-origin gate green with self-hosted assets) |
| 4 | `cd controller && go test ./... -count=1` | state hostname/disk tests pass |
| 5 | Serve the SPA; sidebar | logo mark + gradient "Virtual Me" wordmark in Space Grotesk; favicon shows the mark |
| 6 | Theme button | collapsed by default; popover opens/closes per §2 (Escape, outside click, focus return); selection persists across reload |
| 7 | Cycle all 8 themes × light/dark | terminal no longer oversized (`--font-scale:.9`); editorial palette per §3b; warm radius 12px + Inter body; arctic/solar/studio render with full token sets; wordmark/hero re-tint per theme |
| 8 | Homepage with a live container | hero photo duotone-tinted; hostname, cores+load, memory, disk free, uptime, health all live-update |
| 9 | `curl -fsS localhost:8080/img/hero-earthrise.jpg -o /dev/null -w '%{content_type}'` | `image/jpeg` |
| 10 | `bash test/e2e.sh` | `e2e: OK` incl. §6 assertions |
| 11 | Mobile width (≤ 720px) | hero stacks; popover fits the drawer; facts grid wraps |
| 12 | `/master-update` run | §7 docs updated |

Commit as `spec 011: console UI refresh — brand identity, collapsed theme picker, homepage, 3 new themes`.
