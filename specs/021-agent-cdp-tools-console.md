# Spec 021: CDP Observation Tools and the Tools Console Page

| | |
|---|---|
| Status | Executed (2026-07-23) |
| Depends on | `specs/008-browser-agent.md` (tool loop, read-only CDP client), `specs/012-agent-observation-soak.md` (dense DOM), `specs/013-job-queue-scheduler.md` (`manual-tool` envelopes), `specs/015-jobs-page.md` (activity ledger) |
| Produces | Four new observation-only agent tools abstracting the major CDP operations (`dom_query`, `dom_validate`, `page_eval`, `layout_debug`); a "Tools" nav page at `/tools` with a server-driven authoritative tool list, schema-generated input forms, queue-backed manual invocation, and a typed output pane |
| Followed by | Future specs |

## 0. Executor instructions

- Constitution binds. The CDP policy from spec 008 is absolute: CDP is **observation-only** — no `Input.*`, no `Page.navigate`, no state-changing method, in any of the new tools. `page_eval` runs JavaScript, so §2.3 defines the read-only discipline it must enforce.
- The tool list shown on `/tools` must be generated from `Definitions()` — never hand-duplicated in the SPA. If the page ever lists a tool the agent cannot call (or vice versa), that is an acceptance failure.
- Stop-on-red; finish with §7 Acceptance.

## 1. New agent tools — `controller/internal/agent`

All four go through the existing read-only CDP client (`cdp.go`); reuse its `DOMSnapshot.captureSnapshot` + `Runtime.evaluate` plumbing and the ref map that `dom` maintains. All are registered in `localTools.Definitions`/`Execute` (`tools.go`) with the schemas below (JSON Schema property maps, same style as existing tools).

1. **`dom_query`** — structured extraction from the rendered DOM.
   - Schema: `{"selector": {"type":"string","description":"CSS selector evaluated in the page"}, "attributes": {"type":"array","items":{"type":"string"},"description":"attribute names to return; default: text only"}, "limit": {"type":"integer","minimum":1,"maximum":50,"default":10}}` — required: `selector`.
   - Implementation: `Runtime.evaluate` with a self-contained expression: `JSON.stringify([...document.querySelectorAll(sel)].slice(0,limit).map(n => ({tag:n.tagName.toLowerCase(), text:(n.innerText||"").slice(0,200), attrs:Object.fromEntries(attrNames.map(a=>[a,n.getAttribute(a)]).filter(([,v])=>v!=null))})))` — build the expression by JSON-encoding the selector/attrs into it (no string concatenation of raw user input; `%q`-quote via `json.Marshal`). Result capped at 12 KiB (existing `domCap` constant), truncation noted.
   - This complements `dom` (spec 012), which is substring-hint based; `dom_query` is the precise-CSS counterpart the model kept reaching for (spec 012 §1.3 evidence).
2. **`dom_validate`** — structure/content assertions, for "check that the page really shows X".
   - Schema: `{"assertions": {"type":"array","maxItems":10,"items":{"type":"object","properties":{"selector":{"type":"string"},"exists":{"type":"boolean"},"minCount":{"type":"integer"},"textContains":{"type":"string"},"attribute":{"type":"string"},"attributeEquals":{"type":"string"}},"required":["selector"]}}}` — required: `assertions`.
   - One `Runtime.evaluate` evaluates all assertions; result: `{"pass":bool,"results":[{"selector":…,"pass":bool,"count":N,"detail":"…"}]}`. Every assertion is evaluated (no short-circuit) so the model sees the full picture.
3. **`page_eval`** — bounded read-only JS extraction for cases selectors cannot express.
   - Schema: `{"expression": {"type":"string","maxLength":2000,"description":"A single JavaScript expression evaluated read-only in the page; its JSON-stringified value is returned (max 8 KiB). Mutation attempts fail."}}` — required: `expression`.
   - Read-only discipline: send `Runtime.evaluate` with `returnByValue:true`, `awaitPromise:false`, and wrap: the tool rejects (static check, before CDP) expressions containing the substrings `document.write`, `location=`, `location.href=`, `localStorage`, `sessionStorage`, `fetch(`, `XMLHttpRequest`, `history.`, `submit(`, `click(` — a heuristic tripwire, not a sandbox; the system prompt (spec 022) states eval is for reading. Cap result at 8 KiB.
4. **`layout_debug`** — geometry/visibility for a ref or selector, for diagnosing "why didn't the click land".
   - Schema: `{"ref": {"type":"string"}, "selector": {"type":"string"}}` — exactly one of the two.
   - For a `ref`: return the server-side stored box (API space), plus fresh `Runtime.evaluate` of `getBoundingClientRect`, computed `display/visibility/opacity/zIndex/pointerEvents`, `document.elementFromPoint(cx,cy)` tag at the box center (occlusion check), and scroll offsets. For a `selector`: same via `querySelector`.
   - Result: compact JSON, ≤4 KiB.

All four: `Observe: false` (plain tool results, not observation messages with images), duration + result recorded to the activity ledger like every tool (spec 015 feed point covers them automatically since they dispatch through `Execute`). Hermetic tests with a fake CDP server: expression construction is injection-safe (selector containing `"` and backslash), caps, the `page_eval` tripwire, `dom_validate` full-evaluation semantics, `layout_debug` one-of validation.

## 2. Tool-list serialization

`tools.go`: add `func (t *localTools) Manifest() []ToolManifest` where `ToolManifest{Name, Description string; Schema json.RawMessage}` — a direct re-serialization of `Definitions()` (the OpenAI-format list already built for llama.cpp). No filtering: the Tools page shows the authoritative, complete list, including `bash`, `speak`, and the actuation tools.

## 3. WS surface

| type | direction | payload |
|---|---|---|
| `tools-list-req` | client → server | `{}` — reply `tools-list` to sender |
| `tools-list` | server → client | `{"type":"tools-list","tools":[{"name","description","schema"}…]}` (also pushed on WS connect) |
| `tool-invoke` | client → server | `{"type":"tool-invoke","id":"client-uuid","tool":"dom_query","args":{…}}` |
| `tool-result` | server → client (sender only) | `{"type":"tool-result","id":"…","ok":bool,"durationMs":N,"text":"result text ≤16 KiB","image":"data:… or empty","error":"…"}` |

`tool-invoke` handling: validate the tool exists; enqueue a spec 013 `manual-tool` envelope (`priority:"interactive"`, `initiatorConn` = sender, `visibilityTimeoutSec: 300`). The `manual-tool` executor (register in `main.go`) calls `localTools.Execute` directly with the worker context and sends `tool-result` to the initiating connection (via a hub `SendTo(connID, …)` helper — add it to `internal/ws` if spec 013 didn't). Queue-backed invocation means manual tool calls serialize with everything else and never race an agent mid-task; the UI must communicate queueing (§4).

## 4. Tools page — `/tools`

1. Router: `["/tools", ["tools", "Tools"]]`. Nav link after Jobs: icon `i-wrench` (add to fetch list). No Home quick-link (operator/debug surface, keep Home focused).
2. Markup skeleton:

```html
<section data-page="tools" hidden class="tools-page" aria-labelledby="tools-title">
  <div class="page-heading"><h1 id="tools-title">Tools</h1><p class="page-caption">Every tool available to the local model, invocable by hand. Calls join the job queue.</p></div>
  <div class="tools-grid">
    <nav id="tools-list" class="tools-list" aria-label="Tools"></nav>
    <form id="tool-form" class="tools-form" hidden></form>
    <aside id="tool-output" class="tool-output" hidden aria-live="polite"></aside>
  </div>
</section>
```

3. Module `controller/web/static/js/tools.js`, `initTools(send)`, wired in `app.js` (init; dispatch `tools-list`, `tool-result`; send `tools-list-req` on entering the page if no list cached).
4. **List column**: one row per tool from `tools-list` — name (`--font-mono`, 0.85 rem) + first sentence of the description (muted). Selecting a row builds the form. Order as served (authoritative order = `Definitions()` order).
5. **Form column — schema-generated**: for each property in `schema.properties` (honoring `required`):
   - `type:"string"` + `enum` → `<select>`; `maxLength > 200` or name ∈ {`command`,`expression`,`text`} → `<textarea rows=4>`; else `<input type=text>`.
   - `type:"integer"|"number"` → `<input type=number>` with `min`/`max` from schema.
   - `type:"boolean"` → checkbox.
   - `type:"array"` or `type:"object"` → `<textarea>` accepting JSON, validated client-side with `JSON.parse` before send (inline error text under the field, `--err`).
   - Each field: label = property name (mono), help line = property `description` (muted, 0.75 rem). Required fields marked `*` and enforced.
   - Footer: **Invoke** button; after send it disables and shows `queued…` until `tool-result` with the matching `id` arrives (or 120 s client timeout → re-enable with an error note). A muted line under the button: `Runs through the job queue; a busy agent finishes first.`
6. **Output pane** (right on desktop: `grid-template-columns: 16rem 1fr 24rem`; on mobile the three columns stack list → form → output):
   - Header: tool name, ok/err dot, duration.
   - Body typed by content: if `image` non-empty render `<img>` (max-width 100%); if `text` parses as JSON pretty-print in `<pre>` with `--font-mono`; else plain `<pre>` text. Always scrollable, `max-height: 60vh`.
   - Keep the last result per tool in memory; switching tools restores its last output.
7. Safety note: this page can drive `bash` and the actuation tools by hand — acceptable under the spec 002 trust model (anyone who can reach 8080 already owns the box); state this in a muted footer line: `Trusted-network console; no additional auth (see spec 002 trust model).`

## 5. Soak flow

Add to `test/soak.mjs` (raw-WS `run()` flow like `jobs-queue-probe`): **`tools-roundtrip`** — send `tools-list-req`; hard-assert the reply contains ≥ 15 tools including every name: `screenshot,dom,read_page,click,click_element,type,type_into,key,scroll,navigate,bash,system_info,speak,dom_query,dom_validate,page_eval,layout_debug`; then `tool-invoke` `system_info` `{topic:"os"}` and hard-assert a `tool-result` with `ok:true` and non-empty text arrives ≤ 60 s.

## 6. Tests and docs

- Hermetic Go per §1 + manifest golden test (the manifest marshals to the same JSON the LLM sees).
- Docs: `/master-update` — operate skill (Tools page purpose + trust note), develop skill (four new tool rows; "adding a tool automatically surfaces it on /tools via Manifest()"; WS table), README endpoints/spec tables. The agent system prompt gains one clause about the new observation tools — coordinate with spec 022 (if 022 executes after, add the clause to the prompt file it creates; if before, amend `buildSystemPrompt`).

## 7. Acceptance checklist

- [ ] `npm run check` green; CDP client still contains no state-changing method calls (grep for `Input.`, `Page.navigate` under `internal/agent` — only comments/tripwire strings allowed).
- [ ] `/tools` lists exactly the tools in `Definitions()`; adding a dummy tool in a scratch branch makes it appear with a generated form, no SPA edits.
- [ ] `dom_query` on `https://example.com` with selector `h1` returns the heading; `dom_validate` passes `{selector:"h1",textContains:"Example"}` and fails `textContains:"Nonexistent"` with both results reported.
- [ ] `page_eval` returns `document.title`; an expression containing `fetch(` is rejected before any CDP traffic.
- [ ] `layout_debug` on a ref from a prior `dom` call reports box + computed visibility + elementFromPoint.
- [ ] Invoking a tool while a chat generation runs shows `queued…` and completes after — never interleaves.
- [ ] Soak `tools-roundtrip` passes.

## Amendments

### 2026-07-23 — Execution details

- The CDP transport now rejects every method except `Runtime.evaluate` and
  `DOMSnapshot.captureSnapshot`, making the observation-only contract a
  centralized allowlist. `page_eval` also sets Chromium's
  `throwOnSideEffect:true` in addition to the specified static tripwires.
- Production constructs one `localTools` instance shared by the agent and
  `manual-tool` executor. Sequential queue execution therefore preserves DOM
  refs between manual `dom` and `layout_debug` calls without racing an agent.
- Manual tool failures are terminal tool outcomes (`tool-result.ok:false`) and
  successful queue dispatches, rather than retried infrastructure failures.
  Their failed status, arguments, text, and duration remain authoritative in
  the persistent activity ledger.

### 2026-07-24 — Wider layout and result-image lightbox

Console feedback pass (specs 012–025 amendments). Files:
`controller/web/static/js/tools.js`, `css/app.css`.

1. **Wider three-column layout.** With the site-wide `main > section` cap
   raised to 100rem (spec 024 amendment), `.tools-grid` becomes
   `minmax(16rem, 22rem) minmax(0, 1.4fr) minmax(24rem, 1fr)`: the tool list
   stays compact while the form and output panes grow with the viewport.
2. **Result-image lightbox.** The result image in the output pane is wrapped
   in a borderless `.tool-image-zoom` button (`cursor: zoom-in`, labelled
   "Open <tool> result full screen"). Clicking it appends a
   `role="dialog"` `.lightbox` overlay: dimmed full-viewport backdrop,
   the image at up to 96vw × 85vh, and a fixed top-right action row with a
   **Download** link (`<a download="<slug>.png">` pointing at the same data
   URI) and a **Close** button. Escape, the Close button, or clicking
   anywhere except the Download link dismisses the overlay and restores
   focus to the zoom button. No new endpoints; purely client-side.

### 2026-07-24 — Shape-based structured result rendering (spec 026 T1, T2)

The output pane may recognize result *shapes* (never tool names, preserving
§4's no-hardcoded-names rule): JSON whose keys are a subset of
`{url, title, text}` renders a linked title, muted url, and wrapped plain
page text; runs of three or more `KEY=value` lines inside plain-text results
render as sorted two-column tables. All other results keep the generic
pretty-JSON / plain-text / image paths.

### 2026-07-25 — Context-scaled observation limits (spec 029)

Spec 029 supersedes the tool-result limits in §2 and §3: `dom`/`dom_query`/
`dom_validate` use 24 KiB, `page_eval` uses 16 KiB, and manual tool-result
text uses 64 KiB. The Tools manifest also exposes the development-only
`dump_dom` capture tool; it is intentionally absent from model definitions.

### 2026-07-25 — Console fix and taste sweep

1. **Tool list height.** The left tool list column is viewport-height
   scrollable.
2. **Params width.** Without tool results, the params form fills the
   remaining width (two-column grid: list | form).
3. **Results column.** The results column appears only when a result exists
   for the selected tool and is dismissible with an X; dismissing clears the
   cached result and collapses back to two columns.
