# Spec 028: Data Explorer — Read-Only Console View of the Persistent Volume

| | |
|---|---|
| Status | Accepted (2026-07-24) |
| Depends on | `specs/003-controller.md` (controller HTTP surface, SPA), `specs/007-persistence-locality.md` (the `$VM_DATA_DIR` persistence map this page visualizes), `specs/027-structured-read-page.md` (the reusable collapsible-tree widget and YAML-subset parser — execute 027 first) |
| Produces | A `/data` console tab: a full-height, strictly read-only file explorer over `$VM_DATA_DIR` with icon/list views, sortable size summaries, deep links, a resizable preview pane, type-aware renderers, and per-file raw download; guarded read-only HTTP APIs under `/api/data/` |
| Followed by | Future specs |

## 0. Executor instructions

- Constitution binds: Go stdlib only, no SPA dependencies, no build-step
  changes. The tree widget and YAML parser come from spec 027 — do not
  duplicate them.
- The API is **read-only by construction**: only `GET` handlers exist; there
  is no code path that writes, renames, or deletes anything under
  `$VM_DATA_DIR`. Keep it that way — a write endpoint requires a new spec.
- Trust model (constitution rule 8) governs API visibility: whoever reaches
  port 8080 already owns the deployment, so every path remains addressable,
  including the DKIM key, Chromium profile, and Valkey files. The root UI
  omits `chromium`, `mail`, `metrics`, `valkey`, and `xdg` as presentation
  filtering only; direct deep links and the API remain unrestricted. Do not
  add auth, redaction, or a server-side denylist.
- Stop-on-red per section; finish with §6 Acceptance.

## 1. What it is

The data volume (`$VM_DATA_DIR`, host-mounted per spec 002) accumulates the
deployment's entire observable history: agent step logs and screenshots
(`agent/`), project scratch dirs (`projects/`), metrics tiers (`metrics/`),
speech cache (`tts-cache/`), mail spool and DKIM material (`mail/`), Chromium
profile, Valkey persistence, and XDG state. Today the only windows into it
are `docker exec` and the agent's own `bash` tool. The Data page makes it
browsable: a left-pane single-directory explorer, a right-pane viewer that
renders each well-known file type appropriately, and a raw download for
everything.

## 2. HTTP API (controller, `controller/cmd/controller/main.go` + a new `controller/internal/datafs` package)

Three `GET` endpoints, registered on the existing mux (Go's `http.ServeMux`
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

- Server sorting: directories first, then files, each group in byte-order of
  `name`. `size` is bytes (0 for dirs); `mtimeMs` is Unix milliseconds.
  Symlinks are listed with `kind":"file"` only when they resolve inside the
  root (§2c) — otherwise they are omitted entirely. No recursion; one level
  per call; the SPA navigates one directory at a time and applies the selected
  client-side sort.

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

For all endpoints: reject with `404` (never `403` — do not distinguish
"exists outside root" from "missing") any request where, after
`filepath.Clean` on `root + "/" + rel`:

1. `rel` is absolute or contains a `..` segment, or
2. `filepath.EvalSymlinks` of the target (and of the root, computed once)
   does not leave the target with the root as a strict path prefix on a
   path-separator boundary.

Directories requested via `file` and files requested via `list` or `du` are
`400`. Non-GET methods are `405`. Errors are plain-text bodies; nothing
echoes the raw requested path back into HTML.

### 2d. `GET /api/data/du?path=<rel>`

- Returns recursive byte totals for each immediate child directory:

```json
{"path":"agent","sizes":{"task-a":123456,"task-b":987654}}
```

- The handler returns one JSON response after obtaining all totals. The SPA
  requests it independently after rendering `/list`, so directory navigation
  is never blocked on recursive size work.
- Totals sum regular-file sizes using `filepath.WalkDir`; symlinks are skipped
  rather than followed. Each child total is cached in Valkey for 300 seconds
  under a `vm:datafs:du:` key. Valkey failure degrades to uncached computation.
  Concurrent requests for the same path are coalesced in process.

## 3. SPA: the Data page

1. **Route and nav**: `controller/web/static/js/router.js` gains
   `["/data", ["data", "Data"]]`; `index.html` gains the sidebar link
   (`<a href="/data" data-nav>`) between Tools and Status and a
   `<section data-page="data" hidden>` with the two-pane skeleton. New page
   module `controller/web/static/js/data.js`, initialized from `app.js` like
   the other pages. The Data nav uses the standard folder icon.
2. **Deep links**: browsing state is `/data?path=<relative-path>`. Directory
   entry and file selection use `history.pushState`; page entry and
   `popstate` parse the URL, reject absolute or `..` paths, and restore the
   directory or file preview. Invalid or missing targets show an inline error
   and return to the nearest loadable directory. The sidebar `/data` link
   resets to the volume root.
3. **Layout**: `.data-grid` fills the viewport below the page heading.
   Desktop starts at a 66/34 explorer/viewer split with independently
   scrolling panes. A keyboard-focusable pointer-draggable separator changes
   the split, clamps both panes to usable minimums, resets on double click,
   and persists as `vm-data-split`. Below 47.999 rem the explorer is
   full-width and the viewer is a full-screen right slide-over with Back and
   Escape close behavior.
4. **Left pane — single-directory explorer**:
   - A toolbar contains clickable breadcrumbs, segmented `Name`, `Size`, and
     `Modified` sort buttons (pressing the active sort reverses it), and
     icon/list view buttons. Sort direction is visible, and list column
     headers invoke the same sort behavior.
   - View and sort persist as `vm-data-view` and `vm-data-sort`.
   - Directories remain grouped before files under every sort. One click on a
     directory enters it; one click on a file selects and previews it.
   - List view uses aligned icon/name/size/modified columns. A size bar for
     every entry is proportional to the largest visible sibling. Recursive
     directory totals arrive asynchronously from `/api/data/du`; pending
     sizes render a stable placeholder.
   - Icon view uses responsive tiles with type glyphs, a two-line-clamped
     name, and size. Images use glyphs, not fetched thumbnails.
   - Long names are ellipsized in list view and clamped in icon view, with the
     complete name available in `title`.
   - At the root only, `chromium`, `mail`, `metrics`, `valkey`, and `xdg` are
     omitted. This does not affect API visibility or direct deep links.
5. **Right pane — viewer**: a header (ellipsized full relative path, size,
   mtime, and a standard iconized `Download` button linking to
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
  fallback; `405` on POST; `400` for dir-as-file and file-as-dir; recursive
  `du` totals, skipped symlinks, cache hits, and concurrent coalescing.
- **Node** (`test/data-ui.test.js`): `index.html` carries the nav link and
  data explorer skeleton; `router.js` maps `/data`; `data.js` wires the
  renderer table, toolbar, preference keys, exact root omission list, and
  safe deep-link parsing, and contains no `innerHTML`.
- **e2e** (`test/e2e.sh`, new step after the SPA checks): against the live
  container, `GET /api/data/list` returns JSON; `GET /api/data/du` returns a
  `sizes` object; traversal probes against file and du return `404`; a real
  file under the data dir round-trips with the correct content type.
- Soak needs no new flow (the page is server-data-driven; e2e covers the
  contract).

## 5. Docs

`/master-update` after execution: README endpoint table covers `/api/data/*`
(read-only, trust-model note), the operate skill mentions the Data tab for
troubleshooting (inspecting agent steps, mail spool, tts-cache), the develop
skill records the `datafs` package and the no-write rule. Spec 007's
persistence map is unchanged — this spec adds no state.

## 6. Acceptance checklist

- [x] `npm run check` green including the new Go and Node tests.
- [x] `/data` shows the filtered volume root in icon and list views; entering
      `agent/` lists task dirs; a `steps.jsonl` renders as per-line
      collapsible rows; a
      screenshot `.jpg` previews and opens the lightbox; a `tts-cache`
      `.wav` plays in-page; the DKIM `.pem` renders as text (trust model —
      visible by design).
- [x] Every file offers a working raw download; a >256 KiB log renders
      truncated with the notice and downloads whole.
- [x] Traversal probes (`..`, absolute, encoded, escaping symlink) all
      return `404`; the containment table test locks this.
- [x] The explorer performs zero non-GET requests (verify in devtools
      network log during a browse session).
- [x] Desktop starts 66/34, drag-resizes both panes, and restores the saved
      split; tts-cache hashes never widen or distort rows.
- [x] `/data?path=…` restores directories and file previews; browser
      Back/Forward tracks navigation safely.
- [x] Mobile (375 px): the explorer remains usable and file preview opens as
      a full-screen slide-over with working Back and Escape controls.

## Amendments

### 2026-07-25 — Console fix and taste sweep

1. **Desktop layout.** The browser occupies full width until a file is
   opened; then the 66/34 drag-resizable split (with splitter) appears.
2. **Viewer dismiss.** The viewer is dismissible on desktop (close control);
   closing returns to the single-column browser view.
3. **No placeholder.** The persistent "Select a file to inspect it"
   placeholder pane is removed.
