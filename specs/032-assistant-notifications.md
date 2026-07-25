# Spec 032: Assistant Notifications

| | |
|---|---|
| Status | Accepted (2026-07-25) |
| Depends on | `specs/003-controller.md` (SPA and websocket hub), `specs/007-persistence-locality.md` (canonical persistence map), `specs/013-job-queue-scheduler.md` and `specs/015-jobs-page.md` (tool execution and activity semantics), `specs/021-agent-cdp-tools-console.md` (authoritative tool manifest), `specs/031-master-config.md` (master configuration and deliberate-restart seam) |
| Produces | A durable, server-wide, real-time notification service; shared read state; bell popover and `/notifications` page; lifecycle notifications; agent `notify` tool; producer seams for configuration and Telegram |
| Followed by | `specs/033-telegram.md` consumes the sender/type-safe notification producer seam |

## 0. Executor instructions

- The constitution and all dependency specs bind. Runtime remains Go and
  browser JavaScript stdlib-only. Do not add an HTTP notification API, a
  browser-local notification store, authentication, per-device state, or
  arbitrary HTML.
- Execute this spec only after spec 031. This spec is N1-N3: N1 is the
  durable service and protocol, N2 is the complete console UI, and N3 is
  lifecycle/config/agent producers plus soak/e2e coverage.
- **Tests first is mandatory.** Add the failing tests and probes in §10 before
  production code. Confirm that the focused Go and Node tests fail for the
  intended missing behavior, then implement §§2-9. Do not weaken assertions
  to make an implementation pass.
- Use the package name `notifications`, not `notify`: `notify` is too close to
  common process/signal terminology, while `notifications` avoids ambiguity
  with `os/signal.Notify`.
- All notification mutations are durable before websocket broadcast. A
  persistence failure returns an error only to the initiating connection or
  producer and broadcasts nothing.
- Read state is intentionally global for the v1 single-user trust model. It is
  not scoped to a websocket, browser, device, profile, or future account.
- Do not amend the agent system prompt merely to advertise `notify`.
  `Definitions()` is authoritative and the tool description is sufficient.
  Prompt wording changes remain governed by spec 022 and require a separate
  explicit amendment.
- Stop on any red focused test. Finish implementation with the full sequence
  in §12: reconciliation, canonical gate, e2e, soak, manual checks, then
  `/master-update`.

## 1. Invariants and non-goals

1. A notification is server-owned data, not a toast. It survives browser
   closure, websocket reconnect, controller restart, container stop/start,
   and image replacement through Valkey AOF under `$VM_DATA_DIR/valkey/`.
2. The newest 500 notifications are retained. Retention is count-based and
   deterministic; there is no age expiration and no user deletion in this
   spec.
3. Every connected client converges from server snapshots. Marking one item
   read in tab A must render that item **read** in tabs B/C/D and update every
   bell/nav count without reload.
4. A notification has one immutable content record and mutable global
   `readAtMs`. Repeated create-by-ID, mark-one, and mark-all operations are
   idempotent.
5. Notification text and detail are data. No producer can provide SVG, HTML,
   Markdown, CSS, a URL with executable behavior, or a frontend module name.
   The browser constructs DOM with `createElement`, safe attributes, and
   `textContent`; `innerHTML` is forbidden in the notification module.
6. Notification creation does **not** create an activity-ledger event.
   Notifications communicate operator-relevant outcomes; activity records
   machine work. The existing tool-completion path still records one
   `tool/notify` activity when the agent or Tools page invokes `notify`; do
   not add a second activity record for the resulting notification.
7. This spec does not implement OS desktop notifications, email delivery,
   Telegram delivery, notification actions, snooze, muting, per-type
   preferences, pagination beyond the retained 500, or cross-user isolation.

## 2. Canonical model, registries, and validation

Create `controller/internal/notifications/notifications.go`.

### 2.1 Exact public model

The package exports these JSON shapes (field names and omission behavior are
normative):

```go
type Detail struct {
	Version  int             `json:"version"`
	Renderer string          `json:"renderer"`
	Data     json.RawMessage `json:"data"`
}

type Notification struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Subtype      string `json:"subtype,omitempty"`
	Sender       string `json:"sender"`
	Title        string `json:"title"`
	Summary      string `json:"summary"`
	OccurredAtMS int64  `json:"occurredAtMs"`
	CreatedAtMS  int64  `json:"createdAtMs"`
	ReadAtMS     int64  `json:"readAtMs,omitempty"`
	Detail       Detail `json:"detail"`
}

type CreateRequest struct {
	ID           string
	Type         string
	Subtype      string
	Sender       string
	Title        string
	Summary      string
	OccurredAtMS int64
	Renderer     string
	Detail       json.RawMessage
}
```

- `ID` is generated when absent. An explicitly supplied ID exists only for
  lifecycle recovery/idempotence and package tests; websocket clients and the
  agent tool cannot supply one.
- `OccurredAtMS` is when the represented event happened. Zero becomes the
  service clock at creation. It may precede `CreatedAtMS` (notably crash
  detection) but may not exceed creation time by more than five minutes.
- `CreatedAtMS` is assigned only by the service clock. `ReadAtMS` is absent
  until globally read and is never accepted from producers.
- `Detail.Version` is exactly `1` in this spec. `Detail.Data` is always a JSON
  object; absent producer detail becomes `{}`. There is no nullable detail.

The package-private lifecycle path uses
`createExact(ctx, immutable Notification)`: it accepts only a fully validated
unread notification whose ID, occurred/created timestamps, content, renderer,
and canonical detail were previously reserved in the durable lifecycle marker.
It persists those exact immutable bytes and never regenerates a timestamp.
No producer, websocket request, agent tool, or exported interface can call
this path.

### 2.2 Stable sortable IDs

Implement a package-private, mutex-protected Crockford Base32 generator with
the exact 26-character ULID layout: 48 bits of Unix milliseconds followed by
80 bits from `crypto/rand`. Within one process, if the clock repeats or moves
backward, retain the previous millisecond and increment the prior 80-bit
entropy as an unsigned big-endian integer. Entropy overflow waits until the
clock advances. The alphabet is `0123456789ABCDEFGHJKMNPQRSTVWXYZ`;
lowercase, `I`, `L`, `O`, and `U` are never emitted. Lexical byte order is
creation order. Generator clock and entropy reader are injectable in tests.

Validation for a supplied lifecycle ID requires exactly this 26-character
uppercase format. The service serializes mutations with one `sync.Mutex`, so
list order, IDs, snapshots, and broadcasts agree even under concurrent
producers.

### 2.3 Type registry

The Go registry is the authority:

| Type | Lucide sprite ID | Default renderer | Meaning |
|---|---|---|---|
| `info` | `i-circle-info` | `generic` | neutral information |
| `success` | `i-circle-check` | `generic` | completed desired outcome |
| `warning` | `i-triangle-alert` | `generic` | degraded or attention-needed outcome |
| `error` | `i-circle-x` | `generic` | failed or unclean outcome |

Each registry entry is `TypeDefinition{Name, Icon, DefaultRenderer string;
AllowedRenderers []string}`. Initial allowed renderers are:

- all four types: `generic`;
- `info`, `success`, `warning`, and `error`: `lifecycle`;
- `info` and `success`: `configuration`;
- `info`, `success`, `warning`, and `error`: `agent`.

`Registry()` returns a copy in the table order. The snapshot sends the
registry to the browser; the SPA does not duplicate type-to-icon mappings.
Adding a generic type therefore requires one Go registry entry and one pinned
Lucide icon. Adding a custom presentation also requires exactly one
frontend renderer entry in §7.4.

The service, not an untrusted producer, resolves the renderer. Unknown types,
unknown renderers, and type/renderer combinations not in `AllowedRenderers`
fail validation. The agent tool does not accept `renderer`; it is forced to
`agent`.

### 2.4 Sender and subtype identifiers

Sender identifiers are open but validated, to permit spec 033 without a
central enum migration: lowercase ASCII matching
`^[a-z][a-z0-9._-]{0,31}$`. Initial senders are `controller`, `config`, and
`agent`; spec 033 will use `telegram`. Sender is always assigned by trusted
server wiring, never by a websocket or tool argument.

Subtype is optional lowercase ASCII matching
`^[a-z][a-z0-9._-]{0,47}$`. This spec defines:

- lifecycle: `unclean-startup`, `clean-shutdown`,
  `config-restart-shutdown`, and `config-restart-startup`;
- configuration: `config-saved`;
- agent: any identifier satisfying the rule, with empty meaning generic.

### 2.5 Text and detail sanitization

Apply validation before obtaining an ID or writing Valkey:

- Repair invalid UTF-8 with `strings.ToValidUTF8(value, "�")`.
- Trim outer Unicode whitespace. Remove C0/C1 controls, DEL, bidi formatting
  controls U+202A-U+202E and U+2066-U+2069, and U+FEFF. Convert CR/LF/tab and
  every remaining Unicode whitespace run to one ASCII space.
- `title`: required, 1-120 Unicode code points after sanitization.
- `summary`: required, 1-240 code points, exactly one line after sanitization.
- `sender`, `type`, `subtype`, and `renderer`: ASCII identifier checks above;
  do not silently coerce them.
- Detail must decode with `json.Decoder.UseNumber()` as exactly one JSON
  object with no trailing token. Maximum encoded input is 8192 bytes;
  maximum nesting depth is 8; maximum total object keys plus array elements
  is 256; each key is 1-64 code points; each string is at most 2048 code
  points. Reject non-finite numbers (the standard decoder already does).
  Recursively sanitize keys and strings with the control/bidi removal rule;
  reject duplicate keys after sanitization. Re-marshal the sanitized value
  to canonical compact JSON with Go's deterministic string-key ordering and
  require the result to remain at most 8192 bytes.
- Keys named `html`, `svg`, `innerHTML`, `script`, `style`, `renderer`, or
  `component` (ASCII case-insensitive) are rejected at every depth. Strings
  are not interpreted as markup. URL-bearing renderer fields must use the
  shared safe-link helper from §7.4, which accepts only same-origin paths or
  `http:`/`https:` and otherwise renders plain text.

Return concise stable errors such as `title is required`,
`unknown notification type "x"`, or `detail exceeds 8192 bytes`; do not
include the complete rejected payload.

## 3. Valkey persistence, atomicity, retention, and ordering

### 3.1 Exact keys

All state is in Valkey AOF:

| Key | Type | Contents |
|---|---|---|
| `virtualme:notifications:order` | list | newest-first retained notification IDs |
| `virtualme:notifications:items` | hash | ID → compact immutable `Notification` JSON with `readAtMs` omitted |
| `virtualme:notifications:read` | hash | ID → decimal Unix millisecond read timestamp |

The list is capped at 500. The read hash contains entries only for retained
items. There are no per-client keys.

### 3.2 RESP client extension

Extend `controller/internal/valkey/valkey.go` with:

```go
func (v *Client) Eval(script string, keys []string, args ...string) (any, error)
```

It emits `EVAL <script> <len(keys)> <keys...> <args...>` through the existing
RESP2 encoder and returns the already-supported recursive RESP reply. Do not
expose `do`, add a dependency, or implement `MULTI`. Add package helpers in
`notifications` that strictly decode the expected integer/array/bulk-string
reply shapes and fail closed on malformed replies.

### 3.3 Atomic scripts

Store the following scripts as package constants and cover their behavior
against the existing fake RESP fixture plus a real Valkey in e2e:

1. **Create** (`order`, `items`, `read`; args: ID, immutable JSON, cap):
   - If `HEXISTS items ID == 1`, compare `HGET items ID` byte-for-byte.
     Equal means idempotent success with return `0`; unequal returns a script
     error `ID_CONFLICT`.
   - Otherwise `HSET items ID JSON`, `LPUSH order ID`.
   - Read evicted IDs with `LRANGE order cap -1`, then
     `LTRIM order 0 cap-1`; `HDEL` every evicted ID from both hashes.
   - Return `1` for newly created. This single script prevents orphaned
     content/read state and over-cap history after successful return.
2. **Mark one** (`items`, `read`; args: ID, timestamp):
   - Missing item returns integer `-1`.
   - Existing item uses `HSETNX read ID timestamp`; return `1` when newly
     read and `0` when already read. Preserve the first read timestamp.
3. **Mark all** (`order`, `items`, `read`; arg: timestamp):
   - Iterate the retained order list inside Lua. For each ID still present in
     `items`, `HSETNX read ID timestamp`. Return the number changed.
   - A concurrent create is ordered before or after the script by Valkey. If
     before, it becomes read; if after, it remains unread. This is the exact
     mark-all cutoff semantic.
4. **Snapshot** (`order`, `items`, `read`; no args):
   - Iterate newest-first IDs and return a flat array of alternating immutable
     JSON and read timestamp (`""` when unread), skipping missing hash rows.
   - Service decoding overlays `ReadAtMS`; malformed JSON rows are logged and
     skipped, never sent as partially trusted data.

All service methods hold the service mutex across script execution, snapshot
construction, and any resulting broadcast. This gives every in-process
writer a single total order. Valkey scripts provide storage atomicity without
transactions. External direct writes to these private keys are unsupported.

### 3.4 Snapshot and unread semantics

`Snapshot()` returns all retained items newest-first and computes `unread` by
counting `ReadAtMS == 0`; it does not trust a separately maintained counter.
The 64 KiB websocket frame limit makes sending 500 bounded details in one
frame impossible. Wire representations therefore separate summary paging
from one-item detail:

```go
type Summary struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Subtype      string `json:"subtype,omitempty"`
	Sender       string `json:"sender"`
	Title        string `json:"title"`
	Summary      string `json:"summary"`
	OccurredAtMS int64  `json:"occurredAtMs"`
	CreatedAtMS  int64  `json:"createdAtMs"`
	ReadAtMS     int64  `json:"readAtMs,omitempty"`
	Renderer     string `json:"renderer"`
}
```

- `notifications-state` carries the newest 20 summaries, the registry, and
  exact unread/retained counts across all 500.
- `notifications-page` carries at most 50 summaries. The server marshals and
  checks every page against a 48 KiB cap; if necessary it removes rows from
  the end until the encoded frame fits, but always returns at least one row.
- `notification-detail` carries exactly one complete `Notification`, capped
  naturally by the individual field/detail limits.
- Pagination is newest-first and cursor-based (§4.1). This is websocket
  paging, not an HTTP API. Unread count always covers all retained items,
  including pages the client has not loaded.

## 4. Service API and websocket protocol

Create `controller/internal/notifications/service.go`. `Service` owns the
Valkey client, clock, ID generator, lifecycle marker path, mutex, logger, and
`broadcast func([]byte)`. It exposes:

```go
func New(client *valkey.Client, dataDir string, broadcast func([]byte)) *Service
func (s *Service) Create(ctx context.Context, request CreateRequest) (Notification, error)
func (s *Service) Snapshot() (Snapshot, error)
func (s *Service) Message() ([]byte, error)
func (s *Service) HandleMessage(conn *ws.Conn, payload []byte) bool
```

`Create` validates, assigns ID/timestamps, persists with the create script,
then broadcasts one fresh complete `notifications-state` snapshot. It returns
the persisted notification. An idempotent same-ID create returns the existing
record and does not rebroadcast.

### 4.1 Exact frames

| Frame | Direction | Payload and behavior |
|---|---|---|
| `notifications-req` | client → server | `{"type":"notifications-req"}`; sender-only current snapshot |
| `notification-read` | client → server | `{"type":"notification-read","requestId":"<1-64 ASCII>","id":"<ULID>"}` |
| `notifications-read-all` | client → server | `{"type":"notifications-read-all","requestId":"<1-64 ASCII>"}` |
| `notifications-page-req` | client → server | `{"type":"notifications-page-req","requestId":"...","before":"<exclusive ULID or empty>","limit":50}`; limit 1-50 |
| `notification-detail-req` | client → server | `{"type":"notification-detail-req","requestId":"...","id":"<ULID>"}` |
| `notifications-state` | server → client(s) | `{"type":"notifications-state","notifications":[<newest 20 summaries>],"types":[{"name","icon","defaultRenderer"}...],"unread":N,"retainedCount":N,"revision":"<newest ID or empty>","change":{"kind":"created|read|read-all","id":"...","readAtMs":N}}`; `change` omitted on replay/no-op |
| `notifications-page` | server → initiating client | `{"type":"notifications-page","requestId":"...","notifications":[<summaries>],"nextBefore":"<ULID or empty>","done":bool,"retainedCount":N,"revision":"..."}` |
| `notification-detail` | server → initiating client | `{"type":"notification-detail","requestId":"...","notification":<complete Notification>}` |
| `notification-error` | server → initiating client only | `{"type":"notification-error","requestId":"...","code":"invalid_request|not_found|persistence_failed","error":"bounded text ≤240 code points"}` |

Rules:

- Decode requests with `DisallowUnknownFields`, require one complete JSON
  object and no trailing token. `notifications-req` has no other fields.
- `requestId` is required on mutations and matches
  `^[A-Za-z0-9._:-]{1,64}$`. It correlates errors only; successful mutations
  are acknowledged by the broadcast snapshot.
- Mark-one validates ULID and runs the atomic script. Missing/evicted ID
  returns sender-only `not_found`; no broadcast. Already-read is successful
  and sends the current snapshot to the sender only, avoiding a no-op global
  broadcast.
- Mark-all with zero changes sends the current snapshot to the sender only.
  A changed mark-one or mark-all broadcasts the post-write snapshot with a
  `change` to every connection, including the sender. `read` identifies one
  item; `read-all` has empty `id` and its common `readAtMs`, instructing
  clients to mark every currently loaded retained row read. Creation uses
  `change.kind:"created"` and empty `readAtMs`.
- Page cursors are exclusive IDs from the current retained order. Empty
  `before` starts at newest. A nonempty cursor must still be retained or the
  server returns `not_found`; the client restarts paging from empty. `done`
  means no older retained item follows the returned page. `nextBefore` is
  the last returned ID when more remain, otherwise empty.
- Detail lookup reads the retained immutable item plus its current read
  timestamp. Missing/evicted IDs return `not_found`. Looking up detail does
  not mark read; selection separately sends `notification-read`.
- Every websocket connection receives `notifications-state` in
  `SetOnConnect`, after it is registered and before ordinary periodic state
  is relevant. Explicit `notifications-req` supports recovery/testing.
- Reconnect has no delta protocol. The authoritative snapshot replaces local
  recent state, registry, and count; the full page then restarts summary
  paging from the empty cursor and refetches selected detail.
- Broadcast happens synchronously after the durable script while holding the
  service mutex. Websocket per-connection write locking preserves frame
  integrity. Clients replace recent summaries, apply `change` to loaded
  summaries/detail, and sort by `(createdAtMs DESC, id DESC)`; stale duplicate
  snapshots are harmless. `revision` helps tests/logging but clients must
  still accept an equal revision because read state can change without a new
  ID.
- No HTTP route is added. The existing `/ws` is the sole protocol surface.

## 5. N1 wiring in `main.go`

Modify `controller/cmd/controller/main.go` in this order:

1. Construct one `notifications.Service` from the existing shared Valkey
   client, `dataDir`, and `hub.Broadcast` before constructing local tools.
   Reuse that Valkey client rather than creating notification-specific
   connections at call sites.
2. Inject the service as the agent tool's `NotificationCreator` (§8).
3. In the websocket handler, dispatch
   `notificationService.HandleMessage(conn, payload)` before the generic chat
   fallback.
4. In `SetOnConnect`, send `notificationService.Message()`; on failure log
   once and send a sender-only `notification-error` with
   `code:"persistence_failed"` rather than disconnecting the client.
5. Run lifecycle startup initialization (§6) before accepting HTTP traffic.
   Run lifecycle shutdown finalization before `server.Shutdown`, with its own
   three-second context inside the existing five-second shutdown budget.
6. Update the startup log's wired-subsystems text to include notifications.

Notification startup failure is visible but does not make `/healthz` fail:
log the failure and continue. A tool/config producer receives its concrete
error. The UI retains its last in-memory snapshot and shows disconnected or
error state until a later request succeeds.

## 6. Reliable lifecycle notifications and marker protocol

Create `controller/internal/notifications/lifecycle.go`. The marker is the
persistent file `$VM_DATA_DIR/controller-lifecycle.json`, not a Valkey key.
This deliberately separates crash evidence from Valkey availability/AOF
flush timing. Add it to spec 007's persistence map as a top-level persistent
file owned by `internal/notifications`; no new directory is needed.

### 6.1 Marker schema and writes

```json
{
  "version": 1,
  "state": "running|planned-restart|clean-pending|clean",
  "runId": "<ULID>",
  "startedAtMs": 0,
  "updatedAtMs": 0,
  "reason": "container-stop|config-restart|config-restart-startup|unclean-recovery",
  "deadlineMs": 0,
  "notificationId": "<ULID or empty>",
  "pendingNotification": null
}
```

`pendingNotification` is either null or the complete canonical immutable
`Notification` JSON with `readAtMs` absent. It is permitted only in
`clean-pending`, must match `notificationId`, and is capped by the ordinary
notification limits. This duplication is deliberate crash-recovery state: it
lets retries call `createExact` with byte-identical `createdAtMs` and content
instead of causing `ID_CONFLICT`.

Every marker update writes mode `0600` to a sibling temporary file, calls
`Sync`, closes, renames over the marker, then opens and `Sync`s
`$VM_DATA_DIR`. Reject malformed/version-unknown markers as prior unclean
state, but preserve no untrusted strings in notification text/detail.

### 6.2 Startup algorithm

`Lifecycle.Startup(ctx)` is called exactly once before `ListenAndServe`:

1. Marker absent: first-ever boot. Emit no startup/crash notification.
2. Previous `state:"clean"`: if `reason:"config-restart"`, reserve the complete
   immutable restart-complete notification by rewriting the marker to
   `clean-pending`, reason `config-restart-startup`, with a new ID, fixed
   timestamps/content, and `pendingNotification`; then call `createExact`.
   Its type is `success`, subtype `config-restart-startup`, sender
   `controller`, title `Configuration restart complete`, summary
   `The controller restarted cleanly after configuration changed.`, renderer
   `lifecycle`, with prior run/timestamp fields in detail. For
   `container-stop`, emit nothing.
3. Previous `state:"planned-restart"` whose `deadlineMs >= now-300000`:
   treat it as an interrupted but deliberate configuration restart, never as
   an unclean crash. Reserve and create the same `config-restart-startup`
   notification through step 2's `clean-pending`/`createExact` flow, with
   detail `{"recoveredFrom":"planned-restart"}`.
4. Previous `state:"clean-pending"`: validate and recreate/verify the embedded
   `pendingNotification` through `createExact`. Reason `unclean-recovery`
   recreates `unclean-startup`; `container-stop` recreates `clean-shutdown`;
   `config-restart` recreates `config-restart-shutdown` and then reserves/
   creates restart-complete as step 2; `config-restart-startup` recreates only
   that completion notification. This closes every marker/Valkey crash window
   idempotently.
5. Previous `state:"running"`, stale planned restart
   (`deadlineMs < now-300000`), malformed marker, or unreadable non-ENOENT
   marker: create `type:"error"`, subtype `unclean-startup`, sender
   `controller`, title `Controller restarted unexpectedly`, summary
   `The previous controller run did not shut down cleanly.`, renderer
   `lifecycle`. `occurredAtMs` is the previous `updatedAtMs` when valid,
   otherwise now. Detail version 1 includes only validated
   `previousRunId`, `previousStartedAtMs`, `lastMarkerAtMs`, and
   `markerStatus`.
6. Generate a new run ID and atomically replace the marker with `running`,
   current start/update timestamps, empty reason/deadline/notification ID.
   Marker-write failure is returned and logged; startup continues, but this
   run cannot claim crash detection reliability.

Reserve the complete recovery notification before replacing the old marker.
If notification persistence fails, return failure and leave `clean-pending`
so a later respawn retries byte-identical immutable content. For an ordinary
`running`, stale, malformed, or unreadable crash marker, first construct the
complete notification and atomically rewrite the marker as `clean-pending`
with it and reason `unclean-recovery`; recovery then uses `createExact`.
After every pending notification exists durably in Valkey, atomically write
the next `running` or `clean` marker and clear `pendingNotification`. Tests
lock this retry behavior.

### 6.3 Planned configuration restart

Spec 031's `POST /api/config/restart` handler calls:

```go
func (l *Lifecycle) PlanConfigRestart(ctx context.Context) error
```

only after it verifies the pending hash and immediately before returning the
`202` response and scheduling controller restart. The configuration was
already validated and durably committed by the earlier save. The method
atomically writes `planned-restart` with the current run ID,
`reason:"config-restart"`, `deadlineMs = now + 120000`, and no notification.
Failure aborts the restart and returns a bounded `503` restart-preparation
error; the saved config remains durable and pending, so the operator can retry.

`configapi` owns a narrow `RestartPlanner` interface with that method and a
no-op implementation for isolated spec 031 operation. Spec 032 injects
`Lifecycle` from `main.go`. Neither package imports the other solely for
wiring.

The config package receives this method as a narrow callback interface; it
must not import websocket or agent packages. This is the explicit spec 031
save seam. A config save not requiring restart creates the §9 notification
but does not touch the lifecycle marker.

### 6.4 Graceful shutdown

On `SIGTERM` or `SIGINT`, before stopping the HTTP server:

1. Read the marker. A current non-expired `planned-restart` selects reason
   `config-restart`; otherwise reason is `container-stop`.
2. Construct one complete immutable notification with a reserved ULID and
   fixed occurred/created timestamps. Atomically write `clean-pending`
   carrying it as `pendingNotification`, the current run ID/timestamps, and
   reason.
3. Persist that exact notification through `createExact`:
   - ordinary: `type:"info"`, subtype `clean-shutdown`, title
     `Controller shutting down`, summary
     `The controller received a shutdown request and saved its state.`;
   - planned: `type:"info"`, subtype `config-restart-shutdown`, title
     `Controller restarting`, summary
     `The controller is restarting to apply configuration changes.`;
   both sender `controller`, renderer `lifecycle`, and detail containing
   validated `runId`, `startedAtMs`, `shutdownAtMs`, and `reason`.
4. On durable notification success, atomically write `clean` with the same
   fields/ID and null `pendingNotification`. Then proceed with HTTP shutdown.
5. If any step fails, log it and continue shutdown after the three-second
   lifecycle context expires. Leave `running` or `clean-pending` evidence;
   never write `clean` when the notification write did not succeed.

This protocol handles s6 controller respawns and container stops uniformly.
First-ever boot is quiet, a kill without signal leaves `running` and yields
an unclean-startup notification, a normal stop yields a durable clean
shutdown notification, and a spec 031 restart is explicitly identified on
both sides without a spurious unclean label.

## 7. N2 console UI

Modify `controller/web/static/index.html`,
`controller/web/static/js/app.js`, `controller/web/static/js/router.js`,
`controller/web/static/css/app.css`, and
`controller/tools/fetch-assets.sh`; create
`controller/web/static/js/notifications.js`.

Add pinned Lucide names `bell`, `circle-info`, `circle-check`, and `settings`
to the existing pinned sprite input. Reuse existing `triangle-alert`,
`circle-x`, `bot`, and `monitor`. Never accept an icon path or SVG body from a
frame.

### 7.1 Sidebar bell, popover, and badges

- Place a notification control above the theme control in `.sidebar-footer`.
  It is a real `<button id="notification-bell">` with the bell sprite,
  visible text `Notifications`, `aria-haspopup="dialog"`,
  `aria-controls="notification-popover"`, and accurate `aria-expanded`.
- A badge inside the button and a second badge inside the top-level
  `/notifications` nav link both show the global unread count. Hide at zero;
  render `99+` at 100 or more while accessible text says the exact count.
  Count changes are not an `aria-live` region.
- Add `/notifications` to the nav between Jobs and Tools. The nav label uses
  the bell icon and the same count.
- Bell click toggles a `role="dialog"`, `aria-label="Recent notifications"`
  popover. Opening moves focus to the first unread row, otherwise first row,
  otherwise the `View all notifications` link. It does not mark anything
  read merely by opening.
- Popover shows the newest 10 sent notifications, each as a button with
  registry icon, title, one-line summary, sender, relative/absolute `<time>`,
  and unread dot. Selecting a row sends `notification-read` if needed, closes
  the popover, and navigates with the router to
  `/notifications?id=<encoded ID>`.
- Footer actions are `Mark all read` (disabled when unread is zero) and
  `View all notifications`.
- Escape closes and restores focus to the bell. Pointer down outside closes
  and restores focus only when focus was inside. Tab/Shift+Tab wrap within
  the open popover. Route navigation, mobile sidebar close, and connection
  loss close it. Opening the theme popover closes notifications and vice
  versa; expose small close callbacks rather than duplicating listeners.

### 7.2 Route and page skeleton

Add router entry `["/notifications", ["notifications", "Notifications"]]`.
The router must preserve the `?id=` query when navigating to detail, while
route identity remains pathname-based.

Add:

```html
<section data-page="notifications" hidden class="notifications-page"
         aria-labelledby="notifications-title">
  <div class="page-heading">
    <div><h1 id="notifications-title">Notifications</h1>
      <p class="page-caption">Messages from the assistant and local services.</p>
    </div>
    <button id="notifications-read-all" type="button">Mark all read</button>
  </div>
  <div id="notifications-status" class="notifications-status"></div>
  <div class="notifications-grid">
    <section aria-label="Notification list">
      <div id="notification-filters" class="notification-filters"></div>
      <ol id="notification-list" class="notification-list"></ol>
    </section>
    <aside id="notification-detail" class="notification-detail"
           aria-label="Notification details"></aside>
  </div>
  <div id="notification-detail-curtain" hidden></div>
</section>
```

### 7.3 List, filters, selection, and responsive behavior

- Filters are server registry type (`All` plus each type) and read state
  (`All`, `Unread`, `Read`). Use buttons with `aria-pressed`; filter state is
  in memory, not persisted. Counts use the currently sent snapshot.
- List order is newest-first. Rows expose type icon, title, summary, sender,
  time, subtype when present, and unread state. The selected row has
  `aria-current="true"`. Enter/Space select; Up/Down move row focus;
  Home/End move to the first/last visible row.
- Entering the route chooses `?id=` when present and loaded; otherwise the
  first visible item. On entry/reconnect the module sends
  `notifications-page-req` with empty `before` and limit 50, replacing its
  page cache. A `Load older` button requests `nextBefore` and appends; it is
  hidden when `done:true`. If a cursor is evicted, show the error and restart
  from the empty cursor. Selection sends both `notification-detail-req` and,
  when unread, `notification-read`; it updates `history.replaceState` with
  `?id=` only after detail succeeds. Unknown or evicted IDs show
  `Notification not found` and select nothing; do not silently select a
  different item until the user changes a filter or selects one.
- Desktop uses `minmax(18rem, 28rem) minmax(0, 1fr)` list/detail columns with
  independent scrolling. Below 47.999 rem, detail is a full-screen
  right-side slide-over with Back button, Escape/backdrop close, and focus
  restoration to the selected row. Browser Back/Forward follows `?id=`.
- While older pages remain, render `Showing <loaded> of <retainedCount>
  retained notifications.` above the list. Filters apply only to loaded
  summaries and say `Filter applies to loaded notifications` beside
  `Load older`; after `done:true`, omit that qualifier.

### 7.4 Safe renderer registry

`notifications.js` contains one frozen map from renderer discriminator to a
pair of custom presentation functions:

```js
const renderers = Object.freeze({
  generic: {popover: renderGenericPopover, detail: renderGenericDetail},
  lifecycle: {popover: renderLifecyclePopover, detail: renderLifecycleDetail},
  configuration: {popover: renderConfigurationPopover, detail: renderConfigurationDetail},
  agent: {popover: renderAgentPopover, detail: renderAgentDetail},
});
```

Every function receives `(container, notification)` and appends DOM nodes.
Popover functions receive the bounded summary shape and produce the compact
icon/title/summary/sender/time/subtype row; lifecycle emphasizes shutdown or
recovery subtype, configuration identifies the saved-config subtype, and
agent shows its agent subtype. Detail functions receive the complete notification
and may render the richer structured content below. The bell list and full
page list both use the selected renderer's `popover` function; the detail pane
uses `detail`.
Unknown renderer falls back to `generic` and displays
`Unsupported detail renderer: <name>` as text. Renderers:

- `generic` detail: definition list of sorted detail keys; nested objects/arrays use
  the existing `renderTree` widget.
- `lifecycle` detail: labelled run ID, reason, start/event times, previous run, and
  recovery status; omit absent fields.
- `configuration` detail: labelled changed key names and restart-required state.
  Values/secrets are never included by the producer.
- `agent` detail: structured detail tree plus subtype; no executable actions.

Use `textContent` throughout. For a future detail field explicitly documented
as a URL, `safeLink` parses against `location.origin`, permits only same-origin
relative paths and `http:`/`https:`, applies `rel="noopener noreferrer"` to
external links, and otherwise emits text. No initial renderer needs to make
arbitrary payload strings clickable.

### 7.5 Loading, empty, error, reconnect

- Before first snapshot: stable skeleton rows plus `Loading notifications…`.
- While a summary page or selected detail is outstanding, keep existing
  content stable, disable only its initiating control, and show an adjacent
  loading label. Match responses by `requestId`; ignore superseded responses.
- Successful empty snapshot: `No notifications yet.` in page and
  `No recent notifications.` in popover; detail says `Select a notification
  to inspect it.`
- No filter matches: `No notifications match these filters.`
- `notification-error`: retain current data, render bounded inline error near
  the initiating control, re-enable controls, and allow retry.
- Disconnected: retain data and badges, close popover, disable mutation
  buttons, and show `Disconnected; notification state may be stale.`
- Reconnected: websocket on-connect snapshot replaces recent state, clears
  stale errors, restarts page loading from the empty cursor, refetches the
  selected detail by ID, reapplies filters, and updates all counts.
- All controls have visible focus. Unread distinction is not color-only:
  rows include an unread dot and visually emphasized title.

## 8. N3 agent `notify` tool

Modify `controller/internal/agent/agent.go` configuration as needed and
`controller/internal/agent/tools.go`. Add a narrow interface:

```go
type NotificationCreator interface {
	Create(context.Context, notifications.CreateRequest) (notifications.Notification, error)
}
```

`Config` carries `Notifications NotificationCreator`; production injects the
singleton service. Tests inject a fake. Add this authoritative definition:

```json
{
  "type": "object",
  "properties": {
    "type": {
      "type": "string",
      "enum": ["info", "success", "warning", "error"],
      "description": "Notification severity/type."
    },
    "subtype": {
      "type": "string",
      "maxLength": 48,
      "pattern": "^[a-z][a-z0-9._-]{0,47}$"
    },
    "title": {"type": "string", "maxLength": 120},
    "summary": {"type": "string", "maxLength": 240},
    "detail": {"type": "object"}
  },
  "required": ["type", "title", "summary"],
  "additionalProperties": false
}
```

Description:

> Create a durable notification for the user when a background result,
> warning, or failure deserves attention. Keep the title and one-line summary
> concise; detail must be structured data, never HTML.

Execution:

- Decode with existing `decodeArgs`/`DisallowUnknownFields`.
- Reject a missing configured creator with `notifications unavailable`.
- Marshal `detail` (default `{}`), and call `Create` with sender `agent`,
  renderer `agent`, supplied approved type/subtype/title/summary, and server
  timestamps/ID/read state.
- Return `ToolResult{Text: notification.ID, Summary: "Created notification
  "+notification.ID}`. It is unread by default.
- Do not set `Observe`; no screenshot or follow-up observation is implied.
- It appears on `/tools` automatically through `Definitions()`/`Manifest()`;
  do not add a SPA tool special case. Manual invocation remains queue-backed
  and records its one existing tool activity event.

The agent tool may create notifications but cannot list, read, mark read,
delete, select a sender/renderer, or supply timestamps/IDs.

## 9. Configuration and Telegram producer seams

### 9.1 Spec 031 configuration save

In the config API save path introduced by spec 031
(`controller/internal/configapi/api.go`), after a configuration revision is
durably committed, create:

- type `success`, subtype `config-saved`, sender `config`;
- title `Configuration saved`;
- summary `Master configuration was saved successfully.`;
- renderer `configuration`;
- detail `{"changedKeys":[...],"restartRequired":true|false,"revision":"..."}`
  where changed key paths are sorted, bounded to 64 entries of 120 code
  points, and never include values.

Config persistence remains successful if notification creation fails. Because
spec 031 commits/responds before invoking the notifier, the exact save response
and `config-saved` websocket frame do not gain a warning field; log one bounded
redacted controller error only. If a restart is required, leave it pending.
The later explicit restart request
calls `PlanConfigRestart` as §6.3 specifies; saving alone never marks the
lifecycle as a planned restart. Add focused assertions to
`controller/internal/configapi/api_test.go`.

Wiring uses spec 031's `configapi.ConfigNotifierFunc`: `main.go` installs a
closure that maps `SaveNotice` to the `CreateRequest` above. `configapi` does
not import `internal/notifications`, and the notifications package does not
import configapi. The no-op remains valid for isolated spec 031 tests.

### 9.2 Spec 033 Telegram

Expose `notifications.Creator` as the narrow interface:

```go
type Creator interface {
	Create(context.Context, CreateRequest) (Notification, error)
}
```

Spec 033 injects it into its service and uses sender `telegram`; it does not
write Valkey notification keys or broadcast directly. Spec 033 must append
its renderer/type needs to the authoritative registries and add its own
frontend renderer if generic is insufficient. This spec adds no Telegram
runtime code and no placeholder UI.

## 10. Tests first and required coverage

Create tests before implementation in the order below. Focused tests must
fail initially because the package/module/protocol does not exist.

### 10.1 Go unit and protocol tests

1. `controller/internal/notifications/notifications_test.go`:
   - ULID is 26 allowed characters, lexically increasing for same,
     advancing, and backward clocks; deterministic entropy; overflow path;
   - exact registry order/icons/renderers and defensive copy;
   - sender/subtype rejection tables;
   - UTF-8 repair, whitespace collapse, C0/C1/bidi removal, rune boundaries;
   - title/summary boundaries at 0/1/120/121 and 240/241;
   - detail byte/depth/node/key/string limits, trailing JSON, non-object,
     forbidden keys at depth, duplicate post-sanitize keys, canonical output;
   - unknown type/renderer and disallowed pairing.
2. `controller/internal/notifications/service_test.go`, using a scripted fake
   RESP server:
   - exact `EVAL` wire arguments and strict reply decoding;
   - create/idempotent create/ID conflict; no broadcast before successful
     reply; failed create broadcasts nothing;
   - retention evicts item and read row at 501;
   - mark-one missing/new/already-read behavior and first timestamp;
   - mark-all exact cutoff with a create ordered before and after;
   - newest-first snapshot, read overlay, malformed row skip, unread count;
   - newest-20 state, exact retained/unread counts, 50-row/48 KiB page cap,
     exclusive cursors, detail lookup, registry, and revision;
   - concurrent creators produce total ID/list/broadcast order;
   - request decoding, unknown fields, invalid IDs/request IDs, sender-only
     errors, sender-only no-op snapshots, and global changed broadcasts.
3. `controller/internal/notifications/lifecycle_test.go`, temp directory and
   fake creator/store:
   - first boot quiet and writes `running`;
   - clean container restart quiet; active/stale planned-restart distinction;
   - running/malformed marker creates one idempotent unclean notification;
   - clean-pending recovery retries byte-identical immutable JSON including
     created timestamp, never `ID_CONFLICT`;
   - graceful and config-restart shutdown exact notifications and marker
     transitions; crash before/after restart-complete creation never duplicates;
   - injected failures at temp write, sync, rename, notification create, and
     final clean write leave the specified recoverable state;
   - marker mode `0600`, directory sync path, and no unvalidated marker text
     enters a notification.
4. Extend `controller/internal/valkey/valkey_test.go` with exact `EVAL` RESP
   encoding, nested array/integer/string/nil reply parsing, and Valkey error.
5. Extend `controller/internal/agent/agent_test.go`:
   - manifest schema golden includes `notify` and
     `additionalProperties:false`;
   - success fixes sender/renderer, omits ID/timestamps/read state, passes
     structured detail, and returns ID;
   - unknown fields, schema/service validation, missing creator, and creator
     failure propagate as tool errors;
   - `notify` does not observe and creates no direct activity record.
6. Extend `controller/cmd/controller/main_test.go` for on-connect snapshot,
   handler dispatch ordering, singleton injection, startup-before-listen, and
   graceful lifecycle finalization-before-HTTP-shutdown.

The fake RESP test server must execute enough script semantics to test state,
not merely return canned success for every operation. A test may recognize
the package's four script constants by exact body and maintain in-memory
list/hash state; unknown scripts fail the test.

### 10.2 Node UI tests

Create `test/notifications-ui.test.js` using the repository's existing
zero-dependency DOM/module test style:

- route, nav link, both badges, bell/popover/page skeleton, IDs, and icon
  sprite inputs exist;
- initial loading, empty, no-match, error, disconnected, reconnect replace,
  paged-history counts, Load older, and cursor-expiry recovery;
- newest-first sorting, exact unread counts, 99+ visual cap, global snapshot
  replacing a locally read-looking row;
- popover 10-row cap, first-unread focus, Escape/outside/focus trap, mutual
  exclusion with theme, and focus restoration;
- row selection sends exact detail/read frames, mark-all sends exact frame,
  page loading uses exact cursor/limit frames, no-op buttons, request/error
  correlation, superseded-response rejection, and `?id=` Back/Forward;
- filters, keyboard list navigation, missing ID, desktop/mobile detail;
- every initial renderer, unknown-renderer fallback, malicious strings and
  URLs render as text, and `notifications.js` contains no `innerHTML`;
- the type icon comes only from the server registry after checking the icon
  against the known sprite-ID allowlist.

If existing DOM stubs cannot express focus/history/media-query behavior,
extend the shared zero-dependency test helper; do not introduce jsdom.

### 10.3 Live soak flow

Extend `test/soak.mjs` with `notifications-roundtrip`, using two simultaneous
raw websocket clients A and B:

1. Obtain `tools-list`; hard-assert `notify` exists and its schema has
   `additionalProperties:false`.
2. Invoke `notify` manually through client A with a unique title and detail.
3. Hard-assert A receives successful `tool-result` whose text is a ULID.
4. Hard-assert both A and B receive `notifications-state` containing that ID
   unread and the same unread count.
5. Close/reopen B as C; hard-assert on-connect replay still contains the ID
   unread (persistence/reconnect).
6. A sends `notification-read`; hard-assert A and C both receive the item
   **read** with equal nonzero `readAtMs` and decremented counts.
7. C sends `notifications-read-all`; hard-assert both sockets converge to
   unread zero. Repeating it is idempotent and does not alter read timestamps.

Use per-client frame handlers rather than the current single global handler,
bounded 60-second deterministic waits, and cleanup both sockets on every
exit. The flow has only hard assertions; it does not depend on model prose.

### 10.4 E2E cross-client and lifecycle probe

Create `test/notifications-probe.mjs` and invoke it from `test/e2e.sh`:

- connect two clients, create via queue-backed manual `notify`, verify
  broadcast/reconnect/read/read-all as in soak;
- stop the container normally and restart on the same data dir; verify the
  clean-shutdown notification persists and no `unclean-startup` was added;
- force an unclean controller-only kill (`docker exec ... kill -KILL` the
  supervised controller process), wait for s6 respawn/health, and verify
  exactly one `unclean-startup` for the prior run;
- perform the spec 031 deliberate config-restart probe and verify
  `config-restart-shutdown` plus `config-restart-startup`, with no
  `unclean-startup` for that run;
- reconnect a fresh client and verify all prior notifications/read state.

Use IDs/subtypes/run IDs, not English substring matching, for assertions.
Renumber e2e progress totals. The test remains local and deterministic.

## 11. Exact implementation file manifest

Only these implementation/reconciliation files are in scope when this spec
is executed:

### New

- `controller/internal/notifications/notifications.go`
- `controller/internal/notifications/service.go`
- `controller/internal/notifications/lifecycle.go`
- `controller/internal/notifications/notifications_test.go`
- `controller/internal/notifications/service_test.go`
- `controller/internal/notifications/lifecycle_test.go`
- `controller/web/static/js/notifications.js`
- `test/notifications-ui.test.js`
- `test/notifications-probe.mjs`

### Modified

- `controller/internal/valkey/valkey.go`
- `controller/internal/valkey/valkey_test.go`
- `controller/internal/agent/agent.go`
- `controller/internal/agent/tools.go`
- `controller/internal/agent/agent_test.go`
- `controller/cmd/controller/main.go`
- `controller/cmd/controller/main_test.go`
- `controller/internal/configapi/api.go` (the spec 031 save/restart seam)
- `controller/internal/configapi/api_test.go`
- `controller/web/static/index.html`
- `controller/web/static/js/app.js`
- `controller/web/static/js/router.js`
- `controller/web/static/js/nav.js` (popover mutual exclusion/close hook only)
- `controller/web/static/js/theme.js` (popover mutual exclusion/close hook only)
- `controller/web/static/css/app.css`
- `controller/tools/fetch-assets.sh`
- `test/soak.mjs`
- `test/e2e.sh`
- `specs/007-persistence-locality.md`
- `README.md`
- `AGENTS.md`
- `.cursor/skills/develop/SKILL.md`
- `.cursor/skills/operate/SKILL.md`
- symlinked `.claude/skills/*` views only as produced by repository layout;
  do not create divergent copies

No Docker layer, s6 service definition, HTTP endpoint, prompt file, npm
dependency, or package dependency is added.

## 12. Documentation, reconciliation, and execution order

Implementation order is normative:

1. Add all §10 failing tests/probes and confirm focused red.
2. Implement model/validation/ULID and Valkey `Eval`; focused Go green.
3. Implement atomic service/protocol; service and websocket tests green.
4. Implement lifecycle marker and main wiring; lifecycle/main tests green.
5. Implement agent/config producers; agent/config tests green.
6. Implement UI; Node UI tests and `npm run build:web` green.
7. Implement soak/e2e probes; run focused live probes.
8. Amend spec 007 §1a with:

   | State | Path | Owner / mechanism | Introduced |
   |---|---|---|---|
   | Notification history and global read state | `$VM_DATA_DIR/valkey/` AOF (`virtualme:notifications:*`) | `internal/notifications` via atomic Valkey scripts | 032 |
   | Controller lifecycle crash/clean marker | `$VM_DATA_DIR/controller-lifecycle.json` | `internal/notifications` atomic file replace + directory sync | 032 |

   Also add `controller-lifecycle.json` to smoke/e2e known top-level entries
   and persistence-map gate semantics; because it is a file, do not add it to
   `10-data-dirs.sh`.
9. Perform final reconciliation against the actual tree: verify every frame,
   field, limit, icon, file path, and test named here matches implementation;
   remove stale alternatives and duplicated registries.
10. Run `npm run check`, rebuilt `bash test/e2e.sh`, and
    `SOAK_FLOW=notifications-roundtrip ./cli.sh soak --no-build`.
11. Run `/master-update` last. It must update README (notification UI/tool,
    websocket table, lifecycle behavior, persistence), AGENTS (package,
    frames, tool, UI), develop skill (how to add a type/sender/renderer and
    atomic-write rule), operate skill (bell/page, read state, lifecycle
    diagnosis), spec index, and refreshed screenshots including
    `/notifications`. Re-run `npm run check` after generated/doc changes.

## 13. Acceptance checklist

- [ ] Tests were authored first and observed failing for the intended missing
      package/module/protocol before implementation.
- [ ] `npm run check` passes with all new Go/Node tests, lint, typecheck,
      web build, gofmt, vet, and deterministic gates.
- [ ] Valkey create/read/read-all/snapshot scripts are atomic, exact-wire
      tested, retain exactly 500, clean evicted hash fields, and require no
      `MULTI` or runtime dependency.
- [ ] IDs are 26-character monotonic ULIDs and lexical order remains stable
      across equal/backward injected clocks.
- [ ] Every field and limit in §2 is enforced server-side; malicious detail
      cannot supply HTML, SVG, renderer, component, script, or style.
- [ ] On-connect, summary pages, and detail replies are bounded below
      websocket limits and report accurate retained/loaded/unread counts.
- [ ] Marking one read in tab A renders it read and updates counts in tabs
      B/C/D in real time; mark-all cutoff, retries, reconnect, races, and
      idempotence match §§3-4.
- [ ] Bell/popover behavior passes mouse, keyboard, focus trap, outside,
      Escape, theme mutual-exclusion, zero/99+ badge, and focus-restoration
      checks.
- [ ] `/notifications` passes route/deep-link, list/detail, filters, read
      states, Back/Forward, responsive slide-over, loading/empty/error/
      disconnected states, paged loading, cursor recovery, and accessibility
      checks.
- [ ] Type icons come only from the pinned Lucide sprite and server registry;
      all detail rendering is DOM-built and `notifications.js` has no
      `innerHTML`.
- [ ] First-ever boot is quiet; normal stop records one clean-shutdown
      notification; forced controller kill records one idempotent
      unclean-startup; planned spec 031 restart records both planned
      lifecycle subtypes and no spurious unclean event.
- [ ] `notify` is present in `Definitions()` and `/tools`, rejects unknown
      properties, fixes sender/renderer/timestamps/read state server-side,
      returns the notification ULID, and adds no duplicate activity event.
- [ ] A successful spec 031 save emits only changed key names, no values or
      secrets; spec 033 can inject `notifications.Creator` without direct
      Valkey/websocket coupling.
- [ ] `test/notifications-probe.mjs`, rebuilt `bash test/e2e.sh`, and the
      two-socket `notifications-roundtrip` soak flow pass.
- [ ] Spec 007 persistence map and known-root checks include both notification
      state lanes exactly as §12 specifies.
- [ ] Final implementation-to-spec reconciliation found no mismatched frame,
      field, cap, icon, path, test, or undocumented behavior.
- [ ] `/master-update` completed last, screenshots/docs/skills/spec index are
      current, and the post-reconciliation `npm run check` passes.

## Amendments

None.
