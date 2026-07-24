# Spec 013: Valkey Job Queue, Time-Bucket Scheduler, and Initiator-Bound Cancellation

| | |
|---|---|
| Status | Executed (2026-07-23) |
| Depends on | `specs/003-controller.md` (WS hub, chat service), `specs/007-persistence-locality.md` (persistence map, locality gate), `specs/012-agent-observation-soak.md` (soak runner) |
| Produces | A reliable Valkey-backed job queue (`controller/internal/jobs`) with visibility timeout, retries, and a dead-letter list; a sequential time-bucket scheduler using the server locale; ALL LLM work (interactive chat included) routed through the queue with parallelism 1; immediate cancellation when the initiating WebSocket client disconnects; a Status-page "Active time selectors" widget; WS producer/inspection messages (`job-push`, `queue-peek`, broadcast `queue-state`); host timezone passthrough in the CLI + tzdata layer |
| Followed by | `specs/014-projects.md`, `specs/015-jobs-page.md`, `specs/021-agent-cdp-tools-console.md` |

## 0. Executor instructions

- The constitution (`specs/001-constitution.md` §1) binds this spec. In particular: `controller/go.mod` must keep **zero** `require` lines (all Valkey access goes through the in-repo RESP client), the npm CLI stays zero-dependency, and Docker layers are append-only.
- Work section by section, stop-on-red: after each section, `gofmt`, `go vet`, `go test ./...` in `controller/`, and `npm run check` at the end of every section that touches the SPA or CLI.
- Do not invent new WS message names beyond the ones defined here; the SPA and the soak suite depend on exact `type` strings.
- Finish with the Acceptance checklist (§10) and the docs step (§9).

## 1. What it is

Today the controller has three unrelated ad-hoc execution disciplines: chat single-flights on a `busy` flag (`controller/internal/chat/chat.go` ~L251), TTS serializes on a 1-slot channel, and dma flushes its own spool. There is no way to schedule work for later, no record of what ran, and a dropped browser tab leaves a llama.cpp generation burning CPU for minutes.

This spec introduces one general mechanism:

- A **reliable queue** in Valkey (already supervised as `svc-valkey`, loopback `127.0.0.1:6379`, AOF under `$VM_DATA_DIR/valkey`) using the standard `LMOVE` ready→in-flight pattern with an attempt counter, a visibility-timeout sweeper, and a dead-letter list.
- A **single sequential worker** (parallelism = 1, matching the one browser + one LLM slot reality) that drains an interactive priority list before the scheduled list.
- A **time-bucket scheduler** that turns coarse human selectors ("every weekday morning", "friday night", "mon, wed, fri") into enqueued jobs, using the server-locale timezone.
- **Initiator-bound cancellation**: every job remembers the WS connection that created it; when that connection closes, a running job is context-cancelled immediately and queued jobs from it are dropped.

Spec 014 (projects) and spec 015 (Jobs page) build directly on this substrate. Interactive chat is refactored onto the queue in this spec so there is exactly one execution path for LLM work.

## 2. Shared Valkey client package

The RESP client currently lives unexported in `controller/internal/chat/valkey.go` (fresh dial per op, 2 s deadline, commands RPUSH/LTRIM/LRANGE/DEL/HINCRBY/HGETALL).

1. Create `controller/internal/valkey/valkey.go`: move the client there, exporting `type Client`, `func New(addr string) *Client`, and methods for every command used repo-wide after this spec:
   `RPush`, `LPush`, `LTrim`, `LRange`, `LLen`, `LRem`, `LMove` (`LMOVE src dst LEFT/RIGHT LEFT/RIGHT`), `Del`, `HIncrBy`, `HGetAll`, `HSet` (variadic field/value pairs), `HGet`, `HDel`, `Set`, `Get`, `Keys` is **not** allowed (O(N) scan) — use explicit index structures instead.
   Keep the existing style: one dial per `do(...)` call, `SetDeadline`, RESP2 parser as-is. Add `DoInts`/typed helpers only where a caller needs them.
2. Refactor `controller/internal/chat` to import `internal/valkey` and delete `chat/valkey.go`. Behavior of chat history/stats keys (`virtualme:chat`, `virtualme:chat-stats`) is unchanged.
3. Hermetic tests: port the existing chat valkey tests; add table tests for the new commands against an in-test fake RESP server (`net.Listen` on `127.0.0.1:0`, scripted replies) — no live Valkey needed in `scripts/check.sh`.

## 3. Job model and Valkey layout

Package `controller/internal/jobs`. A job envelope is one JSON document, stored as the list element itself (the list element is the full envelope, not an id — simplest crash-consistent shape for a single-consumer queue):

```json
{
  "id": "9f4c...-uuid",
  "type": "chat" | "project-run" | "manual-tool" | "soak-probe",
  "payload": {},
  "priority": "interactive" | "scheduled",
  "enqueuedTs": 1690000000000,
  "notBeforeTs": 0,
  "attempts": 0,
  "maxRetries": 2,
  "visibilityTimeoutSec": 600,
  "initiatorConn": "c17" ,
  "projectId": "",
  "selector": ""
}
```

- `payload` is type-specific: `chat` → `{"text": "..."}`; `project-run` → `{"projectId": "..."}` (spec 014); `manual-tool` → `{"tool":"...","args":{...}}` (spec 021); `soak-probe` → `{"echo":"..."}` (a no-op job that sleeps 1 s and records a result — exists so tests exercise the queue without an LLM).
- `initiatorConn` is empty for scheduler-enqueued jobs.
- `notBeforeTs` (ms) implements the randomized in-bucket delay (§5); the worker skips-and-requeues envelopes whose time has not come.

Valkey keys (all prefixed `virtualme:jobs:`):

| Key | Type | Purpose |
|---|---|---|
| `virtualme:jobs:ready:interactive` | list | FIFO of interactive envelopes (chat, manual kicks, tool invocations, soak pushes) |
| `virtualme:jobs:ready:scheduled` | list | FIFO of scheduler-enqueued envelopes |
| `virtualme:jobs:inflight` | list | at most 1 envelope, moved here atomically on acquire |
| `virtualme:jobs:inflight-since` | string | ms timestamp written right after acquire (visibility sweeper input) |
| `virtualme:jobs:done` | list | most recent completed/failed envelopes + result summary, `LTRIM` to 200 |
| `virtualme:jobs:dead` | list | envelopes that exhausted `maxRetries`, `LTRIM` to 100 |

Queue operations (all through `internal/valkey`):

- **Enqueue**: `RPUSH` the JSON onto the correct ready list.
- **Acquire**: try `LMOVE ready:interactive inflight LEFT LEFT`; if nil, `LMOVE ready:scheduled inflight LEFT LEFT`; if nil, sleep 500 ms and retry (plain polling — no BLMOVE, so the single connection-per-op client stays trivial). On acquire, `SET inflight-since <now-ms>`.
- **Ack (success)**: append `{"...envelope", "result": {"ok":true,"summary":"...","finishedTs":...}}` to `done`, `LTRIM done -200 -1`, then `DEL inflight inflight-since`.
- **Nack (failure)**: increment `attempts`; if `attempts > maxRetries` push to `dead` else `RPUSH` back onto its ready list; then `DEL inflight inflight-since`. Record the error string in the envelope (`"lastError"`).
- **Visibility sweeper**: goroutine, every 30 s: if `inflight` is non-empty and `now - inflight-since > visibilityTimeoutSec*1000` **and** the worker reports no running job (in-process check — the worker and sweeper live in the same process), treat as a crash leftover and nack it. On controller startup, any leftover `inflight` entry is nacked once before the worker starts (recovers from a controller crash/restart).
- **`notBeforeTs` handling**: on acquire, if `now < notBeforeTs`, move the envelope back to the tail of its ready list and continue the acquire loop (with the 500 ms sleep this is a cheap delay queue; scheduled lists are short).

Add all keys to the persistence map: amend `specs/007-persistence-locality.md` §1a (append under `## Amendments`) with one row: `virtualme:jobs:*` — job queue state — Valkey AOF under `$VM_DATA_DIR/valkey/`.

## 4. Time selectors

File `controller/internal/jobs/selector.go`. A selector is a string with grammar (lowercase, comma/space tolerant):

```
selector   := dayspart [ " " bucketpart ] | bucketpart
dayspart   := "everyday" | "weekday" | "weekend" | dlist
dlist      := day { "," day }            e.g. "mon,wed,fri"
day        := mon|tue|wed|thu|fri|sat|sun
bucketpart := "early-morning" | "morning" | "afternoon" | "evening" | "night" | "late-night" | "anytime"
```

Bucket boundaries (server-local wall clock, half-open):

| Bucket | Window |
|---|---|
| `early-morning` | 05:00–08:00 |
| `morning` | 08:00–12:00 |
| `afternoon` | 12:00–17:00 |
| `evening` | 17:00–21:00 |
| `night` | 21:00–24:00 |
| `late-night` | 00:00–05:00 |
| `anytime` | 00:00–24:00 |

Semantics and required functions:

- `Parse(s string) (Selector, error)` — strict; unknown tokens are errors. Missing dayspart means `everyday`; missing bucketpart means `anytime`. Examples that MUST parse: `"weekday morning"` ("every weekday morning"), `"fri night"` ("friday night"), `"mon,wed,fri"`, `"tue,thu morning"` ("tue, thu mornings").
- `(sel Selector) Matches(t time.Time) bool` — day-of-week set contains `t.Weekday()` AND `t` falls in the bucket window. All computations in `t.Location()`; callers pass `time.Now()` (i.e. `time.Local`).
- `(sel Selector) BucketWindow(t time.Time) (start, end time.Time)` — the concrete window of the current/next occurrence, used for jitter (§5).
- `ActiveBuckets(t time.Time) []string` — every selector token active at `t`: always contains the current named bucket, `anytime`, the current day name, and `weekday` or `weekend`, plus `everyday`. Used by the Status widget (§7).
- Hermetic tests: table-driven across DST transitions (use `time.LoadLocation("America/Los_Angeles")` fixtures), boundary instants (exactly 08:00 belongs to `morning`), and every example string above.

## 5. Scheduler and worker

File `controller/internal/jobs/manager.go`. `type Manager struct` owns: the valkey client, the worker goroutine, the sweeper, the scheduler tick, a registry of executors, and the broadcast function.

- **Executor registry**: `Register(jobType string, fn func(ctx context.Context, env Envelope) (summary string, err error))`. `main.go` registers: `chat` (wraps the existing chat generate path, §6), `soak-probe` (sleep 1 s, return the echo payload), and later specs register more. Unknown type ⇒ immediate nack with error.
- **Worker loop**: acquire → look up executor → run with a `context.WithCancel` retained on the Manager (`m.running` guarded by mutex: envelope + cancel func) → ack/nack → broadcast `queue-state` (§8). Exactly one job runs at a time; there is no concurrency knob.
- **Scheduler tick**: every 60 s, in `time.Local`:
  1. Ask the registered **source providers** (`RegisterSource(func(now time.Time) []Envelope)`; spec 014 registers the projects source) for due work. A provider is responsible for its own dedup (spec 014 stores `lastEnqueuedBucket` per project).
  2. For each returned envelope, set `priority:"scheduled"`, and set `notBeforeTs` to a uniformly random instant in `[now, bucketEnd)` (the "randomized time-respecting" behavior: work lands at an unpredictable moment inside its human bucket, never outside it). If less than 5 minutes of bucket remain, `notBeforeTs = now`.
  3. Enqueue.
- **Startup**: nack any leftover in-flight envelope (crash recovery), then start worker + sweeper + scheduler.

## 6. Interactive chat rides the queue

Refactor `controller/internal/chat/chat.go`:

1. `HandleClientMessage` for `{"type":"chat"}` no longer checks `busy`/starts `generate()` directly. It appends+broadcasts the user message exactly as today, then enqueues a `chat` envelope (`priority:"interactive"`, `initiatorConn` = the sending connection's id, `visibilityTimeoutSec: 900`).
2. The `chat` executor (registered from `main.go`) calls the existing `generate()`/`generateAgent()` path with the worker's context. `finishReply` and every broadcast frame (`chat-delta`, `chat-done`, `llm-status`, `agent-*`) are unchanged — the SPA does not need changes for this section beyond §8.
3. The old `busy` error path becomes queue feedback: when a `chat` envelope is enqueued behind other work, broadcast `llm-status` with phase `queued` and a human detail like `queued behind 2 jobs`. (The `llm-status` phase string `queued` already exists.)
4. `chat-stop` cancels the **running** job via `Manager.CancelRunning(reason)` if the running envelope's type is `chat`; queued chat envelopes from the same initiator are dropped (`LREM` by exact envelope JSON).

## 7. Initiator-disconnect cancellation and the Status widget

1. `controller/internal/ws/ws.go`: give each connection a stable id (`c17`-style counter) exposed to handlers, and add `Hub.SetOnDisconnect(func(connID string))` invoked after a connection is removed from the broadcast set.
2. `main.go` wires `OnDisconnect` → `Manager.DropInitiator(connID)`:
   - If the running job's `initiatorConn == connID` → cancel its context **immediately** (this is the resource-saving requirement: a llama.cpp stream dies within one SSE read). The job is acked into `done` with `result.ok=false, summary:"cancelled: initiator disconnected"` — it is NOT retried.
   - `LREM` every queued envelope (both ready lists) whose `initiatorConn == connID`.
   - Scheduler-enqueued jobs (`initiatorConn == ""`) are never affected by disconnects.
3. **Status page widget** — "Active time selectors". Server: extend the 2 s `state` snapshot (`controller/internal/state/state.go`) with `"scheduler": {"localTime": "<RFC3339 with offset>", "tz": "<IANA name or fixed offset>", "active": ["morning","weekday","everyday","anytime","wed"]}` computed via `jobs.ActiveBuckets(time.Now())`. SPA: in the Status section of `index.html`, insert after the `.system-grid` a card `<article class="metric selector-card"><h2>Active time selectors</h2><p id="scheduler-clock"></p><ul id="scheduler-active" class="legend"></ul></article>`; `render.js` fills the clock (formatted with `Intl.DateTimeFormat(undefined, {dateStyle:"medium", timeStyle:"medium"})` from `scheduler.localTime`) and one pill per active token. Style pills with existing `.legend` tokens; the current named bucket (first array entry) gets `--accent` text.

## 8. WS surface

Client → server (dispatch in `main.go` after the existing metrics/tts/mail/chat cases):

| type | payload | behavior |
|---|---|---|
| `job-push` | `{"type":"job-push","job":{"type":"soak-probe"|"manual-tool","payload":{...}}}` | Producer only. Validates type ∈ {`soak-probe`,`manual-tool`}, stamps id/priority `interactive`/initiatorConn, enqueues. Replies `{"type":"job-pushed","id":"..."}` to the sender. There is deliberately **no** WS consumer/ack surface. |
| `queue-peek` | `{"type":"queue-peek"}` | Replies `{"type":"queue-state", ...}` (below) to the sender. |

Server → client broadcast `queue-state`, sent on every enqueue/acquire/ack/nack/drop and in reply to `queue-peek`:

```json
{
  "type": "queue-state",
  "upcoming": [ {envelope-lite} ],
  "running":  {envelope-lite} | null,
  "finished": [ {envelope-lite + result} ]
}
```

`envelope-lite` = envelope minus payload bodies over 512 bytes (truncate payload string fields; keep ids/types/timestamps intact). `upcoming` is both ready lists in execution order (interactive first), capped at 20; `finished` is the newest 20 of `done`.

## 9. Container, CLI, docs

1. **tzdata + TZ passthrough**: new layer `docker/layers/016-tzdata.sh` (next unused number at execution time; renumber if 016 is taken): `apt-get install -y --no-install-recommends tzdata` with the standard `set -euo pipefail` header and apt-list cleanup; add its `COPY`+`RUN` pair at the END of the layer sequence in `docker/Dockerfile`. In `src/commands/start.js`, add `-e TZ=<zone>` to the `docker run` argv where `<zone>` is `process.env.TZ` if set, else `Intl.DateTimeFormat().resolvedOptions().timeZone`. Result: `time.Local` inside the container matches the host locale. Document in `help.js` output only if a `--tz` override flag is added (optional; env passthrough is sufficient).
2. **Docs**: run the `/master-update` skill at the end. Expected touchpoints: `AGENTS.md` (controller blurb + spec table row 013), `develop` skill (new packages `internal/valkey`, `internal/jobs`; layer table row 016; WS message additions; "how to add a job type" bullet), `operate` skill (queue behavior in chat: messages queue instead of erroring `busy`; note that closing the tab that asked a question cancels it), README spec/ports tables.

## 10. Tests

- Hermetic Go (no Docker, in `scripts/check.sh` scope): fake RESP server tests for `internal/valkey`; queue unit tests (enqueue/acquire priority order, ack/nack/retry/dead-letter, notBeforeTs requeue, sweeper recovery, startup recovery) against the fake; selector table tests (§4); manager cancellation tests with a stub executor (running-job cancel on `DropInitiator`, queued-drop by initiator, scheduled jobs untouched).
- `test/e2e.sh` addition: a `queue-probe.mjs` WS probe that sends `job-push` (`soak-probe`), waits for `queue-state` to show it running then finished, and asserts `job-pushed` echoed the id.
- Live soak flow (spec 015 adds the Jobs-page flow; this spec only guarantees the probe above).

## 11. Acceptance checklist

- [ ] `npm run check` green; `controller/go.mod` still has zero `require` lines.
- [ ] `chat/valkey.go` is gone; chat history/stats behave exactly as before (e2e chat probe passes).
- [ ] Two chat messages sent back-to-back: second broadcasts `llm-status` `queued`, then runs after the first finishes; no `busy:` error string remains in the codebase.
- [ ] Kill the browser tab mid-generation: `docker exec` + controller log shows the llama SSE aborted within ~2 s; `queue-state.finished` shows `cancelled: initiator disconnected`.
- [ ] `queue-peek` over `websocat`/probe returns well-formed `queue-state`.
- [ ] Status page shows the Active-time-selectors card with the server-local clock in the host's timezone (verify `TZ` passthrough by starting with `TZ=Australia/Sydney`).
- [ ] Restart the container with a job mid-flight: after restart the job re-runs (attempts=1) or dead-letters per `maxRetries` — never silently vanishes.

## Amendments

### 2026-07-23 — Queue-backed manual tools (spec 021)

`tool-invoke` creates an interactive `manual-tool` envelope tied to the
initiating WebSocket connection with a 300-second visibility timeout. The
single worker executes the shared agent tool executor, so manual calls wait
behind active chat/project work and never interleave with an agent task.
Disconnect handling cancels or drops these envelopes under the existing
initiator policy.
