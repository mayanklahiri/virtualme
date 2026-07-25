# Spec 026: Console Bugfix Sweep

| | |
|---|---|
| Status | Accepted (2026-07-24) |
| Depends on | `specs/008-browser-agent.md`, `specs/013-job-queue-scheduler.md`, `specs/015-jobs-page.md`, `specs/017-jiggler.md`, `specs/018-gpu-observability.md`, `specs/019-chart-overhaul.md`, `specs/020-speech-audio.md`, `specs/021-agent-cdp-tools-console.md`, `specs/023-mail-transparency.md`, `specs/024-brand-chrome-polish.md` |
| Produces | Twenty-seven root-caused fixes across chat, speech, status charts, jobs, home, core UI, mail, tools, and screenshots, with test-first coverage and amendments to every executed spec whose language the fixes supersede |
| Followed by | Future specs |

## 0. Scope and method

A live review of the console after specs 012 to 025 surfaced the issue list
below (IDs C1 to X1). For each issue this spec records the root cause, the
decided fix, and the test that reveals the bug. Where an executed spec
mandated the old behavior, that spec gains a dated amendment referencing this
one. Fixes land as one commit per group, each preceded by a failing test
where one is feasible.

Decisions fixed by the operator up front: C1 removes `aria-live`; C2 uses
server-side step replay; S1 caps bars client-side; S4 carries the jiggler and
a new scheduler-pause toggle; S6 rates divide by active generation time; S5
groups actions as observe/actuate/bash/speak; J3 uses a persistent third
details column; M2 clears the whole queue behind a confirm step; M3 persists
a durable Valkey outbox with statuses queued / left queue / error / cleared;
U3 sets the wordmark in Chakra Petch Bold; X1 removes the grid only for
manual captures; T2 parses env text client-side.

## 1. Chat (C1, C2, C3)

1. **C1 (spurious notification sound).** The SPA contains no sound-effect
   code; the only `AudioContext` is the TTS `AudioPlayer`. Root cause:
   `aria-live="polite"` on `#chat-log` and `#llm-status` turns every appended
   agent step into an assistive-technology announcement, which several
   OS/browser combinations voice with a notification chime. Fix: remove every
   `aria-live` attribute from `index.html`. The console never plays UI sound
   effects; this becomes a tested invariant (`test/chat-ui.test.js`).
2. **C2 (think steps vanish on tab switch).** Agent steps were broadcast-only
   DOM nodes; a websocket reconnect (common after tab switches) re-sends
   `chat-history`, whose handler calls `log.replaceChildren()`, destroying the
   step cards with no replay. Fix: the agent keeps a bounded (200-frame)
   in-memory buffer of raw `agent-step` frames for the most recent task,
   reset when a new task starts; on every websocket connect the controller
   sends the buffered frames immediately after `chat-history`. Steps for the
   latest task now survive reconnects and full page reloads.
3. **C3 (no Markdown tables).** The hand-rolled renderer had no table
   grammar. Fix: `markdown.js` gains a pure exported `parseTable(lines)`
   (GFM pipe tables: optional leading/trailing pipes, `---`/`:---:` separator
   row, escaped `\|` literal, ragged rows padded/truncated to the header
   width) and the block loop renders detected tables as
   `<div class="md-table"><table>` with per-column alignment, cells run
   through the existing safe inline formatter. DOM-built, never `innerHTML`.

## 2. Speech (P1, P2)

Root cause of both critical failures: `app.js` dispatches `tts-*` frames
without awaiting, while the `tts-start` handler awaits
`AudioPlayer.begin()`; chunks arriving during that await hit a null context
and are silently dropped (cached synthesis delivers everything during the
await, so long text dies after roughly one sentence). Separately the Speak
button is gated on an `active` id that is only cleared by the `tts-done`
timer or Stop, so a dropped socket wedges the page until a hard reload.

Fix: a DOM-free `tts-stream.js` module (`createTtsStream({player,
onActiveChange})`) serializes every frame through a promise queue, owns the
`active` id, clears it on `tts-done` (after the player drains), `tts-error`,
and `reset()`. The Speech tab and chat TTS bubbles both route frames through
it; the connection-status path calls `reset()` on reconnect so Speak always
recovers. `AudioPlayer.push` resumes a suspended context (background tabs).
The module must not construct `AudioContext` (spec 020 invariant holds).

## 3. Status page (S1 to S6)

1. **S1 (bar cap).** Server tiers return up to 2880 samples and the client
   drew one bar per sample. Fix: pure `chart-data.js` `downsample(samples,
   resSec, maxBars, fieldModes)` merges `ceil(n/120)` adjacent samples per
   bucket (mean by default, sum for counter fields) before any render.
   Supersedes spec 019 §2.5's "buckets must not change".
2. **S2 (title gap).** `.chart-head` margin-bottom grows from .5rem to 1rem.
3. **S3 (GPU split).** The combined dual-scale GPU chart becomes two default
   renderer charts, GPU utilization (percent) and GPU memory (displayed in
   GB), side by side on desktop in a `.chart-row`. Amends spec 018 §3.2.
4. **S4 (Quick Options).** The Jiggler pane becomes a "Quick Options" card of
   labelled switches with hover/focus help text: the existing jiggler switch
   and a new scheduler-pause switch. Scheduler pause: Valkey
   `virtualme:scheduler:paused` (`"1"`/`"0"`, absent means running), WS
   `scheduler-set`, snapshot `scheduler.paused`, broadcast `scheduler-state`;
   while paused the time-bucket scheduler stops promoting due scheduled jobs
   (interactive jobs unaffected; due jobs run when unpaused). Amends specs
   013 and 017.
5. **S5/S6 (LLM and action series).** `metrics.Sample` gains sum-aggregated
   counters `tokIn`, `tokOut`, `tokCached`, `llmPromptMs`, `llmPredictMs`,
   `actObserve`, `actActuate`, `actBash`, `actSpeak`. A mutex-guarded
   `metrics.Counters` accumulator is drained into each 2s sample by the state
   collector. Producers: chat and agent LLM finishes (parsing llama.cpp
   `timings` `prompt_ms`, `predicted_ms`, and cached-token count when
   present) and every tool execution (category from the tool: bash, speak,
   `Observe` results, else actuate; jiggler bursts excluded). Tier roll-up
   sums these fields instead of averaging. Three new charts: LLM tokens
   (stacked in/out plus cached when nonzero), LLM throughput in tok/s
   (`tokIn/(llmPromptMs/1000)` and `tokOut/(llmPredictMs/1000)` per bucket),
   and browser actions by category (stacked). Old tier files load with zero
   counters.

## 4. Jobs page (J1 to J4)

1. **J1.** Finished-job green dots read as "running". Queue rows now use a
   check icon (`--ok`) for success, a circle-x (`--err`) for failure, a
   spinning loader for running, and the clock only for upcoming.
2. **J2.** The Activity pane gains two persisted toggles, "Tool calls"
   (default hidden) and "Jiggler" (default hidden, only meaningful when tool
   calls show), implemented over a pure exported `filterActivity`, plus a
   human-short runtime per row from `detail.durationMs`.
3. **J3.** Desktop (>=64rem) becomes three panes: Queue full-height left,
   Activity right of it, details as a persistent third column with an
   empty-state placeholder. Mid widths keep two columns with the slide-in
   details; mobile unchanged.
4. **J4.** Queue rows adopt the activity column rhythm (status, fixed-width
   pill, name, summary, meta) and `.job-chip` pills get a standard min-width
   in both lists. Amends spec 015.

## 5. Home page (H1, H2)

1. **H1.** Fact-grid values render in `var(--accent)` (labels stay muted),
   giving the stacked cockpit read the operator asked for.
2. **H2.** The hero image breathes: a 16s `ease-in-out` infinite `transform:
   scale(1)` to `scale(1.045)` loop, cropped by the existing
   `overflow:hidden`, disabled under `prefers-reduced-motion`. Grounded in
   compositor-only animation practice: transform/opacity only, no `filter`
   animation, no permanent `will-change`.

## 6. Core UI (U1 to U4)

1. **U1.** One shared duration module (`duration.js`): `formatShortDuration`
   (top two nonzero units of d/h/m/s, sub-second as `0.Ns`) and
   `durationElement` (per-unit spans classed `dur-d`/`dur-h`/`dur-m`/`dur-s`
   with graded brightness: larger units brightest, seconds dim). Replaces the
   five divergent formatters in jobs, mail, conn, render, projects, tools.
2. **U2.** Every render site whose CSS ellipsizes sets `title` to the full
   text (jobs summaries/names, mail subjects, project rows).
3. **U3.** The wordmark is regenerated in Chakra Petch Bold with "me" scaled
   to visual balance against "Virtual" (same optical weight, red accent
   kept); the monogram and favicon follow. The font is fetched by pinned
   URL + sha256 in the generator script. Amends spec 024 §4.
4. **U4.** The wristwatch dial is removed everywhere; the host box keeps a
   status pip, `host:port`, and an uptime line, with comfortable padding.
   Amends spec 024 §5.

## 7. Mail (M1 to M4)

1. **M1.** The "Flush ran" session-timeline entry is noise; the write is
   removed (last-flush tracking stays for retry countdowns).
2. **M2.** A "Clear queue" button (two-step confirm) sends WS `mail-clear`;
   the controller best-effort deletes every `Q*`/`M*` pair under
   `$VM_DATA_DIR/mail/spool`, marks affected outbox entries `cleared`, and
   rebroadcasts status.
3. **M3.** Durable outbox: Valkey list `virtualme:mail:outbox` (cap 200),
   entries `{id, ts, to, subject, size, queueId, status, lastError}` with
   statuses `queued`, `left_queue` (dma cannot distinguish delivered from
   bounced), `error`, `cleared`. Submit appends `queued` (queueId from the
   spool diff); refresh transitions entries whose spool ID vanished to
   `left_queue` and copies `lastError` into `error` entries. The session
   activity timeline is replaced by an Outbox view; the "Submitted message
   to" and "left queue" timeline writes are removed.
4. **M4.** Layout: Status facts and DNS rows move to the bottom of the
   Compose card; the right column becomes Outbox (with the Last-send row
   pinned first) above Queue. Amends spec 023.

## 8. Tools page (T1, T2)

Result rendering stays shape-based (no hardcoded tool names, preserving the
spec 021 invariant): `classifyResult(text)` recognizes JSON whose keys are a
subset of `{url, title, text}` (string values, http(s) url) and renders a
linked title, a muted url line, and the page text as wrapped plain text;
`parseEnvBlocks(text)` finds runs of three or more `KEY=value` lines inside
plain-text results and renders each run as a sorted two-column table. All
other results keep the generic pretty-JSON / plain-text / image paths.
Tool outputs themselves are unchanged (the agent contract is untouched).

## 9. Screenshots (X1)

`screenshot()` unconditionally composited the labeled coordinate grid, so
manual Tools-console captures shipped grid lines the operator does not want.
(The reported "vignette" is the lightbox backdrop, UI-only, not in the
file.) Fix: the grid becomes a parameter; the agent-vision path keeps it,
the manual-tool executor requests a pure capture. The tool description now
says the grid applies to agent observations only. Amends spec 008.

## 10. Gates and tests

Every group lands with `scripts/check.sh` green. New deterministic tests:
`test/chat-ui.test.js` (no `aria-live`, no watch dial), `test/markdown.test.js`
(`parseTable`), `test/tts-stream.test.js` (serialized frames, stuck-state
recovery), `test/chart-downsample.test.js` (<=120 buckets, mean/sum modes),
`test/duration.test.js`, `test/tools-render.test.js` (`classifyResult`,
`parseEnvBlocks`), extended `test/jobs-ui.test.js` (`filterActivity`, new
status icons); Go tests for step replay, scheduler pause, counter roll-up,
mail flush silence, outbox transitions, queue clear, and grid-vs-pure
`convert` argv via the injected runner.

## Amendments

(None.)
