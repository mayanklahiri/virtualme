# Spec 017: Jiggler — Humanlike OS-Level Mouse Motion

| | |
|---|---|
| Status | Executed (2026-07-23) |
| Depends on | `specs/008-browser-agent.md` (xdotool actuation, Runner injection), `specs/013-job-queue-scheduler.md` (Valkey client; Status page conventions) |
| Produces | A controller-internal "jiggler" service producing bursty, erratic, humanlike mouse trajectories on the Xvfb display via `xdotool` (never CDP); a Status-page on/off switch persisted in Valkey; automatic yielding to agent actuation |
| Followed by | Future specs |

## 0. Executor instructions

- Constitution binds: Go stdlib only; actuation exclusively via `xdotool` subprocess (`Runner`-injected for tests); CDP must not be touched by this feature at all.
- The trajectory model in §2 is normative — implement the equations as written; tune only the constants marked tunable.
- Stop-on-red; finish with §7 Acceptance.

## 1. What it is

A background service that, at random intervals, moves the OS cursor along a realistic humanlike path to a random on-screen target: a stochastic, elongated, gently spiraling approach with a distinct overshoot-and-correct finish. Purpose: the virtual desktop should exhibit ambient human-scale input rather than a surgically still cursor. It runs inside the controller process (no new s6 service, no new layer — `xdotool` is already installed by layer 004).

Ground truth model (web research, 2026-07): the classic WindMouse algorithm (gravity + decaying random wind) produces curved paths but fails Fitts's law and yields jagged velocity; modern human-motion synthesis (SigmaDrift, pydoll's humanized mouse, minimum-jerk literature: Flash & Hogan 1985, Harris & Wolpert 1998) layers (a) Fitts-law movement time, (b) a bell-shaped minimum-jerk velocity profile, (c) a ballistic phase covering ~93% of distance plus 0–2 corrective sub-movements, (d) signal-dependent noise and 8–12 Hz tremor, (e) overshoot with correction. The jiggler implements a compact version of that synthesis.

## 2. Trajectory model — `controller/internal/jiggler/trajectory.go`

`func Trajectory(from, to Point, width, height int, rng *rand.Rand) []TimedPoint` where `TimedPoint{X, Y int; DelayMS int}`. Pure function (fully testable, no I/O). Construction:

1. **Duration (Fitts)**: `D = dist(from,to)`, `W = 40` (nominal target width px, tunable). `MT = a + b*log2(D/W + 1)` with `a = 120 ms`, `b = 160 ms`; multiply by a lognormal jitter factor `exp(N(0, 0.08))` (≈8% CV). Clamp MT to [250 ms, 2200 ms].
2. **Path shape (elongated spiral)**: base path is a cubic Bézier from `from` to `to`. Control points: `c1 = from + 0.30*(to-from) rotated by θ1`, `c2 = from + 0.72*(to-from) rotated by -θ2`, where `θ1 ~ U(8°, 28°)`, `θ2 ~ U(4°, 14°)` and both rotations share a random handedness (same sign) — same-sign offsets of decaying magnitude are what make the path read as an elongated spiral arc that straightens as it approaches the target, rather than an S-curve.
3. **Velocity (minimum jerk)**: sample the Bézier at arc-length parameter `s(τ) = 10τ³ − 15τ⁴ + 6τ⁵` (the minimum-jerk position profile) for `τ = t/MT`, emitting a point every 12–18 ms (gamma-jittered inter-sample delay, shape 8, mean 15 ms — non-constant polling like real hardware).
4. **Noise**: add to each sample (i) signal-dependent noise: Gaussian offset with σ proportional to instantaneous speed (`σ = 0.012 * speed_px_per_s`, capped 3 px) perpendicular to travel; (ii) tremor: `1.2 px * sin(2π * f * t + φ)` with `f ~ U(8, 12) Hz`, amplitude suppressed by 70% while `τ ∈ [0.15, 0.85]` (tremor shows at slow start/finish, not mid-ballistic).
5. **Overshoot + correction**: with probability 0.7, the ballistic phase targets `to + overshoot` where `overshoot = (4–12% of D, along the travel direction, ±10° jitter)`, then append a corrective sub-movement: a second, small minimum-jerk segment from the overshoot point back to `to` with `MT₂ ~ U(90, 180) ms`. With probability 0.25 add a second tiny correction (≤4 px). Final point is always exactly `to`.
6. Clamp every point to `[2, width-3] × [2, height-3]`.

Hermetic tests: monotonic time; end point exact; total duration within clamps; path length ≥ straight-line and ≤ 1.35×; velocity profile unimodal within tolerance (allow the correction segment its own small peak); with a seeded `rng`, byte-stable output (determinism for tests only — production seeds from `crypto/rand`).

## 3. Service — `controller/internal/jiggler/jiggler.go`

`type Service struct` with `New(runner agent-style Runner, valkey *valkey.Client, broadcast func([]byte), width, height int)`.

- **Burst cadence**: loop — sleep `U(45 s, 4 min)`; then perform a **burst** of `1–4` movements (geometric, p=0.45) to random targets biased toward the content area (uniform over the middle 80% of the screen, occasional `p=0.15` edge/corner excursion), with `U(300 ms, 1.5 s)` pauses between movements in a burst. This is the "bursty erratic" requirement: long silences punctuated by short flurries.
- **Actuation**: for each `TimedPoint`, `xdotool mousemove <x> <y>` then sleep `DelayMS`. Do NOT batch into one `xdotool` invocation with `--sync`/`--delay` (per-point invocation is simpler and honest about jitter). `DISPLAY` comes from `VM_DISPLAY`.
- **Yielding**: never move while the agent might act. Reuse the actuation lock: add package `controller/internal/actuation` exposing a global `sync.Mutex`-like `Lock()/TryLock()/Unlock()`; agent tools in `tools.go` take it for the duration of each xdotool-using tool call; the jiggler `TryLock()`s before each burst and holds it for the burst, skipping the burst entirely if busy. Additionally the jiggler consults the jobs Manager: if any job is running, skip the burst (belt and braces — an idle cursor during work is fine; a fighting cursor is not).
- **Enable state**: Valkey string `virtualme:jiggler:enabled` = `"1"`/`"0"`, default `"0"` (off — a self-moving cursor should be opt-in). Read at startup, cached, updated by WS.
- Current position for `from`: `xdotool getmouselocation --shell` parsed; on failure use screen center.

## 4. WS + Status page switch

1. Client → server `{"type":"jiggler-set","enabled":true|false}` → persist, apply, broadcast. Server state: extend the 2 s `state` snapshot with `"jiggler": {"enabled": bool}`.
2. Status page: in the same `.system-grid` region (after the Active-time-selectors card from spec 013), add:

```html
<article class="metric jiggler-card">
  <div><label id="jiggler-label">Jiggler <span class="metric-caption">ambient humanlike mouse motion</span></label>
  <button id="jiggler-switch" role="switch" aria-checked="false" aria-labelledby="jiggler-label" type="button"><span class="knob"></span></button></div>
</article>
```

3. CSS: a proper switch component (this is the repo's first; build it reusably as `.switch`): 44×24 px track (`--border` background unchecked, `--accent` checked), 18 px `--surface` knob, `transform: translateX(...)` transition gated on `--motion` and `prefers-reduced-motion`. Focus ring via `outline`. `render.js` (or `app.js` dispatch) sets `aria-checked` from `state.jiggler.enabled`; click sends `jiggler-set` with the toggled value (optimistic UI is forbidden — wait for the next `state` frame to flip, so the switch always reflects server truth).
4. Persistence map amendment (spec 007 §1a Amendments): `virtualme:jiggler:enabled` — jiggler switch — Valkey AOF.

## 5. Feed the activity ledger

One `activity` event (spec 015) per burst: `kind:"tool", name:"jiggle", summary:"jiggler: N movements"` — the Jobs page then shows ambient motion honestly. Skipped bursts record nothing.

## 6. Tests and docs

- Hermetic: trajectory tests (§2), service tests with a fake Runner capturing `xdotool` argv (burst yields under held lock; disabled state produces zero calls; enable/disable round-trips Valkey via the fake RESP server).
- e2e: with the container running, WS `jiggler-set true`, then poll `docker exec xdotool getmouselocation` twice 60 s apart — positions must differ; `jiggler-set false`, verify stable for 60 s. Gate this probe behind `E2E_JIGGLER=1` (it costs ≥2 min wall time).
- Docs: `/master-update` — operate skill (what the switch does, default off, where the toggle lives), develop skill (`internal/jiggler`, `internal/actuation`, switch component reuse note).

## 7. Acceptance checklist

- [x] `npm run check` green; no CDP references anywhere under `controller/internal/jiggler/`.
- [x] Switch on the Status page toggles, survives page reload and container restart (Valkey), and reflects server truth (no optimistic flip).
- [x] Watching `/desktop-view` with the jiggler on: motion appears in occasional short flurries; paths are curved with a visible terminal correction; the cursor never moves while an agent task is running.
- [x] Seeded-trajectory unit tests pass and assert the §2 invariants.
- [x] Activity ledger shows `jiggle` events.

## Amendments

### 2026-07-23 — Same-side controls and testable first movement

Section 2's prose requires both Bézier control offsets to use the same side of
the travel vector so the arc straightens rather than forming an S-curve. That
behavior governs the contradictory `-θ2` notation: production uses one random
handedness and rotates both control vectors to that side.

Enabling the jiggler schedules its first burst after three seconds; later
bursts retain the normative 45-second-to-four-minute silence. This makes the
opt-in immediately observable and makes the gated 60-second motion probe
deterministic. Disabling during a burst stops further points promptly.

The console already had a project-specific switch from spec 014. Spec 017
promotes it to the reusable `.switch`/`.knob` component and uses that component
for both projects and the Status-page jiggler control.

### 2026-07-24 — Always-on default, faster cadence, no queue yield

Live-usage feedback: the jiggler should behave like a person is always at the
desk, not like a occasionally-permitted background task.

1. **Default enabled.** `Start` now treats an absent
   `virtualme:jiggler:enabled` key as **on**; only a persisted literal `"0"`
   disables. A fresh data directory therefore jiggles immediately, and an
   explicit Status-page opt-out still round-trips across restarts (e2e
   restart assertion unchanged: enable→disable persists `"0"`).
2. **Burst cadence 8–27 s.** The inter-burst silence window drops from
   45 s–4 min to a uniform 8–27 s, making ambient motion near-constant.
3. **No queue yield.** The `jobs.IsRunning()` guard is removed entirely (from
   both the burst gate and mid-trajectory checks), along with
   `SetJobManager`; queued or running LLM work no longer pauses ambient
   motion. The only remaining yield is the agent's xdotool actuation lock
   (`actuation.TryLock()`), which prevents the jiggler from corrupting
   in-flight agent mouse/keyboard actuation; a held lock skips that burst and
   the loop retries on the next cadence tick.

Unit tests cover the enabled-by-default load, the explicit-"0" opt-out, the
8–27 s cadence bounds, and the actuation-lock yield.

### 2026-07-24 — Jiggler pane becomes Quick Options (spec 026 S4)

The Status-page Jiggler card is renamed "Quick Options" and hosts multiple
labelled switches with hover/focus help text: the jiggler switch (unchanged
semantics and persistence) and the spec 013 scheduler-pause switch.
