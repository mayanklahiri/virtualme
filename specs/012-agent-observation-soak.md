# Spec 012: Agent Observation Reliability, Desktop Coverage, and Soak Tests

| | |
|---|---|
| Status | Executed (2026-07-23) |
| Depends on | `specs/008-browser-agent.md` (agent loop, tools, CDP client), `specs/002-container.md` (layers, s6 services), `specs/006-desktop-reliability.md` (chromium supervision, watchdog) |
| Produces | An information-dense `dom` observation the local model can actually consume; a settled `navigate` tool; observation text persisted in step artifacts; guaranteed full-screen Chromium on the virtual desktop; removal of the unused Node/Playwright image layer; a live end-to-end soak test suite (`test/soak.mjs` + `test/soak.sh`) invocable as `./cli.sh soak` |
| Followed by | Future specs |

## 0. Executor instructions

- The constitution (`specs/001-constitution.md` §1) binds this spec. The npm CLI stays zero-dependency; soak tests use only Node built-ins (global `WebSocket`, Node >= 22).
- Write the failing soak flow **first**, verify it fails against an image built from the pre-fix tree, then land the fixes and verify it passes.
- Stop-on-red per section; finish with the Acceptance Checklist (§8).

## 1. Diagnosis (evidence, 2026-07-23)

Reproduced against a live container on a dense news-feed page:

1. **The CDP transport is healthy.** `DOMSnapshot.captureSnapshot` returns one unfragmented 158 KB websocket frame of valid JSON. Playwright is not involved anywhere in the runtime path (the Go controller speaks raw CDP; layer 007's `playwright-core` is dead weight).
2. **The model never sees the DOM it asked for.** The compacted snapshot serializes to ~69 KB; the `dom` tool paginates it to a ~48 KiB page (`domCap`), but the observation message is then hard-truncated to 8 KiB (`observationTextCap`) — cut mid-JSON. For a dense news feed that is the page header plus part of one story row; every story title lies beyond the cut. The budget is further wasted on layout-only wrappers (`tr`/`td`/`span` with no text and no attributes) and full-precision float boxes (`"box":[83.828125,129,603.765625,10]`).
3. **`selectorHint` misleads the model.** It is a case-insensitive substring filter, but nothing says so; the model passes CSS selectors (observed: `tr[id*="49022152"] td a`), which match nothing and return `{"elements":[]}`.
4. **`navigate` returns before the page settles**, so an immediately following `dom` may observe the previous page.
5. **Step artifacts don't record observation text** (`steps.jsonl` has `args`/`summary`/screenshot only), so failures like the above are invisible after the fact.
6. **Desktop black background**: Xvfb is 1600x900 but the Chromium window had drifted to 1050x880; the uncovered root window is black. noVNC `resize=scale` was working correctly. `--start-maximized` is best-effort and nothing re-asserts geometry at runtime.

## 2. Dense DOM observation (controller/internal/agent)

Supersedes spec 008 §5a sizing; recorded there as an amendment.

- **Serialization** (`cdp.go`): the `dom` result becomes `{"url","title","page","elements",["more"],["note"]}`.
  - `url` and `title` (from the same `Runtime.evaluate` used for viewport offsets) ground every DOM observation.
  - Elements omit `box` from the JSON (screen-space boxes stay in the server-side ref map used by `click_element`/`type_into`). Element JSON is `{ref, tag, text?, attributes?}`.
  - Layout-only noise is skipped: an element with no text, no allowed attributes, and a non-interactive tag is not serialized (its ref stays clickable if a later snapshot lists it). Interactive tags are always serialized: `a`, `button`, `input`, `select`, `textarea`, `option`, `summary`, `label`, `img`, `iframe`, `video`, `audio`.
- **Sizing**: `domCap` (per-page JSON budget) drops from 48 KiB to 12 KiB; `observationTextCap` rises from 8 KiB to 16 KiB. Consequence: a full `dom` page always reaches the model without mid-JSON truncation, and ~100+ meaningful elements fit in one page. Pagination (`more.nextPage`, `more.remaining`) is unchanged.
- **Hint semantics**: the `dom` tool schema documents `selectorHint` as "case-insensitive substring matched against tag/text/attributes — NOT a CSS selector". When a hint matches nothing, the tool returns the **unfiltered** first page plus `"note":"selectorHint matched nothing; it is a substring filter, not a CSS selector; showing unfiltered elements"` so the loop keeps moving.
- **Settled `navigate`**: after the omnibox Return, poll (read-only `Runtime.evaluate`, 250 ms interval, 15 s budget) until `document.readyState === "complete"`; the tool result becomes an observation (`Observe: true`) carrying `{"url","title","ready"}`. On poll timeout it still returns the last-seen state with `"ready":false`.
- **Defensive CDP reader**: `readServerFrame` accepts fragmented frames (opcode 0x0 continuations) instead of erroring; the 16 MiB total cap stays.
- **Step artifacts**: `recordStep` adds a `text` field (the tool result text, capped at `observationTextCap`) to both the broadcast `agent-step` frame and `steps.jsonl`. This is what makes agent failures diagnosable and gives the soak suite its deterministic oracle.

## 3. Desktop full coverage (docker/rootfs)

- New committed `docker/rootfs/etc/virtualme/openbox-rc.xml`: a minimal Openbox config whose `<applications>` rule maximizes every mapped client (kiosk posture — the virtual desktop exists to show one browser). `svc-openbox/run` gains `--config-file /etc/virtualme/openbox-rc.xml`.
- `svc-chromium-watchdog/run` additionally self-heals geometry drift: every cycle, if the visible Chromium window is smaller than ~95% of the display, it re-fits with `xdotool windowmove 0 0` + `windowsize 100% 100%` (xdotool 3.2016 has no `windowstate`). Restart behavior for a *missing* window is unchanged.

## 4. Remove the Node/Playwright layer

`playwright-core` was specified by spec 002 for a Node-based driver that was never built; spec 008 chose raw CDP from Go. Node itself is used only by the build-time manifest generator and cosmetic version listings.

- Delete `docker/layers/007-node-playwright.sh` and its `COPY`+`RUN` pair from `docker/Dockerfile`; the numbering gap is permanent (layers are append-only; 008+ keep their numbers). Recorded as an amendment to spec 002. Note: a bare `nodejs` binary remains in the image as a transitive dependency of the Debian `novnc` package (layer 004); `npm` and `playwright-core` are fully gone.
- `docker/rootfs/usr/local/lib/virtualme/system-manifest.sh`: replace the Node JSON-string escaper with a pure-bash escaper; drop the `node` tools entry.
- `system_info` tool (`tools.go`): drop `node --version` from the `packages`/`all` probes.
- Image label and docs no longer mention Playwright.

## 5. Soak test suite

A live end-to-end flow suite run against a **running controller endpoint**. The runner is feature-agnostic: the initial flows drive the browser agent via chat (real LLM, real browser), and future flows may cover any controller feature (speech, mail, metrics, ...). Nondeterminism is handled with a layered oracle:

- **Hard assertions** (fail the flow): deterministic facts about agent behavior — which tools ran, and what their *observation text* contained (from `agent-step.text` frames). Example: the `dom` observation for `www.lahiri.me` must contain `"Lahiri"` and `"Oracle"`.
- **Soft assertions** (logged `WARN`, never fail): whether the model's final prose surfaced the facts.

### 5a. `test/soak.mjs`

Node >= 22, zero deps, global `WebSocket`. Structure: a small flow runner + per-flow spec objects. Fine-grained console logging: timestamped, ANSI-colored when stdout is a TTY, one line per websocket event of interest (`chat-delta` batched, `agent-step` with tool/summary/text excerpt, `agent-status`, `llm-status` phase changes), plus `PASS`/`FAIL`/`WARN` per assertion and a final summary table. Environment: `SOAK_URL` (default `ws://127.0.0.1:8080/ws`), `SOAK_TIMEOUT` per-flow seconds (default 600), `SOAK_FLOW` regex to select flows. Sends `chat-clear` between flows so each flow starts from an empty shared conversation.

Initial flows:

1. **`lahiri-dom`** — chat: navigate to `https://www.lahiri.me`, wait for load, read the DOM, report who the page belongs to and their current employer, and take a screenshot. Hard: a `navigate` step observes a `lahiri.me` URL with `ready:true`; a `dom` step's text contains `Lahiri` and `Oracle`; a `screenshot` step's text matches `screenshot 1024x\d+ API space` (the token-bounded resize); the step's inline thumbnail data URL decodes to <= 32 KiB. Soft: final reply mentions `Lahiri` and `Oracle`.
2. **`lahiri-readpage`** — chat: navigate to `https://www.lahiri.me`, use `read_page`, and list every article/link on the page with its title and destination. Hard: a `navigate` step observes a `lahiri.me` URL with `ready:true`; a `read_page` step's text parses under the spec 027 YAML subset to a digest whose `body` contains at least 5 nodes carrying absolute `href` values and at least one heading node. Soft: final reply contains a list of >= 3 items. (Replaced the original second flow per the amendment of 2026-07-24 below.)
3. **`readpage-example`** — chat: navigate to `https://example.com` and report the page's heading using `read_page`. Hard: a `read_page` or `dom` step's text contains `Example Domain`. Soft: final reply mentions it.

Exit code: number of hard-failed flows (0 = success).

### 5b. `test/soak.sh`

Orchestration, mirroring `e2e.sh` conventions: `--no-build` flag; otherwise `./cli.sh build` first. Then `./cli.sh stop` (whatever is running), start on a **fresh temp data dir**, wait for all-green `/healthz`, run `node test/soak.mjs` (streaming its log to the console), and on exit stop the soak container and — if a container was running before the run — restart it on the default data dir so the deployment comes back.

### 5c. CLI

New `soak` subcommand (`src/commands/soak.js`, registered in `src/main.js` + help): validates it is running from a source checkout (`test/soak.sh` exists; this is a repo workflow, not an npm-consumer feature), then execs `bash test/soak.sh` passing `--no-build` through. `./cli.sh soak [--no-build]` is the canonical invocation.

## 6. Tests

- Hermetic Go tests: dense serialization (wrapper skipped, interactive kept, no `box` in JSON), hint-miss note + unfiltered fallback, `url`/`title` in the DOM result, fragmented-frame reassembly, navigate settle polling (fake CDP server), `recordStep` persisting `text`.
- Existing `test/e2e.sh` stays authoritative for lifecycle; the soak suite is additive and not part of `scripts/check.sh` (it needs Docker, network, and a live LLM — non-deterministic by design, excluded from the deterministic gate per constitution rule 5).

## 7. Docs

`README.md`, `AGENTS.md`, and the skills are reconciled by `/master-update` at the end (constitution rule 9), including the layer-table row removal and the new soak command.

## 8. Acceptance checklist

- [ ] `npm run check` green.
- [ ] Soak flow `lahiri-dom` fails against a pre-fix image (observation truncation) and passes post-fix.
- [ ] All three soak flows pass hard assertions against the rebuilt container via `./cli.sh soak`.
- [ ] Desktop page shows Chromium covering the full remote framebuffer after container restart, and after a forced un-maximize the watchdog re-fits it within ~6 s.
- [ ] `/opt/agent` contains only `system-manifest.json` (no `node_modules`/`playwright-core`), `npm` is gone from the image, and `/opt/agent/system-manifest.json` is still valid JSON. (A bare `nodejs` binary remains: the Debian `novnc` package from layer 004 depends on it transitively; removing noVNC is out of scope.)
- [ ] `steps.jsonl` lines for observation tools contain a non-empty `text` field.

## Amendments

### 2026-07-23 — Full-screen policy superseded by spec 016

Spec 016 supersedes §3's maximized Openbox posture with an undecorated,
full-screen rule and an empty WM keyboard map. The watchdog now enforces exact
screen position and dimensions on the most recently mapped Chromium surface,
rather than applying the prior approximate size threshold to the first visible
window. No numbered Docker layer changed.

### 2026-07-24 — Explicit screenshot soak instruction

Cumulative live validation found that the local model intermittently claimed
to provide the requested screenshot without invoking the screenshot tool. The
`lahiri-dom` prompt now states the existing hard requirement explicitly: it
must call the tool before answering, and prose alone does not satisfy the
request. The hard assertion remains unchanged.

### 2026-07-24 — e2e becomes a required first phase of every soak run

§6's "soak is additive and separate from e2e" posture is superseded: a soak
run that skips the deterministic lifecycle suite can pass while the container
is broken in ways the live flows never touch. From now on `./cli.sh soak`
includes `test/e2e.sh` as a hard prerequisite:

1. **One build.** `test/soak.sh` builds the image exactly once via
   `./cli.sh build` (skipped entirely with `--no-build`, as today).
   `test/e2e.sh` gains a skip-build mode — environment `E2E_SKIP_BUILD=1`
   bypasses its own `./cli.sh build` step and uses the already-tagged
   development image; all other e2e steps are unchanged.
2. **Order.** After the (optional) build and before starting the soak
   container, `test/soak.sh` runs `E2E_SKIP_BUILD=1 bash test/e2e.sh`,
   streaming its output. A non-zero e2e exit fails the soak run immediately —
   the live flows do not start. Only after e2e passes does soak perform its
   own fresh-data-dir start and run `test/soak.mjs`.
3. **Agent probe stays opt-in.** `E2E_AGENT` is passed through unmodified;
   soak neither forces nor suppresses it.
4. **Restore semantics unchanged.** The existing stop/restart-previous-
   deployment behavior wraps the whole run (e2e phase included).

`scripts/check.sh` is unaffected (constitution rule 5: e2e and soak both need
Docker and are outside the deterministic gate). The `soak` CLI subcommand's
help text notes that soak includes the full e2e suite.

### 2026-07-24 — Live-test site allowlist; news-feed flow moves to lahiri.me

Automated suites may target exactly two external sites: `https://www.lahiri.me`
and `https://www.example.com`. No other external URL may appear in any
automated test, spec, or prompt used by tests. Consequences:

1. The original second soak flow (which browsed a third-party news
   aggregator) is retired and already deleted from `test/soak.mjs` by this
   amendment. Its purpose — multi-item extraction from a dense feed of
   links — is carried by the replacement flow `lahiri-readpage` (defined in
   §5a as rewritten), which additionally pressure-tests the spec 027
   structured `read_page` digest: the flow's hard oracle parses the digest
   and counts link nodes instead of grepping raw DOM text. Executor: add
   `lahiri-readpage` to `test/soak.mjs` with or after spec 027 execution
   (the oracle needs the YAML digest and can reuse the spec 027 subset
   parser module); the soak runner itself is unchanged.
2. By operator decision this amendment also rewrote the historic §1 and §5a
   text of this spec, and the flow lists in spec 022, to remove the retired
   site's name and URL from the repository entirely — a recorded, deliberate
   exception to constitution rule 4's no-rewrite posture, limited to
   scrubbing references to that site.
3. Spec 027 §8 binds future work to the same two-site allowlist.
