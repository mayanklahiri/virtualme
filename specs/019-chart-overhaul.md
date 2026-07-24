# Spec 019: Time-Series Chart Overhaul — Ticks, Titles, Lookback Control, Uniform Color

| | |
|---|---|
| Status | Executed (2026-07-23) |
| Depends on | `specs/005-console-ui.md` (charts, themes, lookbacks), `specs/011-ui-refresh.md` (theme token set `--p1`–`--p8`) |
| Produces | A reusable chart component in `controller/web/static/js/chart.js` with bounded boundary-aligned x-axis ticks and locale-aware labels; per-chart economist-style titles placed left of a compact right-aligned labelled lookback button group (desktop) or above it (mobile); the `--p1`–`--p8` series ramp applied uniformly to every multi-series chart (fixing CPU per core); timestamp-true hover hit-testing |
| Followed by | `specs/018-gpu-observability.md` (GPU chart consumes this component — execute this spec BEFORE 018) |

## 0. Executor instructions

- Constitution binds; SPA-only spec (plus one server constant check) — no Go behavior changes.
- All colors via CSS custom properties; re-read them on `themechange` (existing pattern).
- The live inspection of a running build showed the failure modes to avoid: 11+ x labels at the 1h lookback overlapping on desktop and illegible at 375 px; a 16-pill per-core legend; CPU in a blue-alpha ramp while memory used `--p*`; the lookback pill row wrapping raggedly on mobile. Every one of those is addressed below; regressions on any of them fail acceptance.
- Stop-on-red; finish with §7 Acceptance.

## 1. Requirement mapping (user bullets → sections)

| Bullet | Resolution |
|---|---|
| (a) "no x axis labels" / (b) "incorrect bucketing … do not exceed reasonable divisions, set ticks and labels accordingly, local-i18n short date times" | Read together: today's committed code draws **zero** labels and the live build drew **too many**. The normative behavior is §2: few, boundary-aligned, locale-short labels. |
| (c) lookback right-aligned compact labelled group | §3 |
| (d) economist-style titles left of selector (desktop) / above (mobile) | §3 |
| (e) uniform colors; memory-per-process is the reference | §4 |

## 2. X-axis: bucketing, ticks, labels

All in `chart.js`; the y-axis `axes()` gridline/label behavior is unchanged except it is renamed as part of the refactor in §5.

1. **Tick count bound**: maximum 5 labelled ticks per chart regardless of width ≥ 480 px; at canvas widths < 480 px, maximum 3. Never more.
2. **Boundary alignment**: choose the tick step from this table (smallest step whose resulting tick count ≤ the bound), then place ticks at instants that are exact multiples of the step in **local time** (e.g. step 15 min ⇒ :00/:15/:30/:45; step 6 h ⇒ 00:00/06:00/12:00/18:00 local; step 1 d ⇒ local midnights):

| Lookback | Candidate steps |
|---|---|
| 15m | 5 min |
| 1h | 15 min |
| 3h | 30 min, 1 h |
| 12h | 2 h, 3 h |
| 1d | 4 h, 6 h |
| 3d | 12 h, 1 d |
| 7d | 1 d, 2 d |
| 30d | 7 d, 10 d |

3. **Label format — locale/i18n short forms** via `Intl.DateTimeFormat(undefined, opts)` (the user's browser locale, not a hardcoded format):
   - step < 1 d: `{hour:"numeric", minute:"2-digit"}`;
   - step ≥ 1 d and lookback ≤ 7d: `{weekday:"short", day:"numeric"}`;
   - lookback = 30d: `{month:"short", day:"numeric"}`.
   Construct each formatter once per draw, not per tick.
4. **Rendering**: 10 px `--font-body`, `--muted` fill, centered under the tick; a 4 px `--border` tick mark from the plot floor; first/last labels clamped inside the plot box (`textAlign` start/end at the edges). `PAD.bottom` is already 24 px — sufficient; do not grow it.
5. **Bucketing correctness**: the drawn bar/sample buckets themselves must not change with this spec (server `resSec` drives them); what changes is only ticks/labels. However fix the visual defect where the trailing bucket renders much wider: in `barBounds`, clamp the last sample's right edge to `sample.ts + resSec*500` (it already does for interior samples; the current `halfBucket` fallback is correct — the live artifact came from gap-splitting; verify with a synthetic gap fixture and leave a regression test comment).
6. **Hover hit-testing**: replace the index-ratio lookup (`Math.round(ratio * (samples.length-1))`) with timestamp mapping: convert cursor x back to a timestamp via the inverse of `scales().x`, then binary-search the nearest sample by `ts`. Tooltip timestamp switches from `toLocaleString()` to the same short formatter family (`{dateStyle:"short", timeStyle:"medium"}`).

## 3. Chart header: title + lookback control

1. **Markup** (in `index.html`, replacing the bare `<figcaption>` and the single shared `#lookback` div): each chart becomes

```html
<figure class="chart">
  <div class="chart-head">
    <div class="chart-title"><h3>CPU load</h3><p>per core, stacked percent</p></div>
    <div class="lookback" role="radiogroup" aria-label="Time window" data-lookback></div>
  </div>
  <canvas …></canvas><ul … class="legend"></ul>
</figure>
```

Economist-style titles: a short bold declarative title (`<h3>`, `--font-heading`, ~0.95 rem) with a muted one-line subtitle stating units/series (`<p>`, 0.8 rem, `--muted`). Titles for the two existing charts: **CPU load** / `per core, stacked percent`; **Memory** / `per process, stacked MiB`. (GPU chart title comes from spec 018.)
2. **One shared lookback state, multiple controls**: `initCharts` builds the same radio-group buttons into every `[data-lookback]` container and keeps them in sync (single `select()` updates all groups + `localStorage` `vm-lookback`, exactly the current persistence). The old page-level `#lookback` element is removed.
3. **Compact right-aligned labelled group**: `.chart-head { display:flex; justify-content:space-between; align-items:end; gap:var(--gap) }`. The lookback group: segmented buttons (no gaps: shared 1 px `--border`, radius only on the group ends), 0.7 rem text, 2px 8px padding; selected = `--accent` bg + `--accent-fg`. Prepend a static label element inside the group container: `<span class="lookback-label">Window</span>` (0.7 rem, `--muted`, margin-right) — this is the "label of what it is". Add `title` attribute with the long form (e.g. `Last 12 hours`) per button and `aria-label="Time window: last 12 hours"`.
4. **Mobile** (`max-width: 47.999rem`): `.chart-head` becomes `flex-direction: column; align-items: stretch` — title above, control below, control buttons `flex:1` in a single row that fits 375 px without wrapping (8 buttons × ~40 px works at 0.65 rem; verify — if not, drop per-button horizontal padding to 4 px; wrapping to a ragged second row is the failure mode, a clean single row or an even 2×4 grid via `display:grid; grid-template-columns:repeat(4,1fr)` are both acceptable).

## 4. Uniform series color

Reference behavior: the memory chart's `css(\`--p${i+1}\`)` per-series fill (correct today).

1. **CPU per core**: replace the `--accent` + `globalAlpha` scheme with the `--p1`–`--p8` ramp: core *i* uses `--p{(i % 8)+1}`. For >8 cores, second cycle draws at `globalAlpha 0.55` (deterministic, theme-consistent). Legend swatches use the same rule; delete the `.swatch.core` opacity hack from `app.css` and `updateLegends`.
2. **Legend density**: when series count > 8 (the 16-core case from the live inspection), collapse the legend to `cpu0 … cpu15` with a single row: show first 4 pills, an ellipsis pill `+12 more` that expands on click (`<details>`-style toggle). Memory legend (8 entries) stays as-is.
3. **Every future chart** (GPU in 018, any others) must take colors exclusively from `--p*`; note this rule in the develop skill during the docs pass.

## 5. Refactor for reuse

`initCharts` currently hand-wires exactly two canvases. Restructure minimally (no framework): extract `makeChart({canvas, legend, title, series(sample) → number[], seriesNames() → string[], unit, maxFn})` returning `{draw}`, with shared tick/axis/scale/hover/legend code; `initCharts` composes CPU + memory (+ GPU when spec 018 lands) and keeps the single lookback/request/replace/push state machine. Public return API (`status/replace/push/draw`) is unchanged so `app.js` needs no edits beyond markup ids.

## 6. Tests and docs

- `scripts/check.sh` has no JS unit runner for the SPA; keep it that way but make the tick chooser a pure exported function `chooseTicks(firstTs, lastTs, widthPx, lookback) → {stepMs, ticks:[ts]}` and add a Node test `test/chart-ticks.test.js` importing it directly from `controller/web/static/js/chart.js` (Node ≥22 runs browser-free ESM; the function must not touch `document` — pure math). Table-test: every lookback at 375/900/1600 px yields ≤3/≤5/≤5 labels, all boundary-aligned in a fixed zone (inject a `Date`-free pure computation using a timezone offset parameter; keep `Intl` usage outside the pure function).
- e2e: SPA asset checks unchanged; visual verification is manual per acceptance.
- Docs: `/master-update` — develop skill (chart component contract, `--p*` rule), README screenshots if any exist (none committed today).

## 7. Acceptance checklist

- [x] `npm run check` green including the new tick test.
- [x] Desktop 1600 px, every lookback: ≤5 x labels, aligned to natural boundaries, locale-short format; 375 px: ≤3 labels, nothing overlaps.
- [x] Each chart shows its title/subtitle left of (desktop) or above (mobile) a right-aligned segmented lookback group labelled "Window"; the group does not wrap raggedly at 375 px.
- [x] CPU and memory charts draw from the same `--p1`–`--p8` ramp in all eight themes × light/dark; the 16-core legend collapses behind `+N more`.
- [x] Hover tooltip tracks true timestamps across a data gap (synthetic gap: stop container 2 min, restart, inspect 15m lookback).
- [x] Lookback selection persists across reload and applies to all charts simultaneously.

## Amendments

### 2026-07-23 — Execution after spec 018

Spec 018 had already landed under its direct-execution amendment, so this
execution integrated the existing GPU chart into the reusable component
alongside CPU and memory instead of leaving GPU as a future composition step.
Its utilization bars, memory overlay, dual scale, and first-state visibility
remain unchanged.

The built SPA was inspected at 1600 px and 375 px. Deterministic browser-free
tests cover every lookback/width tick bound and local boundary alignment,
server-resolution trailing buckets across a synthetic gap, and timestamp
nearest-sample selection across that gap. This replaces the destructive
stop/restart form of the manual gap fixture while asserting the same chart
primitives directly.
