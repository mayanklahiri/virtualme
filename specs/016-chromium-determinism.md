# Spec 016: Chromium Determinism and Single Full-Screen Window

| | |
|---|---|
| Status | Executed (2026-07-23) |
| Depends on | `specs/006-desktop-reliability.md` (supervision, sandbox fallback, watchdog), `specs/012-agent-observation-soak.md` (openbox kiosk config, watchdog geometry) |
| Produces | A grounded, per-flag-documented Chromium launch optimized for deterministic browser automation; a fast predictable startup; a hard guarantee of exactly one full-screen Chromium window (no overlapping windows); a documented — but unimplemented — door to a 2×2 four-window tiling layout |
| Followed by | Future specs |

## 0. Executor instructions

- Constitution binds. Layers are append-only: this spec edits only `docker/rootfs/` service scripts and configs — no existing `docker/layers/*.sh` may change, and no new packages are needed.
- Every flag added to the launch line must keep the inline rationale comments specified in §2 — the run script is the documentation of record.
- Verify against the soak suite (spec 012 flows must still pass) and the new checks in §6.

## 1. Grounding (web research, 2026-07)

Sources: the Chromium `content_switches.cc` flag definitions, the chrome-launcher project's `chrome-flags-for-tools.md` (the de-facto canonical list for automation), the k6 browser module's default automation argument set, and ChromeDriver release notes (which recently disabled occluded-window backgrounding for all driver sessions because it caused nondeterministic throttling).

Consensus for deterministic automation:

- **Anti-throttling trio** (already present in our run script, keep): `--disable-background-timer-throttling`, `--disable-backgrounding-occluded-windows`, `--disable-renderer-backgrounding`. Without these, timers and process priorities change based on window occlusion — nondeterministic by definition.
- **`--enable-automation`**: disables a set of behaviors "not appropriate for automation" (prompts, some background services); the standard marker used by ChromeDriver/Puppeteer/Playwright.
- **`--disable-background-networking`**: kills component updater, safe-browsing pings, variations/field-trial fetches, and other background requests that perturb timing and network observations.
- **Field trials off**: `--disable-field-trial-config` — variations otherwise make two identical containers behave differently.
- **First-run/default-browser/session-restore suppression** (already present, keep): `--no-first-run`, `--no-default-browser-check`, crash-restore scrubbing.
- **Feature disables**: extend the existing `--disable-features=` list with `Prewarm` (prewarmed pages interfere with CDP target discovery — ChromeDriver now disables it by default), `OptimizationHints`, `MediaRouter`, `InterestFeedContentSuggestions`, `CalculateNativeWinOcclusion` is Windows-only (do not add).
- **What NOT to add**: the `--deterministic-mode` meta-flag family (`--run-all-compositor-stages-before-draw`, `--enable-begin-frame-control`, …) is for headless screenshot determinism with DevTools-driven BeginFrames; it fights a live interactive desktop. `--kiosk` is rejected because the agent navigates via the omnibox (`ctrl+l`), which kiosk mode hides. `--deterministic-fetch` serializes network fetches and badly slows real pages.

## 2. `svc-chromium/run` changes

Edit `docker/rootfs/etc/s6-overlay/s6-rc.d/svc-chromium/run`. The final exec becomes (order and comments must be preserved; `SANDBOX_FLAGS` sourcing unchanged):

```bash
exec chromium \
  "${SANDBOX_FLAGS[@]}" \
  --user-data-dir="$PROFILE" \
  --class=chromium \
  # Automation marker: disables consumer behaviors inappropriate for driving by tools.
  --enable-automation \
  # Startup determinism: no first-run UX, no default-browser prompt, no crash-restore UI.
  --no-first-run \
  --no-default-browser-check \
  --disable-session-crashed-bubble \
  --hide-crash-restore-bubble \
  # No background work: component updates, safe-browsing pings, variations fetches.
  --disable-background-networking \
  --disable-component-update \
  --disable-field-trial-config \
  # Anti-throttling: timers and process priority must not depend on occlusion/focus.
  --disable-background-mode \
  --disable-background-timer-throttling \
  --disable-backgrounding-occluded-windows \
  --disable-renderer-backgrounding \
  # Features that inject nondeterminism or surprise UI into automation.
  --disable-features=InfiniteSessionRestore,Translate,Prewarm,OptimizationHints,MediaRouter,InterestFeedContentSuggestions \
  # Quiet chrome: no info bars or modal error dialogs stealing input focus from xdotool.
  --noerrdialogs \
  --disable-infobars \
  # Predictable auth/password plumbing (no OS keyring probe at startup).
  --password-store=basic \
  # CDP, loopback only (observation-only per spec 008).
  --remote-debugging-port=9222 \
  --remote-debugging-address=127.0.0.1 \
  # Geometry: openbox + watchdog own the window; ask for max as a hint.
  --start-maximized \
  --window-position=0,0 \
  about:blank
```

Notes for the executor:

- Bash does not allow comments inside a line-continued command; implement by building an array `FLAGS=( … )` with comment lines between array elements, then `exec chromium "${SANDBOX_FLAGS[@]}" "${FLAGS[@]}" about:blank`.
- Do NOT add `--disable-extensions` (none are installed; adding it changes nothing but lengthens the line) and do NOT remove `--disable-gpu` handling — it lives in `chromium-sandbox.sh` and is out of scope.
- If the pinned Debian Chromium rejects any flag (unknown switch is silently ignored by Chromium, so this is about `--disable-features` values only), keep the value anyway: unknown feature names are ignored harmlessly. Verify no startup error in `virtualme logs`.

**Startup speed/predictability check**: after the change, Chromium must reach "CDP `/json` answers with one page target" in a bounded, repeatable time. Add to `svc-chromium-watchdog/run` (or a new `test` assertion in smoke, §6) nothing at runtime — measure in the smoke test instead: three consecutive container starts must each expose a CDP page target within 15 s of `svc-chromium` start.

## 3. Exactly one full-screen window

The window manager already enforces kiosk posture (spec 012 §3: openbox maximizes every mapped client; the watchdog re-fits drift). Harden to "only ever 1 browser window":

1. `docker/rootfs/etc/virtualme/openbox-rc.xml` — extend the `<application class="*">` rule:
   - `<decor>no</decor>` (no titlebars: nothing to drag, no overlapping affordance),
   - `<fullscreen>yes</fullscreen>` replaces `<maximized>yes</maximized>` (fullscreen removes any remaining frame; screenshots and xdotool coordinates keep the exact `VM_RESOLUTION` geometry),
   - `<focus>yes</focus>`, `<layer>normal</layer>`.
   - Add a `<keyboard>` section explicitly empty (`<keyboard/>`) so no WM keybindings can un-fullscreen or cycle windows out from under the agent.
2. **Popup discipline**: Chromium popup windows (`window.open`, `target=_blank` with features) become separate X clients that would tile/stack. Two-part defense:
   - The openbox rule already fullscreens them; last-mapped wins focus, so a popup fully covers the main window instead of overlapping it partially — acceptable, but the watchdog must not fight it.
   - `svc-chromium-watchdog/run`: change the window query from "the visible chromium window" to "the **most recently mapped** chromium-class window" (`xdotool search --class chromium | tail -1`), assert IT is fullscreen-geometry, and additionally count chromium-class windows; when more than 3 exist, log a warning line (`watchdog: N chromium windows mapped`) — do not kill them (the agent may legitimately be mid-popup-flow; killing is worse than logging).
3. The agent already operates the single visible surface; no `tools.go` changes.

## 4. The 2×2 door (documented, NOT implemented)

Requirement: leave open the future option of four Chromium **windows** (not tabs) in a 2×2 grid, without implementing anything now.

- The seam is already narrow: WM choice lives entirely in layer `004-xvfb-desktop.sh` (package) + `svc-openbox/` (service) + `openbox-rc.xml` (policy). Nothing else in the repo knows the WM exists; the agent and watchdog only use `xdotool`/CDP.
- The designated future path (record this in the spec text and in the develop skill, one paragraph, no code): replace openbox with **i3** configured with a fixed 2×2 grid layout (`layout splitv/splith` preset via `i3-msg` at session start), one Chromium instance per cell each with its own `--user-data-dir` and CDP port (9222–9225), and a `VM_BROWSER_GRID=1|4` env selecting posture. i3 is chosen over matchbox (single-window only) and dwm (patch-to-configure) because layouts are scriptable at runtime over its IPC socket.
- Explicit non-goals now: no i3 package install, no multi-profile plumbing, no CDP port fan-out. Any of that requires a new spec.

## 5. Docs

`/master-update`: develop skill (flag rationale pointer: "the run script comments are canonical"; the 2×2 door paragraph), operate skill troubleshooting (windows are undecorated + fullscreen; popups cover the browser rather than tile), README unchanged except spec table.

## 6. Tests / verification

- `bash test/smoke.sh` additions: (a) after health-green, `docker exec` `xdotool getactivewindow getwindowgeometry` reports exactly `VM_RESOLUTION` width×height at 0,0; (b) `xdotool search --class chromium` window count is 1; (c) CDP `curl -s 127.0.0.1:9222/json` (via docker exec) lists exactly one page target titled `about:blank`; (d) startup timing: `svc-chromium` start → CDP answer < 15 s, three consecutive restarts (`s6-svc -r`).
- Soak: all spec 012 flows must still pass (`./cli.sh soak`).
- Hermetic: none (this spec is container-config only).

## 7. Acceptance checklist

- [x] `npm run check` green (shell-syntax gate covers the edited run scripts).
- [ ] Smoke additions in §6 pass on both a userns-sandbox host and with `--no-browser-sandbox`. (Automatic and forced-fallback paths pass on the execution host; its runtime user-namespace probe selects fallback, so a sandbox-capable host was unavailable.)
- [x] Desktop view shows an edge-to-edge browser with no titlebar; dragging is impossible; a `window.open` popup covers the full screen and closing it restores the main window fullscreen.
- [x] `virtualme logs` shows no unknown-flag or feature-parse warnings from Chromium.
- [x] Repo text search: the 2×2/i3 door is documented in this spec + develop skill and nowhere implemented.

## Amendments

### 2026-07-23 — Execution details

- The pinned Chromium creates a hidden 10×10 `chromium`-class X client in
  addition to its mapped browser window, so the literal unfiltered `xdotool`
  count does not represent mapped browser windows. The watchdog starts with
  the required `xdotool search --class chromium` order, filters candidates by
  X `Map State: IsViewable`, then selects the tail. Smoke uses xdotool's
  equivalent `--onlyvisible` filter. This preserves spec 006's
  live-process/no-visible-window recovery while making the newest full-screen
  popup the geometry target.
- Geometry recovery now checks exact `0,0` position and exact display
  dimensions, matching the hard full-screen contract instead of spec 012's
  approximate threshold.
- Smoke runs the full-screen/window/CDP assertions and three timed Chromium
  service restarts in both automatic-sandbox and forced-fallback containers.
  No numbered Docker layer was edited.
- Spec 006's launch/watchdog contract and spec 012's Openbox/geometry contract
  record the corresponding supersession in their own `## Amendments` sections.
- Container smoke passed with six measured service restarts between 1.398 s
  and 2.365 s, exact 1600×900 geometry, one mapped Chromium window, and one
  `about:blank` CDP page target. The execution host selected sandbox fallback
  in automatic mode, so the userns-sandbox-host half of that acceptance item
  remains unverified. All four live soak flows passed.

### 2026-07-24 — Drop `--enable-automation` (infobar regression)

Live desktop feedback: every session showed the persistent "Chrome is being
controlled by automated test software" infobar, stealing a strip of the
1600×900 deterministic surface and shifting page geometry under the
vision/xdotool agent.

- **Change.** `--enable-automation` is removed from the `FLAGS` array in
  `docker/rootfs/etc/s6-overlay/s6-rc.d/svc-chromium/run`. It is the
  documented trigger of that infobar, and modern Chromium ignores
  `--disable-infobars` for it, so the flag cannot be kept and silenced.
- **Determinism preserved.** Every concrete behavior §2 wanted from the
  marker is still pinned by an explicit remaining flag: no first-run or
  default-browser UX (`--no-first-run`, `--no-default-browser-check`), no
  background networking or component/variations churn
  (`--disable-background-networking`, `--disable-component-update`,
  `--disable-field-trial-config`), no throttling
  (`--disable-background*`), no surprise UI (`--noerrdialogs`,
  `--disable-features=…`), and predictable password plumbing
  (`--password-store=basic`).
- **CDP unaffected.** Observation-only CDP is provided by
  `--remote-debugging-port=9222 --remote-debugging-address=127.0.0.1`,
  which does not depend on the automation marker.
- Consequence: `navigator.webdriver` is no longer forced true, which is
  acceptable (and mildly beneficial) for an agent browsing consumer sites.
