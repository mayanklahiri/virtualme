# Spec 029: `read_page` Goldens, Extraction Fidelity, and Context Budgets

| | |
|---|---|
| Status | Accepted (2026-07-25) |
| Depends on | `specs/008-browser-agent.md`, `specs/022-system-prompt.md`, `specs/027-structured-read-page.md`, `specs/028-data-explorer.md` |
| Produces | Live-captured DOM golden fixtures and property assertions; information-preserving layout/data-table simplification; 32K model context with proportionate observation and reply budgets |
| Followed by | Future specs |

## 0. Executor instructions

- Constitution binds: runtime code remains Go/JavaScript stdlib-only, CDP is
  observation-only, and the canonical gate remains deterministic and offline.
- This spec amends spec 027's extractor and digest limits. Existing executed
  spec text is historical; the values below are authoritative.
- Live capture is an explicit development operation. Committed fixtures and
  their assertions run offline in the canonical gate.
- Complete with §6 Acceptance and `/master-update`.

## 1. Problem and diagnosis

The Hacker News front page exposes two independent defects.

1. The page is built from nested layout tables. `read_page` classified every
   `table` as tabular data, flattened cells to text, discarded every link, and
   truncated titles at 80 characters. Descendant-row collection also mixed
   nested rows into the outer table.
2. The model received the complete 30-story digest, but generated chat replies
   with a fixed 1024-token maximum. The controller neither inspected nor
   exposed a `finish_reason` of `length`, so replies ended mid-row without a
   diagnostic.

The implementation must preserve important visible content and interaction
targets while keeping bounded, deterministic representations.

## 2. Live DOM golden capture

Add a manual-only `dump_dom` tool. It evaluates a read-only serializer in the
current page and writes the result under
`$VM_DATA_DIR/agent/dom-dumps/<host>-<timestamp>.dom.json`. The JSON schema is
the fixture schema already consumed by `test/helpers/dom-stub.mjs`:

- top-level `url`, `title`, `lang`, `head`, and `body`;
- element `tag`, relevant `attrs`, `visible`, direct `text`, and `children`;
- no executable content and no browser mutation.

The tool returns only the data-volume-relative path. It is available through
the manual Tools protocol but is excluded from agent tool definitions so the
model cannot spend context or disk on fixture capture.

Add a zero-dependency `test/capture-dom.mjs` development script that invokes
the manual tool over the controller websocket, downloads the resulting file
through the read-only Data API, and writes a named fixture under
`test/fixtures/`. Seed `hn-front.dom.json` from the live Hacker News page.

## 3. Golden property DSL and snapshots

Every `*.dom.json` fixture may have:

- `<name>.props.mjs`, exporting declarative properties over the simplified
  digest; and
- `<name>.digest.golden.yaml`, the complete human-reviewable simplified YAML.

`test/helpers/digest-props.mjs` provides matchers and assertions for node
counts, universal required fields, existence, text regexes, absence of
truncation markers, and YAML byte size. `test/readpage-golden.test.js`
discovers fixtures, executes the production extractor verbatim in the DOM
stub, evaluates properties, and byte-compares snapshots. An explicit
`REGEN_GOLDENS=1` regenerates snapshots.

The Hacker News fixture requires 30 stories, full title links with absolute
URLs, comment-page links, scores and comment counts, no duplicated layout
header, and no truncation marker.

## 4. Simplification heuristics

- A table is data only when it has explicit tabular semantics (`th`, `thead`,
  or `caption`) and is not a nested layout shell. Layout tables are walked as
  ordinary containers; non-semantic `table`/`tbody`/`tr`/`td` wrappers
  collapse while links, text, and meaningful elements survive.
- Data-table extraction visits only rows owned by that table, never rows of a
  nested table.
- Data cells preserve a contained link as structured `{text, href}` data
  rather than reducing it to plain text.
- Consecutive numbered feed title/metadata rows become one `article` record
  with explicit `rank`, `title`, ready-to-copy Markdown `title_link`, `url`,
  `score`, `comments`, `comment_url`, `author`, and `age` fields while
  retaining the source children. This keeps related values model-readable
  without discarding lower-level content.
- General extraction continues to prune scripts, styles, hidden content, and
  empty/decorative wrappers, but must not discard URLs, labels, scores,
  timestamps, or content-bearing text.

## 5. Context and truncation limits

The default model context is 32768 tokens. Proportionate byte budgets are:

| Surface | Limit |
|---|---:|
| `read_page` YAML | 64000 bytes |
| Observation message / stored step text | 64 KiB |
| Ordinary tool result in model context | 8 KiB |
| Rendered DOM tool | 24 KiB |
| `page_eval` | 16 KiB |
| Manual tool-result websocket text | 64 KiB |
| Chat history text | 16 KiB |

Extractor limits become: text and href 500 characters, attributes 200, data
cells 300, 400 rows/items, and 8000 kept nodes. Process output remains 64 KiB.
Display-only activity-ledger summaries retain their smaller limits.

Completion `max_tokens` is one quarter of context (8192 at the default), with
no 1024-token clamp. The streaming parser records `finish_reason`. If generation
ends because of `length`, the returned and streamed reply gains
`…[response truncated at token limit]`; truncation is never silent. LLM HTTP
requests have no wall-clock client timeout because a 32K prompt can exceed
five minutes on CPU; the task context and Stop/disconnect cancellation remain
authoritative.

## 6. Acceptance

1. `npm run check` passes, including fixture discovery, properties, snapshot
   comparison, Go tests, lint, and type checking.
2. A rebuilt container passes `bash test/e2e.sh`.
3. A live Hacker News `read_page` digest contains all 30 stories with real
   story and `/item?id=` URLs, score/comment text, and no digest truncation.
4. Asking for five linked stories produces five complete Markdown rows and
   uses real comment-page links.
5. `/master-update` reconciles repository documentation and skills.

## Amendments

### 2026-07-25 — Context-budget supersession

Spec 034 supersedes §5's independent prompt limits. At the default 32768-token
context, model-facing `read_page` YAML and observation text are capped at
24576 bytes and scale with configured context; the 64 KiB stored-step and
manual transport ceilings remain unchanged. Completion `max_tokens` is now
adaptive within 1024 through one quarter of context after a conservative
preflight prompt estimate and 512-token margin. Spec 034 defines the complete
compaction and overflow-recovery policy.
