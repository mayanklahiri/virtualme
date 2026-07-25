# Spec 033: Telegram Integration — Authorized Long-Poll Chat and Console

| | |
|---|---|
| Status | Accepted (2026-07-25) |
| Milestones | T1: transport, configuration, status, event log, and test-send console; T2: authorized shared-chat ingress, correlated delivery, commands, restart safety, and notifications |
| Depends on | `specs/031-master-config.md` (typed configuration, secret references, `/config`, secret refresh), `specs/032-assistant-notifications.md` (persistent notification producer API and UI), `specs/003-controller.md` (controller and SPA), `specs/007-persistence-locality.md` (persistent-state map), `specs/013-job-queue-scheduler.md` (sequential durable queue and disconnect cancellation). Specs 031 and 032 MUST execute first. |
| Produces | One optional singleton Telegram Bot API integration using controller-initiated HTTPS long polling; mandatory chat allowlisting and optional user allowlisting; a `/telegram` console page; a bounded redacted event log; test sends; shared persisted chat ingress and precisely correlated final-answer delivery; Telegram lifecycle notifications |
| Followed by | Future channel integrations MUST use the channel-neutral chat submission, source, initiator, correlation, and delivery contracts introduced here |

## 0. Executor instructions

1. The constitution binds. Controller runtime code uses only the Go standard
   library; `controller/go.mod` keeps zero `require` lines. Do not add a
   Telegram SDK, generated client, vendor directory, npm runtime dependency,
   inbound HTTP webhook, or new exposed port.
2. This is an outbound-network integration in the networking sense: the
   controller initiates HTTPS requests to the Telegram Bot API. It receives
   user updates through those responses. It MUST NOT listen for Telegram
   callbacks or expose a webhook route. Telegram's Bot API is an HTTP JSON
   API; this spec does not assert that Telegram publishes or supports an
   official OpenAPI description.
3. Implement in the mandatory order below, stop-on-red, and retain the tests:
   - **RED:** add the tests in §13 for the current milestone and run the
     narrowest relevant commands to demonstrate that they fail for the
     missing behavior.
   - **GREEN:** implement only enough production code to satisfy those tests.
   - **REFACTOR:** reconcile naming, bounds, documentation, persistence maps,
     config metadata, and wiring; then run the broader gates.
   T1 tests and implementation precede T2 tests and implementation. Do not
   write the implementation first and backfill tests.
4. At each Go step run `gofmt` and
   `cd controller && go test ./... -count=1`. At each SPA step run the
   relevant `node --test test/...` files and `npm run build:web`. Finish with
   `npm run check`, smoke/e2e as specified in §13, and `/master-update`.
5. Exact protocol names, JSON field names, config paths, Valkey keys, caps,
   state transitions, and authorization rules in this spec are normative. Do
   not invent aliases or accept permissive alternate forms.
6. Secrets are never test fixtures in tracked files. Every token used by
   tests is an obviously fake runtime value supplied through the spec 031
   secret-store test fixture. Never log a token, place one in a URL included
   in an error, return one over WebSocket, persist one in Valkey, put one in
   config YAML/JSON, or render one in the DOM.
7. Existing persisted chat entries and queued envelopes MUST remain readable.
   The migration rules in §8 are part of the implementation, not optional
   cleanup.
8. No real Telegram account, credential, DNS, internet access, or wall-clock
   timing is required by any deterministic gate. The canonical gate remains
   completely offline.

## 1. Goals, boundaries, and fixed decisions

### 1.1 Goals

- An operator can configure one Telegram bot through spec 031, see its
  effective state and identity on `/telegram`, inspect a bounded sanitized
  update log, choose an authorized destination, and send a test message.
- Authorized Telegram text messages enter the same persisted conversation as
  web chat. They appear immediately in every connected web console. The final
  assistant answer appears in the web console and is sent only to the
  Telegram chat that originated that job.
- Restarts resume from the persisted Telegram update offset without injecting
  the same Telegram update twice.
- Authorization occurs before command execution, history persistence,
  statistics changes, job creation, typing actions, or assistant delivery.
- Polling, authentication, rate limiting, cancellation, secret replacement,
  and error state are explicit, bounded, observable, and hermetically tested.

### 1.2 Non-goals

- No webhook, inbound listener, second bot, multiple Telegram accounts,
  media/files/photos/voice/stickers, message edits, reactions, reply markup,
  inline queries, callback queries, topics, forum-thread routing, Markdown or
  HTML parse mode, token streaming, chat-history synchronization into old
  Telegram messages, or Telegram-side configuration.
- No `/clear` and no `/stop`. Telegram supports exactly `/start`, `/help`,
  `/status`, and ordinary text in this spec. Unknown slash commands receive
  the bounded help response. `/clear` and `/stop` are therefore harmless
  unknown commands and MUST NOT mutate or cancel shared work.
- No real-Telegram soak. No optional credential-dependent CI path.
- No duplicated enable or secret-edit control on `/telegram`; configuration
  is owned by spec 031's `/config` page.

### 1.3 Privacy and trust boundary

Telegram is an external cloud service. Enabling this integration deliberately
sends authorized user text, chat actions, assistant answers, and Bot API
metadata outside the host. The bot token grants control of the bot and MUST
remain a secret reference. The console and docs MUST warn that:

1. allowlists are the authentication boundary;
2. leaving `allowedUserIds` empty permits every sender in an allowed chat;
3. group privacy mode affects which group messages Telegram delivers to the
   bot and is configured with BotFather, not Virtual Me; and
4. the repository's private-network trust model still applies to port 8080,
   while Telegram traffic crosses that local boundary.

## 2. Delivery milestones

### 2.1 T1 — integration transport and operator console

T1 includes the complete config schema, secret resolution and refresh,
typed Bot API client, `getMe`, long-poll lifecycle, update offset and redacted
event persistence, known authorized-chat discovery, lifecycle notifications,
WebSocket protocol, `/telegram` page, destination selection, and test sends.
T1 logs authorized text updates but does not yet inject them into chat. The
event outcome is `pending_t2` during this intermediate implementation state.
T1 is not accepted independently; it is an implementation checkpoint.

### 2.2 T2 — shared chat and correlated Telegram delivery

T2 adds channel-neutral chat submission, durable source/initiator/correlation
metadata, idempotent update submission, exact reply routing, typing actions,
commands, bounded user-facing errors, and complete e2e coverage. On completion
no `pending_t2` outcome is emitted.

## 3. Configuration and secrets

### 3.1 Canonical schema

Spec 031's schema gains one object at `integrations.telegram`. The persisted
configuration shape is exactly:

```yaml
integrations:
  telegram:
    enabled: false
    botToken: ""
    allowedChatIds: []
    allowedUserIds: []
    pollTimeoutSeconds: 30
    maxEventLog: 200
```

`botToken` is an empty string while disabled or one exact whole-scalar spec
031 secret reference such as
`"${file:/run/secrets/virtualme-telegram-token}"`. A literal token, embedded
interpolation, object, or null is invalid. The config parser MUST reject
unknown Telegram properties rather than silently ignore misspellings.

| Path | Type/default | Validation and UI metadata |
|---|---|---|
| `integrations.telegram.enabled` | boolean, `false` | Label `Enable Telegram`; category `Integrations`; order 330; `x-vm-restart:"controller"`; description says enabling starts outbound HTTPS long polling after token resolution and allowlist validation |
| `integrations.telegram.botToken` | secret-reference string, `""` | Label `Bot token`; `x-vm-secret:{"cacheTtlSeconds":300,"allowEnv":true,"allowFile":true,"resolveWhen":{"path":"integrations.telegram.enabled","equals":true}}`; `x-vm-ui.component:"vm-secret-reference"`; vendor docs URL `https://core.telegram.org/bots/features#botfather`; non-empty whole-scalar reference required when enabled; UI renders only the unresolved reference and redacted resolution state, never a token input or resolved value |
| `integrations.telegram.allowedChatIds` | unique array of strings, `[]` | Label `Allowed chat IDs`; each value is canonical base-10 `/^-?[1-9][0-9]*$/`; preserve configured order; no numeric JSON values; at least one entry when enabled |
| `integrations.telegram.allowedUserIds` | unique array of strings, `[]` | Label `Allowed user IDs (optional)`; each value is canonical positive base-10 `/^[1-9][0-9]*$/`; preserve configured order; empty has the explicit semantics in §3.2 |
| `integrations.telegram.pollTimeoutSeconds` | integer, `30` | Label `Long-poll timeout`; minimum 1, maximum 50; advanced field; Telegram `getUpdates.timeout` and unit `seconds` |
| `integrations.telegram.maxEventLog` | integer, `200` | Label `Retained Telegram events`; minimum 20, maximum 1000; advanced field; bounds the Valkey list after every append |

Config validation reports all errors at their full paths. `enabled:true` with
no token reference or no allowed chat IDs is invalid and does not partially
start the service. Duplicate IDs after canonical string comparison are
invalid. Whitespace, a leading `+`, leading zero, decimal point, exponent, and
zero are invalid.

The `integrations.telegram` object has
`x-vm-integration:{"external":true,"egressHosts":["https://api.telegram.org"],"capabilities":["chat-ingress","chat-egress","notifications"]}`.
Its `x-vm-doc.overview` begins `Telegram Bot API`. Do not advertise webhook
support or an OpenAPI source.

Every added section/leaf also carries all mandatory spec 031 `x-vm-doc`,
`x-vm-ui`, `x-vm-restart`, and secret metadata, including exemplar YAML,
allowed-value explanations, privacy/latency tradeoffs, BotFather link, and
stable order. Regenerate and stale-check
`docs/src/generated/config-reference.json`; the public configuration reference
must describe the AND allowlist rule and external-cloud boundary.

### 3.2 Exact authorization predicate

All Telegram identifiers are converted to canonical decimal strings before
comparison. An update is authorized if and only if all of these are true:

1. it contains `message.chat.id`;
2. that exact ID is in `allowedChatIds`;
3. it contains `message.from.id`, except for service messages that are ignored
   before authorization; and
4. `allowedUserIds` is empty **or** the exact sender ID is in
   `allowedUserIds`.

This is chat allowlist **AND** optional user allowlist, never OR. The same
predicate applies to private chats, groups, and supergroups. A configured user
cannot use the bot from an unlisted chat. When `allowedUserIds` is empty,
every human user in an allowed chat may use it. Bots are rejected regardless
of allowlist membership.

The configured chat allowlist also authorizes outbound test destinations.
Observed chats never expand authority: an observed chat is selectable only
while its ID remains configured in `allowedChatIds`. A configured chat ID is
selectable even before it has been observed, with the label `Chat <id>`.

### 3.3 Token resolution, cache, and refresh

- Resolve the `integrations.telegram.botToken` whole-scalar reference through
  spec 031's secret resolver only while
  enabled. Keep the resolved token in process memory only.
- Treat an empty resolved value as `secret_unavailable`. Do not start HTTP
  requests. Broadcast status and let spec 031 surface its own secret error.
- Subscribe to spec 031's secret-revision callback for the active reference.
  On revision change: cancel the current poll request, discard the old
  in-memory credential, resolve once, create a new client generation, call
  `getMe`, and only then resume polling. A refresh does not reset the offset,
  known chats, event list, or backoff-injection source.
- Master-config edits remain restart-applied per spec 031; this service has no
  live config subscription. The restarted controller constructs a fresh
  generation from the new effective config. Within one process, only active
  secret revisions replace a generation. Cancellation and replacement are
  serialized; at most one poll request exists. A stale generation MUST NOT
  publish status/events or deliver messages after its context is canceled.
- Never include `http.Request.URL`, a raw `url.Error`, response request dump,
  Authorization header, request body dump, or token-bearing Bot API path in
  logs/errors. Convert transport and API failures to the sanitized error codes
  in §6.6 at the client boundary.

### 3.4 Bot API base URL

Production runtime is fixed to `https://api.telegram.org`; there is no
ordinary config property for a base URL. `telegram.NewClient` accepts an
injected base URL and `*http.Client` for Go tests.

Container e2e alone may set both:

```text
VM_TELEGRAM_TEST_MODE=1
VM_TELEGRAM_API_BASE_URL=http://vmhost:<ephemeral-port>
```

The controller accepts the override only when test mode is exactly `1` and
the parsed URL has scheme `http`, no userinfo/query/fragment, and hostname
`127.0.0.1`, `localhost`, or `vmhost`. Any other combination is a startup
error. Release docs MUST NOT advertise these variables. Production config
cannot weaken HTTPS or redirect credentials to another host.

The container e2e starts through the real CLI, so `src/commands/start.js`
forwards these two variables only when `VM_TELEGRAM_TEST_MODE` is exactly `1`
and both are present, and adds `--add-host vmhost:host-gateway` (deduplicated
with the existing mail-smarthost host mapping). A missing half is a CLI
operational error. In every other case it forwards neither Telegram test
variable. Extend CLI argument tests to prove the guarded pair, missing-half
rejection, host-entry deduplication, and absence in normal starts.

## 4. Package and typed Bot API client

Create `controller/internal/telegram`. It owns Bot API DTOs, the narrow HTTP
client, polling service, authorization, event persistence, known-chat
persistence, chunking, Telegram delivery, and its WebSocket handler. It does
not own shared chat history or job execution.

### 4.1 Narrow client interface

```go
type API interface {
    GetMe(context.Context) (User, error)
    GetUpdates(context.Context, GetUpdatesRequest) ([]Update, error)
    SendMessage(context.Context, SendMessageRequest) (Message, error)
    SendChatAction(context.Context, SendChatActionRequest) error
}
```

Production `Client` implements only those methods. Bot API method names on
the wire are exactly `getMe`, `getUpdates`, `sendMessage`, and
`sendChatAction`. Every call is `POST` with
`Content-Type: application/json`; decode JSON with typed request/response
structs and `json.Decoder`, cap response bodies at 1 MiB, reject trailing
non-whitespace JSON, and close bodies.

The URL is `<base>/bot<escaped-token>/<method>`, constructed internally.
Neither URL nor token escapes the client. Telegram success envelopes decode
as:

```go
type Response[T any] struct {
    OK          bool               `json:"ok"`
    Result      T                  `json:"result"`
    ErrorCode   int                `json:"error_code,omitempty"`
    Description string             `json:"description,omitempty"`
    Parameters  ResponseParameters `json:"parameters,omitempty"`
}
type ResponseParameters struct {
    RetryAfter int `json:"retry_after,omitempty"`
}
```

Use explicit DTOs for only the consumed fields:

- `User`: `id`, `is_bot`, `first_name`, `last_name`, `username`;
- `Chat`: `id`, `type`, `title`, `username`, `first_name`, `last_name`;
- `Message`: `message_id`, `date`, `text`, `chat`, `from`, and bot-command
  `entities`;
- `MessageEntity`: `type`, `offset`, `length`;
- `Update`: `update_id`, `message`, `edited_message`, raw-presence booleans
  for unsupported top-level update kinds, and the exact original update object
  as `json.RawMessage` for the bounded operator log in §7. The custom
  unmarshaler decodes typed fields and retains raw bytes from the same response;
  it never re-fetches an update.

`GetUpdatesRequest` contains `offset`, `timeout`, and `allowed_updates`.
`allowed_updates` is always `["message","edited_message"]`; edited messages
are logged and ignored. `SendMessageRequest` contains only `chat_id` and
`text`: no parse mode and no link-preview options. `SendChatActionRequest`
contains only `chat_id` and `action`, where action is always `typing`.

HTTP status and Telegram envelope are both checked. A non-2xx response may
still contain a Telegram error envelope and should be decoded within the
body cap. Descriptions are mapped to bounded sanitized classifications; they
are never passed through verbatim to logs, the console, notifications, or
chat users.

### 4.2 Client timing

Each long-poll request derives from the service generation context and uses
an HTTP client whose response-header timeout is
`pollTimeoutSeconds + 10 seconds`. Non-poll calls use a 15-second child
deadline. Canceling the generation MUST interrupt an active `getUpdates`
request immediately. Do not use an uncancelable background context.

## 5. Runtime service and state machine

There is exactly one `telegram.Service` instance and one poll goroutine in
the controller process. `main.go` creates it after config/secrets,
notifications, Valkey, jobs, and chat are available; registers its channel
delivery handler; includes its on-connect snapshots; then starts it with the
controller root context.

### 5.1 Effective states

`State` is exactly one of:

| State | Meaning and poll behavior |
|---|---|
| `disabled` | config `enabled` is false; no token resolution and no Bot API traffic |
| `invalid_config` | enabled config failed Telegram validation; no traffic |
| `secret_unavailable` | token reference cannot currently resolve; wait for secret revision or controller restart after config edit |
| `connecting` | resolving/validating a new generation or retrying `getMe` after transient failure |
| `connected` | `getMe` succeeded and polling is active or between healthy polls |
| `backing_off` | transient poll/API failure; timer active |
| `polling_suspended` | repeated transient failures crossed §11 threshold, or API returned 409; retries continue except for auth failure |
| `auth_failed` | API returned 401 from any method; poller stopped until secret revision or controller restart |
| `stopped` | controller context canceled; terminal for that process |

`getMe` runs at initial enable/startup and every token/config generation.
It MUST return `is_bot:true`; otherwise state is `auth_failed` with code
`identity_not_bot`. A successful `getMe` updates bot identity and resets the
transient backoff. `connected` is published immediately after this success;
the first poll then begins.

### 5.2 Status model

Status contains no secret reference name or token:

```json
{
  "enabled": true,
  "state": "connected",
  "code": "",
  "detail": "Polling for authorized messages",
  "bot": {
    "id": "123456",
    "username": "virtualme_bot",
    "displayName": "Virtual Me"
  },
  "poll": {
    "timeoutSeconds": 30,
    "nextOffset": 8124,
    "consecutiveFailures": 0,
    "retryAt": null,
    "lastSuccessTs": 1753480000000
  },
  "destinations": [
    {"chatId":"-10077","label":"Engineering","type":"supergroup","observed":true},
    {"chatId":"42","label":"Chat 42","type":"","observed":false}
  ],
  "eventCount": 17,
  "maxEventLog": 200
}
```

All IDs are strings. `code` is one of the sanitized codes in §6.6 or empty.
`detail` comes from a fixed string table, never a remote description.
`destinations` follows configured `allowedChatIds` order; observed metadata
is joined by ID. `retryAt` is Unix milliseconds or null.

Broadcast `telegram-status` only when a field in this model changes, excluding
sub-second timer passage. A backoff timer publishes once when scheduled and
again on transition, not every tick.

## 6. Long polling, offsets, retries, and update classification

### 6.1 Offset persistence

Use `virtualme:telegram:update-offset`, a Valkey string containing the next
integer update ID. Missing or malformed means `0` and emits one sanitized
event with outcome `offset_reset`; it does not prevent startup.

Every `getUpdates` sends the current persisted/in-memory next offset. Process
returned updates in ascending `update_id`; discard duplicate or lower IDs.
After each update has been classified, call the idempotent ingress path in
§8 when applicable, then persist `update_id + 1` before processing the next
update. If offset persistence fails, stop processing that response, enter
`backing_off` with `storage_unavailable`, and retry the same offset. The
idempotency key `telegram:update:<update_id>` in §8 prevents duplicate chat
history or jobs if submission succeeded before the offset write.

Advance/persist the offset for unauthorized, bot-authored, edited,
unsupported, and non-text updates too; otherwise one ignored update could
replay forever. Empty successful responses retain the offset.

### 6.2 Request loop

1. Load offset once for the generation.
2. Call `getUpdates` with that offset, configured timeout, and exact allowed
   update kinds.
3. On success, reset transient failure count and delay, mark poll success,
   process updates as §6.1, and immediately issue the next poll.
4. On root/generation cancellation, return without event, error, retry, or
   notification.
5. On classified failure, follow §§6.3–6.6.

Only the service poll goroutine calls `getUpdates`. Test sends and chat
deliveries may call send methods concurrently through the same safe client.

### 6.3 Exponential backoff and deterministic jitter

For transient failures, the unjittered delays are
`1s, 2s, 4s, 8s, 16s, 32s, 60s, 60s...`. Apply multiplicative jitter in
`[-20%, +20%]`, then clamp to `[800ms, 60s]`. Inject:

```go
type Clock interface {
    Now() time.Time
    Sleep(context.Context, time.Duration) error
}
type Jitter func() float64 // production returns a value in [0,1)
```

Map jitter `j` to multiplier `0.8 + 0.4*j`. Tests use a fake clock and fixed
values; no wall-clock sleeps. A successful `getUpdates`, including an empty
result, resets the sequence.

### 6.4 Telegram `retry_after`

HTTP/API 429 with positive `parameters.retry_after` sleeps that exact number
of seconds, clamped to `[1s, 15m]`, with no added jitter. It increments
consecutive failures and can trigger `polling_suspended`. Missing/invalid
`retry_after` uses ordinary backoff. Send methods return a typed rate-limit
error to their caller; the poll loop applies this rule only to polling.

### 6.5 Conflict and authentication

- API/HTTP 409 means another `getUpdates` consumer is active. Transition
  immediately to `polling_suspended`, code `poll_conflict`, notify once under
  §11, and continue retrying with the ordinary backoff. A later successful
  poll recovers normally.
- API/HTTP 401 means token authentication failed. Cancel polling, transition
  to `auth_failed`, code `authentication_failed`, send the deduplicated
  notification, and make no further Bot API request until config or active
  secret revision changes.
- HTTP 400 caused by this client's fixed request is `protocol_error` and uses
  ordinary backoff; it must be visible rather than silently ignored.
- Other 4xx is `api_rejected`; other 5xx, transport failure, timeout, invalid
  JSON, oversized body, or `ok:false` without a special code is transient.

### 6.6 Sanitized error codes

The only codes crossing the client boundary are:

`authentication_failed`, `identity_not_bot`, `poll_conflict`,
`rate_limited`, `protocol_error`, `api_rejected`, `remote_unavailable`,
`transport_error`, `invalid_response`, `response_too_large`,
`storage_unavailable`, `send_failed`, and `secret_unavailable`.

Error values may include the HTTP/API integer status and retry duration, but
never remote descriptions, request URLs, token/reference values, user text,
or response bodies. Logs use `telegram: <operation>: <code>` plus numeric
status when present.

### 6.7 Update classification and commands

Classify in this exact order:

1. `edited_message` present: append event outcome `ignored_edit`; advance.
2. no `message`: append `ignored_unsupported`; advance.
3. `message.from.is_bot`: append `ignored_bot`; advance.
4. missing sender/chat needed for authorization: append
   `ignored_malformed`; advance.
5. authorization predicate false: append `denied`; advance. Do not reply,
   type, submit chat, update known chats, or reveal authorization details.
6. authorized chat: update known-chat metadata.
7. empty/non-text text: append `ignored_non_text`; advance; do not reply.
8. recognized command: execute below, append `command`; advance.
9. unknown slash command: send help text, append `unknown_command`; advance.
10. normal text: submit through §8, append `accepted`; advance.

Leading/trailing whitespace is removed. The 4096-rune shared-chat input cap
applies after trimming. Over-limit text is not injected; send
`That message is too long. Please keep it to 4096 characters.` and record
`rejected_too_long`.

Commands are recognized only when Telegram supplies a `bot_command` entity
starting at UTF-16 offset 0. Accept `/start`, `/help`, and `/status`, with an
optional case-insensitive `@<current_bot_username>` suffix. Arguments are not
accepted. Exact responses:

- `/start` and `/help`:
  `Virtual Me shares one conversation with the web console. Send text to ask a question. Commands: /help, /status.`
- `/status`: one bounded line generated from local state:
  `Virtual Me is <idle|queued|working>. Telegram is connected as @<username>.`
  Job manager state determines `working` (a chat job running), `queued` (any
  chat job ready), otherwise `idle`.
- unknown command: `Unknown command. Commands: /help, /status.`

Command replies do not enter shared history, increment chat stats, or enqueue
jobs. Send failures are logged/events/notifications as appropriate but do not
rewind the consumed update.

## 7. Bounded redacted event log and known chats

### 7.1 Persistent keys

| Key | Type/cap | Purpose |
|---|---|---|
| `virtualme:telegram:update-offset` | string | next Bot API update ID |
| `virtualme:telegram:events` | list, `LTRIM -maxEventLog -1` after append | oldest-to-newest bounded audit events with optional raw update objects |
| `virtualme:telegram:known-chats` | list, 200 | most recently observed authorized chat metadata, one record per chat after in-process read/replace reconciliation |
| `virtualme:telegram:notification-state` | hash | Telegram-owned dedup episode/cooldown timestamps; contains no notification content or secret |
| `virtualme:chat:ingress:telegram:<updateID>` | string | idempotency record containing correlation/job IDs; retain newest 1000 via the index below |
| `virtualme:chat:ingress:telegram:index` | list, 1000 | ordered update IDs used to delete idempotency records beyond the cap |

Valkey failures never cause a token or event body to be logged. Offset and
ingress-idempotency failures suspend ingestion because correctness depends on
them. Event/known-chat persistence failures degrade status with
`storage_unavailable` but do not expose data.

### 7.2 Event schema, bounded raw update, and redaction

The operator-facing log retains the bounded raw Telegram update as requested;
it is not merely a derived activity summary. Persist exactly:

```json
{
  "id": "evt_01...",
  "ts": 1753480123456,
  "updateId": 8123,
  "kind": "message",
  "outcome": "accepted",
  "chatId": "-10077",
  "chatType": "supergroup",
  "chatLabel": "Engineering",
  "userId": "42",
  "username": "mayank",
  "messageId": 99,
  "textPreview": "bounded preview…",
  "detail": "",
  "rawUpdate": {"update_id":8123,"message":{"message_id":99}},
  "rawOmitted": false
}
```

Rules:

- `textPreview` is at most 160 Unicode code points, converts every control
  character except newline/tab to U+FFFD, collapses whitespace runs to one
  space, and appends `…` when truncated.
- `detail` is from a fixed local classification table and at most 256 Unicode
  code points. It never contains remote API descriptions or arbitrary
  `%v`-formatted errors.
- `id` is a new server-generated stable event ID using the existing
  dependency-free job/record ID convention; it is never accepted from
  Telegram. Update ID alone is insufficient because system events use zero.
- `rawUpdate` is the exact decoded Telegram update JSON object, compacted
  without changing JSON values or key names, when the compact form is at most
  16384 bytes. Validate that it is exactly one object with no trailing token.
  It is `{}` for local system/API events. If an update exceeds the cap or is
  malformed, store `{}`, set `rawOmitted:true`, preserve the typed summary,
  and classify it `raw_omitted`; never byte-truncate invalid JSON.
- The raw object may contain complete inbound text, including denied text.
  This is deliberate operator diagnostics under the v1 trusted-console model.
  The UI labels it `Raw Telegram update (may contain message content)` and
  renders it only through the safe collapsible JSON tree with text nodes. It
  never treats keys or strings as HTML, links, image sources, or CSS.
- labels/usernames are at most 128 code points and control-sanitized.
- IDs and numeric message/update IDs are retained; no first/last name beyond
  the derived bounded chat label is stored.
- For denied events, `textPreview` is always empty. For ignored unsupported,
  edit, bot, malformed, and non-text events it is empty. Accepted/too-long
  events may carry the bounded preview. The raw object remains available under
  the explicit warning above.
- Never store token, secret reference, Bot API URL, headers, assistant answer,
  stack trace, or Bot API response envelope/body outside the individual raw
  update object. A recursive sentinel scan rejects a raw object containing the
  active token before persistence, even though valid Telegram updates do not
  contain it.

Append an event for state-changing API failures with `updateId:0`, no
chat/user/message fields, `kind:"system"`, and the sanitized outcome/code.
Broadcast only after the bounded event has been persisted.

### 7.3 Known-chat metadata

Store only authorized observed chats:

```json
{"chatId":"-10077","type":"supergroup","label":"Engineering","username":"eng","lastSeenTs":1753480123456}
```

Label preference is title, `@username`, joined first/last name, then
`Chat <id>`. Every string uses the §7.2 sanitizer/cap. Upsert by chat ID and
move to newest; trim to 200. After restart with changed config, stale records
may remain persisted but are filtered from status and destination options
unless their ID is still configured.

## 8. Channel-neutral chat ingress, jobs, and correlation

The current chat service accepts a `*ws.Conn`, creates an uncorrelated
`Message`, writes `{"text":...}` into a job, and broadcasts generation frames.
The queue's `InitiatorConn` is WebSocket-specific. This section replaces those
assumptions without creating a second Telegram conversation.

### 8.1 Shared chat types and submission API

In `controller/internal/chat`, define:

```go
type Source struct {
    Channel  string `json:"channel"`            // "web" or "telegram"
    ChatID   string `json:"chatId,omitempty"`
    UserID   string `json:"userId,omitempty"`
    UpdateID int64  `json:"updateId,omitempty"`
}

type Submission struct {
    Text          string
    InitiatorID   string
    CorrelationID string
    Source        Source
}

type SubmitResult struct {
    MessageID     string
    CorrelationID string
    JobID         string
    Duplicate     bool
    Ahead         int
}

func (s *Service) SubmitUserText(ctx context.Context, in Submission) (SubmitResult, error)
```

Validation:

- `Text` trims and must contain 1–4096 runes.
- `InitiatorID` is required and is `ws:<connID>` for web or
  `tg:<chatID>` for Telegram.
- `CorrelationID` is required. Web uses a new random job-style UUID per
  submit. Telegram uses deterministic `telegram:update:<updateID>`.
- source channel is exactly `web` or `telegram`; Telegram requires chat/user
  IDs and positive update ID.

`HandleClientMessage` becomes a thin WebSocket adapter. For `chat`, it builds
the web submission and maps validation/enqueue errors to sender-only
`chat-error`. Shared `SubmitUserText` alone appends/persists/broadcasts the
user message, increments stats, and enqueues. No Telegram package calls
`HandleClientMessage` or constructs fake `ws.Conn` values.

Extend persisted `chat.Message` compatibly:

```go
type Message struct {
    ID            string `json:"id,omitempty"`
    Role          string `json:"role"`
    Text          string `json:"text"`
    Ts            int64  `json:"ts"`
    CorrelationID string `json:"correlationId,omitempty"`
    Source        *Source `json:"source,omitempty"`
}
```

Old entries lacking these fields decode unchanged. User and assistant messages
for one turn share `CorrelationID`; each has its own random `ID`. Telegram user
messages retain source IDs; assistant messages use the same source so delivery
and history inspection can establish the reply route. Web broadcasts keep
existing `chat-message`, `chat-delta`, `chat-done`, and `chat-history` types;
the added fields are backward-compatible. `Source` is a pointer specifically
so legacy/absent metadata is truly omitted by `encoding/json`; new submissions
allocate and copy it, and code treats nil as legacy web/system origin.

### 8.2 Durable job envelope

Replace new uses of `InitiatorConn` with:

```go
type Initiator struct {
    ID                 string `json:"id"`
    Kind               string `json:"kind"` // "web", "telegram", "system"
    ConnectionID       string `json:"connectionId,omitempty"`
    CancelOnDisconnect bool   `json:"cancelOnDisconnect"`
}

type Envelope struct {
    // existing fields...
    Initiator    Initiator      `json:"initiator"`
    CorrelationID string        `json:"correlationId,omitempty"`
    Source       *origin.Source `json:"source,omitempty"`
    InitiatorConn string        `json:"initiatorConn,omitempty"` // decode compatibility only
}
```

To avoid a jobs→chat import cycle, the production implementation places the
wire-compatible `Source` in a new dependency-leaf package
`controller/internal/origin`, and `chat.Source` is a type alias. Both JSON
shapes above remain exact.

New envelopes:

- web chat: initiator `{id:"ws:c17",kind:"web",connectionId:"c17",
  cancelOnDisconnect:true}`;
- Telegram chat: initiator `{id:"tg:-10077",kind:"telegram",
  cancelOnDisconnect:false}`;
- source/correlation copied from the submission.

Envelope `Source` is likewise a pointer so `omitempty` works under the standard
encoder. New chat envelopes allocate it; unrelated and legacy jobs may keep it
nil. Delivery routing requires non-nil source only for registered external
channels.

Backward decode: if `initiator.id` is empty and legacy `initiatorConn` is
non-empty, normalize in memory to the web form above. Re-encoded envelopes use
the new object and omit empty legacy `initiatorConn`. Existing project,
manual-tool, and soak jobs are migrated at their construction sites to
`system` or `web` initiators as appropriate.

`DropInitiator(connID)` is renamed `DropConnection(connID)` and cancels/drops
only envelopes with `initiator.cancelOnDisconnect` and matching
`initiator.connectionId`. Keep a deprecated forwarding method only if needed
to make the code transition atomic; no new code calls it. Telegram jobs
survive absent web clients and Telegram network outages.

Web `chat-stop` cancels/drops only chat jobs whose initiator connection
matches the sending connection. It cannot stop a Telegram-originated job or
another web connection's job. Telegram has no stop command.

### 8.3 Idempotent Telegram submission

For a new update, use deterministic correlation and job IDs derived without a
secret:

```text
correlationId = telegram:update:<updateID>
jobId         = telegram-chat:<updateID>
messageId     = telegram-user:<updateID>
```

The ingress method is serialized in process and uses a durable stage record:

```json
{
  "updateId": 8123,
  "correlationId": "telegram:update:8123",
  "messageId": "telegram-user:8123",
  "jobId": "telegram-chat:8123",
  "stage": "reserved|message|stats|job|complete"
}
```

Before any history/stat/job mutation, one fixed Lua reservation script
atomically creates the absent per-update stage record **and** appends its update
ID to the index. If the key already exists, it returns that record without
adding another index row. The same script evicts oldest complete records beyond
1000 and deletes their keys; it never evicts a partial record (the index may
temporarily exceed 1000 only while repairing corrupt/partial legacy state).
Thus no crash can create an unindexed idempotency record. Only a returned
`complete` record yields the original `SubmitResult` immediately with
`Duplicate:true`.

Advance recoverably in this exact order:

1. For `reserved`, one fixed Lua script verifies that stage, appends the
   deterministic user message to `virtualme:chat`, applies the history cap,
   and writes stage `message` atomically. It returns whether it mutated.
   Broadcast only after a mutating success. Replay at `message` never appends
   or broadcasts again.
2. For `message`, atomically run one small Lua script that checks stage
   `message`, increments `virtualme:chat-stats.queries` once, and writes stage
   `stats`. Replaying the script after success is a no-op.
3. For `stats`, call a job-manager idempotent enqueue method backed by one fixed
   Lua script. It verifies stage `stats`, appends the deterministic envelope to
   the interactive ready list, and writes stage `job` atomically, returning
   queue-ahead count and whether it mutated. The manager broadcasts queue state
   only after success. No ready/inflight/done retention lookup is needed and a
   crash cannot enqueue without durable stage evidence.
4. For `job`, write stage `complete` including the final `SubmitResult`, then
   return success. Offset advancement in §6 occurs only after this return.

Every script reply/stage write is checked. A storage failure stops processing
and preserves the latest recoverable stage. The four scripts (reserve/index,
history/stage, stats/stage, enqueue/stage) use the `Eval` primitive introduced
by spec 032, are exact constants covered by fake-RESP and real-Valkey tests,
and require no `KEYS`, queue scans, transaction, or dependency.

Completion records are the durable evidence; queue-list retention is never used
for deduplication. Once a complete record is evicted after the newest 1000 and
the persisted update offset has advanced, exact-once protection for that old
update intentionally expires. A malformed/lost offset is already surfaced as
`offset_reset`; Telegram normally does not replay arbitrarily old updates.
Tests MUST inject failures before/after every script and completion write. The
invariant within the retained idempotency window is exactly one query-stat
increment, one history user message, and one chat envelope per update, with
partial work completed rather than discarded.

### 8.4 Delivery router

Create a small channel-neutral delivery registry in `internal/chat`:

```go
type Delivery struct {
    CorrelationID string
    JobID         string
    Source        origin.Source
    Text          string
    Err           error
    Stopped       bool
}

type DeliveryHandler func(context.Context, Delivery) error
func (s *Service) RegisterDelivery(channel string, handler DeliveryHandler)
```

Register exactly one handler for `telegram`; duplicate registration panics at
startup. Web needs no handler because existing frames are broadcast web-wide.
The chat executor carries the current envelope through generation and calls
the handler exactly once after the final assistant message/error is known.
Routing uses `env.Source` and `env.CorrelationID`, never mutable service-global
“current chat” state, last update, latest destination, queue position, or
`InitiatorID` parsing.

All assistant token deltas and agent progress continue to broadcast to web
clients. Telegram receives no token deltas, tool traces, screenshots, or
agent-step frames. While a Telegram-originated chat job is running, the
handler/service may send `sendChatAction(chat_id, "typing")` immediately and
every 4 seconds until completion; stop the ticker before final delivery.
Typing failures are event-log-only and do not fail generation.

On success, append and broadcast the assistant message and `chat-done` as
today, then deliver that exact final text to `env.Source.ChatID`. A delivery
failure does not retry the LLM job and does not append a second assistant
message; it records `reply_send_failed`, emits the notification in §11 when
appropriate, and leaves queue result summary explicit.

On generation error, web receives its existing bounded `chat-error`/terminal
state. For an authorized Telegram origin, send one friendly message selected
locally:

- cancellation: `That request was cancelled before it completed.`
- model/agent failure: `Virtual Me could not complete that request. Please try again.`
- queue/storage rejection before acceptance:
  `Virtual Me could not queue that request. Please try again shortly.`

Never send Go errors, remote bodies, paths, prompts, tool output, or token
details to Telegram.

## 9. Telegram final-message chunking

Telegram `sendMessage` accepts bounded text. Implement
`ChunkText(text string) []string` with these exact properties:

1. Empty input returns no chunks.
2. Every chunk is non-empty and at most 4096 UTF-16 code units, which is the
   conservative Telegram character accounting. No UTF-8 byte sequence,
   Unicode code point, or UTF-16 surrogate pair is split.
3. Concatenating chunks reproduces the input byte-for-byte; no whitespace is
   added, removed, or normalized.
4. At each limit choose the latest boundary in order: `\n\n`, then `\n`, then
   Unicode whitespace, provided the boundary occurs in the final 25% of the
   candidate. Include boundary characters in the preceding chunk. Otherwise
   split at the last complete rune within the limit.
5. Send chunks sequentially to the same source chat. Stop on the first send
   failure; do not send later chunks. Log an event with sent/total chunk
   counts, not answer text.
6. No parse mode is used, so Markdown-like model text is delivered literally
   and cannot create entity-length surprises.

Tests include ASCII boundaries, exactly 4096, 4097, emoji outside the BMP,
combining marks, CJK, whitespace preference, a single long token, and
lossless concatenation.

## 10. WebSocket protocol and `/telegram` console

### 10.1 Exact WebSocket frames

Client → server:

| Type | Exact payload | Response |
|---|---|---|
| `telegram-status-req` | `{"type":"telegram-status-req"}` | sender-only current `telegram-status` |
| `telegram-events-req` | `{"type":"telegram-events-req"}` | sender-only current `telegram-events` |
| `telegram-event-detail-req` | `{"type":"telegram-event-detail-req","requestId":"<client UUID>","id":"<event ID>"}` | sender-only `telegram-event-detail` |
| `telegram-test-send` | `{"type":"telegram-test-send","id":"<client UUID>","chatId":"<decimal string>","text":"<1..4096 runes>"}` | sender-only `telegram-command-result`; actual Bot API send only if enabled/connected and destination currently authorized |

Server → client:

| Type | Delivery | Exact payload |
|---|---|---|
| `telegram-status` | on every WS connect; broadcast on status change; sender-only request reply | `{"type":"telegram-status","status":<§5.2 object>}` |
| `telegram-events` | on every WS connect and sender-only request reply | `{"type":"telegram-events","events":[<newest 50 §7.2 records without rawUpdate>],"eventCount":N}`; records are newest-first and retain `rawOmitted` |
| `telegram-event` | broadcast after durable event append | `{"type":"telegram-event","event":<complete §7.2 object>}`; bounded below 24 KiB |
| `telegram-event-detail` | initiating WS connection only | `{"type":"telegram-event-detail","requestId":"<same>","event":<complete §7.2 object>,"error":""}` or, when missing/evicted, `{"type":"telegram-event-detail","requestId":"<same>","event":null,"error":"Telegram event is no longer retained"}` |
| `telegram-command-result` | initiating WS connection only | `{"type":"telegram-command-result","id":"<same>","ok":true,"error":""}` or `{"type":"telegram-command-result","id":"<same>","ok":false,"error":"<fixed bounded UI error>"}` |

`telegram-test-send` rejects unknown properties, malformed/noncanonical IDs,
IDs not currently in configured `allowedChatIds`, empty/over-limit text,
duplicate in-flight request IDs, and non-connected state. Fixed UI errors are
`Telegram is not connected`, `Destination is not authorized`,
`Message must be 1–4096 characters`, `Request is already running`, and
`Telegram could not send the test message`. It sends text through the same
chunking path as assistant replies. Results are never broadcast.

The on-connect hook sends `telegram-status` and the bounded `telegram-events`
summary after chat history/stats and before queue state. Selecting an event
sends `telegram-event-detail-req`; missing/evicted IDs return the sender-only
detail error above. The SPA may still issue explicit snapshot requests on
reconnect; responses are idempotent.

### 10.2 Route, navigation, and page structure

- Add router entry `["/telegram", ["telegram", "Telegram"]]`.
- Add top-level sidebar navigation after Chat and before Speech, and a Home
  quick link. Use a committed sprite icon selected from the existing pinned
  Lucide source through `controller/tools/fetch-assets.sh`; do not fetch an
  unofficial Telegram brand asset at runtime.
- Add `<section data-page="telegram">` and
  `controller/web/static/js/telegram.js`, initialized/routed/frame-dispatched
  through `app.js`.
- The page has four regions:
  1. **Enabled-state card:** config-enabled versus effective state, fixed
     detail, bot display name, `@username`, numeric bot ID, last successful
     poll, offset, and retry state. It includes `Configure Telegram`, a
     same-origin `data-nav` link to
     `/config#integrations-telegram`. When disabled/unconfigured, this
     card is the primary empty state.
  2. **Privacy warning:** the four points in §1.3 in concise operator copy.
  3. **Integration test:** destination `<select>` from status destinations,
     a textarea capped at 4096 runes prefilled with
     `Virtual Me Telegram integration test.`, Send button, and result banner.
     Disable controls unless connected and at least one destination exists.
     The UI does not permit arbitrary destination entry.
  4. **Event log:** shown only when config `enabled` is true. Newest first in
     the UI. Columns
     are time, outcome, chat, user, and bounded preview/detail. Render with
     DOM APIs and `textContent`; no `innerHTML`. Incremental events are
     prepended and the summary DOM is capped to 50. Selecting an event requests
     its complete record and opens an adjacent desktop detail / mobile
     slide-over containing metadata plus a collapsed
     `Raw Telegram update (may contain message content)` JSON tree. Use the
     existing safe tree renderer; raw keys/strings are never links or markup.
     `rawOmitted:true` renders `Raw update exceeded the 16 KiB retention cap.`

No literal token field, token-presence length, secret reference identifier,
Bot API URL, or unredacted error appears on this page. Raw update JSON appears
only inside the explicitly selected event-detail tree and warning from §10.2;
it never appears in status, destination controls, summary rows, or page source
before a detail response. Config enable/edit always navigates to `/config`;
`/telegram` sends no config-write frame.

### 10.3 Accessibility and responsive behavior

Use existing card/form/result styles where practical. Every status color has
text, labels bind to controls, result messages receive focus after an explicit
test-send completion but are not live regions, table/list content remains
usable at 375 px, and long IDs/previews wrap without widening the viewport.
Respect reduced motion. Do not add a polling animation.

## 11. System notifications

Use spec 032's system-notification API. All notification bodies are local
fixed strings and contain no bot token/reference, user message, chat/user ID,
remote description, or Bot API URL.

Deduplication below is owned by `telegram.Service`, not by spec 032. The
service persists episode-active flags and last-emitted timestamps in
`virtualme:telegram:notification-state` so controller restart cannot produce a
flood. “Resolve” means clearing that Telegram-owned active suppression flag;
the historical notification remains immutable in spec 032 and retains its
global read state. Every create uses spec 032's ordinary `generic` renderer,
sender `telegram`, and the table's severity as its notification type.

| Event | Severity | Title/body | Deduplication and rate rule |
|---|---|---|---|
| New generation's `getMe` succeeds | info | `Telegram connected` / `The configured bot is authenticated and long polling is active.` | Emit once per controller process only on transition from a non-connected state; do not emit for every successful poll or transient recovery |
| Any call returns 401 or `getMe` says not a bot | error | `Telegram authentication failed` / `Update the Telegram bot-token secret in Config.` | Dedup key `telegram:auth-failed`; once per active secret revision; a new revision clears eligibility, successful authentication resolves the active notification |
| Poll failures reach 5 consecutive failures or 60 seconds since last success, whichever occurs first | warning | `Telegram polling suspended` / `Telegram updates are temporarily unavailable; automatic retries continue.` | Dedup key `telegram:polling-suspended`; one active notification; no repeats while active; 15-minute minimum between newly created occurrences |
| Poll returns 409 | warning | same title / `Another consumer is polling this bot. Stop the other poller; automatic retries continue.` | Same active dedup key, immediate regardless of threshold; conflict body wins while conflict remains |
| First successful poll after suspended | info | `Telegram polling recovered` / `Telegram updates are available again.` | Resolve suspended notification, emit once for that suspended episode; no generic recovery notification for failures below threshold |
| Final answer/test/command send fails due to sustained Telegram outage | warning | `Telegram delivery failed` / `A Telegram message could not be delivered. Check the Telegram integration page.` | Dedup key `telegram:delivery-failed`; at most once per 15 minutes; success resolves active notification |

`auth_failed` does not also create polling-suspended or delivery-failed noise.
Controller shutdown/canceled requests generate no notification. Config disable
resolves active Telegram lifecycle notifications without emitting recovery.

## 12. Exact implementation file map and main wiring

### 12.1 New files

| File | Responsibility |
|---|---|
| `controller/internal/origin/origin.go` | dependency-leaf source metadata type shared by jobs/chat |
| `controller/internal/telegram/types.go` | Bot API and status/event DTOs |
| `controller/internal/telegram/client.go` | four-method stdlib HTTP JSON client and sanitized errors |
| `controller/internal/telegram/chunk.go` | lossless 4096-unit chunker |
| `controller/internal/telegram/service.go` | state machine, secret/config generations, polling, authorization, offsets, known chats, events, commands, delivery handler |
| `controller/internal/telegram/client_test.go` | fake Bot API request/response contract |
| `controller/internal/telegram/chunk_test.go` | Unicode chunking contract |
| `controller/internal/telegram/service_test.go` | auth, offset, dedup, retries, refresh, commands, events, notifications |
| `controller/web/static/js/telegram.js` | `/telegram` state, test-send, and bounded event rendering |
| `test/telegram-ui.test.js` | static/pure UI contract |
| `test/telegram-stub.mjs` | local Bot API HTTP stub for container e2e |
| `test/telegram-probe.mjs` | WS/config/chat Telegram e2e driver |

### 12.2 Modified implementation/test files

| File | Required change |
|---|---|
| spec 031 config schema/metadata files | add exact `integrations.telegram` object and secret-reference validation/refresh subscription |
| spec 032 notification package/tests | consume existing API only; add no Telegram-specific logic there unless its typed category registry requires registration |
| `controller/internal/chat/chat.go`, `chat_test.go` | channel-neutral submission, source/correlation fields, idempotency repair, delivery registry, sender-scoped stop |
| `controller/internal/jobs/manager.go`, `manager_test.go` | initiator/source/correlation envelope, legacy normalization, connection-only cancellation, bounded ID lookup |
| `controller/internal/valkey/valkey.go` and tests | reuse spec 032 `Eval` for the four exact ingress scripts; no `KEYS`, `MULTI`, arbitrary script input, or new dependency |
| `controller/internal/agent/agent.go` and tests | return/capture final answer through chat execution while preserving web broadcasts; no Telegram import |
| `controller/cmd/controller/main.go`, `main_test.go` | singleton construction order, config/secret/notification injection, delivery registration, WS dispatch/on-connect, lifecycle start |
| `src/commands/start.js`, `test/cli.test.js` | guarded e2e-only Bot API base/test-mode forwarding and one deduplicated `vmhost` host-gateway mapping |
| `controller/web/static/index.html` | nav, Home quick link, Telegram section |
| `controller/web/static/js/router.js` | `/telegram` route |
| `controller/web/static/js/app.js` | initialize page, connection state, frame dispatch, route entry |
| `controller/web/static/css/app.css` | responsive Telegram cards/form/event log using theme tokens |
| `controller/tools/fetch-assets.sh` | pin/register the chosen existing-source icon if not already present |
| `test/e2e.sh` | launch local stub, test-mode API override, config/secret fixture, run probe, assert no external access |
| `test/smoke.sh` | no new directory; update only if its persistence key assertions enumerate Valkey owners |
| `specs/007-persistence-locality.md` | append §1 amendment rows from §12.3 during implementation reconciliation |

Do not create an s6 service, Docker layer, Telegram sidecar, webhook endpoint,
new volume directory, or health probe. Integration health belongs in
`telegram-status`; the base controller `/healthz` remains local-service health.

### 12.3 Persistence-map amendment required at execution

Append, do not rewrite historical spec 007 text:

| State | Path | Owner / mechanism | Introduced |
|---|---|---|---|
| Telegram next-update offset, bounded redacted event log, and authorized known-chat labels (`virtualme:telegram:*`) | `$VM_DATA_DIR/valkey/` (AOF) | `internal/telegram` via shared Valkey client | 033 |
| Telegram ingress idempotency records (`virtualme:chat:ingress:telegram:*`, bounded 1000) | `$VM_DATA_DIR/valkey/` (AOF) | channel-neutral `internal/chat` ingress | 033 |

No top-level `$VM_DATA_DIR` known-set change is required.

### 12.4 Main wiring order

`main.go` MUST wire in this order:

1. load/validate spec 031 config and construct secret resolver;
2. construct hub, Valkey clients, activity, and spec 032 notifications;
3. construct chat and job manager, register the chat executor;
4. construct Telegram with config snapshot, resolver subscription, API client
   factory, Valkey, notifications, job-state reader, and chat submitter;
5. register `chat.RegisterDelivery("telegram", telegramService.Deliver)`;
6. register Telegram WS handler before fallback chat handling;
7. add Telegram status/events in `Hub.SetOnConnect`;
8. preserve `hub.SetOnDisconnect(jobManager.DropConnection)`;
9. start job manager, then Telegram under the same root context.

History loading begins before Telegram polling. The service waits for
`chat.HistoryReady()` before classifying a normal authorized text update so a
fast startup poll cannot append ahead of restored history. Commands/test sends
do not require history readiness.

## 13. Tests and verification

### 13.1 Mandatory RED-first sequence

For each numbered group, commit or otherwise preserve the failing test diff
before production edits. Record the failing command in implementation notes or
PR description.

1. **T1 client/config tests fail:** typed request encoding, response/errors,
   schema validation, and secret-only storage.
2. **T1 service tests fail:** getMe/startup, auth allowlist, offset, events,
   known chats, retry/429/409/401, notifications, and secret refresh.
3. **T1 UI/e2e contract tests fail:** exact WS types, route, safe DOM, local
   stub and test send.
4. **T2 chat/jobs tests fail:** submission/source/correlation, backward
   envelope/history decoding, disconnect semantics, idempotency repair, exact
   delivery routing, final-only behavior.
5. **T2 chunk/command/e2e tests fail:** Unicode chunks, commands, shared
   history, correlated Telegram reply, restart resume.

### 13.2 Hermetic Go tests

Use `httptest.Server`, fake clocks/jitter, fake secret resolver/revision
stream, fake notification sink, and the existing fake RESP pattern.

**Client:**

- each of four methods uses POST, exact method path, JSON content type, typed
  body, context, and expected fields; `getUpdates` includes offset/timeout/
  exact allowed updates;
- successful envelopes, Telegram errors on 200/non-2xx, malformed/trailing/
  oversized responses, timeout, cancellation, 401/409/429 Retry-After;
- sanitized errors contain no fake token, URL, response description/body, or
  request text.

**Configuration/secrets:**

- defaults and every boundary; canonical string IDs, duplicate rejection,
  mandatory token/chat allowlist when enabled, unknown property rejection;
- persisted config contains only `${env:...}` or `${file:...}` and no fake
  literal token;
- disabled performs no resolution; enable resolves once; revision cancels
  current poll, authenticates replacement, and never reuses old token;
- failed refresh enters exact state without exposing either credential.

**Authorization/events:**

- full matrix proving chat AND optional-user semantics, including private,
  group, configured user in wrong chat, user omitted, allowed-users empty,
  bot sender, malformed, and unauthorized silence;
- authorization occurs before fake submitter/send/known-chat side effects;
- text/detail/labels sanitize and truncate exactly, denied preview empty,
  event trim uses configured cap, exact raw-object retention at/below 16 KiB,
  oversized/malformed omission, sender-only detail lookup/eviction, safe raw
  content from denied updates, and no serialized fake secret;
- known-chat upsert/order/cap/filter and configured-unobserved destination.

**Polling and persistence:**

- offset starts 0, increments per update in sorted order, advances for every
  ignored/denied kind, resumes exact next offset after service reconstruction;
- duplicate/lower update IDs ignored;
- persistence failure stops response processing and replay is idempotent;
- ordinary delays and fixed jitter values, reset on empty success,
  Retry-After exact/clamped, cancellation interrupts poll/sleep;
- 409 immediate suspended/retry, 401 no retry until revision, one poller under
  rapid secret revisions; config changes take effect only after restart;
- notification thresholds, dedup/cooldown/resolve, shutdown silence.

**Chat/jobs/delivery:**

- web adapter and Telegram submitter yield one shared history and stats path;
- old history/envelope JSON decodes; normalized legacy web jobs still cancel;
- web disconnect/stop cancels only matching web jobs; Telegram survives;
- source/correlation survive ready→inflight→done and retry;
- two queued Telegram chats receive only their own final answer despite
  sequential execution; web sees both user/final messages;
- no Telegram delta/tool/agent frames; typing starts/repeats/stops with fake
  clock; final delivery exactly once;
- crash windows around reservation, history, stats script, enqueue, and every
  stage write resume to complete with exactly one stat increment and at most
  one message/job; index eviction deletes only complete old keys;
- friendly errors are bounded and contain no internal fake error.

**Chunking/commands:**

- every §9 case and property;
- exact command recognition using UTF-16 Telegram entity offsets, bot suffix,
  case, arguments rejection, unknown `/clear` and `/stop`, and no history/job
  mutation;
- test sends apply current authorization and same chunking.

### 13.3 Node UI tests

`test/telegram-ui.test.js` verifies:

- exact route/nav/Home link and page regions;
- `app.js` dispatches every server frame in §10.1 and connection state;
- config link is `/config#integrations-telegram`;
- no token input, config-write frame, external URL, or `innerHTML`; raw update
  JSON uses only the safe tree renderer and cannot create links/markup;
- destination is server-provided select-only, controls disable correctly,
  request IDs correlate sender results, event DOM stays capped/newest-first;
- enabled-only log, privacy copy, safe `textContent`, mobile CSS selectors,
  and all exact WS request type strings.

Pure exported helpers should test status labels, event ordering, and rune
count validation without a browser DOM. Keep Node tests zero-dependency.

### 13.4 Offline container e2e

`test/telegram-stub.mjs` uses Node built-ins and binds host loopback. It
implements only the four Bot API routes, accepts the fake token path, records
typed request bodies to a temp file, queues scripted updates, can return
401/409/429, and never contacts Telegram.

`test/e2e.sh` supplies test mode/base URL and a spec 031 config fixture whose
`botToken` is a `${file:/absolute/path}` reference to an ephemeral mode-`0600`
fake-token file. The probe MUST prove:

1. `/telegram` SPA route loads and no token appears in returned assets/status;
2. on-connect status/events arrive; `getMe` identity appears;
3. authorized observed and configured-unobserved destinations are present;
4. test-send reaches the selected chat and sender-only correlated result is
   returned;
5. unauthorized chat and wrong-user updates create no shared chat message or
   Bot API reply;
6. authorized normal text appears in `chat-history`/`chat-message`, produces
   typing, receives the final answer in the originating stub chat, and the web
   receives the same final assistant message;
7. two authorized chat IDs queued in order receive their own correlated
   answers and never each other's;
8. `/help`, `/status`, unknown `/clear`, edit, bot-authored, and non-text
   classification matches §6.7;
9. restart with the same data directory sends persisted next offset, does not
   duplicate history/reply, and preserves bounded events/known chats;
10. scripted 429 honors Retry-After under an e2e-short fake setting exposed
    only by the stub, 409 surfaces suspended, and recovery surfaces status;
11. captured controller logs, WS frames, Valkey event values, and generated
    config contain no fake token.

The stub asserts every request stayed local. E2E teardown removes temporary
secret/config/stub artifacts. No soak flow is added; `./cli.sh soak` merely
runs the normal e2e suite and never requires Telegram credentials.

### 13.5 Final gates

Run in order:

```text
cd controller && go test ./... -count=1
node --test test/telegram-ui.test.js test/chat-ui.test.js test/jobs-ui.test.js
npm run check
bash test/smoke.sh
bash test/e2e.sh
```

The canonical gate must pass with networking disabled.

## 14. Documentation and reconciliation

After all implementation and tests pass, run `/master-update`. It must
reconcile at least:

- `README.md`: optional Telegram feature, `/telegram` route, config example
  containing only a secret reference, allowlist AND semantics, external-cloud
  privacy warning, BotFather setup link, no webhook/open port, and no claim
  that all data remains local when Telegram is enabled;
- `AGENTS.md`: `internal/telegram` and `internal/origin`, channel-neutral chat
  ingress/delivery, exact `telegram-*` WS summary, persistent keys, and spec
  table row 033;
- develop skill: package/file map, config/secret dependency, tests/stub,
  long-poll lifecycle, source/correlation rules, persistence amendment, and
  no-SDK/no-webhook rule;
- operate skill: create token with BotFather, store through spec 031 secret
  UI, obtain/configure chat and optional user IDs, understand group privacy
  mode, inspect `/telegram`, resolve 401/409/429, rotate secret, disable, and
  verify offset/restart behavior without printing the token;
- spec 007 amendment in §12.3 and any spec 031/032 registries/indexes required
  by their own procedures;
- docs screenshots only if `/master-update`'s current procedure includes the
  new top-level route. Screenshots must use the local stub and visibly fake
  identity/data, never a real token or account.

Re-run `npm run check` after reconciliation. Documentation wording must call
this a Telegram Bot API integration over HTTPS long polling, not an official
OpenAPI client and not a webhook.

## 15. Acceptance checklist

- [ ] Specs 031 and 032 are executed before implementation begins.
- [ ] T1 and T2 each have demonstrably failing tests before their production
      changes; the retained tests cover every §13 group.
- [ ] `controller/go.mod` has zero `require` lines; no Telegram SDK/vendor,
      npm runtime dependency, webhook, listener, exposed port, sidecar, s6
      service, or Docker layer was added.
- [ ] Production Bot API base is fixed to `https://api.telegram.org`; the
      local override is rejected unless exact test mode and loopback/vmhost
      validation pass.
- [ ] Config stores only a secret reference; no literal token appears in
      config, DOM, WS, Valkey, logs, notifications, errors, docs, fixtures, or
      screenshots.
- [ ] Enabled config requires at least one exact chat ID. Authorization is
      exact chat allowlist AND optional user allowlist and runs before every
      ingress side effect.
- [ ] Exactly one poller calls typed stdlib-only `getMe`, `getUpdates`,
      `sendMessage`, and `sendChatAction`; startup/enable/secret refresh calls
      `getMe`.
- [ ] Long polling uses persisted next offset, configured 1–50 second timeout,
      cancellation, deterministic-injectable exponential jitter, exact
      Retry-After, 409 suspended state, and 401 no-retry-until-refresh.
- [ ] Restart/replay resumes every partial stage and creates exactly one query
      statistic plus at most one history message and one job per Telegram
      update within the retained 1000-update idempotency window, including
      every tested persistence crash window.
- [ ] Event and known-chat logs are bounded and contain no remote response,
      Bot API URL, or secret; summaries redact denied text while selected raw
      updates safely expose exact inbound content up to 16 KiB with an explicit
      operator warning and omission state.
- [ ] `/telegram` is top-level, shows effective state/identity/privacy,
      provides authorized destination test-send, links enable/edit to
      `/config`, and never renders or accepts a token.
- [ ] Exact `telegram-status-req`, `telegram-events-req`,
      `telegram-event-detail-req`, `telegram-test-send`, `telegram-status`,
      `telegram-events`, `telegram-event`, `telegram-event-detail`, and
      `telegram-command-result` contracts pass sender, broadcast, detail, and
      on-connect tests.
- [ ] Shared chat history receives authorized Telegram text and appears in the
      web UI; source, durable initiator, correlation, and job IDs persist.
- [ ] Two Telegram source chats can queue sequentially and each receives only
      its own final answer. Telegram receives typing actions and final chunks,
      never token deltas, tool traces, or agent frames.
- [ ] Final chunks are lossless, rune-safe, and at most 4096 UTF-16 units.
- [ ] `/start`, `/help`, `/status`, ordinary text, unknown commands, edits,
      bot messages, non-text updates, and over-limit text have exact specified
      behavior; `/clear` and `/stop` cannot mutate/cancel.
- [ ] Web disconnect and `chat-stop` still cancel/drop only that web
      connection's jobs; Telegram jobs have durable `tg:<chatID>` initiators
      and no disconnect cancellation.
- [ ] Connected, auth-failed, suspended, recovered, conflict, and delivery
      notifications obey exact dedupe/rate/resolve rules without floods.
- [ ] `cd controller && go test ./... -count=1`, targeted Node tests,
      `npm run check`, `bash test/smoke.sh`, and offline
      `bash test/e2e.sh` all pass.
- [ ] No real Telegram credential or external network is needed by check,
      smoke, e2e, or soak.
- [ ] Spec 007 persistence map is amended, and `/master-update` reconciles
      README, AGENTS, develop/operate skills, specs, and screenshots as
      applicable.

## Amendments

None.
