# Spec 015: Jobs Page — Activity Ledger and Queue Peek

| | |
|---|---|
| Status | Executed (2026-07-23) |
| Depends on | `specs/013-job-queue-scheduler.md` (queue, `queue-state`, `job-push`), `specs/014-projects.md` (project-run envelopes exist) |
| Produces | A "Jobs" nav entry and `/jobs` page showing (a) a reverse-chronological ledger of what the machine is actually doing (browser, LLM, and other tool calls) and (b) a queue peek (upcoming / one currently-executing / recently finished, in time order); a right details pane (desktop) or slide-in sheet (mobile) tailored to job type; a Valkey-backed activity ledger fed from tool dispatch and LLM lifecycle; a soak flow proving the page's data path with basic queued jobs |
| Followed by | `specs/021-agent-cdp-tools-console.md` |

## 0. Executor instructions

- Constitution binds; execute after specs 013–014.
- Exact WS `type` strings and Valkey keys below are load-bearing (soak asserts them).
- Theme-token-only styling; verify eight themes × light/dark.
- Stop-on-red per section; finish with §7 Acceptance.

## 1. What it is

The Jobs page answers "what is the machine *actually* doing?" It has two data sources:

1. **Activity ledger** — every externally-visible action, newest first: each agent tool call (navigate, click, dom, bash, speak, …), each LLM generation start/finish (with token counts), each TTS synthesis, each mail submit. This is finer-grained than the queue: one chat job may produce a dozen ledger events.
2. **Queue peek** — the spec 013 `queue-state`: up to N upcoming envelopes, exactly one (or zero) currently-executing envelope, and up to N recently finished, presented top-to-bottom in execution-time order (upcoming first at top… no: see §4 layout — time flows down the page: upcoming at top, running in the middle, finished below, each block internally time-ordered). This view covers **queued jobs and finished ones only** — it is not the ledger.

Clicking any row (ledger event or queue envelope, past, current, or future) opens a details pane tailored to its type.

## 2. Controller: activity ledger

New file `controller/internal/jobs/activity.go` (same package as the queue so both share the valkey client and broadcast func).

1. **Event model** — one JSON per event, key `virtualme:activity` (Valkey list, `LPUSH` newest-first, `LTRIM` to 500):

```json
{
  "id": "uuid",
  "ts": 1690000000000,
  "kind": "tool" | "llm" | "tts" | "mail",
  "name": "navigate",
  "jobId": "envelope id or empty",
  "summary": "≤200 chars, human sentence",
  "detail": { "args": {}, "resultText": "≤2048 chars", "durationMs": 0, "ok": true,
              "promptTokens": 0, "completionTokens": 0, "screenshotThumb": "data:… ≤32KiB or empty" }
}
```

2. **Feed points** (each is a one-line call to `activity.Record(...)`; keep producers dumb):
   - `controller/internal/agent/tools.go` `Execute`: after each tool call, record `kind:"tool"`, `name`=tool name, args (JSON, values truncated to 256 chars each), result text capped 2048, duration, error state, and the step thumbnail when present (same ≤32 KiB data URL that `agent-step` broadcasts).
   - `controller/internal/chat/chat.go`: on generation start record `kind:"llm", name:"generate", summary:"prompt: <first 120 chars>"`; in `finishReply` record completion with token counts, duration, stopped flag.
   - `controller/internal/tts`/`main.go` speech path: one event per synthesis request (`name:"synthesize"`, chars, voice, duration).
   - `controller/internal/mail/service.go`: one event per submit (`name:"submit"`, recipient domain only — not the full address — size, ok).
3. **Broadcast**: after each `Record`, broadcast `{"type":"activity-event","event":{…}}`. On WS connect (extend the `SetOnConnect` block) send `{"type":"activity","events":[newest 100]}`.
4. **Client → server** `{"type":"activity-req"}` replies the same `activity` frame to the sender (used on page (re)entry and by soak).
5. Persistence map amendment (spec 007 §1a Amendments): `virtualme:activity` — activity ledger — Valkey AOF.
6. Hermetic tests: record/trim/caps; producer truncation (256/2048/32 KiB); domain-only mail redaction.

## 3. SPA: route, nav, markup

1. Router: add `["/jobs", ["jobs", "Jobs"]]`. Nav link after Projects: `<a href="/jobs" data-nav><svg class="icon"><use href="/icons.svg#i-list-checks"/></svg>Jobs</a>` (add `list-checks` to the icon fetch list). Home quick-link card: `<strong>Jobs</strong><span>See what the machine is doing</span>`.
2. Section skeleton in `index.html`:

```html
<section data-page="jobs" hidden class="jobs-page" aria-labelledby="jobs-title">
  <div class="page-heading"><h1 id="jobs-title">Jobs</h1></div>
  <div class="jobs-grid">
    <div class="jobs-main">
      <article class="jobs-card"><h2>Queue</h2>
        <ol id="queue-upcoming" class="queue-block" aria-label="Upcoming"></ol>
        <ol id="queue-running" class="queue-block running" aria-label="Executing"></ol>
        <ol id="queue-finished" class="queue-block" aria-label="Recently finished"></ol>
        <p id="queue-empty" class="empty-note" hidden>Queue idle. Nothing upcoming.</p>
      </article>
      <article class="jobs-card"><h2>Activity</h2>
        <ol id="activity-list" class="activity-list" aria-live="polite"></ol>
        <p id="activity-empty" class="empty-note" hidden>No recorded activity yet.</p>
      </article>
    </div>
    <aside id="job-detail" class="job-detail" hidden aria-label="Details"></aside>
  </div>
</section>
```

## 4. SPA: behavior and design (`controller/web/static/js/jobs.js`)

Export `initJobs(send)`; wire into `app.js` (init; dispatch `queue-state`, `activity`, `activity-event`; on navigating to `jobs` send `activity-req` + `queue-peek`).

**Queue block** (single timeline, time flowing top → bottom):
- `#queue-upcoming`: up to N=10 upcoming envelopes in execution order (the order `queue-state.upcoming` already provides). Row: muted clock glyph, type chip (`chat` / `project-run` / `manual-tool` / `soak-probe` — chip colored by type with `--p1`–`--p4`), one-line summary (chat text excerpt / project name / tool name), and `not before <local short time>` when `notBeforeTs > now`.
- `#queue-running`: **exactly one row or nothing** (parallelism is 1). Visually distinct: `--accent` left border, subtle pulse animation (respect `prefers-reduced-motion` / `--motion`), live elapsed timer updated with `setInterval` 1 s.
- `#queue-finished`: up to N=10 newest finished, each with ok/err dot, summary, finished local time (short `Intl.DateTimeFormat`), duration.
- Every row is a `<button>` opening details (§ below). Cap logic is display-side; server already caps at 20.

**Activity list**: newest first, one compact row per event: local time (short), kind icon (`tool`→wrench, `llm`→sparkles→ use existing Lucide names: `wrench`, `brain` or `cpu`, `volume-2`, `mail`; add missing ones to the fetch list), `name`, summary. Live-prepends on `activity-event` (cap at 200 rows in the DOM). Rows are buttons too.

**Details pane**:
- Desktop (`min-width: 48rem`): `#job-detail` is a sticky right column (`.jobs-grid { display:grid; grid-template-columns: 1fr 24rem; gap: var(--gap) }`), empty state hidden.
- Mobile: the pane becomes a slide-in sheet: `position: fixed; inset: 0 0 0 auto; width: min(92vw, 24rem); transform: translateX(100%);` with `.open { transform: none }`, a scrim, Escape/scrim-click to close, focus moved into the sheet on open and restored on close (mirror the `nav.js` drawer conventions).
- Content by type:
  - queue `chat`: full prompt text, envelope timestamps (enqueued/notBefore/finished, all local), attempts, initiator, result summary or error.
  - queue `project-run`: project name as a link (`<a data-nav href="/projects/<id>">`), the grounding line, run result, duration.
  - queue `manual-tool` / `soak-probe`: pretty-printed payload JSON (`<pre>` with `--font-mono`), result.
  - activity `tool`: args pretty-printed, result text (scrollable `<pre>`, max-height), screenshot thumbnail when present (`<img>` from the data URL), duration, ok/err.
  - activity `llm`: prompt excerpt, token counts, duration, stopped flag.
  - activity `tts` / `mail`: their summary fields as a definition list.
- The pane header shows the type chip + local timestamp and a close button (mobile only).

**Design intent**: monochrome-quiet rows, color only in the type chips and status dots; both cards share the page width on mobile with Queue above Activity.

## 5. Soak flow

Append to the `flows` array in `test/soak.mjs` (runner is feature-agnostic per spec 012 §5a; flows there use chat — this flow instead speaks raw WS, so extend the runner minimally: allow a flow to define `run(ws, log)` overriding the default chat driver; keep the `{name, hard, soft}` contract):

- **`jobs-queue-probe`** — no LLM involved. Steps:
  1. Send `{"type":"job-push","job":{"type":"soak-probe","payload":{"echo":"soak-1"}}}`; capture `job-pushed.id`.
  2. Immediately push a second probe (`"soak-2"`).
  3. Hard: a `queue-state` frame shows both ids with `soak-2` in `upcoming` while `soak-1` is `running` (order proof); a later `queue-state` shows both in `finished` with `result.ok:true`, `soak-1` finishing before `soak-2`.
  4. Send `{"type":"activity-req"}`; hard: the `activity` reply is well-formed JSON with `events` array (content not asserted — probes do not produce tool events).
  5. Soft: none.

Because this flow needs no model, it must run in well under 30 s; give it `SOAK_TIMEOUT`-independent internal timeout of 60 s.

## 6. Tests and docs

- Hermetic Go tests per §2.6.
- e2e: extend `queue-probe.mjs` (spec 013) to also assert one `activity-event` per lifecycle isn't required; keep e2e minimal — the soak flow is the integration oracle.
- Docs: `/master-update` — operate skill (Jobs page purpose: "look here to see what the machine is actually doing", queue reading order), develop skill (activity feed points, `jobs.js`, WS table rows `activity`, `activity-event`, `activity-req`), README endpoints + spec tables.

## 7. Acceptance checklist

- [ ] `npm run check` green.
- [ ] With a chat generation running: `/jobs` shows exactly one running row with a live timer; the finished block fills when it completes; the activity list shows the paired `llm` start/finish events and each agent tool call with thumbnails where applicable.
- [ ] Clicking a finished `project-run` opens the pane with a working link to the project.
- [ ] Mobile viewport (375 px): pane slides in over content, Escape closes, focus is restored.
- [ ] `./cli.sh soak --no-build` (against a running dev container) passes `jobs-queue-probe` hard assertions.
- [ ] Ledger survives a container restart (Valkey AOF): activity from before the restart still lists.

## Amendments

### 2026-07-23 — Execution details

- Queue envelopes gain `startedTs` when execution begins so the running timer
  and finished durations use actual execution time rather than enqueue time.
- Project-run payloads include the project name for bounded queue summaries;
  project identity and selector remain authoritative envelope fields.
- Agent tool activity is emitted immediately after artifact recording rather
  than inside `localTools.Execute`, because that is the first point where the
  exact bounded thumbnail broadcast in `agent-step` is available.
- Activity thumbnails are rejected when the complete data URL exceeds 32 KiB.
  Producers otherwise remain dumb: `Activity.Record` owns argument, result,
  summary, and thumbnail caps.

### 2026-07-23 — Manual tool activity (spec 021)

Queue-backed manual calls record the same `tool` activity detail as agent
calls: tool name, arguments, bounded result text, duration, status, and the
`manual-tool` job ID. The Tools page keeps only in-memory per-tool output;
the activity ledger remains the durable operator record.
