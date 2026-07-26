# Spec 030: Documentation and Marketing Site

| | |
|---|---|
| Status | Accepted (2026-07-25) |
| Depends on | `specs/001-constitution.md` (CLI, deterministic gate, CI conventions; a separate append-only 001 carveout must authorize the isolated `docs/` package before implementation), `specs/011-ui-refresh.md` and `specs/024-brand-chrome-polish.md` (the eight console themes and brand), `specs/028-data-explorer.md` (the current `docs/src/screenshots/` asset location), `specs/029-readpage-goldens.md` (current documented product behavior) |
| Produces | A self-contained, static Astro documentation and marketing site under `docs/`; source-checkout-only `docs dev` and `docs build` CLI commands; one shared, generated theme-token source for the controller and docs; deterministic docs checks; and a GitHub Pages workflow that publishes the built site from an orphan `docs` branch |
| Followed by | `specs/031-master-config.md`, which defines the authoritative configuration schema and exporter consumed by the generated configuration-reference page; 031 is required before the configuration-reference portion of this spec can receive final acceptance |

## 0. Executor instructions

1. **This is a test-first spec.** Execute in three strictly ordered passes:
   1. Add the failing unit, build-contract, workflow-contract, and browser/e2e
      tests in §11. Run the smallest applicable command after each test group
      and retain the red output in the execution notes.
   2. Implement §§2–10 until those tests pass. Do not weaken a test to fit an
      implementation unless the test contradicts this spec.
   3. Perform the reconciliation pass in §12 last, then run every gate in
      §11. Append an execution amendment recording red-first commands,
      implementation deviations, final commands, and results.
2. The root npm package remains dependency-free at runtime. `package.json`
   at repository root keeps `"dependencies"` absent or empty. Astro and all
   site-only tooling live in `docs/package.json` and `docs/package-lock.json`.
   A separate append-only amendment to spec 001 must explicitly permit this
   isolated docs build package and its committed static output inputs. Do not
   silently reinterpret spec 001.
3. The site is a customized Astro implementation, not an Astro documentation
   theme and not a hosted documentation service. Do not add Starlight, React,
   Vue, Tailwind, a CMS, a remote font service, or client-side search.
4. Ordinary builds and checks are offline. `npm ci --prefix docs` is the
   explicit setup/CI dependency-fetch step; neither `./cli.sh docs build` nor
   `npm run check` invokes install, `npx`, `curl`, or another network client.
5. Authored documentation content and documentation assets live under
   `docs/`. The only shared sources introduced outside that tree are
   `common/themes/themes.json` and its deterministic generator. Spec 031 owns
   `controller/internal/config/` and `controller/cmd/configctl/` as the
   configuration schema/exporter source. Astro source must never
   import a path outside `docs/`; before Astro starts, generators materialize
   the only shared inputs it consumes under `docs/src/generated/` and
   `docs/src/styles/`.
6. Generated files are not edited by hand. Each carries the provenance
   header specified below. Update its source and rerun the generator.
7. Stop on the first red gate. Complete §13 with every item still unchecked
   in this accepted, unexecuted spec.

## 1. Goals and fixed decisions

This spec delivers six inseparable parts:

- **D1 — isolated site:** a static Astro project wholly rooted at `docs/`,
  with exact-pinned dependencies and all page content, fonts, images,
  screenshots, scripts, and styles inside that tree;
- **D2 — local CLI:** source-checkout-only `./cli.sh docs dev` and
  `./cli.sh docs build`;
- **D3 — deployment:** path-filtered GitHub CI and orphan-branch publication
  to GitHub Pages using plain Git;
- **D4 — shared appearance:** one JSON source for all eight themes and both
  color variants, deterministically rendered into controller and docs CSS;
- **D5 — useful information architecture:** a strong home page, a
  five-minute guide, architecture and configuration references, a blog, and
  two intentionally unlisted editorial pages;
- **D6 — operational quality:** analytics that is disabled by default,
  canonical/base-correct output, accessibility and reduced-motion behavior,
  deterministic build assertions, and browser checks.

The production origin is
`https://mayanklahiri.github.io/virtualme/`. Astro uses
`site: "https://mayanklahiri.github.io"` and `base: "/virtualme"`.
The deployment is static, with `output: "static"` and
`trailingSlash: "always"`. There is no server adapter.

## 2. Repository shape and package boundary

### 2.1 Required tree

The implementation produces this structure. Small support modules may be
added within an already listed directory, but page routes and ownership must
not change.

```text
common/
└── themes/
    └── themes.json
scripts/
└── generate-themes.mjs
src/
└── commands/
    └── docs.js
docs/
├── package.json
├── package-lock.json
├── astro.config.mjs
├── tsconfig.json
├── README.md
├── public/
│   ├── .nojekyll
│   ├── favicon.svg
│   ├── fonts/
│   ├── images/
│   │   ├── memory-snowglobe/
│   │   └── no-more-bills/
│   └── social-card.jpg
├── scripts/
│   ├── ensure-generated.mjs
│   ├── make-404.mjs
│   └── verify-build.mjs
├── src/
│   ├── assets/
│   ├── components/
│   │   ├── CodeBlock.astro
│   │   ├── DocsNav.astro
│   │   ├── MemorySnowglobe.astro
│   │   ├── PageFooter.astro
│   │   ├── PageHeader.astro
│   │   ├── ThemePicker.astro
│   │   └── Toc.astro
│   ├── config/
│   │   ├── navigation.ts
│   │   └── site.ts
│   ├── content/
│   │   ├── blog/
│   │   │   └── welcome.md
│   │   └── config.ts
│   ├── generated/
│   │   ├── config-reference.json
│   │   └── themes.json
│   ├── layouts/
│   │   ├── BaseLayout.astro
│   │   ├── DocsLayout.astro
│   │   └── EditorialLayout.astro
│   ├── pages/
│   │   ├── index.astro
│   │   ├── guide.astro
│   │   ├── architecture.astro
│   │   ├── configuration.astro
│   │   ├── blog/
│   │   │   ├── index.astro
│   │   │   └── [...slug].astro
│   │   ├── about.astro
│   │   ├── no-more-bills.astro
│   │   └── 404.astro
│   ├── scripts/
│   │   ├── memory-snowglobe.ts
│   │   └── theme-picker.ts
│   ├── screenshots/
│   │   └── (all existing console JPEGs)
│   └── styles/
│       ├── generated-themes.css
│       ├── global.css
│       ├── docs.css
│       └── editorial.css
└── test/
    ├── browser.test.mjs
    └── helpers.mjs
```

The existing duplicate `docs/screenshots/home-route.jpg` is removed during
implementation; `docs/src/screenshots/` remains the sole screenshot source.
README image paths continue to point into that directory.

### 2.2 Exact package contract

`docs/package.json` is private ESM and contains no runtime deployment server:

```json
{
  "name": "@virtualme/docs",
  "private": true,
  "type": "module",
  "engines": {"node": ">=22.12.0"},
  "scripts": {
    "dev": "astro dev",
    "build": "node scripts/ensure-generated.mjs && astro build && node scripts/make-404.mjs && node scripts/verify-build.mjs",
    "check": "node scripts/ensure-generated.mjs --check && astro check && astro build && node scripts/make-404.mjs && node scripts/verify-build.mjs",
    "test:browser": "node --test test/browser.test.mjs"
  },
  "devDependencies": {
    "@astrojs/check": "0.9.9",
    "@playwright/test": "1.62.0",
    "astro": "7.1.3",
    "typescript": "6.0.3"
  }
}
```

`package-lock.json` is lockfile version 3, generated by npm under Node 24,
committed, and accepted only when `npm ci --prefix docs` leaves both docs
package files unchanged. Every direct dependency is an exact version with no
`^`, `~`, tag, URL, Git reference, workspace range, or wildcard. Transitive
integrity hashes remain in the lock. The root package does not gain a
workspace declaration or any Astro dependency.

`docs/README.md` gives exactly these setup paths:

```console
npm ci
npm ci --prefix docs
./cli.sh docs dev
./cli.sh docs build
```

It explains that the first command prepares the root quality gate, the second
prepares the isolated site, ordinary commands are then offline, and the
Playwright browser download is a separate explicit browser-test setup step:

```console
npm --prefix docs exec playwright install chromium
```

CI may add `--with-deps`; local docs build does not need a browser.

### 2.3 Hermetic source boundary

- No `.astro`, `.ts`, `.js`, `.md`, or `.mdx` file loaded by Astro imports
  `../..` out of `docs/`, reads the repository root at runtime, or fetches a
  network URL during build.
- Images and fonts are committed below `docs/`. CSS contains no remote
  `url(...)`; pages contain no externally hosted image, font, script, iframe,
  stylesheet, or module.
- External hyperlinks are normal anchors and do not affect hermeticity.
- `ensure-generated.mjs` runs before Astro and may execute the dependency-free
  root theme generator. It never builds/runs Go or invokes the spec 031
  exporter; it validates the committed config-reference artifact that the root
  canonical gate generated earlier. Astro consumes only docs-local files.
- `docs/dist/`, `docs/.astro/`, `docs/node_modules/`, Playwright browser
  caches, and browser-test screenshots are ignored. Generated source inputs
  in `docs/src/generated/` and `docs/src/styles/generated-themes.css` are
  committed and verified for drift.

## 3. Source-checkout CLI

### 3.1 Registration

Add `src/commands/docs.js`, import its `run` function in `src/main.js`, add the
flat key `docs` to the existing command record, and add this help row:

```text
docs dev [--host <host>] [--port <port>]  Serve the documentation site (source checkout)
docs build                               Build the documentation site (source checkout)
```

This is flat registration: `docs` is one root command whose handler parses
the next token. Do not create a general nested-command framework.

### 3.2 Invocation and argument behavior

`./cli.sh docs` and `./cli.sh docs --help` print:

```text
Usage:
  virtualme docs dev [--host <host>] [--port <port>]
  virtualme docs build
```

They return 0. `docs dev --help` and `docs build --help` print the same usage
and return 0 without spawning npm.

`docs dev`:

- defaults to `--host 127.0.0.1 --port 4321`;
- accepts each long option once, in either order;
- requires a non-empty host value and a base-10 integer port from 1 through
  65535;
- rejects duplicate options, `--host=value`, `--port=value`, positional
  values, `--open`, and every unknown flag;
- launches `npm run dev -- --host <host> --port <port>` with `cwd` set to the
  absolute `docs/` directory and inherited stdio.

`docs build` accepts no arguments and launches `npm run build` in that same
directory with inherited stdio.

The command computes the repository root from `import.meta.url`, not
`process.cwd()`. It therefore behaves identically when `cli.sh` is invoked
from the checkout root or when `bin/virtualme.js` is invoked with another
current directory. It verifies `docs/package.json` and
`docs/node_modules/.bin/astro` before spawning. A missing source tree prints
`docs requires a virtualme source checkout` to stderr. Missing installed docs
dependencies print `docs dependencies missing; run npm ci --prefix docs` to
stderr. Neither case invokes npm.

Exit status is deterministic:

| Condition | Exit |
|---|---:|
| help or successful child | 0 |
| missing checkout/dependencies, spawn failure, build/dev child failure | 1 |
| missing subcommand, invalid option/value, extra argument | 2 |
| child terminated by a POSIX signal | `128 + signal number` |

`docs.js` exports parser and runner seams so unit tests inject filesystem and
child-process behavior without starting Astro. The production path uses only
Node built-ins and adds no root runtime dependency.

## 4. Shared theme source and deterministic generation

### 4.1 Canonical JSON schema

`common/themes/themes.json` becomes the only authored source for theme
identity and design tokens. It is strict JSON with this top-level shape:

```json
{
  "schemaVersion": 1,
  "defaultTheme": "modern",
  "defaultVariant": "auto",
  "themes": []
}
```

`themes` contains exactly eight records in this order:
`modern`, `editorial`, `terminal`, `warm`, `contrast`, `arctic`, `solar`,
`studio`. Each record has:

```json
{
  "id": "modern",
  "label": "Modern",
  "swatch": ["#4055c8", "#ffffff"],
  "typography": {
    "heading": "InterVariable, system-ui, sans-serif",
    "body": "InterVariable, system-ui, sans-serif",
    "mono": "\"JetBrains Mono\", monospace",
    "scale": 1
  },
  "shape": {
    "radius": "10px",
    "motion": "150ms",
    "iconStroke": 2
  },
  "light": {},
  "dark": {}
}
```

Each `light` and `dark` object contains exactly these fields:

```text
bg surface fg muted accent accentFg ok err border brandA brandB
p1 p2 p3 p4 p5 p6 p7 p8
```

Every color is a six-digit lowercase `#rrggbb` value. IDs match
`^[a-z][a-z0-9-]*$`, labels are non-empty, numeric scale and icon stroke are
finite and positive, radius and motion use explicit CSS units, IDs are
unique, and no unknown keys are accepted. The initial token values are
transcribed byte-for-byte from the current declarations in
`controller/web/static/css/app.css`; this refactor must not alter the visual
palette.

Font files are application assets, not embedded JSON. The controller keeps
its local font-face declarations. Docs commits the fonts it uses below
`docs/public/fonts/`. Both applications use the typography stacks emitted by
the shared source.

### 4.2 Generator

`scripts/generate-themes.mjs` is dependency-free ESM using Node built-ins.
It validates the complete schema, formats from in-memory strings, and writes
atomically only when bytes differ:

1. `controller/web/static/css/generated-themes.css`;
2. `docs/src/styles/generated-themes.css`;
3. `docs/src/generated/themes.json`, a normalized docs-local registry used
   to render picker labels and swatches;
4. `controller/web/static/js/generated-themes.js`, a frozen ESM registry for
   the console picker;
5. `controller/web/static/js/generated-theme-boot.js`, a classic-script
   allowlist for pre-paint theme resolution.

Both CSS outputs contain the same selector/token blocks. They begin:

```css
/* Generated by scripts/generate-themes.mjs from common/themes/themes.json.
 * DO NOT EDIT. */
```

The normalized JSON begins with a top-level string field:

```json
"_generated": "scripts/generate-themes.mjs from common/themes/themes.json; DO NOT EDIT"
```

CSS formatting is fixed: LF endings, two-space indentation, themes in source
order, typography block first, light then dark, properties in schema order,
one property per line, one final newline, no timestamp, absolute path,
platform-specific data, random value, or locale-dependent sorting.

Default mode writes all five files. `--check` generates in memory, prints
every stale/missing path, writes nothing, and returns 1 on drift. Unknown
arguments return 2. Validation errors identify the JSON pointer and return 1.

The generated CSS maps camel-case JSON fields to existing CSS custom
properties:

```text
heading/body/mono/scale -> --font-heading/--font-body/--font-mono/--font-scale
radius/motion/iconStroke -> --radius/--motion/--icon-stroke
accentFg/brandA/brandB -> --accent-fg/--brand-a/--brand-b
all remaining color fields -> --<field>
```

`app.css` removes its authored theme token blocks and imports
`./generated-themes.css` before rules that consume tokens. Theme swatch CSS is
generated from each record's `swatch`; the authored eight-color swatch list
is removed. `theme.js` imports the frozen array from
`controller/web/static/js/generated-themes.js`. The inline pre-paint script
in `index.html` receives its allowlist through the generated classic script
`controller/web/static/js/generated-theme-boot.js`, loaded synchronously
before the pre-paint resolver. All five generated outputs carry the applicable
provenance comment and are included in `--check`.

Update `scripts/build-web.sh` to process
`static/js/generated-theme-boot.js` into `dist/js/generated-theme-boot.js`
before copying `index.html`; the generated classic script is not imported into
the bundled application entry because it must run synchronously before first
paint. The build fails if this generated input is absent or stale.

This removes every duplicated hard-coded eight-theme registry. A Node test
asserts that generated controller registry, boot registry, CSS selectors, and
docs registry all expose the same ordered IDs.

### 4.3 Docs picker parity

The docs theme picker offers all eight themes plus `Auto`, `Light`, and
`Dark`. It uses the same persistence keys as the console, `vm-theme` and
`vm-variant`, and the same default/fallback rules. A small inline pre-paint
script in `BaseLayout` resolves the saved values before CSS paints. Invalid
storage values fall back to `modern`/`auto`. `auto` tracks
`prefers-color-scheme`; explicit variants do not.

The control is keyboard operable, exposes current values through
`aria-pressed`, closes on Escape and outside click, and never traps focus.
When local storage is unavailable, selection remains functional for the
current page. Theme selection changes colors and typography, not document
meaning, content order, or visibility.

## 5. Information architecture and content contract

### 5.1 Listed routes

The global header and mobile navigation list these routes in this order:

| Route | Navigation label | Purpose |
|---|---|---|
| `/` | Home | Product proposition, visual story, quick start, proof through real console screenshots |
| `/guide/` | 5-minute guide | Install, start, first task, recurring project, observe/take over, persistence, stop/update |
| `/architecture/` | How it works | Local components, data flow, trust boundary, model/browser/controller/container responsibilities |
| `/configuration/` | Configuration | Generated, deep-linkable option reference from spec 031 |
| `/blog/` | Blog | Reverse-chronological post index and future content scaffold |

Header utility links point to the GitHub repository and its root README.
There is no search box until a later spec defines a deterministic index.

### 5.2 Hidden routes

`/about/` and `/no-more-bills/` are built, canonicalized, included in the
sitemap only if a sitemap is later added, and directly reachable, but absent
from the header, footer, mobile menu, home cards, docs sidebars, blog index,
and RSS. A single subtle, accessible snowglobe interaction on the home page
may reveal `/about/`; crawlers and keyboard users can still follow the real
anchor after it is revealed.

### 5.3 Five-minute guide

`/guide/` is written for a technically casual user and has a visible
“About five minutes” reading/doing estimate. It is one linear path:

1. verify Node 22+ and Docker;
2. run `npx virtualme doctor`;
3. run `npx virtualme start`;
4. wait for health and open `http://localhost:8080`;
5. ask one concrete browser task in Chat;
6. watch Desktop and use takeover;
7. create one recurring Project;
8. find results and artifacts in Jobs and Data;
9. explain the persistent data directory, trusted-private-network warning,
   Stop, Update, and troubleshooting links.

Every command has a copy affordance that preserves exact text. The guide
states first-start model download and CPU latency without promising a fixed
duration. It distinguishes interactive chat from recurring projects. It does
not require reading architecture or configuration first.

### 5.4 Architecture

`/architecture/` uses an HTML/CSS diagram, not a rasterized text diagram.
It shows browser/user → port 8080 → Go controller → queue → local model and
browser, plus Valkey and the mounted data directory. It explicitly documents:

- one container and one exposed port;
- local model inference and OS-level browser actuation;
- CDP observation-only boundaries;
- data persistence and the prototype private-network trust model;
- the lack of authentication/TLS in v1;
- where jobs, projects, screenshots, mail, speech, and metrics live;
- CPU operation and optional GPU acceleration.

Diagram semantics survive without CSS and have a text equivalent adjacent to
the visual.

### 5.5 Blog

Astro content collections define a `blog` collection with required
`title`, `description`, `published` (`Date`), and optional `updated`,
`draft` (default false), and `image` fields. Production excludes drafts.
`/blog/` sorts descending by published date with slug as a byte-order
tiebreaker. `/blog/[...slug]/` renders a post with canonical metadata,
article timestamps, table of contents, and previous/next links.

The initial `welcome.md` is a concrete welcome post explaining why the
project exists, what runs locally, where to begin, and what the blog will
cover. It makes no roadmap promise and contains no fabricated benchmark.

## 6. Visual system and page-specific creative direction

### 6.1 Global design

The site uses the shared theme tokens but has its own composition. It combines
clear Apple-style product hierarchy with a restrained 1970s long-form
magazine-ad rhythm: large editorial headlines, generous negative space,
warm paper-like sections, strong rules, compact factual captions, and
full-bleed moments. This is a direction, not a license to imitate Apple
branding, copy, product chrome, or trade dress.

Content remains concrete and product-specific. Headings describe tasks and
facts. Screenshots are real committed Virtual Me captures with dimensions,
alt text, `loading="lazy"` below the fold, and responsive `srcset` using the
existing 480/960/1280 variants. The home hero asset is eagerly loaded and
does not become the largest page dependency without an explicit size check.

The SPA copy scrub in spec 024 applies only to copy visible in the controller
SPA. Do not broaden `test/brand-chrome.test.js` to scan docs. Documentation
copy nevertheless remains polished, direct, and free of generic AI marketing
filler. The implementation owns exact prose; this spec owns the facts,
structure, and tone.

### 6.2 Home: memory snowglobe

The homepage hero is inspired by the console home hero but is newly composed.
Its centerpiece is a “memory snowglobe”: a circular glass vessel containing
small layered fragments from a private digital life (calendar card, browser
window, note, waveform, mail envelope, and task slip) above a quiet local
machine base. It communicates that the system keeps and works with memories
on the user's hardware.

Implementation uses semantic HTML, local raster/SVG assets, CSS transforms,
and at most one small docs-local script. Snow particles are decorative and
bounded to 24 DOM nodes. Pointer movement may produce at most 4 degrees of
parallax; focus, hover, and a “Gently shake” button produce the same reveal
without requiring pointer precision. The animation settles within six
seconds and never continuously burns CPU. Under
`prefers-reduced-motion: reduce`, particles and parallax are static, the
button changes the caption without motion, and all content remains available.

Hero copy establishes: a private background agent, a real local browser,
recurring plain-English work, local memory, and no per-token bill. Primary
CTA is “Start in five minutes” to `/guide/`; secondary CTA is “See how it
works” to `/architecture/`. Subsequent sections cover:

1. three-step start;
2. real browser work and takeover;
3. recurring projects;
4. local data and privacy boundary;
5. console screenshot sequence;
6. hardware/CPU/GPU facts;
7. final guide CTA.

### 6.3 `/about/` easter egg

`/about/` expands the snowglobe into an interactive memory cabinet. Activating
a floating memory fragment moves focus to a textual card describing one
project principle: locality, inspectability, patience, ownership, or
dependability. Controls are buttons with accessible names; cards form a
labelled list; Escape returns focus; touch, keyboard, mouse, and reduced
motion are equivalent. There is no canvas-only content, audio autoplay,
device-orientation access, drag-only interaction, or infinite animation.

### 6.4 `/no-more-bills/` pull-page

`/no-more-bills/` is a hidden, unlisted 1970s/1980s lifestyle-magazine
pull-page: oversized headline, multi-column long-form body at wide widths,
pull quotes, a local-machine still life, clipped factual sidebars, and a
print-friendly single column.

Its narrative must include all of these ideas in an honest sequence:

- local AI can be slow, and waiting can be a rational trade for ownership;
- a small always-available machine in roughly the 25-watt device class is a
  useful comparison frame, not a measured consumption guarantee;
- work and memory remain private on hardware the user controls;
- there is no per-token AI bill for the bundled local model;
- the system welcomes tinkering and inspection;
- dependable recurring work matters more than theatrical speed;
- “eventually” is a feature of patient background computing when the result
  arrives reliably.

Do not state that Virtual Me consumes exactly 25 W, always consumes less than
a named alternative, has zero electricity cost, replaces every cloud model,
or guarantees task completion. Any energy sentence must use comparison
framing such as “the class of a compact 25 W machine” and immediately note
that actual system draw depends on hardware, workload, and optional GPU use.
“No token bills” is scoped to bundled local inference; electricity, hardware,
internet, and optional external services still cost money.

## 7. Layout, accessibility, metadata, analytics, and links

### 7.1 Shared layout

Every route uses `BaseLayout.astro`. It emits:

- UTF-8, viewport, title, description, canonical URL, Open Graph and Twitter
  metadata, theme-color, favicon, and local stylesheet links;
- a skip link to `#main`;
- one header/nav, one `main`, and the exact shared footer;
- the theme pre-paint script and picker;
- analytics behavior from §7.3.

Every page has one visible `h1`; heading levels do not skip for styling.
Landmarks have labels where repeated. Interactive targets are at least
44×44 CSS pixels. Focus is never hidden under the sticky header. Text zoom at
200% and viewport widths 320–1920 px produce no page-level horizontal
scroll. Color is not the sole status signal. Body text and controls meet
WCAG AA contrast in all sixteen theme/variant combinations; the contrast
theme remains especially strong.

### 7.2 Footer

The footer's copyright is exactly:

```html
© 2026 <a href="https://www.linkedin.com/in/mayanklahiri/">Mayank Lahiri</a>
```

It appears on every generated page, including hidden pages, blog posts, and
404 output. The link is not rewritten through a tracking redirect.

### 7.3 Editable Google Analytics

The sole editable site-level configuration is
`docs/src/config/site.ts`. It exports a frozen object including:

```ts
const committedAnalyticsMeasurementId = "";
const environmentAnalyticsMeasurementId =
  (process.env.PUBLIC_GA_MEASUREMENT_ID ?? "").trim();

export const site = Object.freeze({
  productionUrl: "https://mayanklahiri.github.io/virtualme/",
  analyticsMeasurementId:
    environmentAnalyticsMeasurementId || committedAnalyticsMeasurementId,
});
```

The empty string is the committed default. A maintainer may replace it with a
public `G-...` measurement ID in this file; this ID is configuration, not a
secret. The environment variable provides a hermetic test/deployment
override and takes precedence only when non-empty.

`BaseLayout` conditionally includes the standard Google tag loader and inline
`dataLayer`/`gtag("config", id)` bootstrap on every generated HTML page when
the trimmed ID is non-empty. The ID is validated against
`^G-[A-Z0-9]+$`; an invalid non-empty ID fails the build. When empty, output
contains no `googletagmanager.com`, `google-analytics.com`, `dataLayer`,
`gtag(`, preconnect, analytics placeholder request, or empty script element.
There is no cookie banner because disabled-by-default analytics stores
nothing; enabling analytics is an explicit maintainer decision that also
requires checking applicable disclosure/consent obligations.

Build tests run once disabled and once with
`PUBLIC_GA_MEASUREMENT_ID=G-TEST1234`, enumerating **every** generated HTML
file. Disabled output must contain no analytics tokens. Enabled output must
contain exactly one loader and one matching config initialization per HTML
file, including `404.html`.

### 7.4 Base-aware URLs and crosslinks

All internal links and assets use Astro's base-aware URL helper in one
docs-local module. No source template contains a root-absolute application
path such as `href="/guide/"` or `src="/images/..."`; generated production
HTML uses `/virtualme/...`. Canonical URLs are absolute, have one
`/virtualme/` prefix, use trailing slashes for content pages, and never
contain `localhost`, `docs`, duplicate slashes after the origin, or `.html`
except the 404 canonical.

Root `README.md`, directly under `# Virtual Me`, gains a visible
`[Documentation](https://mayanklahiri.github.io/virtualme/)` link. The docs
header has a visible `GitHub` link to
`https://github.com/mayanklahiri/virtualme` and a `README` link to
`https://github.com/mayanklahiri/virtualme#readme`. Contract tests check both
directions.

### 7.5 404 handling

`src/pages/404.astro` is a useful not-found page with navigation home, guide,
configuration, and GitHub issue links. Astro first builds its route normally.
`docs/scripts/make-404.mjs` deterministically copies that rendered document to
`docs/dist/404.html`, rewriting only the canonical to
`https://mayanklahiri.github.io/virtualme/404.html`. GitHub Pages therefore
finds the required branch-root `404.html` while all links/assets retain the
`/virtualme/` base. The script fails if source output is absent and does not
delete it.

## 8. Configuration-reference contract and spec 031 handoff

### 8.1 Generated input

When spec 030 executes before spec 031, it scaffolds
`docs/src/generated/config-reference.json` with:

```json
{
  "_generated": "placeholder for spec 031; DO NOT EDIT",
  "schemaVersion": 1,
  "status": "pending-spec-031",
  "sections": []
}
```

While that placeholder is present, `/configuration/` still builds and clearly
states that the generated reference is pending spec 031, then links to the
current README configuration guidance. It must not invent options.

If spec 031 has already executed, spec 030 preserves its complete generated
artifact and does not install the placeholder. Spec 031 owns the authoritative
configuration schema and the deterministic `configctl docs` exporter that
replaces the placeholder. Spec 030 is independently executable because the
placeholder builds; spec 031 follows and is consumed by 030 regardless of
execution order. Final configuration-reference acceptance in §13 remains
blocked until 031 emits a complete artifact with `schemaSha256`.

### 8.2 Export schema consumed by docs

The handoff format is the exact versioned JSON contract from spec 031 §13:

```json
{
  "schemaVersion": 1,
  "schemaSha256": "...",
  "sections": [
    {
      "id": "llama",
      "title": "Inference",
      "anchor": "llama",
      "consoleDeepLink": "/config#llama",
      "overview": "One-sentence purpose.",
      "details": ["Operational detail."],
      "exemplarYaml": "llama:\n  contextTokens: 32768\n",
      "settings": [
        {
          "path": "llama.contextTokens",
          "anchor": "llama-context-tokens",
          "consoleDeepLink": "/config#llama-context-tokens",
          "type": "integer",
          "default": 32768,
          "required": true,
          "choices": [],
          "constraints": {"minimum": 2048, "maximum": 131072},
          "restart": "llama",
          "legacyEnv": "VM_LLAMA_CTX",
          "secret": false,
          "overview": "One-sentence behavior.",
          "details": ["Operational detail."],
          "tradeoffs": [],
          "examples": [],
          "links": []
        }
      ]
    }
  ]
}
```

Section and setting `anchor` values are stable docs fragments supplied by the
exporter; `consoleDeepLink` points back to the corresponding `/config` field.
Docs combines its base-aware `/configuration/` route with `anchor` and never
uses the console path as a docs-site URL. Sections and settings arrive in
canonical schema/UI order; docs does not re-sort them. Each section has valid
exemplar YAML. Every setting retains type, required state, default, choices,
constraints, restart impact, legacy environment mapping, secret policy,
overview, details, tradeoffs, examples, and links exactly as exported. Docs
does not infer missing schema facts or expose secret values.

### 8.3 Stripe-quality reference behavior

When status is complete, `/configuration/` provides:

- a persistent desktop section rail and compact mobile section picker;
- stable fragment URLs for every section and option;
- visible permalink controls;
- option rows/cards showing path, type, required/optional, default, allowed
  values/constraints, example, details, and tradeoffs without hidden hover;
- one copyable exemplar YAML block per section;
- active-section highlighting driven by `IntersectionObserver`, with a
  no-script usable document and reduced-motion scrolling;
- exact-match client-side filtering over option path/title/summary, with
  query state in `?q=` and a result count announced only after user input;
- keyboard focus on the target when following an in-page option deep link.

The generated page never duplicates schema facts in authored Markdown.
Unknown schema versions and malformed/duplicate anchors fail before Astro.

## 9. Build pipeline and optimized-output contract

`docs/scripts/ensure-generated.mjs` has write mode and `--check` mode:

1. invoke `node ../scripts/generate-themes.mjs` with the corresponding mode;
2. validate `config-reference.json` as either the exact pending placeholder
   or the complete spec 031 export; for a complete export, read
   `controller/internal/config/schema.json` only in this pre-Astro script,
   compute its deterministic SHA-256, and require exact equality with
   `schemaSha256` so schema changes cannot deploy stale reference data;
3. fail on any unexpected file change in check mode.

It uses `process.execPath`, absolute paths derived from `import.meta.url`, and
inherited stderr. No shell is involved. Spec 031 separately wires
`configctl docs --check --output docs/src/generated/config-reference.json`
after Go tests in the canonical root gate. Docs builds consume that committed,
pre-generated artifact and never require Go or a running controller.

Astro optimization is fixed:

- static HTML and scoped/minified CSS/JS;
- local responsive screenshot source sets with explicit width/height;
- no client hydration except theme picker, snowglobe interaction, config
  filter/active section, and copy buttons;
- those scripts are plain Astro-bundled TypeScript with no framework;
- no source map is deployed;
- no file in `dist/_astro/` is unreferenced;
- `.nojekyll` is present at output root;
- output contains no source `.ts`, `.astro`, Markdown, package manifest,
  lockfile, test, or repository-relative path.

`verify-build.mjs` recursively inspects `dist` in byte-order and fails unless:

- expected route files exist for `/`, `/guide/`, `/architecture/`,
  `/configuration/`, `/blog/`, `/blog/welcome/`, `/about/`,
  `/no-more-bills/`, plus `404.html`;
- all local `href`, `src`, and `srcset` targets resolve under the production
  base, ignoring fragments and approved external schemes;
- all same-page fragments name an existing ID and all site route fragments
  resolve when their target HTML is available;
- each HTML document has one title, description, canonical, `h1`, main,
  footer, and exact copyright;
- no HTML file exceeds 256 KiB; total first-party JavaScript loaded by any
  one page is at most 96 KiB uncompressed; total first-party CSS loaded by
  any one page is at most 160 KiB uncompressed;
- no non-screenshot raster asset exceeds 500 KiB; screenshot variants keep
  their existing size budget and lazy-load below the fold;
- no remote resource URL exists except conditional Google Analytics output;
- canonical/base/analytics rules in §7 hold.

These are deterministic “Lighthouse-like” budgets. Do not run Lighthouse,
PageSpeed Insights, WebPageTest, or an unpinned network audit in a gate.
Wall-clock scores, network latency, and CPU timing are not acceptance
criteria.

## 10. GitHub Pages workflow

### 10.1 Trigger and permissions

Add `.github/workflows/docs.yml`:

```yaml
name: docs

on:
  pull_request:
    paths:
      - "docs/**"
      - "common/themes/**"
      - "controller/internal/config/**"
      - "controller/cmd/configctl/**"
      - "scripts/generate-themes.mjs"
      - "src/commands/docs.js"
      - "src/main.js"
      - "src/commands/help.js"
      - "test/docs*.test.js"
      - ".github/workflows/docs.yml"
  push:
    branches: [main]
    paths:
      - "docs/**"
      - "common/themes/**"
      - "controller/internal/config/**"
      - "controller/cmd/configctl/**"
      - "scripts/generate-themes.mjs"
      - "src/commands/docs.js"
      - "src/main.js"
      - "src/commands/help.js"
      - "test/docs*.test.js"
      - ".github/workflows/docs.yml"

concurrency:
  group: docs-${{ github.ref }}
  cancel-in-progress: true
```

The workflow has two jobs:

1. `build`, on pull requests and main pushes, with
   `permissions: {contents: read}`;
2. `deploy`, only when
   `github.event_name == 'push' && github.ref == 'refs/heads/main'`, needing
   `build`, with `permissions: {contents: write}`.

This prevents pull-request code from receiving a write-capable token.
Triggers exclude the `docs` publication branch, so the force push cannot
recursively start the workflow.

### 10.2 Action pinning and setup

Only official `actions/checkout` and `actions/setup-node` actions are used.
Pin the accepted v7 tags as resolved on 2026-07-25:

```yaml
- uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7
- uses: actions/setup-node@820762786026740c76f36085b0efc47a31fe5020 # v7
```

Future action updates require separately verifying the official tag and
reviewing the action diff before changing the immutable SHA and its release
comment. Floating tags, branches, abbreviated SHAs, and third-party Pages
actions are forbidden. A workflow-contract test enforces the exact pins and
permits no other `uses:` owner.

Both jobs use Ubuntu 24.04 and Node 24, with setup-node npm caching keyed by
`docs/package-lock.json`. Setup commands are explicit:

```console
npm ci
npm ci --prefix docs
npm --prefix docs exec playwright install --with-deps chromium
```

The build job runs root docs-related unit/contracts, theme `--check`,
`npm --prefix docs run check`, serves `docs/dist` on loopback using a
zero-dependency Node static server from the test harness, and runs the
Playwright browser suite. Browser installation is setup, not an ordinary
build action.

The deploy job checks out and rebuilds from the exact main commit after
`npm ci --prefix docs`; it does not trust or download a mutable artifact from
the build job. It reruns generated checks and `npm --prefix docs run build`.

### 10.3 Plain-Git orphan publication

Deployment uses shell and Git only. From a fresh temporary directory:

1. copy the contents of `docs/dist/`, including dotfiles, to the directory;
2. `git init --initial-branch=docs`;
3. set repository-local identity to
   `github-actions[bot] <41898282+github-actions[bot]@users.noreply.github.com>`;
4. add all output and create one commit
   `docs: deploy ${GITHUB_SHA}`;
5. add an authenticated origin using the workflow token without printing it;
6. `git push --force origin HEAD:docs`.

The published branch is orphaned and contains site output at branch root,
not a nested `docs/dist` directory. It contains `.nojekyll`, `index.html`,
`404.html`, assets, and route directories only. It contains no source,
node_modules, GitHub workflow, package file, or prior history. Force push is
allowed only for this generated `docs` branch and never for `main`.

No `actions/upload-pages-artifact`, `actions/deploy-pages`, peaceiris action,
or marketplace deployment action is used.

### 10.4 One-time repository setting

After the first successful branch publication, a repository administrator
must configure GitHub Pages once:

1. Repository **Settings → Pages**;
2. **Deploy from a branch**;
3. branch `docs`, folder `/ (root)`;
4. save and verify
   `https://mayanklahiri.github.io/virtualme/`.

The workflow must not call the GitHub Pages settings API. `contents: write`
can publish the branch but does not safely imply the administration permission
needed to mutate Pages source settings. The README maintainer section records
this one-time step and the expected URL.

## 11. Tests and test-first execution

### 11.1 Red-first pass

Before adding implementation files, add these tests against the contracts
below and run them to demonstrate failure:

1. `node --test test/docs-cli.test.js`;
2. `node --test test/themes-generated.test.js`;
3. `node --test test/docs-build-contract.test.js`;
4. `node --test test/docs-workflow.test.js`;
5. after `npm ci --prefix docs`,
   `npm --prefix docs run check`;
6. after explicit Playwright setup,
   `npm --prefix docs run test:browser`.

Tests may initially fail because files/routes do not exist. They must fail for
an assertion described here, not because of syntax errors in the tests.

### 11.2 Root Node tests

`test/docs-cli.test.js` covers:

- main/help flat registration and exact help rows;
- no subcommand and every invalid/duplicate/missing-value case;
- default and explicit host/port argv;
- build rejecting extra args;
- repository-root derivation independent of cwd;
- missing checkout and missing dependency messages without spawn;
- exact npm command, docs cwd, inherited stdio;
- propagation of 0, nonzero, spawn failure, and signal-derived status.

`test/themes-generated.test.js` uses temporary destinations and exported
generator seams to cover:

- schema rejection for every missing/unknown field class, duplicate ID,
  malformed color/unit/number, wrong order, and unknown CLI argument;
- deterministic bytes across two runs;
- no timestamp or absolute path;
- write mode changes only stale outputs;
- check mode is read-only and reports all stale files;
- exact eight-theme order, two variants, typography and all `--p1`…`--p8`;
- parity among controller CSS/registry/boot, docs CSS/registry, and source;
- current palette values preserved from the pre-refactor CSS fixture.

`test/docs-build-contract.test.js` is built-in-only and covers:

- package isolation, exact direct pins, lockfile v3 and integrity entries;
- root runtime dependencies unchanged;
- required tree/routes/content/frontmatter;
- no Astro source import/read outside docs;
- no remote resource references in authored source;
- base/site/trailing-slash/static Astro configuration;
- required generated provenance and placeholder/complete config validation;
- all `verify-build.mjs` assertions against disabled and test-enabled
  analytics builds;
- README↔site crosslink contracts and exact footer text.

`test/docs-workflow.test.js` parses the small YAML subset textually and covers:

- exact path filters, main-only push, pull requests, concurrency;
- build read and deploy write permissions;
- deploy event/ref condition and `needs: build`;
- full-SHA official checkout/setup-node pins only;
- Node 24 and both explicit npm installs;
- explicit Playwright setup;
- no Pages/marketplace deployment action;
- orphan init, exact branch/root copy, force push only to `docs`;
- no trigger for branch `docs` and no Pages-settings API request.

These tests join `node --test test/*.test.js` and therefore the canonical root
gate.

### 11.3 Docs build tests

`npm --prefix docs run check` performs generated check, Astro type/content
validation, a clean static build, 404 creation, and deterministic output
verification. The build-contract harness performs two isolated output builds:

- empty analytics ID;
- `PUBLIC_GA_MEASUREMENT_ID=G-TEST1234`.

It enumerates all HTML, not a sample. Invalid analytics ID is a separate
expected-failure test. A third build compares sorted file names and SHA-256
bytes with the first disabled build; the same tree must produce the same
output.

### 11.4 Browser/e2e checks

`docs/test/browser.test.mjs` uses the exact-pinned Playwright Chromium and a
built-in Node static server mounted as GitHub Pages would mount it at
`/virtualme/`. It never visits the public internet. It tests every expected
route at 1440×900 and 375×812, plus the home page at 320×700 and 1920×1080.

Assertions:

- each route returns expected content with no console error, page error,
  failed first-party request, or mixed/root-base asset request;
- primary navigation, guide CTA, blog welcome link, README/GitHub links,
  404 recovery links, and all internal fragments work;
- header, one `h1`, main, and exact footer exist;
- keyboard Tab reaches skip link, navigation, theme picker, CTAs, copy
  controls, snowglobe controls, and config controls in logical order;
- skip link moves focus to main; theme picker has correct pressed state and
  persistence after reload;
- all eight themes render in light and dark without missing computed token;
- body/document has no horizontal overflow at tested widths;
- mobile menu opens, traps no focus, closes with Escape, and restores focus;
- home snowglobe interaction reveals the same textual result with pointer and
  keyboard;
- `/about/` memory controls and `/configuration/` deep links restore focus;
- reduced-motion emulation yields no running CSS animation or nonzero smooth
  scroll behavior after page settle;
- hidden pages are absent from listed navigation and remain directly usable;
- each route can be screenshotted; screenshots are retained only on failure.

Do not use screenshot pixel goldens, accessibility services, Lighthouse, or
wall-clock performance thresholds. The deterministic DOM, computed-style,
request, overflow, resource-byte, and navigation assertions are the bounded
visual/quality gate.

### 11.5 Canonical gates

Update `scripts/check.sh` after root Node tests to:

```console
node scripts/generate-themes.mjs --check
npm --prefix docs run check
```

It assumes both root and docs setup have already run. It performs no install
and no network access. Browser tests remain in the docs workflow because the
browser binary is an explicit heavyweight setup artifact, not part of the
canonical offline root gate.

Update the existing `.github/workflows/ci.yml` check job to run
`npm ci --prefix docs` after root `npm ci` and before `npm run check`; its
container job continues to depend on that completed check. This is dependency
setup, not part of the canonical gate. The CI workflow path is therefore an
implementation/reconciliation file even though publication remains isolated
in `docs.yml`.

Update `.github/workflows/release.yml` wherever it runs
`CHECK_SKIP_GO=1 npm run check` (currently the npm-publish job) to run
`npm ci --prefix docs` first. `CHECK_SKIP_GO` skips controller Go work, not
documentation checks. Release verification must not fail from an absent docs
toolchain or silently omit the public-site contract.

Final verification order:

```console
npm ci
npm ci --prefix docs
npm run check
./cli.sh docs build
npm --prefix docs run test:browser
```

Then verify `git status --short` shows no build or generator drift.

## 12. Documentation and `/master-update` reconciliation

Run `/master-update` only after implementation and tests are green. Its
reconciliation must update:

- root `README.md`: documentation link under the title, docs commands/setup,
  production URL, one-time Pages branch/root setup, workflow summary, theme
  shared-source note, analytics edit location/default-disabled behavior, and
  spec table rows for 030 and 031 when 031 exists;
- `AGENTS.md`: `docs/`, `common/themes/`, generator, docs commands, workflow,
  shared-theme contract, and spec 030 row. Add this normative instruction:
  all authored documentation-site content and site-owned assets must remain
  below `docs/`; Astro sources must not import outside that tree; shared
  themes and config docs enter only through checked generated files under
  `docs/src/generated/` or `docs/src/styles/`;
- develop skill: dual npm setup, offline docs gate, CLI docs command,
  generator/check mode, docs workflow and Pages branch;
- master-update skill: inspect both package manifests/locks, generated-theme
  drift, docs routes/build, analytics config, crosslinks, and screenshots
  without moving assets outside `docs/`;
- operate skill only where the public guide changes operational facts; do
  not turn it into a site-development guide.

Run `bash scripts/refresh-doc-screenshots.sh` only if implementation changes
the controller's visible UI. Shared-token extraction must preserve appearance,
so a pure extraction does not justify recapturing identical screenshots.
Always run `bash scripts/update-doc-images.sh` after confirming screenshot
paths, then verify it creates no unintended README diff.

The reconciliation pass checks all public product statements against current
code/specs. It removes no trust-model warning and invents no feature,
benchmark, energy measurement, release date, or compatibility promise.

## 13. Acceptance checklist

- [x] The separate append-only spec 001 carveout authorizes the isolated docs
      package without weakening root zero-runtime-dependency or offline-gate
      rules.
- [x] Red-first outputs are recorded for CLI, themes, build, workflow, and
      browser contracts before implementation; the execution amendment
      records the final green commands.
- [x] `docs/package.json` and lock are isolated, exact-pinned, reproducible
      with `npm ci --prefix docs`, and root runtime dependencies remain empty.
- [x] `./cli.sh docs dev` and `./cli.sh docs build` meet every parsing, cwd,
      setup, spawn, and exit-status rule from §3.
- [x] Ordinary docs build and `npm run check` complete with network disabled
      after explicit root/docs setup.
- [x] One validated JSON source generates byte-deterministic controller/docs
      CSS and registries; `--check` catches drift; all eight themes, both
      variants, typography, semantics, and `--p1`…`--p8` are in parity.
- [x] Existing controller visual tokens are unchanged by extraction, and the
      controller pre-paint/theme picker contains no duplicate handwritten
      theme allowlist.
- [x] Expected listed, hidden, blog, welcome, and 404 routes build beneath
      `/virtualme/`; all internal links/assets and canonicals are base-correct.
- [x] Home delivers the responsive memory-snowglobe concept and concrete
      product path; the guide can be completed in about five minutes by a
      casual technical user.
- [x] `/about/` and `/no-more-bills/` are usable but absent from listed
      navigation; the latter includes every required narrative point and no
      unsupported exact energy/cost claim.
- [x] Blog collection, reverse-chronological index, post route, metadata,
      table of contents, and initial welcome post work with JavaScript off.
- [x] Empty analytics emits no analytics code/request hint in any HTML;
      `G-TEST1234` emits one valid loader/config pair in every HTML; invalid
      IDs fail; edit location is documented as `docs/src/config/site.ts`.
- [x] Every generated page has the exact linked copyright
      `© 2026 Mayank Lahiri`; README and docs crosslink in both directions.
- [x] Keyboard, focus, contrast, 200% zoom, 320 px layout, reduced motion,
      no-script content, and touch controls satisfy §7 and deterministic
      browser assertions.
- [x] Build output meets route, reference, resource, size, hermeticity,
      determinism, `.nojekyll`, and 404 contracts in §9.
- [x] Pull requests build/test only with read permission; main pushes rebuild
      and force-publish branch-root output to orphan `docs` with write
      permission; actions use audited full SHAs and no Pages action.
- [ ] Repository Pages is manually configured once for `docs` `/ (root)` and
      `https://mayanklahiri.github.io/virtualme/` serves the deployed commit.
- [x] Spec 031 has replaced the placeholder with an export containing
      `schemaSha256`; every
      configuration section/option is deep-linkable and exposes exemplar
      YAML, values/constraints, defaults, examples, details, and tradeoffs.
- [x] `npm run check`, `./cli.sh docs build`, and the Playwright browser suite
      pass, and a final `git status --short` shows no generated/build drift.
- [x] `/master-update` completes the §12 reconciliation and all links/spec
      indexes/skills match repository ground truth.

## Amendments

### 2026-07-25 — Execution evidence

Implementation followed the required three-pass order.

#### Red-first evidence

All failures were contract failures against syntactically valid tests:

| Command | Red result |
|---|---|
| `node --test test/docs-cli.test.js` | Exit 1; 0/3 passed. Registration assertion failed and `src/commands/docs.js` was absent. |
| `node --test test/themes-generated.test.js` | Exit 1; 0/3 passed because `scripts/generate-themes.mjs` was absent. |
| `node --test test/docs-build-contract.test.js` | Exit 1; 1/4 passed. The package/config/routes and README crosslink were absent. |
| `node --test test/docs-workflow.test.js` | Exit 1; 0/3 passed because `.github/workflows/docs.yml` was absent. |
| `npm --prefix docs run check` | Exit 1 because the required `docs/scripts/ensure-generated.mjs` build entrypoint was absent. |
| `npm --prefix docs run test:browser` | Exit 1; 0/2 passed because site output/routes and the theme control were absent. |

Explicit setup used `npm install --prefix docs` to create the accepted lock and
install the exact-pinned toolchain, followed by
`npm --prefix docs exec playwright install chromium`. Those were the only
non-package public downloads. Ordinary builds and all gates were subsequently
offline.

#### Implementation notes and deviations

- Spec 031 had not executed, so the exact `pending-spec-031` artifact from §8.1
  was installed. No 031 schema, exporter, controller config package, or option
  content was added.
- Astro 7 treats `404.astro` specially and directly emits `dist/404.html`
  instead of `dist/404/index.html`. `make-404.mjs` therefore rewrites the
  emitted file's canonical in place. The resulting branch-root 404, base-aware
  links/assets, canonical, and deterministic output match the acceptance
  contract; no rendered source route is deleted.
- Shared-token extraction expanded three-digit source colors to the canonical
  six-digit JSON form without changing their values or controller appearance.
  No controller screenshot refresh was required. The mandatory
  `bash scripts/update-doc-images.sh` reconciliation completed with the
  existing sole screenshot source under `docs/src/screenshots/`.

#### Final verification evidence

| Command | Result |
|---|---|
| `npm ci` | Exit 0; 80 root development packages installed. npm reported one pre-existing high-severity development-tree audit finding; installation and gates were unaffected. |
| `npm ci --prefix docs` | Exit 0; 289 documentation packages installed, 0 vulnerabilities. |
| `npm run check` | Exit 0; locality, ESLint, typecheck, 99 Node tests, generated themes, Astro check/build, 9-route/21-file output verification, CLI dry run, generated controller SPA, gofmt, vet, and all Go tests passed. |
| `./cli.sh docs build` | Exit 0; 9 static routes built and `verify-build` reported 9 HTML / 21 total files. |
| `npm --prefix docs run test:browser` | Exit 0; 4/4 Playwright suites passed across required routes/viewports, keyboard/focus interactions, all 16 theme combinations, persistence, hidden routes, overflow, and reduced motion. |
| `bash scripts/update-doc-images.sh` | Exit 0; all three README screenshot markers remained wired to `docs/src/screenshots/`. |

Two acceptance items intentionally remain open. Repository Pages requires the
documented one-time administrator action after the workflow first publishes
the orphan branch; this execution did not contact GitHub or mutate repository
settings. The complete generated configuration reference remains blocked on
accepted, unimplemented spec 031 exactly as §8 requires.

### 2026-07-25 — Post-031 implementation-audit remediation

After specs 031–033 executed, a read-only audit found that the generated
configuration artifact was current but the public page rendered only a subset
of its fields. The remediation now renders choices, constraints, secret
policy, examples, tradeoffs, links, and local-console deep links; adds desktop
and mobile section navigation, focusable fragments, active-section state, and
literal query filtering; and closes the spec-031 acceptance item above.

The same audit removed the docs script's duplicate authored theme-ID registry,
made analytics verification accept any valid committed or environment ID,
completed static-output resource/fragment/budget checks, expanded browser
accessibility and interaction coverage, added exporter fidelity checks to both
documentation workflow jobs, and completed POSIX signal propagation for the
source-checkout CLI. The hard-coded Markdown base path was replaced by a
base-aware generated link.

Browser contrast checks exposed three pre-existing light-mode accent pairs
below WCAG AA: Arctic, Solar, and Studio. Their canonical accents were darkened
to the existing `p1` shades (and Solar's accent foreground changed to white),
then all five generated theme outputs were regenerated. This deliberately
changes those three extracted tokens after the original extraction-only
acceptance while preserving the one-source generation contract.

The manual GitHub Pages item remains open. This remediation did not commit,
push, deploy, contact an external service, or alter repository settings.

Final remediation verification:

| Command | Result |
|---|---|
| `node --test test/docs*.test.js` | Exit 0; all 11 docs CLI/build/workflow contracts passed. |
| `npm --prefix docs run check` | Exit 0; Astro check and the 9-route/21-file verified static build passed. |
| `./cli.sh docs build` | Exit 0; source-checkout build and complete output verification passed. |
| `npm --prefix docs run test:browser` | Exit 0; all 5 expanded desktop/mobile, focus, interaction, theme, configuration, no-script, zoom, target-size, and reduced-motion suites passed. |
| `npm run check` | Exit 0; all canonical gates, 108 Node tests, docs build, SPA build, and Go tests passed. |
| `git diff --check` | Exit 0. |
