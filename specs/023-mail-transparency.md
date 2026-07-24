# Spec 023: Mail Queue Transparency

| | |
|---|---|
| Status | Draft |
| Depends on | `specs/010-outbound-mail.md` (dma queue, controller mail service, Mail tab) |
| Produces | Per-message queue insight on the Mail tab: what a queued message is (recipient, subject, size, contents preview), why it is still queued (last delivery error), when it will be retried (countdown derived from the flush cadence), and a "what happened" timeline for the last send; clarified compose affordances flagged by the live UI inspection |
| Followed by | Future specs |

## 0. Executor instructions

- Constitution binds. dma stays the delivery owner (spec 010 §3); this spec only **reads** its spool and log output — never write to spool files, never re-implement retry.
- The live inspection (2026-07-23) confirmed the pain: the queue table showed `ID 4ad638cb… / 8368 B / 30931 s` directly above the text "No messages submitted in this controller session" with no way to learn what the message was, why it sat 8.6 hours, or when anything would happen next. That exact confusion is what this spec removes.
- Stop-on-red; finish with §7 Acceptance.

## 1. dma spool format (ground truth to verify first)

The spool at `$VM_DATA_DIR/mail/spool/` (symlinked from `/var/spool/dma`) holds pairs per message: a queue file `Q<id>` (envelope metadata: version line, sender, per-recipient lines with delivery state) and a message file `M<id>` (full RFC 5322 message as submitted). **First implementation step**: submit a test message with an unroutable recipient in a dev container and read both files; adjust the parser below to the observed dma (Debian stable) format — the parser must be defensive (unknown lines skipped, never fatal) and covered by fixture tests using the captured real files (committed under `controller/internal/mail/testdata/`).

## 2. Controller: enriched queue model

Extend `controller/internal/mail` (`outbound.go` `Queue()` currently returns `{id,size,ageSec}` from file stats):

1. `type QueueMessage struct` gains:

```json
{"id":"…","size":8368,"ageSec":30931,
 "to":"user@example.com","subject":"…","from":"…",
 "submittedTs":1690000000000,
 "preview":"first 500 chars of the text/plain part, headers stripped",
 "lastError":"connect to mx.example.com: connection refused (from svc-mailq log)",
 "nextRetrySec":42}
```

2. **Envelope + headers**: parse `Q<id>` for sender/recipient(s); parse `M<id>` headers (`net/mail.ReadMessage`) for `Subject`, `From`, `Date` (→ `submittedTs`; fall back to file mtime). `preview`: walk the MIME structure with the existing stdlib MIME code from `mime.go`; take the first `text/plain` part, decode quoted-printable/base64 as needed, cap 500 chars. Multi-recipient messages: `to` is the first + `(+N more)`.
3. **Why is it queued / last error**: dma writes delivery attempts to its log (syslog-style; in this container dma's stderr flows to the s6 `svc-mailq` log stream). Two sources, in preference order:
   - If the `Q` file carries a per-recipient error/status field (verify in §1), use it.
   - Else maintain a controller-side ring buffer: `svc-mailq/run` is modified to pipe `dma -q` output through `tee -a $VM_DATA_DIR/mail/flush.log` (truncate the log to the last 500 lines each cycle with `tail`). The controller parses `flush.log` for lines mentioning the message id and extracts the newest error line. If neither yields anything: `lastError:"no delivery attempt recorded yet"`.
4. **When will it be retried**: dma retries whenever `svc-mailq` runs `dma -q` (every `VM_MAIL_FLUSH_SEC`, default 60). The controller computes `nextRetrySec` = seconds until the next flush tick: `svc-mailq/run` writes `date +%s` to `$VM_DATA_DIR/mail/last-flush` after each cycle; `nextRetrySec = max(0, lastFlush + VM_MAIL_FLUSH_SEC − now)`. (dma also has internal backoff for messages that failed very recently — state honestly in the UI: "next flush attempt", not "guaranteed delivery attempt".)
5. **Session timeline**: keep an in-memory (not persisted) list of the last 20 mail lifecycle events in the mail `Service`: `submitted`, `flush ran (N queued before/after)`, `left queue (delivered or bounced)` — the leave event is inferred by diffing queue snapshots between flushes. Exposed as `timeline` in `mail-status`.
6. `mail-status` frame gains `queue:[QueueMessage…]`, `flushEverySec`, `nextRetrySec` (global), `timeline:[{ts,text}…]`. Recompute on `mail-status-req`, after each `mail-send`, and on a 30 s ticker while any client is connected (queue state changes from `svc-mailq`, not only from user actions — this fixes the stale "No messages submitted in this controller session" contradiction: that line now only describes `lastResult` and is relabelled, §3.4).

## 3. Mail tab UI

`controller/web/static/js/mail.js` + `index.html` mail section:

1. **Queue rows become expandable**: replace the 3-column table with a list of disclosure rows (`<details class="mail-msg">`): summary line = status dot (amber while queued), `to`, `subject` (or `(no subject)`), size, age as humanized text (`8.6 h`, not `30931 s`), and `retry in <n>s` counting down client-side from `nextRetrySec`. Expanded body: From / submitted local time / full recipient list, **Last error** (mono, `--err` tint; or the "no delivery attempt recorded yet" text), and **Contents** — the `preview` in a scrollable `<pre>` (this answers "can I see its contents": yes, the text part; attachments are listed by MIME type/size only).
2. **Empty state**: `Queue empty — messages deliver on submit or wait here between flush runs (every <flushEverySec>s).`
3. **What happened — timeline card**: under the queue, `<h2>Activity</h2>` list of `timeline` entries (local short time + text). Newest first, 20 max.
4. **Copy fixes** (from the live inspection):
   - `#mail-last`'s idle text changes to `No sends yet in this controller session.` and moves visually under a small `Last send` heading — it no longer sits ambiguously below the queue table contradicting it.
   - The deliverability aside must never truncate: remove any fixed height/overflow on `.mail-guidance` in `app.css`; let it wrap fully (the live build cut it mid-word at "reliab").
   - `Send Gmail test` button gains a `title` and sub-caption: `Prefills a deliverability test addressed to a Gmail inbox — Gmail's headers show SPF/DKIM verdicts.`
   - `Embed test image` checkbox gets a help line: `Attaches a small inline image to exercise multipart/CID composition.` and is only enabled when composing (it was observed read-only-looking).

## 4. WS surface

No new message types: everything rides the existing `mail-status` (extended shape, §2.6) and `mail-status-req`. The SPA must tolerate the old shape (missing new fields → hide the new UI parts) so the page never breaks against a mid-upgrade controller.

## 5. Tests

- Hermetic Go: spool parser against committed fixtures (`Q`/`M` pairs captured per §1, including a multipart message with attachment and a quoted-printable body); `nextRetrySec` math; timeline diffing (fake queue snapshots); preview caps and header fallbacks.
- e2e: extend `mail-probe.mjs` — submit via the existing SMTP-sink path, then assert `mail-status.queue` entries carry `to`/`subject`/`preview`, and after the sink accepts delivery the timeline contains a `left queue` event.
- Manual: the §1 unroutable-recipient exercise — queue row shows a real `lastError` and a live countdown.

## 6. Docs

`/master-update` — operate skill (reading the queue: what age/retry/last-error mean; `flush.log` location; the honest "next flush" phrasing), develop skill (spool parser fixtures note, `svc-mailq` tee change), README.

## 7. Acceptance checklist

- [ ] `npm run check` green.
- [ ] A queued unroutable message shows: recipient, subject, submitted time, contents preview, a non-empty last error, and a retry countdown that resets each flush.
- [ ] Age renders humanized (`8.6 h`), never raw seconds.
- [ ] `Last send` and the queue are visually and semantically separate; the contradiction from the live inspection cannot reproduce.
- [ ] Deliverability aside wraps fully at 1600 px and 375 px.
- [ ] Queue updates within ~30 s of `svc-mailq` delivering a message with no user interaction.
- [ ] e2e mail probe passes; spool-parser fixtures cover multipart + QP.
