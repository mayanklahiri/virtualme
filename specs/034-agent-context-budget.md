# Spec 034: Agent Context Budgeting

| | |
|---|---|
| Status | Accepted (2026-07-25) |
| Depends on | `specs/008-browser-agent.md`, `specs/022-system-prompt.md`, `specs/029-readpage-goldens.md`, `specs/030-docs-site.md` |
| Produces | Preflight prompt budgeting, context-scaled observations, adaptive completion limits, stale-observation supersession, and graduated overflow recovery |
| Followed by | Future specs |

## 0. Executor instructions

- Constitution binds. Runtime code remains Go-stdlib-only and the canonical
  gate remains deterministic and offline.
- This spec amends spec 029's context limits and spec 022's agent prompt.
  Their executed text remains historical; this spec is authoritative where
  the values or wording conflict.
- Complete with §6 Acceptance and `/master-update`.

## 1. Problem and evidence

The default llama.cpp context is 32768 tokens, but each request reserves 8192
completion tokens while admitting a 64 KiB `read_page` observation, 16 KiB of
chat history, four 8 KiB tool rounds, the system prompt, and all tool schemas.
Those independent byte caps can exceed the context in one observation cycle.

The controller has no preflight token estimate. It learns `prompt_n` only
after successful inference. On llama.cpp `exceed_context_size_error`, it drops
old history but retains the latest oversized observation and retries only
once. That retry commonly repeats the same failure.

Screenshots are not accumulated: existing compaction retains only the latest
observation, and Gemma vision represents an image with a bounded token
sequence. Text observations and retained ordinary tool output are the dominant
unbounded combination.

## 2. Context budget

Before every model request, estimate prompt tokens from message and tool-schema
bytes using a conservative initial three bytes per token. Count each image as
256 tokens rather than charging its base64 transport size. Calibrate the
bytes-per-token ratio from successful llama.cpp `prompt_n` measurements,
bounded to a conservative range.

Reserve 512 tokens for chat-template and estimator error and at least 1024
tokens for completion. If the estimated prompt does not fit, degrade it in
this order:

1. retain no more than the four newest tool rounds;
2. reduce older ordinary tool results to their first 512 bytes;
3. remove additional oldest tool rounds;
4. halve the newest observation repeatedly as needed;
5. discard old chat turns while retaining system and current user messages.

Completion `max_tokens` is adaptive:

```
clamp(context - estimated_prompt - 512, 1024, context / 4)
```

The request must not be sent until the estimated prompt, reserve, and selected
completion budget fit the configured context.

## 3. Observation and history policy

At the default 32768-token context, `read_page` YAML and the model-facing
observation text are capped at 24576 bytes. The cap scales as one quarter of
context multiplied by the conservative three bytes per token, bounded by
4 KiB and the existing 64 KiB storage/transport ceiling. Manual tool output,
stored activity text, process output, and websocket limits remain unchanged.

Within retained rounds, stale observations become
`[observation superseded by a newer one]` instead of surviving at full size.
Their paired tool responses become `[observation superseded]`; no retained
message may instruct the model to use an observation that was removed.

The newest ordinary tool round retains up to 8 KiB per result. Older retained
rounds keep at most 512 bytes and prefer the first line. Consecutive identical
observations are detected by a SHA-256 hash over text and image bytes. The
newest duplicate becomes `Page unchanged since the last observation.` while
the prior full observation remains available as the current page state.

## 4. Overflow recovery and observability

Preflight budgeting is primary. A server-side context rejection still uses a
graduated recovery ladder:

1. hard-compact to system, current user, and the newest tool round;
2. if rejected again, halve the surviving observation and remove its image;
3. fail only if the reduced retry is also rejected.

Each model attempt records estimated prompt tokens, chosen `max_tokens`,
preflight degradations, actual `prompt_n`/`predicted_n` when available, and
overflow recovery in the activity ledger and controller log.

## 5. Small-model tool policy

Append this instruction to `controller/prompts/agent-system.txt` after the
existing observation-tool policy:

```
Prefer dom_query for specifics, read_page for broad content, and screenshots only to ground visual actions.
```

This is a narrow amendment to spec 022. Tool signatures remain exclusively in
the injected schemas.

## 6. Acceptance

1. Hermetic Go tests cover estimation, adaptive completion limits, degradation
   order, recency truncation, stale-observation sentinels, duplicate
   observations, and all overflow retries.
2. `read_page` tests prove the default scaled cap and explicit cap behavior.
3. The prompt golden includes the amended small-model tool policy.
4. `npm run check` passes, including Go formatting, vet, and tests.
5. `/master-update` reconciles specs, `AGENTS.md`, README, and skills.

## Execution notes

Implemented 2026-07-25 with stdlib-only prompt estimation, calibrated token
accounting, context-scaled page digests, adaptive completion limits,
recency-weighted tool compaction, observation supersession/deduplication, and
three-stage server-overflow handling. Focused agent tests, agent vet,
`read_page` goldens, generated-theme parity, the offline docs build, and the
docs browser suite pass.

The canonical `npm run check` reaches type checking and then stops on
concurrent, incomplete spec 031 work: `test/config-ui.test.js` imports the
not-yet-present `controller/web/static/js/config-model.js`. A full
`go test ./...` likewise reaches and fails the unfinished
`controller/internal/config` package. The modified `internal/agent` package is
green; this spec does not fill in or weaken those unrelated configuration
contracts. As that concurrent work advanced, a later full Go run passed the
configuration package and stopped at its unfinished `cmd/ttsd` wiring instead.
