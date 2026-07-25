# Spec 027: Structured `read_page` — YAML Page Digest and Collapsible Tree Rendering

| | |
|---|---|
| Status | Accepted (2026-07-24) |
| Depends on | `specs/008-browser-agent.md` (agent loop, tools, CDP client), `specs/012-agent-observation-soak.md` (observation caps, soak suite), `specs/021-agent-cdp-tools-console.md` (CDP observation tools, Tools console), `specs/022-system-prompt.md` (prompt references `read_page`) |
| Produces | A `read_page` tool that returns a deterministic, hierarchically structured YAML digest of the important visible DOM (title, url, head, body); a hand-written deterministic YAML-subset emitter in Go and a matching zero-dependency parser in JS; a polished reusable collapsible-tree widget rendering the digest on the Tools console; unit tests against a committed raw-DOM fixture of `www.lahiri.me`; the `dom_query` nil-attributes fix with its failing-test-first record; a binding tool-testing discipline (every tool bug gets a failing test first; every tool is soak-exercised live on `www.lahiri.me`) |
| Followed by | Future specs |

## 0. Executor instructions

- The constitution (`specs/001-constitution.md` §1) binds this spec: runtime code imports only `node:*`/Go stdlib; the SPA gains no dependencies; the YAML emitter, YAML parser, and tree widget are hand-written.
- Every algorithm in §3–§4 is normative. Where a constant is marked *tunable* you may adjust it during execution and record the final value in an amendment; everything else is implemented exactly as written.
- Determinism is a hard requirement: the same DOM in must produce byte-identical YAML out. No randomness, no timestamps, no map-iteration-order dependence anywhere in the pipeline.
- Stop-on-red per section; finish with the Acceptance Checklist.

## 1. Problem (evidence, 2026-07-24)

`read_page` today (`controller/internal/agent/cdp.go`, `ReadPage`) evaluates

```
({url:location.href,title:document.title,text:(document.body?.innerText||"").slice(0,65536)})
```

and returns compact JSON `{"url","title","text"}`. All structure, metadata, and
context are lost: links lose their targets, images and media are invisible,
tables flatten into word soup, forms disappear, and there is no way for the
model to reference any element it read. On a dense news-feed page the
concatenated `innerText` exceeds the 16 KiB observation cap and is truncated
blind, so the model reads an arbitrary prefix of the page. The tool is
currently the *least* informative observation; this spec makes it the primary
one for information extraction.

## 2. Grounding: why YAML (web research, 2026-07)

Tool results are model **input**. Published measurements (MightyBot format
ranking; MarkDone "Token Tax" benchmark; WildandFree JSON-vs-YAML study;
tensorlake TOON benchmark; dev.to Claude-format comparison) agree on:

1. For **input**, YAML costs ~15–30% fewer tokens than pretty JSON on nested,
   irregular data (no braces, no quote-and-comma tax), and its
   indentation-as-structure reads naturally to instruction-tuned models.
2. Uniform-table formats (TOON, CSV) beat YAML only on flat arrays of
   identical rows. A page digest is an irregular tree — YAML's sweet spot.
3. Strict JSON remains preferable for machine *output* contracts (tool-call
   arguments), which this spec does not change: tool schemas stay JSON; only
   the *result text* of `read_page` becomes YAML.
4. Small models (the 4 GB-class deployment target) benefit most: shallower
   punctuation nesting measurably improves extraction accuracy at fixed
   context.

## 3. The page digest

### 3a. Result shape

`read_page` returns one YAML document (a mapping) with exactly four top-level
keys, always in this order:

```yaml
title: "Example Domain"
url: "https://example.com/"
head:
	lang: "en"
	description: "…"          # meta[name=description], if present
	canonical: "https://…"    # link[rel=canonical], if present
	og:                       # meta[property^="og:"], if any; keys sorted
		image: "https://…"
		title: "…"
body:
	- tag: "h1"
		text: "Example Domain"
	- tag: "p"
		text: "This domain is for use in illustrative examples…"
		children:
			- tag: "a"
				href: "https://www.iana.org/domains/example"
				text: "More information..."
```

(Indentation above is literal tabs — see §4.)

The previous plain-text output is **dropped entirely** — there is no `text`
top-level key; text lives on the structured nodes.

### 3b. Node schema

Each digest node is a mapping. Keys are emitted in this fixed order, omitting
absent ones (never emit empty strings, empty sequences, or empty mappings):

| Key | When present | Content |
|---|---|---|
| `tag` | always | lowercased element tag |
| `text` | element has own text | own visible text (§3d), normalized, ≤ 300 chars (*tunable*), truncated with a trailing `…` |
| `href` | `a`, `area` | absolute resolved URL, ≤ 300 chars |
| `src` | `img`, `video`, `audio`, `source`, `iframe`, `embed` | absolute resolved URL, ≤ 300 chars |
| `alt` | `img` with non-empty alt | alt text, ≤ 120 chars |
| `type`, `name`, `value`, `placeholder` | form controls | attribute values, each ≤ 120 chars (`value` omitted for `type=password`) |
| `action`, `method` | `form` | resolved action URL and lowercased method |
| `label` | form control with an associated `<label>` | the label's normalized text, ≤ 120 chars |
| `rows` | `table` | sequence of sequences of cell strings (§3f) |
| `items` | `ul`, `ol`, `dl` | sequence of item digests (§3f) |
| `children` | node has kept descendants not otherwise absorbed | sequence of nodes |
| `note` | truncation markers only | fixed strings defined below |

### 3c. Extraction algorithm

Extraction runs in the page as **one** CDP `Runtime.evaluate` with
`returnByValue: true`. The script is not an inline Go string: it lives in a
new committed file `controller/internal/agent/js/readpage.js`, embedded via
`//go:embed`, written as a single expression `(() => { … })()` that reads only
the globals `document` and `location` — this exact file is also executed by
the Node unit tests (§9) under `node:vm` against a DOM stub, so it must use
only the DOM APIs listed here: `document.title`, `location.href`,
`document.documentElement`, `document.head`, `document.body`, and per element
`tagName`, `id`, `children`, `childNodes`, `nodeType`, `nodeValue`,
`getAttribute`, `getClientRects`, and `window.getComputedStyle(el)` returning
at least `visibility` and `display`. Do not use `innerText`, `innerHTML`,
`querySelectorAll`, or `checkVisibility` in this script.

Traversal is a single depth-first, document-order walk of `document.body`:

1. **Prune subtrees** rooted at: `script`, `style`, `noscript`, `template`,
   `svg` (emit `svg` itself as a childless node only if it has a non-empty
   `aria-label`), and any element with `aria-hidden="true"`.
2. **Visibility**: an element is visible iff `getClientRects().length > 0`
   and computed `visibility !== "hidden"`. Invisible elements are pruned with
   their subtrees. (`display:none` yields zero client rects; no separate
   check needed.)
3. **Own text** (§3d) is computed from the element's direct child text nodes
   only — descendant element text belongs to the descendants.
4. Every visited visible element becomes a **candidate node** with the §3b
   properties it can populate.
5. **Wrapper collapse** (the core simplification): bottom-up, a candidate is
   *hoisted away* — replaced in its parent's child list by its own children —
   iff all of: it has no `text`; it populates none of `href`, `src`, `alt`,
   `type`, `name`, `value`, `placeholder`, `action`, `rows`, `items`; and its
   tag is not in the semantic keep-set `{h1…h6, a, img, video, audio, iframe,
   table, form, input, select, textarea, button, label, ul, ol, dl, li, p,
   blockquote, pre, code, time, figcaption}`. Nested content-free `div`/
   `span`/`section`/`article` chains therefore vanish while their content is
   promoted. Apply until fixpoint (a single bottom-up pass suffices because
   children are collapsed before their parent is considered).
6. **Node budget**: at most 800 kept nodes (*tunable*), counted in document
   order. When the budget is hit, stop descending and append to `body` one
   final marker node `note: "truncated: node budget reached"`.

The script returns the digest as a plain JSON-safe object tree; the Go side
(§4) converts it to YAML. `ReadPage` in `cdp.go` keeps its signature; the
dispatch in `tools.go` keeps `Observe: true` with summary `Read page digest`.

### 3d. Text normalization

Own text = concatenation of direct child text-node values, then: collapse
every run of Unicode whitespace to one space, trim, and if the result exceeds
the cap, cut at the cap and append `…`. An element whose normalized own text
is empty has no `text` key.

### 3e. Selectors (removed by A2)

Digest nodes carry **no** `sel` key. Selector paths dominated digest bytes
(≈ 40 % on component-heavy pages) and tokenized poorly, crowding out actual
content; follow-up interaction goes through `dom`/`click_element` refs or a
model-constructed `dom_query` selector instead. The original per-node
selector design is recorded in this spec's history; see amendment A2.

### 3f. Tables, lists, forms

- **Tables**: `rows` holds the first 40 rows (*tunable*) as sequences of cell
  strings (each `th`/`td` normalized as §3d, ≤ 80 chars). Nested markup
  inside cells is flattened to the cell's full visible text (for cells,
  descendant text is included). If rows were dropped, append one final row
  `["…truncated"]`. A table node has no `children`.
- **Lists**: `items` holds up to 40 item digests. An `li` whose subtree
  contains no kept structural nodes other than inline links collapses to a
  mapping with `text` (full visible text of the item, ≤ 300 chars) and, when
  it contains exactly one link, that link's `href` — this keeps news-feed
  lists one line per story. Items with richer structure recurse as normal
  nodes. If items were dropped, append `note: "…truncated"` as the last item.
- **Forms**: a `form` node lists its visible controls under `children`
  (controls populate `type`/`name`/`value`/`placeholder`/`label`); buttons
  keep their `text`.

## 4. YAML subset: Go emitter, JS parser

One strict subset, produced by Go and parsed by JS. Anything outside the
subset is a bug in the emitter or a rejection in the parser.

- Block-style mappings and sequences only; **one tab per indentation level**
  (a tab is one token where two-space levels compound); sequence items as
  `- ` at the parent's indent + 1 tab. Tabs may appear only as indentation;
  spaces may not appear in indentation.
- Scalars: strings are always double-quoted using JSON string escaping
  without HTML escaping (Go: `json.Encoder` with `SetEscapeHTML(false)`;
  embedded newlines become `\n`, embedded tabs `\t` — no multi-line scalars,
  no plain-style strings). Integers bare. No booleans, null, anchors,
  aliases, tags, flow style, comments, or document markers.
- Mapping keys are bare `[a-z][a-z0-9_]*` identifiers.
- Key order: top-level `title, url, head, body`; head `lang, description,
  canonical, og`; nodes as the §3b table order; any key not covered by a
  priority table sorts alphabetically. The emitter takes an explicit
  priority-ordered key list — never Go map iteration order.

**Go**: new `controller/internal/agent/yaml.go` with
`encodeYAML(v any) string` operating on `map[string]any` / `[]any` / `string`
/ `float64` (JSON-decoded values) plus the key-priority table. New
`readPageCap = 16000` bytes: after emission, while the document exceeds the
cap, remove the digest's last remaining body node (deepest-last first:
repeatedly drop the final element of the deepest trailing `children`/`items`/
`rows`/`body` sequence) and re-emit; then append body marker node
`note: "truncated: page digest exceeded budget"`. 16000 < the 16 KiB
`observationTextCap` minus the `Observation from read_page:\n` prefix, so the
model always receives a complete, well-formed YAML document — never a
mid-document cut.

**JS**: new `controller/web/static/js/yaml-lite.js` exporting
`parseYamlLite(text)` → plain JS values, hand-written, line-based,
indentation-driven; quoted scalars are decoded with `JSON.parse`. It throws
on anything outside the subset (space indentation, tabs outside indentation,
flow style, unquoted strings). It is a pure module (no `document`),
unit-testable under Node.

## 5. Tool description and agent wiring

The `Definitions()` entry for `read_page` (`controller/internal/agent/tools.go`)
becomes (normative):

> Read the current page as a structured YAML digest: title, url, head
> metadata, and a simplified hierarchy of the important visible elements —
> headings, text blocks, links (href), images (src, alt), media, tables
> (rows), lists, and form structures.
> This is the primary tool for extracting information from a page; prefer it
> over screenshots or dom for reading content, and use dom_query or dom for
> follow-up detail on specific elements.

Schema stays the empty-object schema (no parameters). The spec-022 system
prompt already names `read_page` in its observe-first policy; its wording is
unchanged (no 022 amendment needed — descriptions live in tool schemas, not
prompt files).

## 6. Tools console: collapsible tree widget

1. **Widget**: new reusable module `controller/web/static/js/tree.js`
   exporting `renderTree(value, {expandDepth = 2}) → HTMLElement`. Renders any
   parsed subset value as a nested tree: mappings and sequences become
   collapsible branches with a disclosure `<button aria-expanded>` (rotating
   chevron drawn in CSS, gated on `--motion`/`prefers-reduced-motion`), key
   names styled `.tree-key`, scalar values `.tree-val`, string values quoted,
   sequences labelled with their length (e.g. `body (12)`). Branches at depth
   ≤ `expandDepth` start open. Built entirely with `createElement`/
   `textContent` — never `innerHTML`. Keyboard: the disclosure buttons are
   ordinary buttons (tab + Enter/Space work for free). Styling in `app.css`
   under a `.tree` block using existing theme tokens (`--muted`, `--accent`,
   `--border`, `--font-mono` for values); monospace values, 0.85 rem,
   comfortable 1.5 line height; hovering a row highlights it with a
   `--surface`-tinted background. The widget is generic — spec 028 reuses it
   for the Data explorer — so it must not import from `tools.js` or reference
   tool concepts.
2. **Detection**: `classifyResult` (`controller/web/static/js/tools-render.js`)
   gains a `yaml-digest` classification, tried before the existing JSON
   branches: the text parses under `parseYamlLite` to a mapping with string
   `title` and `url` keys where `url` matches `^https?:\/\/`. Classification
   stays pure (no DOM) for unit tests.
3. **Rendering**: for `yaml-digest` results, `tools.js` renders the existing
   page-shaped header (linked title + url line, reusing `.tool-page-title`/
   `.tool-page-url`) followed by `renderTree({head, body})`. All other result
   shapes render exactly as today.

## 7. `dom_query` nil-attributes crash: root cause and fix

### 7a. Evidence

Manual Tools-console invocation of `dom_query` with only
`{"selector":"img"}` fails:

```
TypeError: Cannot read properties of null (reading 'map')
    at <anonymous>:1:169
```

Root cause (`controller/internal/agent/tools.go`, `domQuery`): the decoded
`Attributes []string` is `nil` when the argument is omitted, and
`json.Marshal(nil)` emits the JS literal `null`, which is spliced into the
evaluated expression as `Object.fromEntries(null.map(a=>…))`. The selector is
incidental — **every** `dom_query` call that omits `attributes` (the schema's
own documented default, "default: text only") throws. It escaped detection
because (a) the only Go test (`tools_cdp_test.go`) always passes
`"attributes":["href"]`, and (b) no automated suite ever invokes `dom_query`
against a real page — soak's `tools-roundtrip` only lists tools and invokes
`system_info`.

### 7b. Fix — failing test first

1. Write the failing tests **before** the fix and record the red run in the
   execution amendment:
   - Go: `dom_query` with `{"selector":"img"}` (attributes omitted) — the
     generated expression must splice `[]` where the attribute array goes and
     must contain no `null` token; a second case passes explicit JSON
     `"attributes":null` and expects the same. Both fail against the current
     code.
   - Node: `test/dom-query-expr.test.js` executes the *actual* generated
     expression (the Go test writes it to
     `test/fixtures/dom-query-img.expr.txt` as a golden file, regenerated by
     `go test`) in `node:vm` against the §9 DOM-stub loaded with the
     `lahiri-me.dom.json` fixture, asserting it evaluates without throwing
     and returns a JSON array of `{tag:"img", …}` objects. This harness
     executes tool-generated JS in a real engine — exactly the class of bug
     Go-side string assertions cannot catch. (The stub gains
     `querySelectorAll` with the tiny selector subset the tools emit: tag,
     `#id`, and `tag:nth-of-type(k)` paths.)
2. Fix: in `domQuery`, marshal `[]string{}` when `args.Attributes` is nil, so
   the expression always embeds a JSON array. No schema change; behavior for
   explicit attribute lists is untouched.

## 8. Tool-testing discipline (binding policy)

Agent tools are critical infrastructure and none of their correctness depends
on an LLM. This section binds this spec and all future work, like spec 001 §1
binds structure:

1. **Failing test first.** Every discovered tool bug gets a failing test
   *before* the fix — a hermetic unit test when the failure is reproducible
   offline (Go fake-CDP, or the `node:vm` DOM-stub harness for generated JS),
   otherwise a live flow assertion on `https://www.lahiri.me`. The execution
   record (commit message or amendment) must name the test and state that it
   was observed red first.
2. **Every tool is exercised live.** New soak flow `tools-live-lahiri`
   (`test/soak.mjs`, no LLM involvement — pure `tool-invoke` round-trips
   through the manual-tool queue path): first `navigate` to
   `https://www.lahiri.me` and assert `ready:true`, then invoke every
   observation tool with hard oracles:
   - `read_page` — parses under the §4 subset parser; `title` non-empty;
     `url` matches `lahiri.me`; body contains ≥ 1 `a` node with absolute
     `href`, and ≥ 1 heading node; total ≤ 16000 bytes.
   - `dom` — result JSON contains `lahiri.me` url and ≥ 10 elements.
   - `dom_query` twice: `{"selector":"a"}` **without** `attributes` (the §7
     regression guard, must return a non-empty array), and
     `{"selector":"a","attributes":["href"]}` (every entry has `attrs.href`).
   - `dom_validate` — an expectation that holds on the page returns pass.
   - `page_eval` — `document.title` round-trips.
   - `layout_debug` — returns without error.
   - `screenshot` — returns an image; thumbnail ≤ 32 KiB (existing bound).
   - `system_info` — retains its `tools-roundtrip` oracle.
   Then safe actuation (never clicks, never typing into the page): `scroll`
   down then up, `key` End then Home, and one `click`-free mouse move via the
   existing mouse-move pathway if exposed as a tool (skip otherwise —
   actuation coverage must never mutate remote state). Every invocation must
   return `ok:true` with a non-empty result; any error string fails the flow.
3. **Allowed live-test sites** are exactly `https://www.lahiri.me` and
   `https://www.example.com` (see the spec 012 amendment of even date). No
   other external site may appear in any automated suite.
4. `scripts/check.sh` stays deterministic and offline (constitution rule 5):
   the live coverage lives in soak, which now fronts e2e (spec 012
   amendment); the offline reproductions live in the unit gates.

## 9. Tests

1. **Committed raw-DOM fixtures** (new, the foundation for all tool unit
   testing): `test/fixtures/lahiri-me.dom.json` and
   `test/fixtures/example-com.dom.json` — serialized element trees (tag, id,
   attributes, visibility, text nodes, children) captured once from the live
   sites and committed. New zero-dep helper `test/helpers/dom-stub.mjs`
   builds, from such a fixture, the exact DOM API surface §3c enumerates
   (`document`, `location`, `getComputedStyle`, elements with
   `getClientRects` et al.) inside a `node:vm` sandbox. Deterministic, no
   network — gate-safe.
2. **Extraction tests** `test/readpage-extract.test.js`: run
   `controller/internal/agent/js/readpage.js` verbatim in the stub sandbox
   against both fixtures. Assert for lahiri.me: top-level shape; every
   structured node has `tag` and no node has `sel` (A2); all `a` nodes carry
   absolute `href`s; no node with a
   collapse-eligible wrapper tag and no content survives; text nodes are
   normalized (no runs of whitespace); byte-identical output across two runs.
   Synthetic mini-fixtures cover: hidden/`aria-hidden` pruning, nested-div
   hoisting, table row shaping and truncation row, list flattening with
   single-link hoist, form control properties, password `value` omission,
   node-budget marker.
3. **Go tests** (`controller/internal/agent/yaml_test.go`, additions to
   `cdp_test.go`): emitter golden tests (key priority order, quoting/escaping
   incl. newlines and unicode, deterministic output over shuffled map
   insertions); budget truncation drops trailing nodes and appends the marker
   at exactly `readPageCap`; `ReadPage` sends the embedded script and
   converts a fake-CDP JSON digest to YAML (fake CDP server pattern already
   in `cdp_test.go`).
4. **Node tests**: `test/yaml-lite.test.js` — parser round-trips emitter
   output for nested fixtures (a golden YAML fixture generated by the Go
   emitter and committed under `test/fixtures/`), rejects out-of-subset
   documents (space indentation, tabs outside indentation, flow style,
   unquoted scalar). `test/tools-render.test.js`
   gains `yaml-digest` classification cases (positive, and negatives: valid
   YAML without url, valid JSON page shape still classifies as before).
   `test/tools-ui.test.js` asserts `tree.js` exists, exports `renderTree`,
   and contains no `innerHTML`.
5. **Live**: the soak suite exercises the digest on `www.lahiri.me` twice —
   the §8 `tools-live-lahiri` manual-invocation flow, and the chat-driven
   `lahiri-readpage` extraction flow defined by the spec-012 amendment of
   even date (the news-feed pressure test: extract the page's links,
   headings, and metadata from a single digest).
6. **dom_query**: the §7b failing-first tests (Go expression tests, golden
   expression file, `node:vm` execution harness).

All unit tests run inside `scripts/check.sh` (they are deterministic and
offline); soak stays outside the gate per constitution rule 5.

## 10. Docs

`README.md`, `AGENTS.md`, and the skills are reconciled by `/master-update`
after execution (constitution rule 9): the agent-capabilities paragraph
describes the YAML digest, the develop skill notes the YAML subset contract
(emitter/parser pairing), the reusable tree widget, and the §8 tool-testing
discipline (failing test first; live coverage on lahiri.me only).

## 11. Acceptance checklist

- [ ] `npm run check` green, including the new fixture-driven extraction
      tests, emitter/parser tests, and render classification tests.
- [ ] `read_page` on `https://example.com` (Tools console) renders a
      collapsible tree whose `body` contains the `h1` and the IANA link with
      absolute `href`; the raw result is valid subset YAML.
- [ ] `read_page` on `https://www.lahiri.me` yields a digest within
      `readPageCap` containing the page's headings and links; repeated calls
      on the unchanged page are byte-identical.
- [ ] A 4 GB-class local model, asked in chat to list the stories on a
      news-feed-style page, extracts titles and links from a single
      `read_page` observation without needing `dom` pagination (manual
      verification, recorded in the execution amendment).
- [ ] The tree widget expands/collapses with mouse and keyboard, respects
      reduced motion, and renders no HTML from result text.
- [ ] `dom_query` with `{"selector":"img"}` (no `attributes`) succeeds in the
      Tools console; the §7b tests exist and their red-first runs are
      recorded in the execution amendment.
- [ ] Soak flow `tools-live-lahiri` passes: every observation tool returns a
      valid result on `https://www.lahiri.me`, including both `dom_query`
      shapes, plus the safe-actuation steps.
- [ ] `grep` finds no automated-test URL outside `lahiri.me` and
      `example.com`.

## Amendments

### A1 (2026-07-24): story-feed extraction fix — budgets, visibility, density

Observed on a live `reddit.com/r/wallstreetbets` session: `read_page`
returned only 2 story links from a feed of 34 posts. Three root causes and a
set of density measures follow; acceptance for the amendment was ≥ 30 story
links extracted live from that page (verified: 31 unique story URLs, 86 total
hrefs, 31969 bytes ≈ 12978 tokens).

**Visibility (§3c rule 2, corrected).** The claim that "`display:none` yields
zero client rects; no separate check needed" pruned every boxless container —
`display: contents` custom elements (Reddit's `shreddit-*`/`faceplate-*`
wrappers) and inline wrappers around block children also have zero client
rects yet render their subtrees, which hid the entire story feed. The rule is
now: an element is pruned iff computed `display === "none"` or
`visibility === "hidden"`; an element with zero client rects is still visible
when `display === "contents"` or it has element children. The `svg` branch
now runs *after* this check so hidden labelled icons (per-post status
tooltips) no longer leak into the digest. The `test/helpers/dom-stub.mjs`
stub models hidden fixtures as computed `display: none` to match real
browsers.

**Budgets (tunables raised).**

- `NODE_BUDGET`: 800 → 4000 kept nodes (the header/sidebar alone exhausted
  800 before the walker reached the feed).
- Table `rows` / list `items` caps: 40 → 120 each.
- `readPageCap`: 16000 → 32000 bytes.
- `observationTextCap` (spec 012): 16 KiB → 32 KiB, and the manual
  tool-result cap in the controller rises to 32 KiB to match, so the digest
  reaches both the model and the Tools console without mid-document
  truncation. 32000 bytes ≈ 13k tokens is the practical maximum for the
  16384-token model context (system prompt + tool schemas ≈ 3k tokens; only
  the latest observation is retained in the prompt).

**Density (more content per byte; §3b/§3e adjusted).**

- The §4 emitter quotes strings with JSON escaping but **without** HTML
  escaping (`json.Encoder.SetEscapeHTML(false)`): each `\u003e` selector
  separator wasted five bytes. The committed golden fixture is regenerated.
- Selector segments omit `:nth-of-type(1)` when the element is the only
  sibling of its tag (uniqueness is unchanged under the child combinator).
- `sel` is emitted only where it serves interaction or follow-up queries:
  nodes whose tag is in `{a, video, audio, iframe, table, form, input,
  select, textarea, button, h1…h6}` keep it; childless text-only leaves drop
  it (the nearest kept ancestor locates them); `img` and `svg` nodes never
  carry one (the enclosing link/button is the actuation target, and
  `dom_query` finds media by attribute).
- Duplicate links are dropped: a later `a` (or flattened list item) whose
  `href` matches an earlier one and whose full text is empty or identical is
  removed with its subtree. Feed pages double every story link with invisible
  overlay/screen-reader anchors.
- A link, button, label, or heading with no text of its own and a single
  plain text-only leaf child absorbs the child's text in place.
- Childless text-only leaves whose text contains no letters or digits
  (decorative separators such as "•") are dropped.
- Media `src` values longer than 80 characters lose their query string
  (resize/signature parameters); the asset path identifies the resource.
  `href` values are never stripped — links must stay navigable.

Normative references to the old constants and rules in §3b, §3c, §3e, §4,
§9, and §10 are to be read with these amendments.

### A2 (2026-07-24): drop `sel`, tab indentation, denser digests

Even after A1, selector paths remained ~25 % of digest bytes and the budget
marker appeared well before full feed coverage. This amendment rewrites the
normative sections in place (§1 Produces, §3a example, §3b table, §3e, §4,
§5, §9, §11) as follows:

- **`sel` is removed from the digest entirely.** No node carries a selector;
  the emitter's node key order drops it and the extraction script no longer
  builds selector paths. Follow-up interaction uses `dom`/`click_element`
  refs or a model-written `dom_query` selector. The extraction tests now
  assert `sel` is absent.
- **Indentation is one literal tab per level** in both the Go emitter and
  the JS parser. Tabs are indentation-only; spaces are forbidden in
  indentation; tabs inside scalars stay JSON-escaped (`\t`). This roughly
  halves indentation bytes at depth and tokenizes as one token per level.
- The CDP `dom` tool already restricts entries to visible elements (laid-out
  nodes with positive bounds, skipping computed `display: none` and
  `visibility: hidden`, both on- and off-screen); recorded here as verified,
  no change required.
- The committed golden fixture is regenerated (tabs, no `sel`).

### A3 (2026-07-25): live goldens, layout tables, and 32K context (spec 029)

Spec 029 supersedes the A1 budget values and table behavior. The gate now
executes live-captured DOM fixtures through the production extractor with
declarative properties and complete YAML snapshots. Tables without explicit
header semantics, and nested table shells, are treated as layout rather than
flattened data; data-table cells retain links. Extractor limits are 500
characters for text/hrefs, 200 for attributes, 300 for cells, 400 rows/items,
and 8000 nodes. The YAML and observation caps rise to 64000 bytes and 64 KiB
respectively for the 32768-token model context.
