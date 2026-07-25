# Spec 028: Data Explorer — Read-Only Console View of the Persistent Volume

| | |
|---|---|
| Status | Accepted (2026-07-24) |
| Depends on | `specs/003-controller.md` (controller HTTP surface, SPA), `specs/007-persistence-locality.md` (the `$VM_DATA_DIR` persistence map this page visualizes), `specs/027-structured-read-page.md` (the reusable collapsible-tree widget and YAML-subset parser — execute 027 first) |
| Produces | A new `/data` console tab: an interactive, strictly read-only file explorer over `$VM_DATA_DIR` with type-aware right-pane renderers (image preview, JSON/YAML collapsible trees, JSONL rows, WAV playback, capped plain text, binary metadata) and per-file raw download; a guarded read-only HTTP API under `/api/data/` |
| Followed by | Future specs |

## 0. Executor instructions

- Constitution binds: Go stdlib only, no SPA dependencies, no build-step
  changes. The tree widget and YAML parser come from spec 027 — do not
  duplicate them.
- The API is **read-only by construction**: only `GET` handlers exist; there
  is no code path that writes, renames, or deletes anything under
  `$VM_DATA_DIR`. Keep it that way — a write endpoint requires a new spec.
- Trust model (constitution rule 8) governs visibility: whoever reaches port
  8080 already owns the deployment, so the full tree is shown — including
  the DKIM key, Chromium profile, and Valkey files. Do not add a denylist,
  auth, or redaction speculatively.
- Stop-on-red per section; finish with §6 Acceptance.

## 1. What it is

The data volume (`$VM_DATA_DIR`, host-mounted per spec 002) accumulates the
deployment's entire observable history: agent step logs and screenshots
(`agent/`), project scratch dirs (`projects/`), metrics tiers (`metrics/`),
speech cache (`tts-cache/`), mail spool and DKIM material (`mail/`), Chromium
profile, Valkey persistence, and XDG state. Today the only windows into it
are `docker exec` and the agent's own `bash` tool. The Data page makes it
browsable: a left-pane directory tree, a right-pane viewer that renders each
well-known file type appropriately, and a raw download for everything.

## 2. HTTP API (controller, `controller/cmd/controller/main.go` + a new `controller/internal/datafs` package)

Two `GET` endpoints, registered on the existing mux (Go's `http.ServeMux`
prefers the longer registered pattern, so the SPA catch-all is unaffected;
add the `/api/data/` prefix to any explicit `spaHandler` exclusion list if
one exists rather than relying on ordering):

### 2a. `GET /api/data/list?path=<rel>`

- `path` is a relative path within the volume; empty or absent means the
  root.
- Response `200 application/json`, deterministic:

```json
{
  "path": "agent",
  "entries": [
    {"name": "2026-07-24-abc123", "kind": "dir", "size": 0, "mtimeMs": 0},
    {"name": "steps.jsonl", "kind": "file", "size": 18324, "mtimeMs": 1753412345678}
  ]
}
```

- Sorting: directories first, then files, each group in byte-order of
  `name`. `size` is bytes (0 for dirs); `mtimeMs` is Unix milliseconds.
  Symlinks are listed with `kind":"file"` only when they resolve inside the
  root (§2c) — otherwise they are omitted entirely. No recursion; one level
  per call (the SPA lazy-loads on expand).

### 2b. `GET /api/data/file?path=<rel>[&download=1]`

- Streams the file with a `Content-Type` chosen from a fixed extension map
  (`.json`/`.jsonl` → `application/json` / `application/x-ndjson`, `.yaml`/
  `.yml` → `text/yaml`, `.png .jpg .jpeg .webp .gif` → matching `image/*`,
  `.wav` → `audio/wav`, `.txt .log .md .sh .go .js .css .html .pem` →
  `text/plain; charset=utf-8`), falling back to
  `http.DetectContentType` on the first 512 bytes.
- **Inline text cap**: without `download=1`, responses whose resolved
  content type is text or JSON are truncated at 262144 bytes (256 KiB) and
  carry `X-VM-Truncated: 1` plus the full size in `X-VM-Size`. Images and
  audio stream whole either way (the SPA needs complete media).
- With `download=1`: the complete file streams with
  `Content-Disposition: attachment; filename="<base name>"` and no cap.
- `Last-Modified` is set from mtime; no ETag machinery.

### 2c. Path containment (normative, tested)

For both endpoints: reject with `404` (never `403` — do not distinguish
"exists outside root" from "missing") any request where, after
`filepath.Clean` on `root + "/" + rel`:

1. `rel` is absolute or contains a `..` segment, or
2. `filepath.EvalSymlinks` of the target (and of the root, computed once)
   does not leave the target with the root as a strict path prefix on a
   path-separator boundary.

Directories requested via `file` and files requested via `list` are `400`.
Non-GET methods are `405`. Errors are plain-text bodies; nothing echoes the
raw requested path back into HTML.

## 3. SPA: the Data page

1. **Route and nav**: `controller/web/static/js/router.js` gains
   `["/data", ["data", "Data"]]`; `index.html` gains the sidebar link
   (`<a href="/data" data-nav>`) between Tools and Status and a
   `<section data-page="data" hidden>` with the two-pane skeleton. New page
   module `controller/web/static/js/data.js`, initialized from `app.js` like
   the other pages; it fetches lazily (nothing loads until the page is first
   shown, and the tree refreshes when navigated back to).
2. **Layout**: `.data-grid` — desktop: two columns (`minmax(16rem, 28%)`
   tree, rest viewer) with the page's full height and independently
   scrolling panes; below the existing 47.999 rem breakpoint the panes
   stack (tree above viewer). Styles in `app.css` from existing tokens.
3. **Left pane — directory tree**: built by `data.js` (the spec 027
   `renderTree` widget renders *values*; the explorer needs lazy fetching,
   so `data.js` composes its own tree UI from the same CSS classes and
   disclosure-button pattern for visual consistency). Each directory row
   has a disclosure button; first expansion calls `/api/data/list` for that
   path and caches the result until page re-entry. File rows show name and
   human-readable size; clicking selects the file, highlights the row
   (`aria-current="true"`), and drives the viewer. Errors (fetch failure,
   removed file) render as a muted inline message, never an alert.
4. **Right pane — viewer**: a header (full relative path, size, mtime via
   the shared duration/format helpers, and a `Download` link to
   `/api/data/file?path=…&download=1`) above a renderer chosen by
   extension:
   - `.png .jpg .jpeg .webp .gif` — `<img>` capped to the pane, click opens
     the existing Tools-console lightbox (reuse `openLightbox`, which moves
     to a shared module if needed).
   - `.json` — fetch text, `JSON.parse`, render with spec 027
     `renderTree(value, {expandDepth: 2})`; parse failure falls back to the
     text renderer.
   - `.yaml .yml` — `parseYamlLite` + `renderTree`; parse failure (the file
     may be arbitrary YAML beyond the subset) falls back to text.
   - `.jsonl` — split lines, `JSON.parse` each; render one collapsed tree
     row per line labelled by index (agent `steps.jsonl` is the motivating
     case); unparseable lines render as plain text rows.
   - `.wav` — `<audio controls>` sourced from the file endpoint
     (tts-cache playback).
   - Text extensions and any file whose fetched body is valid UTF-8 —
     `<pre>` with the existing tool-output styling; when `X-VM-Truncated`
     is set, a notice line links to the download.
   - Everything else — a metadata card (name, size, mtime, sniffed type)
     and the download link only.
   All rendering is DOM-built (`createElement`/`textContent`); `innerHTML`
   is forbidden in `data.js`.

## 4. Tests

- **Go** (`controller/internal/datafs/datafs_test.go`, temp-dir fixtures):
  containment table test — `../x`, absolute paths, encoded traversal, a
  symlink pointing outside the root, and a symlink pointing inside (first
  four `404`, last one served); listing determinism (dirs-first byte order,
  stable across runs); text cap + `X-VM-Truncated`/`X-VM-Size`;
  `download=1` disposition and uncapped body; extension map and sniff
  fallback; `405` on POST; `400` for dir-as-file and file-as-dir.
- **Node** (`test/data-ui.test.js`): `index.html` carries the nav link and
  `data` section; `router.js` maps `/data`; `data.js` exists, wires the
  renderer table, and contains no `innerHTML`.
- **e2e** (`test/e2e.sh`, new step after the SPA checks): against the live
  container, `GET /api/data/list` returns JSON listing at least `metrics`
  or `valkey`; `GET "/api/data/file?path=../../etc/passwd"` and a
  URL-encoded traversal both return `404`; a real file under the data dir
  round-trips with the correct content type.
- Soak needs no new flow (the page is server-data-driven; e2e covers the
  contract).

## 5. Docs

`/master-update` after execution: README endpoint table gains `/api/data/*`
(read-only, trust-model note), the operate skill mentions the Data tab for
troubleshooting (inspecting agent steps, mail spool, tts-cache), the develop
skill records the `datafs` package and the no-write rule. Spec 007's
persistence map is unchanged — this spec adds no state.

## 6. Acceptance checklist

- [ ] `npm run check` green including the new Go and Node tests.
- [ ] `/data` shows the volume root; expanding `agent/` lazily lists task
      dirs; a `steps.jsonl` renders as per-line collapsible rows; a
      screenshot `.jpg` previews and opens the lightbox; a `tts-cache`
      `.wav` plays in-page; the DKIM `.pem` renders as text (trust model —
      visible by design).
- [ ] Every file offers a working raw download; a >256 KiB log renders
      truncated with the notice and downloads whole.
- [ ] Traversal probes (`..`, absolute, encoded, escaping symlink) all
      return `404`; the containment table test locks this.
- [ ] The explorer performs zero non-GET requests (verify in devtools
      network log during a browse session).
- [ ] Mobile (375 px): panes stack, tree rows and viewer remain usable.
