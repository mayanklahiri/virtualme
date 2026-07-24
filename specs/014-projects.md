# Spec 014: Projects — Periodic Natural-Language Tasks

| | |
|---|---|
| Status | Draft |
| Depends on | `specs/013-job-queue-scheduler.md` (queue, selectors, scheduler sources), `specs/005-console-ui.md` (themes, page conventions) |
| Produces | A "Projects" nav entry and `/projects` page (list + create-project modal), a per-project overview at `/projects/<id>`, Valkey-persisted project records, a per-project data subdirectory under `$VM_DATA_DIR/projects/<id>/`, manual "Run now" kicks, and scheduler integration |
| Followed by | `specs/015-jobs-page.md` |

## 0. Executor instructions

- The constitution binds this spec: zero Go `require` lines, zero npm runtime deps, SPA stays hand-written ESM with no framework.
- Execute after spec 013; this spec assumes `controller/internal/jobs` (Manager, `Register`, `RegisterSource`, envelopes, selectors) and `controller/internal/valkey` exist.
- UI work must use existing theme tokens only (`--surface`, `--border`, `--fg`, `--muted`, `--accent`, `--ok`, `--err`, `--p1`…`--p8`, `--radius`, `--motion`, `--font-*`). No hex colors, no new fonts, no images. Verify every one of the eight themes × light/dark renders acceptably before finishing.
- Stop-on-red per section; finish with §8 Acceptance and §7 Docs.

## 1. What it is

A **project** is a recurring background task: a natural-language description of what to do ("check the tide tables for Half Moon Bay and mail me a summary") plus a coarse schedule selector from spec 013 §4 ("weekday morning", "fri night", "mon,wed,fri"). When the scheduler fires, a `project-run` job is enqueued; the executor feeds the task text through the existing agent loop (same path as a chat message, but non-interactive). Projects can also be kicked off manually; if the browser/LLM is busy the kick simply queues (spec 013 queue semantics — nothing special to build).

Each project owns a data subdirectory it may use any way it chooses (the agent's `bash` tool can read/write it; future specs may add structured artifacts).

## 2. Persistence model

All through `controller/internal/valkey`:

| Key | Type | Content |
|---|---|---|
| `virtualme:projects` | hash | field = project id (UUID), value = project JSON (below) |
| `virtualme:projects:runs:<id>` | list | newest-first run summaries, `LTRIM` to 50 |

Project JSON:

```json
{
  "id": "uuid",
  "name": "Tide report",
  "task": "Check the NOAA tide tables for Half Moon Bay ...",
  "selector": "weekday morning",
  "enabled": true,
  "createdTs": 1690000000000,
  "lastRunTs": 0,
  "lastEnqueuedBucket": ""
}
```

`lastEnqueuedBucket` is the dedup token for the scheduler source: the string `<YYYY-MM-DD>/<bucket-name>` of the most recent bucket occurrence for which a run was enqueued. Format it in server-local time.

Run summary JSON (appended by the executor): `{"ts":…, "jobId":"…", "ok":true, "summary":"first 300 chars of the final reply or error", "durationMs":…, "manual":false}`.

Amend `specs/007-persistence-locality.md` §1a (append under `## Amendments`) with two rows: `virtualme:projects*` (Valkey AOF) and `$VM_DATA_DIR/projects/<id>/` (project scratch space, created on demand).

## 3. Controller: `controller/internal/projects`

New package with a `Service` owning the valkey client, the jobs Manager handle, and the broadcast func.

1. **CRUD via WS** (dispatch added in `main.go` after the spec 013 cases):

| type (client→server) | payload | reply / broadcast |
|---|---|---|
| `projects-req` | `{}` | `projects` (below) to sender |
| `project-create` | `{"name":"…"}` (1–80 chars, trimmed, non-empty) | creates with empty task, selector `"weekday morning"`, `enabled:false`; broadcasts `projects` |
| `project-update` | `{"id","name?","task?","selector?","enabled?"}` | validates selector with `jobs.Parse`; broadcasts `projects` |
| `project-delete` | `{"id"}` | removes hash field + runs list; broadcasts `projects`; does NOT delete the data dir (operator data is sacred; document in operate skill) |
| `project-run` | `{"id"}` | enqueues a `project-run` envelope (`priority:"interactive"`, `initiatorConn` = sender — note: per spec 013 §7 the run dies if the kicking tab closes; that is accepted v1 behavior and must be stated in the UI as "runs while this page stays open, otherwise schedule it") |

  Server → client `projects` frame: `{"type":"projects","projects":[…full project JSON…],"runs":{"<id>":[…up to 5 newest…]}}`. Broadcast on any mutation and on WS connect (add to the `SetOnConnect` block in `main.go`).
  Validation errors reply `{"type":"project-error","error":"…"}` to the sender only.

2. **Scheduler source** (spec 013 §5): register `func(now) []Envelope` that, for each `enabled` project whose selector `Matches(now)` and whose `lastEnqueuedBucket != currentBucketToken(now)`, returns one `project-run` envelope (`initiatorConn:""`, `visibilityTimeoutSec: 1800`, `maxRetries: 1`) and writes the new `lastEnqueuedBucket` back (write-before-return so a crash cannot double-enqueue more than once per bucket).
3. **Executor** (registered with the Manager under type `project-run`):
   - Ensure `$VM_DATA_DIR/projects/<id>/` exists (`os.MkdirAll`, 0755) — "create if not exists".
   - Build the task prompt: the project's `task` text, prefixed with one grounding line: `Project "<name>" scratch directory: <abs path>. You may read and write files there with the bash tool.`
   - Run it through the same code path as a chat turn (`chat.Service` must export a `RunTask(ctx, text) (reply string, err error)` that wraps `generateAgent` without touching the shared chat history — project runs must NOT pollute `/chat`; give the agent a fresh message list with the standard system prompt).
   - Append the run summary (§2) and `HSET` `lastRunTs`; broadcast `projects`.
4. Hermetic tests: CRUD validation, dedup token behavior across bucket edges, executor writes run summaries and creates the directory (use `t.TempDir()` as `VM_DATA_DIR`), and a stub `RunTask`.

## 4. SPA: routes and nav

1. `controller/web/static/js/router.js`: routes are a `Map` of exact paths; add `["/projects", ["projects", "Projects"]]`. For the overview, add prefix support: in `render()`, if `location.pathname` starts with `/projects/` treat page id as `project-detail` with title `Project`. Keep the fallback-to-home behavior for anything else.
2. `controller/web/static/index.html` nav (`.nav-links`, after the Home link): `<a href="/projects" data-nav><svg class="icon"><use href="/icons.svg#i-folder-kanban"/></svg>Projects</a>`. Add the `folder-kanban` Lucide icon to `controller/tools/fetch-assets.sh`'s icon list and rebuild the sprite (`scripts/build-icons.mjs` path is already wired into `build-web.sh`). Also add a Projects card to `.quick-links` on Home: `<strong>Projects</strong><span>Recurring background tasks</span>`.
3. New sections in `index.html`:

```html
<section data-page="projects" hidden aria-labelledby="projects-title">
  <div class="page-heading"><h1 id="projects-title">Projects</h1>
    <button id="project-new" type="button"><svg class="icon"><use href="/icons.svg#i-plus"/></svg>New project</button></div>
  <ul id="project-list" class="project-list"></ul>
  <p id="projects-empty" class="empty-note" hidden>No projects yet. A project is a task Virtual Me runs on a schedule.</p>
  <dialog id="project-dialog">
    <form id="project-create-form" method="dialog">
      <h2>New project</h2>
      <label for="project-name">Name</label>
      <input id="project-name" maxlength="80" required autocomplete="off">
      <div class="dialog-actions"><button value="cancel" class="secondary" type="button" id="project-cancel">Cancel</button><button type="submit">Create</button></div>
    </form>
  </dialog>
</section>
<section data-page="project-detail" hidden aria-labelledby="project-detail-title">…(§5)…</section>
```

Use the native `<dialog>` element (`showModal()`/`close()`); style it with theme tokens (`background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius)`; `::backdrop` with a translucent `--bg`). Add `i-plus` (and `i-play`, `i-trash-2` is already present) to the icon fetch list if missing.

## 5. SPA: pages and UI design

New module `controller/web/static/js/projects.js` exporting `initProjects(send)`; wire it in `app.js` (init + route `projects`/`project-detail` render + `projects`/`project-error` WS frames). Design intent: **refined, minimal, quiet** — this page should read like a well-set table of contents, not a dashboard.

**List page** (`#project-list`): one row per project, a bordered card row (`display:grid; grid-template-columns: 1fr auto auto`):
- Left: project name (`--font-heading`, weight 600) over a one-line muted summary: the selector rendered as prose (write a `selectorLabel()` that maps `"weekday morning"` → `Every weekday morning`, `"fri night"` → `Fridays at night`, `"mon,wed,fri"` → `Mon, Wed, Fri, any time`) plus `· last ran <relative time>` when `lastRunTs > 0` (relative via `Intl.RelativeTimeFormat`).
- Middle: a small status dot + word — `Enabled` (`--ok`) / `Paused` (`--muted`).
- Right: chevron affordance; the whole row is a link to `/projects/<id>` (an `<a data-nav>` so the router intercepts it).
- Sorted by name. Empty state shows `#projects-empty`.

**Create flow**: `#project-new` opens the dialog, autofocuses the name field; on submit sends `project-create`; on the next `projects` broadcast containing a project whose name matches and which was not previously known, navigate to its detail page. Cancel/Escape closes without sending.

**Detail page** (`section[data-page="project-detail"]`), populated from the cached `projects` frame by id parsed from the path:
- Heading row: editable name (click-to-edit inline: an `<input>` styled as an `<h1>` until blur, then `project-update`), a Run-now button (`i-play` icon, primary style; disabled with tooltip text `queued — will run when the current job finishes` handling is automatic via queue), and a Delete button (secondary, two-click arm like chat Clear: first click turns it into `Confirm delete`, second sends `project-delete` and navigates to `/projects`).
- **Task** card: `<textarea>` (maxlength 4096) with the project's `task`; a muted caption `Written in plain language; Virtual Me's agent follows it with the browser, bash, and mail tools.`; saves on blur or Ctrl/Cmd+Enter via `project-update`.
- **Schedule** card: composable chips, two rows.
  - Row 1 "Days": chips `Every day` `Weekdays` `Weekend` `Mon` `Tue` `Wed` `Thu` `Fri` `Sat` `Sun`. The three group chips are exclusive with the individual-day chips; individual days multi-select.
  - Row 2 "Time": chips `Any time` `Early morning` `Morning` `Afternoon` `Evening` `Night` `Late night` (single-select).
  - Selected chips use `--accent` background + `--accent-fg` text; unselected use `--surface` + `--border`. Below the chips render the live prose label (`selectorLabel()`) and the serialized selector string; every change sends `project-update` with the serialized selector (serialize to the spec 013 §4 grammar; e.g. days {mon,wed,fri} + `anytime` → `"mon,wed,fri"`).
  - An `Enabled` switch (reuse the `role="switch"` component introduced by spec 017 if it has landed; otherwise a styled checkbox with the same markup contract: `<button role="switch" aria-checked>`) gating scheduling; manual runs work regardless.
- **Recent runs** card: up to 5 rows from `runs[id]`: local time (`Intl.DateTimeFormat` medium), ok/err dot, summary excerpt, duration. Empty state `No runs yet.`
- **Data** line (muted, monospace): `Scratch directory: ~/.virtualme/projects/<id>/` — display-only.

Mobile (`@media (max-width: 47.999rem)`): cards stack; chips wrap; the heading row wraps with Run-now full-width.

## 6. Tests

- Hermetic Go tests per §3.4.
- `test/e2e.sh`: new `projects-probe.mjs` — over WS: `project-create` → assert `projects` broadcast contains it; `project-update` selector `"tue,thu morning"` → echoed back; `project-run` → a `queue-state` frame eventually shows a `project-run` envelope (running or finished; with no LLM assertion — the e2e model reply content is not asserted, only that the job lifecycle completed and a run summary appeared in the next `projects` frame); `project-delete` → gone. Restart cycle in e2e must show the project surviving (Valkey AOF).
- Soak: no new flow here (spec 015 covers queue visibility).

## 7. Docs

Run `/master-update`. Expected touchpoints: operate skill (Projects page usage, the "manual kick dies if you close the tab" caveat, scratch dir location, delete-keeps-data note), develop skill (`internal/projects` row, WS message table, icon-fetch note), README + AGENTS spec tables, endpoints list gains `/projects`.

## 8. Acceptance checklist

- [ ] `npm run check` green; zero new Go/npm deps.
- [ ] Create → rename → write task → pick `Tue, Thu` + `Morning` → enable: serialized selector is `tue,thu morning` and survives container restart.
- [ ] Run now with an idle queue: run starts within ~2 s; with a chat generation in flight: envelope visible as upcoming, runs after.
- [ ] A run creates `$VM_DATA_DIR/projects/<id>/` and appends a run summary visible on the detail page.
- [ ] Project runs do not appear in `/chat` history.
- [ ] Scheduler: with selector `anytime` and enabled, a scheduled run enqueues within one minute (with random in-bucket delay ≤ bucket end), and does not enqueue twice in the same bucket occurrence.
- [ ] All eight themes × light/dark: list, dialog, chips, and detail cards render with correct contrast (spot-check `terminal`, `contrast`, `solar` dark).
