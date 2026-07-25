# Spec 022: System Prompts — On-Disk Sources, go:embed, SLM-Optimized Rewrite

| | |
|---|---|
| Status | Executed (2026-07-23) |
| Depends on | `specs/008-browser-agent.md` (agent prompt construction), `specs/021-agent-cdp-tools-console.md` (new observation tools referenced by the prompt — execute 021 first, or land the prompt clause with 021 per its §6 note) |
| Produces | Both system prompts moved from Go string literals to reviewable on-disk sources under `controller/prompts/`, embedded via `go:embed`, linked from the README; a rewrite grounded in small-language-model best practices tuned for this deployment (Gemma-class small model doing computer use and web-task automation) |
| Followed by | Future specs |

## 0. Executor instructions

- Constitution binds. The prompt files are runtime-embedded source, not fetched assets — they are committed, and the README must link to them (constitution rule 9 keeps docs in sync).
- The prompt text in §4 and §5 is normative — copy verbatim, placeholders included. Do not "improve" the wording; wording changes require a spec amendment (prompts are behavior).
- After the swap, the soak suite is the regression oracle: all flows must still pass.

## 1. Current state (evidence)

- **Agent prompt**: built inline by `buildSystemPrompt` in `controller/internal/agent/agent.go` (~L152–166): a single `fmt.Sprintf` paragraph block with `%dx%d` API/display resolutions and the `/opt/agent/system-manifest.json` content (capped 4096 bytes) appended.
- **Chat fallback prompt**: string constant in `controller/internal/chat/chat.go` L28 (88 chars).
- Neither is inspectable without reading Go source; neither is linked from docs. `go:embed` is currently used only for the SPA (`controller/embed.go`).

## 2. Grounding (web research, 2026-07)

Best-practice consensus for small instruction-tuned models (Gemma 3/4 class, ~2–8 B) in agentic/tool settings:

1. **Short, modular, imperative.** Small models drift under long prose; system prompts should be compact sections with one concern each (role, tool policy, stop criteria, output constraints). Every sentence must earn its context tokens — the prompt competes with DOM observations for a 16 K window (`VM_LLAMA_CTX`).
2. **Never duplicate tool schemas in prose.** llama.cpp injects the `tools` JSON through the model's chat template in the format the model was trained on; re-describing parameters in the system prompt causes conflicts and wasted tokens. The prompt states *policy* about tools, not their signatures.
3. **Respect the chat template.** Gemma 4 has first-class `system` role support; formatting is owned by llama.cpp's template. The prompt file is plain text with no role markers, no special tokens.
4. **Positive instruction over prohibition lists.** Small models follow "do X" more reliably than "never do Y" chains; keep the few prohibitions that matter (claiming unverified success) and phrase the rest as procedure.
5. **State grounding facts once, tersely** (resolution mapping, manifest), and keep few-shot examples out (context cost outweighs benefit at this scale; the tool-call format is enforced by the template, not examples).

## 3. Mechanics

1. New directory `controller/prompts/`:
   - `controller/prompts/agent-system.txt` — the §4 text.
   - `controller/prompts/chat-system.txt` — the §5 text.
   - `controller/prompts/README.md` — three lines: what these are, "wording changes require a spec amendment", and the placeholder table (`{{API_W}} {{API_H}} {{DISPLAY_W}} {{DISPLAY_H}} {{MANIFEST}}`).
2. Embedding: in `controller/internal/agent/agent.go` replace the literal with

```go
//go:embed prompts/agent-system.txt
var agentSystemPrompt string
```

   Note `go:embed` cannot reach outside the package dir — so either (a) place the files under `controller/internal/agent/prompts/` and `controller/internal/chat/prompts/` with `controller/prompts` as a **symlink-free** convention instead, or (b) preferred: create tiny package `controller/prompts` (`prompts.go` with `//go:embed *.txt` + exported `Agent`, `Chat` strings) and import it from both packages. Choose (b); it keeps one canonical directory that the README links to.
3. Placeholder interpolation: replace the `fmt.Sprintf("%d…%s")` call with a small `strings.NewReplacer("{{API_W}}", …, "{{MANIFEST}}", …)` — named placeholders survive reordering and are self-documenting in the file. Manifest cap (4096) unchanged.
4. README: in the architecture/controller section add one line: `The model's system prompts are plain text in [controller/prompts/](controller/prompts/) (spec 022).`

## 4. `agent-system.txt` (normative text)

```
You are Virtual Me: a private assistant on the user's own machine, operating a real Chromium browser on a virtual desktop for one trusted user.

How to work:
1. Answer directly, without tools, when the request needs no browser or system access.
2. For browser or system tasks: observe first (screenshot, dom, read_page, dom_query), then act (click_element, type_into, navigate, key, scroll), then verify the result with a fresh observation before reporting it.
3. Prefer element refs from dom for clicking and typing; use raw coordinates only when no ref exists. Screenshots are {{API_W}}x{{API_H}} API coordinates mapped onto the {{DISPLAY_W}}x{{DISPLAY_H}} display.
4. CDP-based tools (dom, dom_query, dom_validate, page_eval, read_page, layout_debug) are read-only observation. All real input goes through the OS-input tools.
5. Use dom_validate to confirm a page shows what you expect; use layout_debug when a click did not land.
6. Use bash for local files and system questions; project tasks state their scratch directory.
7. Use speak only when the user asks to hear something.

Report:
- Finish as soon as the task is done. State what you did and what you observed.
- Only claim success that an observation confirmed. If blocked after a few attempts, say what failed and what you saw.
- Be brief. Plain sentences, no filler.

Environment manifest: {{MANIFEST}}
```

Rationale trace (record in the commit message, not the file): section 1 = role+trust in one line; "observe → act → verify" is the loop discipline that spec 012's diagnosis showed the model skipping; the resolution fact stays because coordinate mapping errors were a real failure mode; tool *names* appear only to bind policy groups, signatures stay in the schemas; stop criteria and honesty rules carried over from the current prompt (they exist because of observed overclaiming).

## 5. `chat-system.txt` (normative text)

```
You are Virtual Me, a private assistant running locally in the user's own container. Nothing you are told leaves this machine. Answer plainly and briefly; say so when you do not know.
```

## 6. Tests and docs

- Hermetic Go: `TestPromptsEmbedded` — both exported strings non-empty, contain every placeholder exactly once (agent) / no placeholders (chat), no `{{` remains after interpolation with dummy values; a golden test locking the interpolated output for fixed inputs (so accidental edits fail loudly).
- Soak: `./cli.sh soak` — all flows pass with the new prompt (this is the real gate; the prompt rewrite must not regress `lahiri-dom`, `readpage-example`, or the other live flows).
- e2e chat probe unchanged.
- Docs: `/master-update` — README link (§3.4), develop skill (prompts package row + "wording changes need a spec amendment"), operate skill unchanged.

## 7. Acceptance checklist

- [x] `npm run check` green; `gofmt`/`go vet` clean; prompts package golden tests pass.
- [x] `grep -rn "You are Virtual Me" controller/ --include=*.go` returns no string literals (only the embed variables/usages).
- [x] README links to `controller/prompts/` and the link resolves on GitHub.
- [x] Interpolated agent prompt (log it once at startup at debug level, or verify via test) contains real resolutions and manifest, no `{{` residue.
- [x] Full soak suite passes on a rebuilt image.
- [x] Token count of the interpolated agent prompt (excluding manifest) is ≤ 350 tokens by llama.cpp's `/tokenize` (measure once, record in the commit message) — the rewrite must not grow past the current budget.

## Amendments

### 2026-07-23 — Execution details

- `controller/prompts` is the canonical package and embeds both normative text
  files. The agent performs named replacement at construction time while
  retaining the 4096-byte manifest cap; fallback chat uses the embedded text
  directly.
- Hermetic tests lock placeholder cardinality, chat's placeholder-free source,
  fallback-chat wiring, complete interpolation, and fixed-output golden text.
- The rebuilt-image soak passed `lahiri-dom`, `readpage-example`,
  `tools-roundtrip`, `jobs-queue-probe`, and the since-retired second
  browse flow (see the spec 012 site-allowlist amendment of 2026-07-24).
- llama.cpp `/tokenize` measured 327 tokens for the interpolated agent prompt
  with the environment-manifest content excluded, below the 350-token limit.

### 2026-07-24 — Empty completion recovery

Live soak validation observed llama.cpp return a successful SSE stream with no
content and no tool call after a valid DOM observation. The agent now retries
one such empty terminal completion per task. A second empty completion returns
an explicit failed response instead of silently persisting a blank assistant
message. Hermetic coverage locks the retry and recovery path.

### 2026-07-25 — Context-aware observation policy

Spec 034 adds one compact tool-selection rule for the Gemma-class edge model:
use `dom_query` for a specific element or value, reserve `read_page` for broad
page content, and use screenshots only when visual layout is needed to ground
an action. This reduces avoidable full-page observations while leaving tool
signatures exclusively in their injected schemas.
